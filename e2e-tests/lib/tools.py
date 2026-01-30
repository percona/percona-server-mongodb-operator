import base64
import json
import logging
import os
import re
import subprocess
import tempfile
import time
import urllib.parse
from pathlib import Path
from typing import Any, Callable, Dict, Optional

import yaml
from deepdiff import DeepDiff

logger = logging.getLogger(__name__)

RED = "\033[31m"
GREEN = "\033[32m"
YELLOW = "\033[33m"
BLUE = "\033[34m"
MAGENTA = "\033[35m"
CYAN = "\033[36m"
RESET = "\033[0m"


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


def cat_config(config_file: str) -> str:
    """Process config file with yq transformations"""
    with open(config_file, "r") as f:
        config = yaml.safe_load(f)

    # Apply transformations similar to yq eval commands
    if "spec" in config:
        spec = config["spec"]

        if "image" not in spec or spec["image"] is None:
            spec["image"] = os.environ.get("IMAGE_MONGOD")

        if "pmm" in spec:
            spec["pmm"]["image"] = os.environ.get("IMAGE_PMM_CLIENT")

        if "initImage" in spec:
            spec["initImage"] = os.environ.get("IMAGE")

        if "backup" in spec:
            spec["backup"]["image"] = os.environ.get("IMAGE_BACKUP")

        if "upgradeOptions" not in spec:
            spec["upgradeOptions"] = {}
        spec["upgradeOptions"]["apply"] = "Never"

    return yaml.dump(config)


def apply_cluster(config_file: str) -> None:
    """Apply cluster configuration"""
    logger.info("Creating PSMDB cluster")
    config_yaml = cat_config(config_file)

    if not os.environ.get("SKIP_BACKUPS_TO_AWS_GCP_AZURE"):
        kubectl_bin("apply", "-f", "-", input_data=config_yaml)
    else:
        config = yaml.safe_load(config_yaml)
        if "spec" in config and "backup" in config["spec"] and "tasks" in config["spec"]["backup"]:
            # Keep only the first backup task, remove cloud backup tasks
            config["spec"]["backup"]["tasks"] = config["spec"]["backup"]["tasks"][:1]
        kubectl_bin("apply", "-f", "-", input_data=yaml.dump(config))


def delete_crd_rbac(src_dir: Path) -> None:
    logger.info("Deleting old CRDs and RBACs")
    crd_path = (src_dir / "deploy" / "crd.yaml").resolve()

    docs = list(yaml.safe_load_all(crd_path.read_text()))
    crd_names = []
    resource_kinds = []
    for doc in docs:
        if doc and doc.get("kind") == "CustomResourceDefinition":
            crd_names.append(doc["metadata"]["name"])
            group = doc["spec"]["group"]
            plural = doc["spec"]["names"]["plural"]
            resource_kinds.append(f"{plural}.{group}")

    kubectl_bin("delete", "-f", str(crd_path), "--ignore-not-found", "--wait=false", check=False)

    for kind in resource_kinds:
        try:
            items_json = kubectl_bin("get", kind, "--all-namespaces", "-o", "json")
            data = json.loads(items_json)
            for item in data.get("items", []):
                ns = item["metadata"]["namespace"]
                name = item["metadata"]["name"]
                kubectl_bin(
                    "patch",
                    kind,
                    "-n",
                    ns,
                    name,
                    "--type=merge",
                    "-p",
                    '{"metadata":{"finalizers":[]}}',
                )
        except subprocess.CalledProcessError:
            pass

    for name in crd_names:
        kubectl_bin("wait", "--for=delete", "crd", name, check=False)


