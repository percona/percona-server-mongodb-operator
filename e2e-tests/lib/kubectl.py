import json
import logging
import os
import re
import subprocess
import time
import urllib.request
from collections.abc import Callable
from dataclasses import dataclass
from pathlib import Path

from .arch import add_arch_to_manifest, arch, arch_run_overrides

logger = logging.getLogger(__name__)

# keep in sync with KUBECTL_TRANSIENT_ERRORS in e2e-tests/functions
TRANSIENT_ERRORS = re.compile(
    r"ENHANCE_YOUR_CALM|ResourceExhausted|TooManyRequests|TLS handshake timeout"
    r"|etcdserver: request timed out|the server is currently unable to handle the request"
    r"|Unable to connect to the server|connection reset by peer"
)


def _read_manifest(source: str) -> str | None:
    """Return manifest text for a local file or http(s) URL, else None."""
    if os.path.isfile(source):
        return Path(source).read_text()
    if source.startswith(("http://", "https://")):
        with urllib.request.urlopen(source) as response:
            return str(response.read().decode())
    return None


def _inline_apply_files(args: list[str]) -> tuple[list[str], str]:
    """Read local/remote `-f` manifests, patch with arch scheduling, feed via stdin."""
    new_args: list[str] = []
    manifests: list[str] = []
    i = 0
    while i < len(args):
        arg = args[i]
        if arg in ("-f", "--filename") and i + 1 < len(args):
            filename, step = args[i + 1], 2
        elif arg.startswith(("-f=", "--filename=")):
            filename, step = arg.split("=", 1)[1], 1
        else:
            new_args.append(arg)
            i += 1
            continue

        manifest = None if filename == "-" else _read_manifest(filename)
        if manifest is not None:
            manifests.append(manifest)
        else:
            new_args.extend(args[i : i + step])
        i += step

    if not manifests:
        return args, ""
    new_args.extend(["-f", "-"])
    return new_args, add_arch_to_manifest("\n---\n".join(manifests))


def _apply_arch_scheduling(args: list[str], input_data: str) -> tuple[list[str], str]:
    """Inject arch nodeSelector/tolerations into `apply` and `run` commands."""
    if not args or not arch():
        return args, input_data

    if args[0] == "apply":
        if input_data:
            return args, add_arch_to_manifest(input_data)
        return _inline_apply_files(args)

    if args[0] == "run" and not any(a.split("=", 1)[0] == "--overrides" for a in args):
        override = f"--overrides={arch_run_overrides()}"
        if "--" in args:
            idx = args.index("--")
            return args[:idx] + [override] + args[idx:], input_data
        return args + [override], input_data

    return args, input_data


def kubectl_bin(
    *args: str, check: bool = True, input_data: str = "", return_stderr: bool = False
) -> str:
    """Execute kubectl command"""
    arg_list, input_data = _apply_arch_scheduling(list(args), input_data)
    cmd = ["kubectl"] + arg_list
    logger.debug(" ".join(map(str, cmd)))
    for attempt in range(10):
        result = subprocess.run(cmd, capture_output=True, text=True, input=input_data, check=False)
        if result.returncode == 0 or attempt == 9 or not TRANSIENT_ERRORS.search(result.stderr):
            break
        delay = min(5 * (attempt + 1), 30)
        logger.warning(
            "kubectl transient error, retrying in %ds: %s", delay, result.stderr.strip()
        )
        time.sleep(delay)

    if result.stderr:
        log = logger.warning if check else logger.debug
        log("kubectl error: %s", result.stderr.strip())

    if check and result.returncode != 0:
        raise subprocess.CalledProcessError(
            result.returncode, cmd, output=result.stdout, stderr=result.stderr
        )

    if result.returncode != 0 and return_stderr and not result.stdout:
        return result.stderr

    return result.stdout


# Container waiting reasons that do not fix themselves. A wait fails once one of them has
# lasted FATAL_GRACE seconds, instead of running into its full timeout. The grace period
# covers short-lived cases such as a registry rate limit or a secret created a bit later.
FATAL_WAIT_REASONS = frozenset(
    {
        "ErrImagePull",
        "ImagePullBackOff",
        "InvalidImageName",
        "CreateContainerConfigError",
        "CreateContainerError",
    }
)
FATAL_GRACE = 120
PROBLEM_CHECK_INTERVAL = 10


@dataclass(frozen=True)
class Problem:
    """What a wait found wrong with the resource. `fatal` means it will not recover."""

    detail: str
    fatal: bool = False


