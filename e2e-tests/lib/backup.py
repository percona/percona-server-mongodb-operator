import logging
import time
from pathlib import Path

import yaml

from .kubectl import kubectl_bin

logger = logging.getLogger(__name__)

_POLL_INTERVAL = 2


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
    """Wait for a psmdb-backup to reach target_state, failing fast on 'error'."""
    logger.info(f"Waiting for {backup_name} to reach {target_state} state")
    deadline = time.monotonic() + timeout

    while True:
        state = kubectl_bin(
            "get",
            "psmdb-backup",
            backup_name,
            "-o",
            "jsonpath={.status.state}",
            check=False,
        ).strip()

        if state == target_state:
            logger.info(f"Backup {backup_name} reached {target_state}")
            return

        if state == "error" or time.monotonic() >= deadline:
            error = kubectl_bin(
                "get",
                "psmdb-backup",
                backup_name,
                "-o",
                "jsonpath={.status.error}",
                check=False,
            ).strip()
            raise AssertionError(
                f"Backup {backup_name} is in '{state}' state, expected '{target_state}'"
                + (f": {error}" if error else "")
            )

        time.sleep(_POLL_INTERVAL)
