"""Fail a test when a credential shows up in the logs or objects of its namespace.

Pilot of a check that every e2e suite should run after backups and restores: the
storage credentials a test creates must never appear in operator or database logs,
in backup and restore objects, in events or in the CR status.

Two scans run over the collected text. The exact scan reads the values of the
storage Secrets (raw and base64) and looks for them verbatim. The pattern scan
looks for well-known credential shapes (AWS keys, Azure account keys, GCP service
account keys, private key blocks, MongoDB URIs with a password). A finding reports
the source, the line number and the rule; the scan's own assertion message masks
the matched text. The failure report the harness attaches (operator logs, events)
is not redacted.

The operator replaces the database pods when a physical restore completes, and
their logs go with them. LogRecorder keeps them: run the restore inside it and pass
its sources to check_logs_for_secrets.
"""

import base64
import json
import logging
import re
import subprocess
import threading
import time
from dataclasses import dataclass
from types import TracebackType
from typing import Any, Self

from .kubectl import kubectl_bin

logger = logging.getLogger(__name__)

# Secrets the e2e tests create for backup storages (conf/s3-secret.yml and the
# gitignored conf/cloud-secret.yml). Missing ones are skipped.
STORAGE_SECRETS = (
    "s3-secret",
    "aws-s3-secret",
    "gcp-cs-secret",
    "gcp-cs-sa-key-secret",
    "azure-secret",
    "minio-gw-gcs-secret",
    "do-spaces-secret",
)

# Shorter values (bucket names, regions, "admin") match too much.
MIN_VALUE_LENGTH = 8

# Placeholders from example manifests that are not credentials.
ALLOWED_VALUES = frozenset(
    {
        "S3-BACKUP-BUCKET-NAME-HERE",
        "GCS-BACKUP-BUCKET-NAME-HERE",
        "OSS-BACKUP-BUCKET-NAME-HERE",
        "YOUR-ACCESS-KEY",
        "YOUR-SECRET-KEY",
        "REPLACE-WITH-YOUR-KEY",
    }
)

# Credential shapes, independent of the values the test knows.
PATTERNS = (
    ("aws-access-key-id", re.compile(r"AKIA[0-9A-Z]{16}")),
    ("aws-secret-access-key", re.compile(r"aws_secret_access_key", re.IGNORECASE)),
    ("azure-account-key", re.compile(r"AccountKey=")),
    ("gcp-private-key", re.compile(r'"private_key"\s*:')),
    ("private-key-block", re.compile(r"-----BEGIN [A-Z ]*PRIVATE KEY-----")),
    ("mongodb-uri-password", re.compile(r"mongodb(?:\+srv)?://[^\s:/@]+:([^\s@]+)@")),
)
# A URI whose password is already masked is not a leak.
MASKED_PASSWORD = re.compile(r"^(\*+|x+|<[^>]*>|\[[^\]]*\]|REDACTED)$", re.IGNORECASE)

# A pod can be deleted, and a container can be still starting, between listing the
# pod and reading its log: a read is tried this many times, READ_DELAY seconds apart.
READ_ATTEMPTS = 3
READ_DELAY = 5

# LogRecorder reads every RECORD_INTERVAL seconds, so it misses a pod that lives
# shorter and the last lines a pod logs before it is deleted.
RECORD_INTERVAL = 15
# Upper bound for the recorder thread to finish its current read on exit.
RECORD_STOP_TIMEOUT = 120


@dataclass(frozen=True)
class Finding:
    source: str
    line: int
    rule: str
    masked: str

    def __str__(self) -> str:
        return f"{self.source}:{self.line}: {self.rule}: {self.masked}"


def mask(value: str) -> str:
    return value[:4] + "***"


def secret_values(namespace: str, secret_names: tuple[str, ...]) -> dict[str, str]:
    """Credential values to search for, keyed by "<secret>/<key>#<line>" and
    "<secret>/<key> (base64)".

    Every distinct line of every data value of the given Secrets (a service account
    key is a JSON document), plus the value as stored in the Secret's `data` (a
    dumped Secret leaks that). Missing Secrets are skipped; any other read error
    raises. Short and allowlisted values are left out.
    """
    values: dict[str, str] = {}
    for secret in secret_names:
        raw = kubectl_bin(
            "get", "secret", secret, "-n", namespace, "-o", "json", "--ignore-not-found"
        )
        if not raw.strip():
            continue
        for key, encoded in (json.loads(raw).get("data") or {}).items():
            values.update(data_values(f"{secret}/{key}", encoded))
    return values


def data_values(name: str, encoded: str) -> dict[str, str]:
    """Search values for one Secret data entry: its base64 and its distinct lines, each
    keyed by the number of its first line in the value."""
    decoded = base64.b64decode(encoded).decode(errors="replace")
    values: dict[str, str] = {}
    if len(decoded.strip()) >= MIN_VALUE_LENGTH and decoded.strip() not in ALLOWED_VALUES:
        values[f"{name} (base64)"] = encoded
    seen = set()
    for number, raw_line in enumerate(decoded.splitlines(), start=1):
        line = raw_line.strip()
        if line in seen or len(line) < MIN_VALUE_LENGTH or line in ALLOWED_VALUES:
            continue
        seen.add(line)
        values[f"{name}#{number}"] = line
    return values


