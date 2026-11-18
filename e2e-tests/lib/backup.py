"""Helpers for PerconaServerMongoDBBackup / PerconaServerMongoDBRestore objects."""

import json
import logging
import os
import time
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

import yaml

from .instances import wait_until
from .kubectl import kubectl_bin, wait_cluster_consistency

logger = logging.getLogger(__name__)

API_VERSION = "psmdb.percona.com/v1"

# pbm-agent prints this once it has joined the PBM cluster and can take work.
AGENT_READY_LOG = "listening for the commands"

# PLATFORM -> manifest under e2e-tests/conf (mirrors deploy_volume_snapshot_class in functions)
_SNAPSHOT_CLASS_FILES = {
    "aks": "volume-snapshot-class-aks.yaml",
    "eks": "volume-snapshot-class-ebs.yaml",
    "openshift": "volume-snapshot-class-ebs.yaml",
    "gke": "volume-snapshot-class-gke.yaml",
    "rancher": "volume-snapshot-class-rancher.yaml",
}


def _setup_rancher_backup_storage(
    conf_dir: str, namespace: str, s3_fullname: str = "s3-storage"
) -> None:
    """Point Longhorn's default backup target at the in-cluster S3 fixture."""
    secret = yaml.safe_load(Path(f"{conf_dir}/s3-secret.yml").read_text())
    secret["metadata"]["name"] = "s3-secret-longhorn"
    secret.setdefault("stringData", {})["AWS_ENDPOINTS"] = (
        f"http://{s3_fullname}-all-in-one.{namespace}.svc.cluster.local:8333"
    )
    kubectl_bin("apply", "-n", "longhorn-system", "-f", "-", input_data=yaml.dump(secret))

    kubectl_bin(
        "-n",
        "longhorn-system",
        "patch",
        "backuptargets.longhorn.io",
        "default",
        "--type=merge",
        "-p",
        json.dumps(
            {
                "spec": {
                    "backupTargetURL": "s3://operator-testing@us-east-1/",
                    "credentialSecret": "s3-secret-longhorn",
                }
            }
        ),
    )


def deploy_volume_snapshot_class(conf_dir: str, namespace: str) -> None:
    """Install the VolumeSnapshotClass matching the platform under test."""
    platform = os.environ.get("PLATFORM", "")
    manifest = _SNAPSHOT_CLASS_FILES.get(platform)
    if not manifest:
        raise RuntimeError(
            f"Unsupported Kubernetes environment for volume snapshot class: {platform or 'unset'}"
        )

    if platform == "rancher":
        _setup_rancher_backup_storage(conf_dir, namespace)

    kubectl_bin("apply", "-f", f"{conf_dir}/{manifest}")


def get_volume_snapshot_class() -> str:
    """Return the name of the first VolumeSnapshotClass in the cluster."""
    name = kubectl_bin(
        "get", "volumesnapshotclass", "-o", "jsonpath={.items[0].metadata.name}", check=False
    ).strip()
    if not name:
        raise RuntimeError("No VolumeSnapshotClass found in the cluster")
    return name


def get_backup_snapshots(backup_name: str) -> list[dict[str, str]]:
    """Return .status.snapshots of a psmdb-backup, keyed by PBM's replset names."""
    raw = backup_field(backup_name, ".status.snapshots")
    if not raw:
        return []
    snapshots: list[dict[str, str]] = json.loads(raw)
    return snapshots


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


def run_backup(
    test_dir: str,
    storage: str,
    backup_name: str = "",
    backup_type: str = "logical",
) -> str:
    """Start a backup from the test's conf/backup-<storage>.yml and return its name."""
    name = backup_name or f"backup-{storage}"
    manifest = Path(test_dir) / "conf" / f"backup-{storage}.yml"

    doc = yaml.safe_load(manifest.read_text())
    doc["metadata"]["name"] = name
    doc["spec"]["storageName"] = storage
    doc["spec"]["type"] = backup_type

    logger.info(f"Running backup {name}")
    kubectl_bin("apply", "-f", "-", input_data=yaml.dump(doc))
    return name


def backup_field(name: str, path: str) -> str:
    return kubectl_bin(
        "get", "psmdb-backup", name, "-o", f"jsonpath={{{path}}}", check=False
    ).strip()