def check_crd_for_deletion(file_path: str) -> None:
    """Check and remove finalizers from CRDs to allow deletion"""
    with open(file_path, "r") as f:
        yaml_content = f.read()

    for doc in yaml_content.split("---"):
        if not doc.strip():
            continue
        try:
            parsed_doc = yaml.safe_load(doc)
            if not parsed_doc or "metadata" not in parsed_doc:
                continue

            crd_name = parsed_doc["metadata"]["name"]

            result = kubectl_bin(
                "get",
                f"crd/{crd_name}",
                "-o",
                "jsonpath={.status.conditions[-1].type}",
                "--ignore-not-found",
            )
            is_crd_terminating = result.strip() == "Terminating"

            if is_crd_terminating:
                logger.info(f"Removing finalizers from CRD {crd_name} to allow deletion")
                kubectl_bin(
                    "patch",
                    f"crd/{crd_name}",
                    "--type=merge",
                    "-p",
                    '{"metadata":{"finalizers":[]}}',
                )
                try:
                    kubectl_bin(
                        "patch",
                        crd_name,
                        "--all-namespaces",
                        "--type=merge",
                        "-p",
                        '{"metadata":{"finalizers":[]}}',
                    )
                except Exception as patch_error:
                    logger.warning(
                        f"Could not patch {crd_name} instances (may not exist): {patch_error}"
                    )

        except yaml.YAMLError as yaml_error:
            logger.error(f"Error parsing YAML document: {yaml_error}")
        except Exception as e:
            logger.error(f"Error removing finalizers from CRD: {e}")


def deploy_operator(test_dir: str, src_dir: str) -> None:
    """Deploy the operator with simplified logic."""
    logger.info("Start PSMDB operator")
    operator_ns = os.environ.get("OPERATOR_NS")

    crd_file = f"{test_dir}/conf/crd.yaml"
    if not os.path.isfile(crd_file):
        crd_file = f"{src_dir}/deploy/crd.yaml"

    kubectl_bin("apply", "--server-side", "--force-conflicts", "-f", crd_file)

    rbac_type = "cw-rbac" if operator_ns else "rbac"
    operator_file = f"{src_dir}/deploy/{'cw-' if operator_ns else ''}operator.yaml"

    apply_rbac(src_dir, rbac_type)

    with open(operator_file, "r") as f:
        data = yaml.safe_load(f)

    for container in data["spec"]["template"]["spec"]["containers"]:
        container["image"] = os.environ.get("IMAGE")
        if "env" in container:
            env_vars = {env["name"]: env for env in container["env"]}
            if "DISABLE_TELEMETRY" in env_vars:
                env_vars["DISABLE_TELEMETRY"]["value"] = "true"
            if "LOG_LEVEL" in env_vars:
                env_vars["LOG_LEVEL"]["value"] = "DEBUG"

    yaml_content = yaml.dump(data, default_flow_style=False)
    kubectl_bin("apply", "-f", "-", input_data=yaml_content)
    operator_pod = get_operator_pod()
    wait_pod(operator_pod)

    logs = kubectl_bin("logs", operator_pod)
    startup_logs = [line for line in logs.splitlines() if "Manager starting up" in line]
    if startup_logs:
        logger.info(f"Operator startup: {startup_logs[0]}")
    else:
        logger.warning("No 'Manager starting up' message found in logs")


def get_operator_pod() -> str:
    """Get the operator pod name"""
    args = [
        "get",
        "pods",
        "--selector=name=percona-server-mongodb-operator",
        "-o",
        "jsonpath={.items[].metadata.name}",
    ]
    operator_ns = os.environ.get("OPERATOR_NS")
    if operator_ns:
        args.extend(["-n", operator_ns])
    try:
        out = kubectl_bin(*args)
        names = [n for n in out.strip().split() if n]
        if not names:
            raise RuntimeError(
                "No Running operator pod found. Ensure the operator deployment succeeded"
            )
        if len(names) > 1:
            raise RuntimeError(f"Multiple operator pods found: {names}")
        return names[0]
    except Exception as e:
        raise RuntimeError(f"Failed to get operator pod: {e}") from e


def apply_rbac(src_dir: str, rbac: str = "rbac") -> None:
    """Apply RBAC YAML with namespace substitution"""
    operator_ns = os.getenv("OPERATOR_NS", "psmdb-operator")
    path = Path(src_dir) / "deploy" / f"{rbac}.yaml"

    yaml_content = path.read_text()
    modified_yaml = re.sub(
        r"^(\s*)namespace:\s*.*$", rf"\1namespace: {operator_ns}", yaml_content, flags=re.MULTILINE
    )

    args = ["apply", "-f", "-"]
    if os.getenv("OPERATOR_NS"):
        args = ["apply", "-n", operator_ns, "-f", "-"]

    kubectl_bin(*args, input_data=modified_yaml)


