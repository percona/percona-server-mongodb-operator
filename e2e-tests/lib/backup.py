import json
import logging
import os
import time
from pathlib import Path

import yaml

from .kubectl import kubectl_bin, wait_cluster_consistency

logger = logging.getLogger(__name__)

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
    raw = kubectl_bin(
        "get", "psmdb-backup", backup_name, "-o", "jsonpath={.status.snapshots}"
    ).strip()
    if not raw:
        return []
    snapshots: list[dict[str, str]] = json.loads(raw)
    return snapshots


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


def wait_backup(backup_name: str, target_state: str = "ready", timeout: int = 600) -> None:
    """Wait for a psmdb-backup to reach `target_state`, failing fast on error."""
    logger.info(f"Waiting for psmdb-backup/{backup_name} to reach {target_state} state")
    deadline = time.monotonic() + timeout
    state = ""
    while time.monotonic() < deadline:
        state = kubectl_bin(
            "get", "psmdb-backup", backup_name, "-o", "jsonpath={.status.state}", check=False
        ).strip()
        if state == target_state:
            logger.info(f"psmdb-backup/{backup_name} is {target_state}")
            return
        if state == "error":
            error = kubectl_bin(
                "get", "psmdb-backup", backup_name, "-o", "jsonpath={.status.error}", check=False
            ).strip()
            raise AssertionError(f"psmdb-backup/{backup_name} is in error state: {error}")
        time.sleep(1)

    raise TimeoutError(
        f"Timeout waiting for psmdb-backup/{backup_name} to reach {target_state} state. "
        f"Last state: {state or 'unknown'}"
    )


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
        state = kubectl_bin(
            "get", "psmdb-restore", restore_name, "-o", "jsonpath={.status.state}", check=False
        ).strip()
        if state == target_state:
            logger.info(f"psmdb-restore/{restore_name} is {target_state}")
            break
        if state == "error":
            error = kubectl_bin(
                "get", "psmdb-restore", restore_name, "-o", "jsonpath={.status.error}", check=False
            ).strip()
            raise AssertionError(f"psmdb-restore/{restore_name} is in error state: {error}")
        time.sleep(5)
    else:
        raise TimeoutError(
            f"Timeout waiting for psmdb-restore/{restore_name} to reach {target_state} state. "
            f"Last state: {state or 'unknown'}"
        )

    if wait_consistency:
        wait_cluster_consistency(cluster_name)


def wait_for_pbm_operations(cluster_name: str, timeout: int = 2700) -> None:
    """Wait until no PBM operation is running on the cluster."""
    logger.info(f"Waiting for PBM operations on {cluster_name} to finish")
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        out = kubectl_bin(
            "exec",
            f"{cluster_name}-rs0-0",
            "-c",
            "backup-agent",
            "--",
            "pbm",
            "status",
            "-o",
            "json",
            "-s",
            "running",
            check=False,
        )
        try:
            running = json.loads(out).get("running") or {}
        except json.JSONDecodeError:
            running = {"opID": "unknown"}
        if running.get("opID") is None:
            logger.info(f"No PBM operation is running on {cluster_name}")
            return
        time.sleep(5)

    raise TimeoutError(f"Timeout waiting for PBM operations on {cluster_name} to finish")
