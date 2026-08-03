import logging
import subprocess

logger = logging.getLogger(__name__)


def _run_bin(
    executable: str,
    *args: str,
    check: bool = True,
    capture: bool = False,
) -> subprocess.CompletedProcess[str]:
    cmd = [executable, *args]
    logger.debug(" ".join(cmd))
    return subprocess.run(cmd, check=check, capture_output=capture, text=True)


def git_bin(
    *args: str, check: bool = True, capture: bool = True
) -> subprocess.CompletedProcess[str]:
    """Execute a git command."""
    return _run_bin("git", *args, check=check, capture=capture)


def helm_bin(
    *args: str, check: bool = True, capture: bool = False
) -> subprocess.CompletedProcess[str]:
    """Execute a helm command."""
    return _run_bin("helm", *args, check=check, capture=capture)


def oc_bin(
    *args: str, check: bool = True, capture: bool = False
) -> subprocess.CompletedProcess[str]:
    """Execute an OpenShift CLI command."""
    return _run_bin("oc", *args, check=check, capture=capture)
