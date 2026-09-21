"""Helpers for PerconaServerMongoDBBackup / PerconaServerMongoDBRestore objects.

Backup and restore objects are built here as dicts and applied through stdin
rather than templated with sed, which is what the bash harness does. The shapes
are small and the fields the tests vary (name, type, PITR target) are exactly
the ones sed was patching, so a dict is both shorter and harder to get wrong.
"""

import json
import logging
import time
from datetime import UTC, datetime
from typing import Any

import yaml

from .instances import wait_until
from .kubectl import kubectl_bin

logger = logging.getLogger(__name__)

API_VERSION = "psmdb.percona.com/v1"

# pbm-agent prints this once it has joined the PBM cluster and can take work.
AGENT_READY_LOG = "listening for the commands"


def wait_backup_agent(pod: str, timeout: int = 1800) -> None:
    """Wait for the pbm-agent sidecar in pod to be ready to take commands.

    Only data-bearing pods have the sidecar at all: pkg/psmdb/statefulset.go
    adds backup-agent in the data-bearing branch, so an arbiter never gets one
    and waiting on it here would hang until the timeout.
    """

    def ready() -> bool:
        logs = kubectl_bin("logs", pod, "-c", "backup-agent", "--tail=200", check=False)
        return AGENT_READY_LOG in logs

    wait_until(f"pbm-agent in {pod} to be ready", ready, timeout=timeout)


def create_backup(
    name: str, cluster: str, storage: str = "minio", backup_type: str | None = None
) -> None:
    """Create a backup object. backup_type None means the operator's default (logical)."""
    spec: dict[str, Any] = {"clusterName": cluster, "storageName": storage}
    if backup_type:
        spec["type"] = backup_type

    logger.info(f"Creating {backup_type or 'logical'} backup {name}")
    kubectl_bin(
        "apply",
        "-f",
        "-",
        input_data=yaml.dump(
            {
                "apiVersion": API_VERSION,
                "kind": "PerconaServerMongoDBBackup",
                "metadata": {"name": name},
                "spec": spec,
            }
        ),
    )


def backup_field(name: str, path: str) -> str:
    return kubectl_bin(
        "get", "psmdb-backup", name, "-o", f"jsonpath={{{path}}}", check=False
    ).strip()


def wait_backup(name: str, target_state: str = "ready", timeout: int = 1800) -> None:
    """Wait for a backup to reach target_state, failing fast on 'error'."""

    def reached() -> bool:
        state = backup_field(name, ".status.state")
        logger.info(f"backup {name}: {state or '<none>'}")
        if state == "error":
            raise AssertionError(f"backup {name} failed: {backup_field(name, '.status.error')}")
        return state == target_state

    wait_until(f"backup {name} to reach {target_state}", reached, timeout=timeout)


def create_restore(
    name: str,
    cluster: str,
    backup_name: str,
    pitr_type: str | None = None,
    pitr_date: str | None = None,
) -> None:
    """Create a restore object, optionally recovering to a PITR target.

    Whether the restore runs logically or physically is not stated here: the
    operator takes it from the backup being restored.
    """
    spec: dict[str, Any] = {"clusterName": cluster, "backupName": backup_name}
    if pitr_type:
        pitr: dict[str, Any] = {"type": pitr_type}
        if pitr_date:
            pitr["date"] = pitr_date
        spec["pitr"] = pitr

    logger.info(f"Creating restore {name} from {backup_name} (pitr={pitr_type} {pitr_date})")
    kubectl_bin(
        "apply",
        "-f",
        "-",
        input_data=yaml.dump(
            {
                "apiVersion": API_VERSION,
                "kind": "PerconaServerMongoDBRestore",
                "metadata": {"name": name},
                "spec": spec,
            }
        ),
    )


def restore_field(name: str, path: str) -> str:
    return kubectl_bin(
        "get", "psmdb-restore", name, "-o", f"jsonpath={{{path}}}", check=False
    ).strip()


def wait_restore(name: str, target_state: str = "ready", timeout: int = 1800) -> None:
    """Wait for a restore to reach target_state, failing fast on 'error'."""

    def reached() -> bool:
        state = restore_field(name, ".status.state")
        logger.info(f"restore {name}: {state or '<none>'}")
        if state == "error":
            raise AssertionError(f"restore {name} failed: {restore_field(name, '.status.error')}")
        return state == target_state

    wait_until(f"restore {name} to reach {target_state}", reached, timeout=timeout)


def pbm_status(pod: str) -> dict[str, Any]:
    """Run `pbm status -o json` in a pod's backup-agent container."""
    out = kubectl_bin("exec", pod, "-c", "backup-agent", "--", "pbm", "status", "-o", "json")
    status: dict[str, Any] = json.loads(out)
    return status


def latest_oplog_chunk_ts(pod: str) -> int:
    """End of the newest uploaded PITR oplog chunk, as a unix timestamp.

    0 when PITR has not produced a chunk yet, so callers can poll on it without
    special-casing the empty state.
    """
    chunks = ((pbm_status(pod).get("backups") or {}).get("pitrChunks") or {}).get(
        "pitrChunks"
    ) or []
    if not chunks:
        return 0
    return int(((chunks[-1].get("range") or {}).get("end")) or 0)


def wait_for_oplog_past(pod: str, target_ts: int, timeout: int = 900) -> None:
    """Wait until an uploaded oplog chunk covers target_ts.

    A PITR restore to a target the chunks do not reach yet is rejected by the
    operator (GetPITRChunkContains), so every date restore has to wait here
    first rather than assume the agent has caught up.
    """

    def caught_up() -> bool:
        latest = latest_oplog_chunk_ts(pod)
        logger.info(
            f"latest oplog chunk {format_pitr_date(latest)} vs target {format_pitr_date(target_ts)}"
        )
        return latest > target_ts

    wait_until(f"oplog chunks to cover {format_pitr_date(target_ts)}", caught_up, timeout=timeout)


def format_pitr_date(ts: int | float) -> str:
    """Format a unix timestamp the way spec.pitr.date is parsed ("2006-01-02 15:04:05" UTC)."""
    return datetime.fromtimestamp(int(ts), tz=UTC).strftime("%Y-%m-%d %H:%M:%S")


def now_ts() -> int:
    return int(time.time())
