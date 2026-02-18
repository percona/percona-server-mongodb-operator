import json
import logging
import subprocess
import time

logger = logging.getLogger(__name__)


def kubectl_bin(*args: str, check: bool = True, input_data: str = "") -> str:
    """Execute kubectl command"""
    cmd = ["kubectl"] + list(args)
    logger.debug(" ".join(map(str, cmd)))
    result = subprocess.run(cmd, check=check, capture_output=True, text=True, input=input_data)

    if result.stderr:
        logger.warning(f"kubectl error: {result.stderr}")

    if result.returncode != 0 and not result.stdout:
        return result.stderr

    return result.stdout


def wait_pod(pod_name: str, timeout: int = 360) -> None:
    """Wait for pod to be ready."""
    start_time = time.time()
    logger.info(f"Waiting for pod/{pod_name} to be ready...")
    while time.time() - start_time < timeout:
        try:
            result = kubectl_bin(
                "get",
                "pod",
                pod_name,
                "-o",
                "jsonpath={.status.conditions[?(@.type=='Ready')].status}",
            ).strip("'")
            if result == "True":
                logger.info(f"Pod {pod_name} is ready")
                return
        except subprocess.CalledProcessError:
            pass
        time.sleep(1)

    status = (
        kubectl_bin(
            "get",
            "pod",
            pod_name,
            "-o",
            "jsonpath={.status.phase} (Ready={.status.conditions[?(@.type=='Ready')].status})",
            check=False,
        ).strip()
        or "not found"
    )
    raise TimeoutError(f"Timeout waiting for {pod_name} to be ready. Last status: {status}")


def wait_for_running(
    cluster_name: str, expected_pods: int, check_cluster_readyness: bool = True, timeout: int = 600
) -> None:
    """Wait for pods to be in running state using custom label selector"""
    last_pod = expected_pods - 1
    rs_name = cluster_name.split("-")[-1]

    for i in range(last_pod + 1):
        if i == last_pod and get_jsonpath(cluster_name, rs_name, "arbiter.enabled") == "true":
            wait_pod(f"{cluster_name}-arbiter-0")
        else:
            wait_pod(f"{cluster_name}-{i}")

    for pod_type, path_prefix in [("nv", "non_voting"), ("hidden", "hidden")]:
        if get_jsonpath(cluster_name, rs_name, f"{path_prefix}.enabled") == "true":
            size = get_jsonpath(cluster_name, rs_name, f"{path_prefix}.size")
            if size:
                for i in range(int(size)):
                    wait_pod(f"{cluster_name}-{pod_type}-{i}")

    cluster_name = cluster_name.replace(f"-{rs_name}", "")
    if check_cluster_readyness:
        start_time = time.time()
        logger.info(f"Waiting for cluster {cluster_name} readiness")
        while time.time() - start_time < timeout:
            try:
                state = kubectl_bin(
                    "get", "psmdb", cluster_name, "-o", "jsonpath={.status.state}"
                ).strip("'")
                if state == "ready":
                    logger.info(f"Cluster {cluster_name} is ready")
                    return
            except subprocess.CalledProcessError:
                pass
            time.sleep(1)

        state = (
            kubectl_bin(
                "get",
                "psmdb",
                cluster_name,
                "-o",
                "jsonpath={.status.state}",
                check=False,
            ).strip("'")
            or "unknown"
        )
        raise TimeoutError(f"Timeout waiting for {cluster_name} to be ready. Last state: {state}")


def wait_for_delete(resource: str, timeout: int = 180) -> None:
    """Wait for a specific resource to be deleted"""
    logger.info(f"Waiting for {resource} to be deleted")
    time.sleep(1)
    try:
        kubectl_bin("wait", "--for=delete", resource, f"--timeout={timeout}s")
    except subprocess.CalledProcessError as e:
        raise TimeoutError(f"Resource {resource} was not deleted within {timeout}s") from e
    logger.info(f"{resource} was deleted")


def get_jsonpath(cluster_name: str, rs_name: str, path: str) -> str:
    """Get value from PSMDB resource using JSONPath"""
    jsonpath = f'{{.spec.replsets[?(@.name=="{rs_name}")].{path}}}'
    try:
        return kubectl_bin("get", "psmdb", cluster_name, "-o", f"jsonpath={jsonpath}")
    except subprocess.CalledProcessError:
        return ""


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
        excluded = ("kube-", "default", "psmdb-operator", "openshift", "gke-", "gmp-")

        namespaces = [
            parts[0]
            for line in result.strip().splitlines()
            if (parts := line.split())
            and len(parts) == 2
            and not any(ex in parts[0] for ex in excluded)
            and parts[1] != "Terminating"
        ]

        if namespaces:
            kubectl_bin("delete", "ns", *namespaces)
    except subprocess.CalledProcessError:
        logger.error("Failed to clean namespaces")


def detect_k8s_provider(provider: str) -> str:
    """Detect if the Kubernetes provider matches the given string"""
    try:
        output = kubectl_bin("version", "-o", "json")
        git_version = json.loads(output)["serverVersion"]["gitVersion"]
        return "1" if provider in git_version else "0"
    except Exception as e:
        logger.error(f"Failed to detect Kubernetes provider: {e}")
        return "0"


def get_k8s_versions() -> tuple[str, str]:
    """Get Kubernetes git version and semantic version."""
    output = kubectl_bin("version", "-o", "json")
    version_info = json.loads(output)["serverVersion"]

    git_version = version_info["gitVersion"]
    major = version_info["major"]
    minor = version_info["minor"].rstrip("+")
    kube_version = f"{major}.{minor}"

    return git_version, kube_version


def is_openshift() -> str:
    """Detect if running on OpenShift. Returns '1' or ''."""
    try:
        result = subprocess.run(["oc", "get", "projects"], capture_output=True)
        return "1" if result.returncode == 0 else ""
    except FileNotFoundError:
        return ""


def is_minikube() -> str:
    """Detect if running on Minikube. Returns '1' or ''."""
    result = kubectl_bin("get", "nodes", check=False)
    return "1" if any(line.startswith("minikube") for line in result.splitlines()) else ""