def clean_all_namespaces() -> None:
    """Delete all namespaces except system ones."""
    try:
        logger.info("Cleaning up all old namespaces")
        result = kubectl_bin("get", "ns", "-o", "jsonpath={.items[*].metadata.name}")
        excluded_prefixes = {
            "kube-",
            "default",
            "Terminating",
            "psmdb-operator",
            "openshift",
            "gke-",
            "gmp-",
        }

        namespaces = [
            ns
            for ns in result.strip().split()
            if not any(prefix in ns for prefix in excluded_prefixes)
        ]

        if namespaces:
            kubectl_bin("delete", "ns", *namespaces)
    except subprocess.CalledProcessError:
        logger.error("Failed to clean namespaces")


def wait_pod(pod_name: str, timeout: int = 360) -> None:
    """Wait for pod to be ready."""
    start_time = time.time()
    logger.info(f"Waiting for {CYAN}pod/{pod_name}{RESET} to be ready...")
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
                logger.info(f"Pod {CYAN}{pod_name}{RESET} is ready")
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

    # Wait for regular pods
    for i in range(last_pod + 1):
        if i == last_pod and get_jsonpath(cluster_name, rs_name, "arbiter.enabled") == "true":
            wait_pod(f"{cluster_name}-arbiter-0")
        else:
            wait_pod(f"{cluster_name}-{i}")

    # Wait for non-voting pods if enabled
    if get_jsonpath(cluster_name, rs_name, "non_voting.enabled") == "true":
        size = get_jsonpath(cluster_name, rs_name, "non_voting.size")
        if size:
            for i in range(int(size)):
                wait_pod(f"{cluster_name}-nv-{i}")

    # Wait for hidden pods if enabled
    if get_jsonpath(cluster_name, rs_name, "hidden.enabled") == "true":
        size = get_jsonpath(cluster_name, rs_name, "hidden.size")
        if size:
            for i in range(int(size)):
                wait_pod(f"{cluster_name}-hidden-{i}")

    cluster_name = cluster_name.replace(f"-{rs_name}", "")
    if check_cluster_readyness:
        start_time = time.time()
        logger.info(f"Waiting for cluster {CYAN}{cluster_name}{RESET} readiness")
        while time.time() - start_time < timeout:
            try:
                state = kubectl_bin(
                    "get", "psmdb", cluster_name, "-o", "jsonpath={.status.state}"
                ).strip("'")
                if state == "ready":
                    logger.info(f"Cluster {CYAN}{cluster_name}{RESET} is ready")
                    return
            except subprocess.CalledProcessError:
                pass
            time.sleep(1)

        # Get state for error message
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
    logger.info(f"Waiting for {CYAN}{resource}{RESET} to be deleted")
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


def compare_kubectl(test_dir: str, resource: str, namespace: str, postfix: str = "") -> None:
    """Compare kubectl resource with expected output using yq filtering"""
    expected_result = f"{test_dir}/compare/{resource.replace('/', '_')}{postfix}.yml"

    try:
        actual_yaml = kubectl_bin("get", resource, "-o", "yaml")
        with open(expected_result, "r") as f:
            expected_yaml = f.read()

        filtered_actual = filter_yaml(actual_yaml, namespace)
        filtered_expected = filter_yaml(expected_yaml, namespace)

        actual_data = yaml.safe_load(filtered_actual)
        expected_data = yaml.safe_load(filtered_expected)

        diff = DeepDiff(expected_data, actual_data)
        assert not diff, f"YAML files differ: {diff.pretty()}"

    except subprocess.CalledProcessError as e:
        raise ValueError(f"Failed to process resource {resource}: {e}")


def apply_runtime_class(test_dir: str) -> None:
    """Apply runtime class configuration"""

    # from K8s 1.24 and later, runc is used
    logger.info("Applying runc runtime class")
    with open(f"{test_dir}/../conf/container-rc.yaml", "r") as f:
        content = f.read()
    if os.environ.get("EKS"):
        content = content.replace("docker", "runc")
    kubectl_bin("apply", "-f", "-", input_data=content)


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


def is_openshift() -> bool:
    """Detect if running on OpenShift"""
    result = subprocess.run(["oc", "get", "projects"], capture_output=True)
    return result.returncode == 0


def is_minikube() -> bool:
    """Detect if running on Minikube"""
    result = kubectl_bin("get", "nodes", check=False)
    return any(line.startswith("minikube") for line in result.splitlines())


