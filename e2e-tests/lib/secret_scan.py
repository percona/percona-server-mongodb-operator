"""Fail a test when a credential shows up in the logs or objects of its namespace.

Pilot of a check that every e2e suite should run after backups and restores: the
storage credentials a test creates must never appear in operator or database logs,
in backup and restore objects, in events or in the CR status.

Two scans run over the collected text. The exact scan reads the values of the
storage Secrets (raw and base64) and looks for them verbatim. The pattern scan
looks for well-known credential shapes (AWS keys, Azure account keys, GCP service
account keys, private key blocks, MongoDB URIs with a password). A finding reports
the source, the line number and the rule; the matched text is masked, so the CI
output cannot leak it either.
"""

import base64
import json
import logging
import re
from dataclasses import dataclass

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
    """Credential values to search for, keyed by "<secret>/<key>[ (base64)]".

    Every data value of the given Secrets, line by line (a service account key is a
    JSON document), raw and base64-encoded (a dumped Secret leaks the latter). Short
    and allowlisted values are left out.
    """
    values: dict[str, str] = {}
    for secret in secret_names:
        raw = kubectl_bin("get", "secret", secret, "-n", namespace, "-o", "json", check=False)
        if not raw.strip():
            continue
        for key, encoded in (json.loads(raw).get("data") or {}).items():
            decoded = base64.b64decode(encoded).decode(errors="replace")
            for value in {line.strip() for line in decoded.splitlines()}:
                if len(value) < MIN_VALUE_LENGTH or value in ALLOWED_VALUES:
                    continue
                values[f"{secret}/{key}"] = value
                values[f"{secret}/{key} (base64)"] = base64.b64encode(value.encode()).decode()
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


def _pod_logs(namespace: str, pod: str) -> dict[str, str]:
    """Current and previous logs of every container of `pod`, keyed by source name."""
    containers = kubectl_bin(
        "get",
        "pod",
        pod,
        "-n",
        namespace,
        "-o",
        "jsonpath={.spec.initContainers[*].name} {.spec.containers[*].name}",
        check=False,
    ).split()
    logs = {}
    for container in containers:
        logs[f"pod/{pod}/{container}"] = kubectl_bin(
            "logs", pod, "-n", namespace, "-c", container, check=False
        )
        previous = kubectl_bin(
            "logs", pod, "-n", namespace, "-c", container, "--previous", check=False
        )
        if previous:
            logs[f"pod/{pod}/{container} (previous)"] = previous
    return logs


def _names(namespace: str, resource: str, selector: str = "") -> list[str]:
    args = ["get", resource, "-n", namespace, "-o", "name"]
    if selector:
        args += ["-l", selector]
    return [name.split("/", 1)[1] for name in kubectl_bin(*args, check=False).split()]


def collect_sources(namespace: str, cluster: str, operator_pod: str) -> dict[str, str]:
    """Text to scan, keyed by source: pod logs, backup and restore objects, events, CR."""
    pods = [operator_pod]
    pods += _names(namespace, "pods", f"app.kubernetes.io/instance={cluster}")
    for job in _names(namespace, "jobs"):
        pods += _names(namespace, "pods", f"job-name={job}")

    sources: dict[str, str] = {}
    for pod in dict.fromkeys(pods):
        sources.update(_pod_logs(namespace, pod))
    for resource in ("psmdb-backup", "psmdb-restore", "events"):
        sources[resource] = kubectl_bin(
            "get", resource, "-n", namespace, "-o", "yaml", check=False
        )
    sources[f"psmdb/{cluster}"] = kubectl_bin(
        "get", "psmdb", cluster, "-n", namespace, "-o", "yaml", check=False
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