def scan_text(source: str, text: str, values: dict[str, str]) -> list[Finding]:
    """Findings for every line of `text` that holds a known value or matches a pattern."""
    findings = []
    for number, line in enumerate(text.splitlines(), start=1):
        for name, value in values.items():
            if value in line:
                findings.append(Finding(source, number, f"secret value {name}", mask(value)))
        for rule, pattern in PATTERNS:
            for match in pattern.finditer(line):
                password = match.group(1) if match.groups() else ""
                if password and MASKED_PASSWORD.match(password):
                    continue
                findings.append(Finding(source, number, rule, mask(match.group(0))))
    return findings


def _pod(namespace: str, pod: str) -> dict[str, Any] | None:
    """The pod object, or None when it is gone."""
    raw = kubectl_bin("get", "pod", pod, "-n", namespace, "-o", "json", "--ignore-not-found")
    return dict(json.loads(raw)) if raw.strip() else None


def _containers(pod: dict[str, Any]) -> tuple[list[str], list[str]]:
    """(started, not started) containers of `pod`, init containers first."""
    status = pod.get("status", {})
    statuses = {
        c["name"]: c
        for c in (status.get("initContainerStatuses") or [])
        + (status.get("containerStatuses") or [])
    }
    started, waiting = [], []
    for container in (pod["spec"].get("initContainers") or []) + pod["spec"]["containers"]:
        container_status = statuses.get(container["name"], {})
        states = {**container_status.get("lastState", {}), **container_status.get("state", {})}
        if "running" in states or "terminated" in states:
            started.append(container["name"])
        else:
            waiting.append(container["name"])
    return started, waiting


def _container_log(namespace: str, pod: str, container: str) -> str | None:
    """Current log of `container`, or None when its pod is gone. Fails after READ_ATTEMPTS."""
    error = ""
    for attempt in range(READ_ATTEMPTS):
        if attempt:
            time.sleep(READ_DELAY)
        try:
            return kubectl_bin("logs", pod, "-n", namespace, "-c", container)
        except subprocess.CalledProcessError as e:
            error = str(e.stderr or "").strip()
        if _pod(namespace, pod) is None:
            return None
    raise AssertionError(
        f"Cannot read the log of container {container} of pod {pod} after "
        f"{READ_ATTEMPTS} attempts: {error}"
    )


def _pod_logs(namespace: str, pod: str) -> dict[str, str] | None:
    """Current and previous logs of every container of `pod`, keyed by source name.

    None when the pod is gone. Waits for containers that have not started yet and
    fails when they do not start within the read attempts.
    """
    waiting: list[str] = []
    for attempt in range(READ_ATTEMPTS):
        if attempt:
            time.sleep(READ_DELAY)
        obj = _pod(namespace, pod)
        if obj is None:
            return None
        started, waiting = _containers(obj)
        if waiting:
            continue
        logs = {}
        for container in started:
            current = _container_log(namespace, pod, container)
            if current is None:
                return None
            logs[f"pod/{pod}/{container}"] = current
            # Absent unless the container restarted.
            previous = kubectl_bin(
                "logs", pod, "-n", namespace, "-c", container, "--previous", check=False
            )
            if previous:
                logs[f"pod/{pod}/{container} (previous)"] = previous
        return logs
    raise AssertionError(
        f"Containers {waiting} of pod {pod} did not start in "
        f"{(READ_ATTEMPTS - 1) * READ_DELAY}s, so their logs cannot be scanned"
    )


def _names(namespace: str, resource: str, selector: str = "") -> list[str]:
    args = ["get", resource, "-n", namespace, "-o", "name"]
    if selector:
        args += ["-l", selector]
    return [name.split("/", 1)[1] for name in kubectl_bin(*args).split()]


def collect_sources(namespace: str, cluster: str, operator_pod: str) -> dict[str, str]:
    """Text to scan, keyed by source: pod logs, backup and restore objects, events, CR."""
    pods = [operator_pod]
    pods += _names(namespace, "pods", f"app.kubernetes.io/instance={cluster}")
    for job in _names(namespace, "jobs"):
        pods += _names(namespace, "pods", f"job-name={job}")

    sources: dict[str, str] = {}
    for pod in dict.fromkeys(pods):
        logs = _pod_logs(namespace, pod)
        if logs is None:
            logger.info(f"Pod {pod} was deleted before its logs were read, skipping it")
            continue
        sources.update(logs)
    for resource in ("psmdb-backup", "psmdb-restore", "events"):
        sources[resource] = kubectl_bin("get", resource, "-n", namespace, "-o", "yaml")
    sources[f"psmdb/{cluster}"] = kubectl_bin(
        "get", "psmdb", cluster, "-n", namespace, "-o", "yaml"
    )
    return sources