def get_git_commit() -> str:
    result = subprocess.run(["git", "rev-parse", "HEAD"], capture_output=True, text=True)
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
        result = subprocess.run(
            ["git", "rev-parse", "--abbrev-ref", "HEAD"],
            capture_output=True,
            text=True,
            check=True,
        )
        branch = result.stdout.strip()
    except (subprocess.CalledProcessError, FileNotFoundError):
        return "unknown"

    return re.sub(r"[^a-zA-Z0-9-]", "-", branch.lower())


def get_secret_data(secret_name: str, data_key: str) -> str:
    """Get and decode secret data from Kubernetes"""
    try:
        result = kubectl_bin(
            "get", f"secrets/{secret_name}", "-o", f"jsonpath={{.data.{data_key}}}"
        ).strip()
        decoded_data = base64.b64decode(result).decode("utf-8")
        return decoded_data
    except subprocess.CalledProcessError as e:
        logger.error(f"Error: {e.stderr}")
        return ""


def get_user_data(secret_name: str, data_key: str) -> str:
    """Get and URL-encode secret data"""
    secret_data = get_secret_data(secret_name, data_key)
    return urllib.parse.quote(secret_data, safe="")


def filter_yaml(
    yaml_content: str, namespace: str, resource: str = "", skip_generation_check: bool = False
) -> str:
    """Filter YAML content using yq command"""

    # TODO: consider using Python for filtering instead of yq
    yq_filter = f"""
        del(.metadata.ownerReferences[].apiVersion) |
        del(.metadata.managedFields) |
        del(.. | select(has("creationTimestamp")).creationTimestamp) |
        del(.. | select(has("namespace")).namespace) |
        del(.. | select(has("uid")).uid) |
        del(.metadata.resourceVersion) |
        del(.spec.template.spec.containers[].env[] | select(.name == "NAMESPACE")) |
        del(.metadata.selfLink) |
        del(.metadata.annotations."cloud.google.com/neg") |
        del(.metadata.annotations."kubectl.kubernetes.io/last-applied-configuration") |
        del(.. | select(has("image")).image) |
        del(.. | select(has("clusterIP")).clusterIP) |
        del(.. | select(has("clusterIPs")).clusterIPs) |
        del(.. | select(has("dataSource")).dataSource) |
        del(.. | select(has("procMount")).procMount) |
        del(.. | select(has("storageClassName")).storageClassName) |
        del(.. | select(has("finalizers")).finalizers) |
        del(.. | select(has("kubernetes.io/pvc-protection"))."kubernetes.io/pvc-protection") |
        del(.. | select(has("volumeName")).volumeName) |
        del(.. | select(has("volume.beta.kubernetes.io/storage-provisioner"))."volume.beta.kubernetes.io/storage-provisioner") |
        del(.. | select(has("volume.kubernetes.io/storage-provisioner"))."volume.kubernetes.io/storage-provisioner") |
        del(.spec.volumeMode) |
        del(.. | select(has("volume.kubernetes.io/selected-node"))."volume.kubernetes.io/selected-node") |
        del(.. | select(has("percona.com/last-config-hash"))."percona.com/last-config-hash") |
        del(.. | select(has("percona.com/configuration-hash"))."percona.com/configuration-hash") |
        del(.. | select(has("percona.com/ssl-hash"))."percona.com/ssl-hash") |
        del(.. | select(has("percona.com/ssl-internal-hash"))."percona.com/ssl-internal-hash") |
        del(.spec.volumeClaimTemplates[].spec.volumeMode | select(. == "Filesystem")) |
        del(.. | select(has("healthCheckNodePort")).healthCheckNodePort) |
        del(.. | select(has("nodePort")).nodePort) |
        del(.status) |
        (.. | select(tag == "!!str")) |= sub("{namespace}"; "NAME_SPACE") |
        del(.spec.volumeClaimTemplates[].apiVersion) |
        del(.spec.volumeClaimTemplates[].kind) |
        del(.spec.ipFamilies) |
        del(.spec.ipFamilyPolicy) |
        del(.spec.persistentVolumeClaimRetentionPolicy) |
        del(.spec.internalTrafficPolicy) |
        del(.spec.allocateLoadBalancerNodePorts) |
        (.. | select(. == "extensions/v1beta1")) = "apps/v1" |
        (.. | select(. == "batch/v1beta1")) = "batch/v1"
    """

    cmd = ["yq", "eval", yq_filter.strip(), "-"]
    result = subprocess.run(cmd, input=yaml_content, text=True, capture_output=True, check=True)
    filtered_yaml = result.stdout

    # Remove generation for cronjobs or if skip_generation_check is True
    if "cronjob" in resource.lower() or skip_generation_check:
        cmd = ["yq", "eval", "del(.metadata.generation)", "-"]
        result = subprocess.run(
            cmd, input=filtered_yaml, text=True, capture_output=True, check=True
        )
        filtered_yaml = result.stdout

    return filtered_yaml