def _wait_for(
    what: str,
    resource: list[str],
    jsonpath: str,
    expected: str,
    timeout: int,
    find_problem: Callable[[], Problem | None] | None = None,
) -> None:
    """Poll `kubectl get` until the jsonpath value equals `expected`.

    Raises TimeoutError after `timeout` seconds, or RuntimeError once `find_problem`
    has reported a fatal problem for FATAL_GRACE seconds. Both errors carry the last
    status and the last problem seen.
    """
    logger.info(f"Waiting for {what} to be ready...")
    start = time.monotonic()
    deadline = start + timeout
    status = ""
    problem: Problem | None = None
    fatal_since: float | None = None
    next_check = start + PROBLEM_CHECK_INTERVAL
    while time.monotonic() < deadline:
        status = kubectl_bin(
            "get", *resource, "-o", f"jsonpath={jsonpath}", check=False, return_stderr=True
        ).strip("'\n ")
        if status == expected:
            logger.info(f"{what} is ready after {time.monotonic() - start:.0f}s")
            return
        now = time.monotonic()
        if find_problem and now >= next_check:
            next_check = now + PROBLEM_CHECK_INTERVAL
            problem = find_problem()
            if problem and problem.fatal:
                fatal_since = fatal_since or now
                if now - fatal_since >= FATAL_GRACE:
                    raise RuntimeError(
                        f"{what} cannot become ready, stopped after {now - start:.0f}s: "
                        f"{problem.detail}. Last status: {status or 'unknown'}"
                    )
            else:
                fatal_since = None
        time.sleep(1)

    detail = f". Last problem: {problem.detail}" if problem else ""
    raise TimeoutError(
        f"Timeout waiting for {what} to be ready after {timeout}s. "
        f"Last status: {status or 'unknown'}{detail}"
    )


def _pod_problem(pod_name: str) -> Problem | None:
    """Report the containers of a pod that are waiting, and whether any reason is fatal."""
    out = kubectl_bin("get", "pod", pod_name, "-o", "json", check=False)
    try:
        status = json.loads(out).get("status", {})
    except json.JSONDecodeError:
        return None
    waiting = []
    fatal = False
    for container in status.get("initContainerStatuses", []) + status.get("containerStatuses", []):
        state = container.get("state", {}).get("waiting")
        if not state or state.get("reason") in (None, "PodInitializing", "ContainerCreating"):
            continue
        reason = state["reason"]
        fatal = fatal or reason in FATAL_WAIT_REASONS
        message = state.get("message", "")
        waiting.append(f"{container.get('name')}: {reason}" + (f" ({message})" if message else ""))
    if not waiting:
        return None
    return Problem("; ".join(waiting), fatal)


def wait_pod(pod_name: str, timeout: int = 360) -> None:
    """Wait for pod to be ready. Fails early when a container cannot start."""
    _wait_for(
        f"pod/{pod_name}",
        ["pod", pod_name],
        "{.status.phase} {.status.conditions[?(@.type=='Ready')].status}",
        "Running True",
        timeout,
        find_problem=lambda: _pod_problem(pod_name),
    )


def _wait_cluster_ready(cluster_name: str, timeout: int) -> None:
    _wait_for(
        f"cluster {cluster_name}", ["psmdb", cluster_name], "{.status.state}", "ready", timeout
    )


def _replset_field(cluster_name: str, rs_name: str, path: str) -> str:
    """Read a field of a replset from the PSMDB spec, '' when unavailable."""
    jsonpath = f'{{.spec.replsets[?(@.name=="{rs_name}")].{path}}}'
    return kubectl_bin(
        "get", "psmdb", cluster_name, "-o", f"jsonpath={jsonpath}", check=False
    ).strip()


def wait_for_running(
    cluster_name: str, expected_pods: int, check_cluster_readyness: bool = True, timeout: int = 600
) -> None:
    """Wait for all pods of a replset (incl. arbiter/nonvoting/hidden) to be ready."""
    last_pod = expected_pods - 1
    rs_name = cluster_name.split("-")[-1]

    for i in range(expected_pods):
        if i == last_pod and _replset_field(cluster_name, rs_name, "arbiter.enabled") == "true":
            wait_pod(f"{cluster_name}-arbiter-0")
        else:
            wait_pod(f"{cluster_name}-{i}")

    for pod_type, component in (("nv", "nonvoting"), ("hidden", "hidden")):
        if _replset_field(cluster_name, rs_name, f"{component}.enabled") == "true":
            size = _replset_field(cluster_name, rs_name, f"{component}.size")
            for i in range(int(size or 0)):
                wait_pod(f"{cluster_name}-{pod_type}-{i}")

    time.sleep(10)
    if check_cluster_readyness:
        _wait_cluster_ready(cluster_name.replace(f"-{rs_name}", ""), timeout)


