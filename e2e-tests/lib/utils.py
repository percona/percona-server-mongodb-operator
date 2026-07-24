import logging
import os
import re
import time
from collections.abc import Callable
from subprocess import CalledProcessError
from typing import Any, ClassVar, TypedDict

from rich.highlighter import RegexHighlighter
from rich.theme import Theme

from .cli import git_bin

logger = logging.getLogger(__name__)


class Paths(TypedDict):
    """Filesystem paths relative to a test file (values are strings)."""

    test_file: str
    test_dir: str
    conf_dir: str
    src_dir: str


def env_bool(name: str, default: bool = False) -> bool:
    """Parse a boolean-ish environment variable (1/true/yes/on)."""
    value = os.environ.get(name)
    if value is None:
        return default
    return value.strip().lower() in ("1", "true", "yes", "on")


class K8sHighlighter(RegexHighlighter):
    """Highlight Kubernetes resources in logs."""

    base_style = "k8s."
    highlights: ClassVar[list[str]] = [
        r"(?P<tool>kubectl)\s+(?P<action>exec)\s+(?P<pod>[\w-]+)\s+--\s+(?P<timeout>timeout\s+\d+)\s+(?P<client>mongosh)\s+(?P<uri>mongodb(?:\+srv)?://[^ ]+)\s+--eval\s+(?P<eval>\S.+?)\s+--quiet",
        r"(?P<pod>(?<!\S)pod\s+[\w-]+)",
        r"(?P<resource>(?<!\S)(pod|psmdb|namespace|cluster|secret|configmap|pvc|service)/[\w-]+)",
        r"(?P<namespace>(?<!\S)(?:\w+\/[\w-]+-\w+|[\w-]+-\d+\b))",
        r"(?P<bad_state>pending|failed|error|deleted)",
        r"(?P<state>ready|running)",
    ]


k8s_theme = Theme(
    {
        "logging.level.debug": "blue",
        "logging.level.info": "green",
        "k8s.pod": "green",
        "k8s.uri": "magenta",
        "k8s.resource": "cyan",
        "k8s.namespace": "green",
        "k8s.bad_state": "red",
        "k8s.state": "blue",
    }
)


def retry(
    func: Callable[[], Any],
    max_attempts: int = 5,
    delay: int = 1,
    condition: Callable[[Any], bool] | None = None,
    backoff: float = 1.0,
) -> Any:
    """Retry a function until it succeeds or max attempts reached.

    With backoff > 1.0 the delay grows exponentially per attempt.
    """
    for attempt in range(max_attempts):
        try:
            result = func()
            if condition is None or condition(result):
                return result
        except Exception:
            if attempt == max_attempts - 1:
                raise

        time.sleep(delay * backoff**attempt)

    raise RuntimeError(f"Max attempts ({max_attempts}) reached")


def qualify_image(ref: str, registry_full: str) -> str:
    """Prefix percona/perconalab images with the registry (mirrors bash with_registry)."""
    if not ref:
        return ref
    host = ref.split("/")[0]
    if "." in host or ":" in host or host == "localhost":
        return ref
    if ref.startswith(("percona/", "perconalab/")):
        return f"{registry_full}{ref}"
    return ref


def get_git_commit() -> str:
    result = git_bin("rev-parse", "HEAD", check=False)
    return result.stdout.strip()


def get_cr_version() -> str:
    """Get CR version from cr.yaml"""
    try:
        with open(
            os.path.realpath(
                os.path.join(os.path.dirname(__file__), "..", "..", "deploy", "cr.yaml")
            )
        ) as f:
            return next(line.split()[1] for line in f if "crVersion" in line)
    except (StopIteration, Exception) as e:
        logger.error(f"Failed to get CR version: {e}")
        raise RuntimeError("CR version not found in cr.yaml")


def get_git_branch() -> str:
    """Get current git branch or version from environment variable"""
    if version := os.environ.get("VERSION"):
        return version

    try:
        result = git_bin("rev-parse", "--abbrev-ref", "HEAD")
        branch = result.stdout.strip()
    except (CalledProcessError, FileNotFoundError):
        return "unknown"

    return re.sub(r"[^a-zA-Z0-9-]", "-", branch.lower())