def get_cloud_secret_default(conf_dir: Optional[Path] = None) -> str:
    """Return default for SKIP_BACKUPS_TO_AWS_GCP_AZURE based on cloud-secret.yml existence."""
    if conf_dir is None:
        conf_dir = Path(__file__).parent.parent / "conf"
    if (conf_dir / "cloud-secret.yml").exists():
        return ""  # Enable cloud backups
    return "1"  # Skip cloud backups


def apply_s3_storage_secrets(conf_dir: str) -> None:
    """Apply secrets for cloud storages."""
    if not os.environ.get("SKIP_BACKUPS_TO_AWS_GCP_AZURE"):
        logger.info("Creating secrets for cloud storages (minio + cloud)")
        kubectl_bin(
            "apply",
            "-f",
            f"{conf_dir}/minio-secret.yml",
            "-f",
            f"{conf_dir}/cloud-secret.yml",
        )
    else:
        logger.info("Creating secrets for cloud storages (minio only)")
        kubectl_bin("apply", "-f", f"{conf_dir}/minio-secret.yml")


def setup_gcs_credentials(secret_name: str = "gcp-cs-secret") -> bool:
    """Setup GCS credentials from K8s secret for gsutil."""
    result = subprocess.run(["gsutil", "ls"], capture_output=True, check=False)
    if result.returncode == 0:
        logger.info("GCS credentials already set in environment")
        return True

    logger.info(f"Setting up GCS credentials from K8s secret: {secret_name}")

    access_key = get_secret_data(secret_name, "AWS_ACCESS_KEY_ID")
    secret_key = get_secret_data(secret_name, "AWS_SECRET_ACCESS_KEY")

    if not access_key or not secret_key:
        logger.error("Failed to extract GCS credentials from secret")
        return False

    boto_fd, boto_path = tempfile.mkstemp(prefix="boto.", suffix=".cfg")
    try:
        with os.fdopen(boto_fd, "w") as f:
            f.write("[Credentials]\n")
            f.write(f"gs_access_key_id = {access_key}\n")
            f.write(f"gs_secret_access_key = {secret_key}\n")
        os.chmod(boto_path, 0o600)
        os.environ["BOTO_CONFIG"] = boto_path
        logger.info("GCS credentials configured successfully")
        return True
    except Exception as e:
        logger.error(f"Failed to create boto config: {e}")
        os.unlink(boto_path)
        return False


# TODO: implement this function
def check_passwords_leak(namespace: Optional[str] = None) -> None:
    """Check for password leaks in Kubernetes pod logs."""
    pass


def retry(
    func: Callable[[], Any],
    max_attempts: int = 5,
    delay: int = 1,
    condition: Optional[Callable[[Any], bool]] = None,
) -> Any:
    """Retry a function until it succeeds or max attempts reached."""
    for attempt in range(max_attempts):
        try:
            result = func()
            if condition is None or condition(result):
                return result
        except Exception:
            if attempt == max_attempts - 1:
                raise

        time.sleep(delay)

    raise Exception(f"Max attempts ({max_attempts}) reached")