def wait_backup(backup_name: str, target_state: str = "ready", timeout: int = 600) -> None:
    """Wait for a psmdb-backup to reach `target_state`, failing fast on error."""
    logger.info(f"Waiting for psmdb-backup/{backup_name} to reach {target_state} state")
    deadline = time.monotonic() + timeout
    state = ""
    while time.monotonic() < deadline:
        state = backup_field(backup_name, ".status.state")
        if state == target_state:
            logger.info(f"psmdb-backup/{backup_name} is {target_state}")
            return
        if state == "error":
            error = backup_field(backup_name, ".status.error")
            raise AssertionError(f"psmdb-backup/{backup_name} is in error state: {error}")
        time.sleep(1)

    raise TimeoutError(
        f"Timeout waiting for psmdb-backup/{backup_name} to reach {target_state} state. "
        f"Last state: {state or 'unknown'}"
    )


def run_restore(
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


def wait_restore(
    restore_name: str,
    cluster_name: str,
    target_state: str = "ready",
    wait_consistency: bool = True,
    timeout: int = 1780,
) -> None:
    """Wait for a psmdb-restore to reach `target_state`, failing fast on error.

    `restore_name` is the restore object name, not the backup it restores from.
    """
    logger.info(f"Waiting for psmdb-restore/{restore_name} to reach {target_state} state")
    deadline = time.monotonic() + timeout
    state = ""
    while time.monotonic() < deadline:
        state = restore_field(restore_name, ".status.state")
        if state == target_state:
            logger.info(f"psmdb-restore/{restore_name} is {target_state}")
            break
        if state == "error":
            error = restore_field(restore_name, ".status.error")
            raise AssertionError(f"psmdb-restore/{restore_name} is in error state: {error}")
        time.sleep(5)
    else:
        raise TimeoutError(
            f"Timeout waiting for psmdb-restore/{restore_name} to reach {target_state} state. "
            f"Last state: {state or 'unknown'}"
        )

    if wait_consistency:
        wait_cluster_consistency(cluster_name)


def pbm_status(pod: str, *extra_args: str, check: bool = True) -> str:
    """Run `pbm status -o json` in a pod's backup-agent container and return the raw output."""
    return kubectl_bin(
        "exec",
        pod,
        "-c",
        "backup-agent",
        "--",
        "pbm",
        "status",
        "-o",
        "json",
        *extra_args,
        check=check,
    )


def wait_for_pbm_operations(
    cluster_name: str, pod: str | None = None, timeout: int = 2700
) -> None:
    """Wait until no PBM operation is running on the cluster.

    `pod` defaults to `<cluster>-rs0-0`. A replica set using instances[] has
    that pod only if it has a group named `mongod`, so pass a real pod there.
    """
    pod = pod or f"{cluster_name}-rs0-0"
    logger.info(f"Waiting for PBM operations on {cluster_name} to finish (via {pod})")
    deadline = time.monotonic() + timeout
    running: dict[str, Any] = {}
    while time.monotonic() < deadline:
        out = pbm_status(pod, "-s", "running", check=False)
        try:
            running = json.loads(out).get("running") or {}
        except json.JSONDecodeError:
            running = {"opID": "unknown"}
        if running.get("opID") is None:
            logger.info(f"No PBM operation is running on {cluster_name}")
            return
        time.sleep(5)

    raise TimeoutError(
        f"Timeout waiting for PBM operations on {cluster_name} to finish. Last running: {running}"
    )


def latest_oplog_chunk_ts(pod: str) -> int:
    """End of the newest uploaded PITR oplog chunk, as a unix timestamp.

    0 when PITR has not produced a chunk yet, so callers can poll on it without
    special-casing the empty state.
    """
    status: dict[str, Any] = json.loads(pbm_status(pod))
    chunks = ((status.get("backups") or {}).get("pitrChunks") or {}).get("pitrChunks") or []
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


def format_pitr_date(ts: float) -> str:
    """Format a unix timestamp the way spec.pitr.date is parsed ("2006-01-02 15:04:05" UTC)."""
    return datetime.fromtimestamp(int(ts), tz=UTC).strftime("%Y-%m-%d %H:%M:%S")


def now_ts() -> int:
    return int(time.time())