def check_logs_for_secrets(
    namespace: str,
    cluster: str,
    operator_pod: str,
    secret_names: tuple[str, ...] = STORAGE_SECRETS,
    extra_sources: dict[str, str] | None = None,
) -> None:
    """Fail when a storage credential or a credential-shaped string is in the logs.

    Scans the operator pod, every pod of `cluster` (all containers, previous logs
    included), the pods of Jobs in the namespace, the psmdb-backup and psmdb-restore
    objects, the events and the psmdb object, plus `extra_sources`: text read
    earlier, such as the logs a LogRecorder kept of the pods that ran a physical
    restore. The report masks every match.
    """
    values = secret_values(namespace, secret_names)
    assert values, f"No credential values to scan for: none of {secret_names} in {namespace}"
    sources = collect_sources(namespace, cluster, operator_pod)
    sources.update(extra_sources or {})
    findings = [
        finding for source, text in sources.items() for finding in scan_text(source, text, values)
    ]
    lines = sum(text.count("\n") for text in sources.values())
    logger.info(
        f"Scanned {len(sources)} sources ({lines} lines) of {namespace} for "
        f"{len(values)} secret values and {len(PATTERNS)} patterns: "
        f"{len(findings) or 'no'} findings"
    )
    assert not findings, "Credentials found in logs (masked):\n" + "\n".join(
        str(finding) for finding in findings
    )


class LogRecorder:
    """Keeps the logs of the pods of `cluster` that are deleted while it runs.

    A context manager. A thread reads the current log of every started container
    (init containers included) of the cluster's pods every `interval` seconds and
    keeps the latest read per pod UID and container. On exit it stops the thread,
    reads once more and raises any error of the thread. sources() returns the kept
    logs of the pods that were gone at that last read; check_logs_for_secrets reads
    the running ones itself.
    """

    def __init__(self, namespace: str, cluster: str, interval: float = RECORD_INTERVAL) -> None:
        self.namespace = namespace
        self.cluster = cluster
        self.interval = interval
        # (pod UID, container) -> (source name, log)
        self._logs: dict[tuple[str, str], tuple[str, str]] = {}
        self._running: set[str] = set()
        self._stop = threading.Event()
        self._error: Exception | None = None
        self._thread = threading.Thread(target=self._run, name=f"logs-{cluster}", daemon=True)

    def record(self) -> None:
        """Read the logs of the cluster's pods once."""
        pods = json.loads(
            kubectl_bin(
                "get",
                "pods",
                "-n",
                self.namespace,
                "-l",
                f"app.kubernetes.io/instance={self.cluster}",
                "-o",
                "json",
            )
        )["items"]
        running = set()
        for pod in pods:
            name, uid = pod["metadata"]["name"], pod["metadata"]["uid"]
            # A terminating pod is gone by the time the final scan reads the cluster.
            if not pod["metadata"].get("deletionTimestamp"):
                running.add(uid)
            for container in _containers(pod)[0]:
                log = _container_log(self.namespace, name, container)
                if log is None:
                    logger.debug(f"Pod {name} ({uid}) is gone, keeping its last recorded logs")
                    running.discard(uid)
                    break
                self._logs[(uid, container)] = (f"pod/{name}/{container} (uid {uid[:8]})", log)
        self._running = running

    def sources(self) -> dict[str, str]:
        """Kept logs of the pods that were gone at the last read, keyed by source name."""
        return {
            source: log
            for (uid, _), (source, log) in self._logs.items()
            if uid not in self._running
        }

    def _run(self) -> None:
        try:
            while True:
                self.record()
                if self._stop.wait(self.interval):
                    return
        except Exception as e:
            self._error = e

    def __enter__(self) -> Self:
        logger.info(f"Recording the logs of the {self.cluster} pods every {self.interval}s")
        self._thread.start()
        return self

    def __exit__(
        self,
        exc_type: type[BaseException] | None,
        exc: BaseException | None,
        traceback: TracebackType | None,
    ) -> None:
        self._stop.set()
        self._thread.join(RECORD_STOP_TIMEOUT)
        if self._thread.is_alive():
            self._error = TimeoutError(f"the recorder did not stop in {RECORD_STOP_TIMEOUT}s")
        elif self._error is None:
            try:
                self.record()
            except Exception as e:
                self._error = e
        logger.info(
            f"Stopped recording the logs of the {self.cluster} pods: kept "
            f"{len(self._logs)} pod/container logs, {len(self.sources())} of pods now gone"
        )
        if self._error is None:
            return
        message = f"Recording the logs of the {self.cluster} pods failed: {self._error!r}"
        if exc_type is not None:
            # The error of the step itself is the one to report.
            logger.error(message)
            return
        raise AssertionError(message) from self._error
