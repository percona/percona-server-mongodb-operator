import logging
import time

from .kubectl import kubectl_bin, wait_cr_state

logger = logging.getLogger(__name__)

_AGENT_READY_MARKER = "listening for the commands"


def wait_backup_agent(pod: str, timeout: int = 1800) -> None:
    """Wait until the pbm-agent sidecar of a pod is listening for commands."""
    logger.info(f"Waiting for pbm-agent in {pod}")
    deadline = time.time() + timeout
    while time.time() < deadline:
        logs = kubectl_bin("logs", pod, "-c", "backup-agent", check=False)
        if _AGENT_READY_MARKER in logs:
            logger.info(f"pbm-agent is ready in {pod}")
            return
        time.sleep(5)

    raise TimeoutError(f"pbm-agent in {pod} was not ready within {timeout}s")


def wait_backup(name: str, target_state: str = "ready", timeout: int = 600) -> None:
    wait_cr_state("psmdb-backup", name, target_state, timeout)


def wait_restore(name: str, target_state: str = "ready", timeout: int = 1800) -> None:
    wait_cr_state("psmdb-restore", name, target_state, timeout)