def wait_for_delete(resource: str, timeout: int = 180) -> None:
    """Wait for a specific resource to be deleted"""
    logger.info(f"Waiting for {resource} to be deleted")
    time.sleep(1)
    try:
        kubectl_bin("wait", "--for=delete", resource, f"--timeout={timeout}s")
    except subprocess.CalledProcessError as e:
        raise TimeoutError(f"Resource {resource} was not deleted within {timeout}s") from e
    logger.info(f"{resource} was deleted")


def wait_for_cluster_state(cluster_name: str, target_state: str, timeout: int = 1500) -> None:
    """Wait for psmdb cluster to reach a specific state (e.g. 'ready', 'paused')."""
    _wait_for(
        f"cluster {cluster_name} state={target_state}",
        ["psmdb", cluster_name],
        "{.status.state}",
        target_state,
        timeout,
    )


def wait_cluster_consistency(cluster_name: str, timeout: int = 180) -> None:
    """Wait for the PSMDB cluster status to settle back to ready."""
    time.sleep(5)  # give the operator time to leave the "ready" state first
    _wait_cluster_ready(cluster_name, timeout)


def clean_all_namespaces() -> None:
    """Delete all namespaces except system ones."""
    try:
        logger.info("Cleaning up all old namespaces")
        result = kubectl_bin(
            "get",
            "ns",
            "-o",
            "jsonpath={range .items[*]}{.metadata.name} {.status.phase}{'\\n'}{end}",
        )
        excluded = [
            "kube-",
            "default",
            "psmdb-operator",
            "openshift",
            "gke-",
            "gmp-",
            "cattle-",
            "fleet-",
            "cluster-fleet-",
            "local",
            "longhorn-",
            "metallb-",
            "p-",
        ]

        if os.environ.get("RANCHER") == "1":
            excluded.append("cert-manager")
        namespaces = [
            parts[0]
            for line in result.strip().splitlines()
            if (parts := line.split())
            and len(parts) == 2
            and not any(parts[0].startswith(ex) for ex in excluded)
            and parts[1] != "Terminating"
        ]

        if namespaces:
            kubectl_bin("delete", "ns", "--wait=false", *namespaces)
    except subprocess.CalledProcessError:
        logger.error("Failed to clean namespaces")


def get_k8s_versions() -> tuple[str, str]:
    """Get Kubernetes git version and semantic version."""
    out = json.loads(kubectl_bin("version", "-o", "json"))["serverVersion"]
    kube_version = re.sub(r"[^0-9.]+", "", f"{out['major']}.{out['minor']}")
    return out["gitVersion"], kube_version


def detect_platform() -> str:
    """Detect the Kubernetes platform.

    Returns one of: openshift, eks, gke, aks, kind, minikube, rancher, unknown.
    """
    if kubectl_bin(
        "api-resources", "--api-group=project.openshift.io", "-o", "name", check=False
    ).strip():
        return "openshift"

    labels = kubectl_bin("get", "nodes", "-o", "jsonpath={.items[0].metadata.labels}", check=False)
    for needle, name in (
        ("eksctl.io", "eks"),
        ("cloud.google.com/gke", "gke"),
        ("kubernetes.azure.com", "aks"),
        ("minikube.k8s.io", "minikube"),
    ):
        if needle in labels:
            return name

    provider_id = kubectl_bin(
        "get", "nodes", "-o", "jsonpath={.items[0].spec.providerID}", check=False
    ).lower()
    for needle, name in (("kind://", "kind"), ("rke2", "rancher")):
        if needle in provider_id:
            return name

    return "unknown"


def detect_arch() -> str:
    """Detect target arch for scheduling: 'arm64' on arm64-only clusters, else ''."""
    arches = kubectl_bin(
        "get",
        "nodes",
        "-o",
        r'jsonpath={range .items[*]}{.metadata.labels.kubernetes\.io/arch}{"\n"}{end}',
        check=False,
    )
    unique = {a for a in arches.split() if a}
    return "arm64" if unique == {"arm64"} else ""