class MongoManager:
    def __init__(self, client: str):
        self.client = client

    def run_mongosh(
        self,
        command: str,
        uri: str,
        driver: str = "mongodb+srv",
        suffix: str = ".svc.cluster.local",
        mongo_flag: str = "",
        timeout: int = 30,
    ) -> str:
        """Execute mongosh command in PSMDB client container."""
        replica_set = "cfg" if "cfg" in uri else "rs0"
        connection_string = f"{driver}://{uri}{suffix}/admin?ssl=false&replicaSet={replica_set}"
        if mongo_flag:
            connection_string += f" {mongo_flag}"

        result = kubectl_bin(
            "exec",
            self.client,
            "--",
            "timeout",
            str(timeout),
            "mongosh",
            f"{connection_string}",
            "--eval",
            command,
            "--quiet",
            check=False,
        )
        return result

    def compare_mongo_user(self, uri: str, expected_role: str, test_dir: str | Path) -> None:
        """Compare MongoDB user permissions"""

        def get_expected_file(test_dir: str | Path, user: str) -> Dict[str, Any]:
            """Get the appropriate expected file based on MongoDB version"""
            base_path = Path(test_dir) / "compare"
            base_file = base_path / f"{user}.json"

            # Check for version-specific files
            image_mongod = os.environ.get("IMAGE_MONGOD", "")
            version_mappings = [("8.0", "-80"), ("7.0", "-70"), ("6.0", "-60")]

            for version, suffix in version_mappings:
                if version in image_mongod:
                    version_file = base_path / f"{user}{suffix}.json"
                    if version_file.exists():
                        logger.info(f"Using version-specific file: {version_file}")
                        with open(version_file) as f:
                            return json.load(f)

            # Fall back to base file
            if base_file.exists():
                logger.info(f"Using base file: {base_file}")
                with open(base_file) as f:
                    return json.load(f)
            else:
                raise FileNotFoundError(f"Expected file not found: {base_file}")

        def clean_mongo_json(data: Dict[str, Any]) -> Dict[str, Any]:
            """Remove timestamps and metadata from MongoDB response"""

            def remove_timestamps(obj):
                if isinstance(obj, dict):
                    return {
                        k: remove_timestamps(v)
                        for k, v in obj.items()
                        if k not in {"ok", "$clusterTime", "operationTime"}
                    }
                elif isinstance(obj, list):
                    return [remove_timestamps(v) for v in obj]
                elif isinstance(obj, str):
                    # Remove ISO timestamp patterns
                    return re.sub(
                        r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}[\+\-]\d{4}", "", obj
                    )
                else:
                    return obj

            return remove_timestamps(data)

        # Get actual MongoDB user permissions
        result = retry(
            lambda: self.run_mongosh(
                "EJSON.stringify(db.runCommand({connectionStatus:1,showPrivileges:true}))",
                uri,
            )
        )
        actual_data = clean_mongo_json(json.loads(result))
        expected_data = get_expected_file(test_dir, expected_role)

        diff = DeepDiff(expected_data, actual_data, ignore_order=True)
        assert not diff, f"MongoDB user permissions differ: {diff.pretty()}"

    def compare_mongo_cmd(
        self,
        command: str,
        uri: str,
        postfix: str = "",
        suffix: str = "",
        database: str = "myApp",
        collection: str = "test",
        sort: str = "",
        test_file: str = "",
    ) -> None:
        """Compare MongoDB command output"""
        full_cmd = f"{collection}.{command}"
        if sort:
            full_cmd = f"{collection}.{command}.{sort}"

        logger.info(f"Running: {CYAN}{full_cmd}{RESET} on db {CYAN}{database}{RESET}")

        mongo_expr = f"EJSON.stringify(db.getSiblingDB('{database}').{full_cmd})"
        result = json.loads(self.run_mongosh(mongo_expr, uri, "mongodb"))

        logger.info(f"MongoDB output: {CYAN}{result}{RESET}")

        with open(test_file) as file:
            expected = json.load(file)

        diff = DeepDiff(expected, result)
        assert not diff, f"MongoDB command output differs: {diff.pretty()}"

    def get_mongo_primary(self, uri: str, cluster_name: str) -> str:
        """Get current MongoDB primary node"""
        primary_endpoint = self.run_mongosh("EJSON.stringify(db.hello().me)", uri)

        if cluster_name in primary_endpoint:
            return primary_endpoint.split(".")[0].replace('"', "")
        else:
            endpoint_host = primary_endpoint.split(":")[0]
            result = kubectl_bin("get", "service", "-o", "wide")

            for line in result.splitlines():
                if endpoint_host in line:
                    return line.split()[0].replace('"', "")
            raise ValueError("Primary node not found in service list")
