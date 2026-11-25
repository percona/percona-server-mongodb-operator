"""Fail a test when a credential shows up in the logs or objects of its namespace.

Pilot of a check that every e2e suite should run after backups and restores: the
storage credentials a test creates must never appear in operator or database logs,
in backup and restore objects, in events or in the CR status.

Two scans run over the collected text. The exact scan reads the values of the
storage Secrets (raw and base64) and looks for them verbatim. The pattern scan
looks for well-known credential shapes (AWS keys, Azure account keys, GCP service
account keys, private key blocks, MongoDB URIs with a password). A finding reports
the source, the line number and the rule; the scan's own assertion message masks
the matched text. The failure report the harness attaches (operator logs, events,
bash output) masks the values of the users and storage Secrets of the test
namespace (register_known_secrets) and credential shapes (allure_support.mask).

Only the pods that exist at the time of the scan are read. The operator replaces
the database pods when a physical restore completes, so a scan after it misses
their logs from the restore: scan before it as well.
"""

import base64
import json
import logging
import re
import subprocess
from dataclasses import dataclass
from typing import Any

from .allure_support import add_known_values
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

# Data keys of user names in the users Secret (MONGODB_BACKUP_USER): not credentials.
USER_NAME_KEY = re.compile(r"_USER$")

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
            if not USER_NAME_KEY.search(key):
                values.update(data_values(f"{secret}/{key}", encoded))
    return values


def users_secret_names(namespace: str) -> tuple[str, ...]:
    """The users Secret of every psmdb cluster in `namespace`, and the operator's
    internal copy of it."""
    out = kubectl_bin(
        "get",
        "psmdb",
        "-n",
        namespace,
        "-o",
        r'jsonpath={range .items[*]}{.metadata.name}{" "}{.spec.secrets.users}{"\n"}{end}',
    )
    names: list[str] = []
    for line in out.splitlines():
        cluster, _, users = line.partition(" ")
        if cluster:
            names += [users.strip() or f"{cluster}-secrets", f"internal-{cluster}-users"]
    return tuple(names)


def register_known_secrets(namespace: str) -> None:
    """Mask the values of the users and storage Secrets of `namespace` in the report.

    Errors are logged: a failure report without these values is masked by
    credential shape only.
    """
    try:
        names = users_secret_names(namespace) + STORAGE_SECRETS
        values = secret_values(namespace, names)
    except (subprocess.CalledProcessError, ValueError) as e:
        logger.warning(f"Could not read the secrets of {namespace} to mask them: {e}")
        return
    add_known_values(values.values())


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


def _pod_logs(namespace: str, pod: dict[str, Any]) -> dict[str, str]:
    """Current logs of every container of `pod`, and the previous log of a restarted one,
    keyed by source name. Empty when the pod is deleted during the read."""
    name = pod["metadata"]["name"]
    status = pod.get("status", {})
    containers = (status.get("initContainerStatuses") or []) + (
        status.get("containerStatuses") or []
    )
    logs = {}
    for container in containers:
        source = f"pod/{name}/{container['name']}"
        args = ("logs", name, "-n", namespace, "-c", container["name"])
        try:
            logs[source] = kubectl_bin(*args)
        except subprocess.CalledProcessError:
            if kubectl_bin("get", "pod", name, "-n", namespace, "--ignore-not-found"):
                raise
            logger.info(f"Pod {name} was deleted while its logs were read, skipping it")
            return {}
        if container.get("restartCount"):
            # The previous log can already be gone; the current one is read either way.
            logs[f"{source} (previous)"] = kubectl_bin(*args, "--previous", check=False)
    return logs


def collect_sources(namespace: str, cluster: str, operator_pod: str) -> dict[str, str]:
    """Text to scan, keyed by source: logs of the operator, cluster and Job pods, the
    backup and restore objects, the events and the psmdb object."""
    pods = json.loads(kubectl_bin("get", "pods", "-n", namespace, "-o", "json"))["items"]
    sources: dict[str, str] = {}
    for pod in pods:
        labels = pod["metadata"].get("labels") or {}
        if (
            pod["metadata"]["name"] == operator_pod
            or labels.get("app.kubernetes.io/instance") == cluster
            or "job-name" in labels
        ):
            sources.update(_pod_logs(namespace, pod))
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
) -> None:
    """Fail when a storage credential or a credential-shaped string is in the logs.

    Scans the operator pod, every pod of `cluster` (all containers, previous logs
    included), the pods of Jobs in the namespace, the psmdb-backup and psmdb-restore
    objects, the events and the psmdb object. The report masks every match.
    """
    values = secret_values(namespace, secret_names)
    assert values, f"No credential values to scan for: none of {secret_names} in {namespace}"
    sources = collect_sources(namespace, cluster, operator_pod)
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
