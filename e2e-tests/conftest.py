import logging
import os
import random
import subprocess
import time
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
from typing import Any, Callable, Dict, Generator

import pytest
import yaml
from lib.kubectl import (
    clean_all_namespaces,
    get_k8s_versions,
    is_minikube,
    is_openshift,
    kubectl_bin,
    wait_pod,
)
from lib.mongo import MongoManager
from lib.operator import check_crd_for_deletion, delete_crd_rbac, deploy_operator
from lib.secrets import get_cloud_secret_default
from lib.utils import (
    K8sHighlighter,
    get_cr_version,
    get_git_branch,
    get_git_commit,
    k8s_theme,
    retry,
)
from rich.console import Console
from rich.logging import RichHandler

pytest_plugins = ["lib.report_generator"]

logging.basicConfig(
    level=os.environ.get("LOG_LEVEL", "INFO").upper(),
    format="%(message)s",
    handlers=[
        RichHandler(
            console=Console(theme=k8s_theme),
            highlighter=K8sHighlighter(),
            show_time=True,
            show_path=False,
            markup=False,
            rich_tracebacks=True,
            log_time_format="[%X.%f]",
        )
    ],
)
logging.getLogger("pytest_dependency").setLevel(logging.WARNING)
logger = logging.getLogger(__name__)

_current_namespace: str | None = None


def pytest_addoption(parser: pytest.Parser) -> None:
    parser.addoption("--test-name", action="store", default=None, help="Bash test name to run")


def pytest_collection_modifyitems(
    session: pytest.Session, config: pytest.Config, items: list[pytest.Item]
) -> None:
    """Rename bash wrapper tests to show actual test name."""
    test_name = config.getoption("--test-name")
    if not test_name:
        return

    for item in items:
        if item.name == "test_bash_wrapper":
            item._nodeid = item._nodeid.replace(
                "test_bash_wrapper", f"test_bash_wrapper[{test_name}]"
            )


@pytest.hookimpl(tryfirst=True)
def pytest_runtest_setup(item: pytest.Item) -> None:
    """Print newline after pytest's verbose test name output."""
    print()


def _get_current_namespace() -> str | None:
    """Get namespace from global or temp file (for bash wrapper tests)."""
    if _current_namespace:
        return _current_namespace
    try:
        with open("/tmp/pytest_current_namespace") as f:
            return f.read().strip() or None
    except Exception:
        return None


@pytest.hookimpl(tryfirst=True, hookwrapper=True)
def pytest_runtest_makereport(
    item: pytest.Item, call: pytest.CallInfo[None]
) -> Generator[None, None, None]:
    """Collect K8s resources when a test fails and add to HTML report."""
    outcome: Any = yield
    report = outcome.get_result()

    if report.when == "call" and report.failed:
        namespace = _get_current_namespace()
        if not namespace:
            return

        try:
            from lib import report_generator

            report.extras = report_generator.generate_report(namespace)
        except Exception as e:
            logger.warning(f"Failed to generate HTML report extras: {e}")


@pytest.fixture(scope="session", autouse=True)
def setup_env_vars() -> None:
    """Setup environment variables for the test session."""
    git_branch = get_git_branch()
    git_version, kube_version = get_k8s_versions()

    defaults = {
        "KUBE_VERSION": kube_version,
        "EKS": "1" if "eks" in git_version else "0",
        "GKE": "1" if "gke" in git_version else "0",
        "OPENSHIFT": is_openshift(),
        "MINIKUBE": is_minikube(),
        "API": "psmdb.percona.com/v1",
        "GIT_COMMIT": get_git_commit(),
        "GIT_BRANCH": git_branch,
        "OPERATOR_VERSION": get_cr_version(),
        "IMAGE": f"perconalab/percona-server-mongodb-operator:{git_branch}",
        "IMAGE_MONGOD": "perconalab/percona-server-mongodb-operator:main-mongod8.0",
        "IMAGE_MONGOD_CHAIN": (
            "perconalab/percona-server-mongodb-operator:main-mongod6.0\n"
            "perconalab/percona-server-mongodb-operator:main-mongod7.0\n"
            "perconalab/percona-server-mongodb-operator:main-mongod8.0"
        ),
        "IMAGE_BACKUP": "perconalab/percona-server-mongodb-operator:main-backup",
        "IMAGE_PMM_CLIENT": "percona/pmm-client:2.44.1-1",
        "IMAGE_PMM_SERVER": "perconalab/pmm-server:dev-latest",
        "IMAGE_PMM3_CLIENT": "perconalab/pmm-client:3-dev-latest",
        "IMAGE_PMM3_SERVER": "perconalab/pmm-server:3-dev-latest",
        "CERT_MANAGER_VER": "1.19.1",
        "CHAOS_MESH_VER": "2.7.1",
        "MINIO_VER": "5.4.0",
        "PMM_SERVER_VER": "9.9.9",
        "CLEAN_NAMESPACE": "0",
        "DELETE_CRD_ON_START": "0",
        "SKIP_DELETE": "1",
        "SKIP_BACKUPS_TO_AWS_GCP_AZURE": get_cloud_secret_default(),
        "UPDATE_COMPARE_FILES": "0",
    }

    for key, value in defaults.items():
        os.environ.setdefault(key, value)

    env_lines = [f"{key}={os.environ.get(key)}" for key in defaults]
    logger.info("Environment variables:\n" + "\n".join(env_lines))


@pytest.fixture(scope="class")
def test_paths(request: pytest.FixtureRequest) -> Dict[str, str]:
    """Fixture to provide paths relative to the test file."""
    test_file = request.path
    test_dir = test_file.parent
    conf_dir = test_dir.parent / "conf"
    src_dir = test_dir.parent.parent

    return {
        "test_file": str(test_file),
        "test_dir": str(test_dir),
        "conf_dir": str(conf_dir),
        "src_dir": str(src_dir),
    }


def _wait_for_project_delete(project: str, timeout: int = 180) -> None:
    """Wait for OpenShift project to be fully deleted."""
    start = time.time()
    while time.time() - start < timeout:
        result = subprocess.run(
            ["oc", "get", "project", project],
            capture_output=True,
            check=False,
        )
        if result.returncode != 0:
            return
        time.sleep(5)
    logger.warning(f"Project {project} not deleted within {timeout}s, continuing anyway")


@pytest.fixture(scope="class")
def create_namespace() -> Callable[[str], str]:
    def _create_namespace(namespace: str) -> str:
        """Create kubernetes namespace and clean up if exists."""

        if int(os.environ.get("CLEAN_NAMESPACE") or "0"):
            clean_all_namespaces()

        if int(os.environ.get("OPENSHIFT") or "0"):
            logger.info("Cleaning up existing OpenShift project if exists")
            subprocess.run(
                ["oc", "delete", "project", namespace, "--ignore-not-found"],
                check=False,
            )
            _wait_for_project_delete(namespace)

            logger.info(f"Create namespace {namespace}")
            subprocess.run(["oc", "new-project", namespace], check=True)
            subprocess.run(["oc", "project", namespace], check=True)
            subprocess.run(
                ["oc", "adm", "policy", "add-scc-to-user", "hostaccess", "-z", "default"],
                check=False,
            )
        else:
            logger.info("Cleaning up existing namespace")

            # Delete namespace if exists
            try:
                kubectl_bin("delete", "namespace", namespace, "--ignore-not-found")
                kubectl_bin("wait", "--for=delete", f"namespace/{namespace}")
            except subprocess.CalledProcessError:
                pass

            logger.info(f"Create namespace {namespace}")
            kubectl_bin("create", "namespace", namespace)
            kubectl_bin("config", "set-context", "--current", f"--namespace={namespace}")
        return namespace

    return _create_namespace


@pytest.fixture(scope="class")
def create_infra(
    test_paths: Dict[str, str], create_namespace: Callable[[str], str]
) -> Generator[Callable[[str], str], None, None]:
    global _current_namespace
    created_namespaces: list[str] = []

    def _create_infra(test_name: str) -> str:
        """Create the necessary infrastructure for the tests."""
        global _current_namespace
        logger.info("Creating test environment")
        if os.environ.get("DELETE_CRD_ON_START") == "1":
            delete_crd_rbac(Path(test_paths["src_dir"]))
            check_crd_for_deletion(f"{test_paths['src_dir']}/deploy/crd.yaml")

        operator_ns = os.environ.get("OPERATOR_NS")
        if operator_ns:
            create_namespace(operator_ns)
            deploy_operator(test_paths["test_dir"], test_paths["src_dir"])
            namespace = create_namespace(f"{test_name}-{random.randint(0, 32767)}")
        else:
            namespace = create_namespace(f"{test_name}-{random.randint(0, 32767)}")
            deploy_operator(test_paths["test_dir"], test_paths["src_dir"])

        # Track created namespace for cleanup and failure collection
        created_namespaces.append(namespace)
        _current_namespace = namespace
        return namespace

    yield _create_infra

    # Teardown code
    _current_namespace = None

    if os.environ.get("SKIP_DELETE") == "1":
        logger.info("SKIP_DELETE=1. Skipping test environment cleanup")
        return

    def run_cmd(cmd: list[str]) -> None:
        try:
            kubectl_bin(*cmd)
        except (subprocess.CalledProcessError, FileNotFoundError, OSError) as e:
            logger.debug(f"Command failed (continuing cleanup): {' '.join(cmd)}, error: {e}")

    def cleanup_crd() -> None:
        crd_file = f"{test_paths['src_dir']}/deploy/crd.yaml"
        run_cmd(["delete", "-f", crd_file, "--ignore-not-found", "--wait=false"])

        try:
            with open(crd_file, "r") as f:
                for doc in f.read().split("---"):
                    if not doc.strip():
                        continue
                    crd_name = yaml.safe_load(doc)["metadata"]["name"]
                    run_cmd(
                        [
                            "patch",
                            "crd",
                            crd_name,
                            "--type=merge",
                            "-p",
                            '{"metadata":{"finalizers":[]}}',
                        ]
                    )
                    run_cmd(["wait", "--for=delete", "crd", crd_name, "--timeout=60s"])
        except (FileNotFoundError, yaml.YAMLError, KeyError, TypeError) as e:
            logger.debug(f"CRD cleanup failed (continuing): {e}")

    logger.info("Cleaning up test environment")

    commands = [
        ["delete", "psmdb-backup", "--all", "--ignore-not-found"],
        [
            "delete",
            "-f",
            f"{test_paths['test_dir']}/../conf/container-rc.yaml",
            "--ignore-not-found",
        ],
        [
            "delete",
            "-f",
            f"{test_paths['src_dir']}/deploy/{'cw-' if os.environ.get('OPERATOR_NS') else ''}rbac.yaml",
            "--ignore-not-found",
        ],
    ]

    with ThreadPoolExecutor(max_workers=3) as executor:
        futures = [executor.submit(run_cmd, cmd) for cmd in commands]
        futures.append(executor.submit(cleanup_crd))

    # Clean up all created namespaces
    namespaces_to_delete = created_namespaces.copy()
    operator_ns = os.environ.get("OPERATOR_NS")
    if operator_ns:
        namespaces_to_delete.append(operator_ns)

    for ns in namespaces_to_delete:
        run_cmd(["delete", "--grace-period=0", "--force", "namespace", ns, "--ignore-not-found"])


@pytest.fixture(scope="class")
def deploy_chaos_mesh() -> Generator[Callable[[str], None], None, None]:
    """Deploy Chaos Mesh and clean up after tests."""
    deployed_namespaces = []

    def _deploy(namespace: str) -> None:
        subprocess.run(
            ["helm", "repo", "add", "chaos-mesh", "https://charts.chaos-mesh.org"], check=True
        )
        subprocess.run(["helm", "repo", "update"], check=True)
        subprocess.run(
            [
                "helm",
                "install",
                "chaos-mesh",
                "chaos-mesh/chaos-mesh",
                "--namespace",
                namespace,
                "--version",
                os.environ["CHAOS_MESH_VER"],
                "--set",
                "dashboard.create=false",
                "--set",
                "chaosDaemon.runtime=containerd",
                "--set",
                "chaosDaemon.socketPath=/run/containerd/containerd.sock",
                "--wait",
            ],
            check=True,
        )
        deployed_namespaces.append(namespace)

    yield _deploy

    for ns in deployed_namespaces:
        try:
            subprocess.run(
                [
                    "helm",
                    "uninstall",
                    "chaos-mesh",
                    "--namespace",
                    ns,
                    "--wait",
                    "--timeout",
                    "60s",
                ],
                check=True,
            )
        except subprocess.CalledProcessError as e:
            logger.error(f"Failed to cleanup chaos-mesh in {ns}: {e}")


@pytest.fixture(scope="class")
def deploy_cert_manager() -> Generator[None, None, None]:
    """Deploy Cert Manager and clean up after tests."""
    logger.info("Deploying cert-manager")
    cert_manager_url = f"https://github.com/cert-manager/cert-manager/releases/download/v{os.environ.get('CERT_MANAGER_VER')}/cert-manager.yaml"
    try:
        kubectl_bin("create", "namespace", "cert-manager")
        kubectl_bin(
            "label", "namespace", "cert-manager", "certmanager.k8s.io/disable-validation=true"
        )
        kubectl_bin("apply", "-f", cert_manager_url, "--validate=false")
        kubectl_bin(
            "wait",
            "pod",
            "-l",
            "app.kubernetes.io/instance=cert-manager",
            "--for=condition=ready",
            "-n",
            "cert-manager",
        )
    except Exception as e:
        try:
            kubectl_bin("delete", "-f", cert_manager_url, "--ignore-not-found")
        except (subprocess.CalledProcessError, FileNotFoundError, OSError) as cleanup_error:
            logger.warning(
                f"Failed to cleanup cert-manager during error handling: {cleanup_error}"
            )
        raise e

    yield

    try:
        kubectl_bin("delete", "-f", cert_manager_url, "--ignore-not-found")
    except Exception as e:
        logger.error(f"Failed to cleanup cert-manager: {e}")


@pytest.fixture(scope="class")
def deploy_minio() -> Generator[None, None, None]:
    """Deploy MinIO and clean up after tests."""
    service_name = "minio-service"
    bucket = "operator-testing"

    logger.info(f"Installing MinIO: {service_name}")

    subprocess.run(["helm", "uninstall", service_name], capture_output=True, check=False)
    subprocess.run(["helm", "repo", "remove", "minio"], capture_output=True, check=False)
    subprocess.run(["helm", "repo", "add", "minio", "https://charts.min.io/"], check=True)

    endpoint = f"http://{service_name}:9000"
    minio_ver = os.environ.get("MINIO_VER") or ""
    minio_args = [
        "helm",
        "install",
        service_name,
        "minio/minio",
        "--version",
        minio_ver,
        "--set",
        "replicas=1",
        "--set",
        "mode=standalone",
        "--set",
        "resources.requests.memory=256Mi",
        "--set",
        "rootUser=rootuser",
        "--set",
        "rootPassword=rootpass123",
        "--set",
        "users[0].accessKey=some-access-key",
        "--set",
        "users[0].secretKey=some-secret-key",
        "--set",
        "users[0].policy=consoleAdmin",
        "--set",
        "service.type=ClusterIP",
        "--set",
        "configPathmc=/tmp/",
        "--set",
        "securityContext.enabled=false",
        "--set",
        "persistence.size=2G",
        "--set",
        f"fullnameOverride={service_name}",
        "--set",
        "serviceAccount.create=true",
        "--set",
        f"serviceAccount.name={service_name}-sa",
    ]

    retry(lambda: subprocess.run(minio_args, check=True), max_attempts=10, delay=60)

    minio_pod = kubectl_bin(
        "get",
        "pods",
        f"--selector=release={service_name}",
        "-o",
        "jsonpath={.items[].metadata.name}",
    ).strip()
    wait_pod(minio_pod)

    operator_ns = os.environ.get("OPERATOR_NS")
    if operator_ns:
        namespace = kubectl_bin(
            "config", "view", "--minify", "-o", "jsonpath={..namespace}"
        ).strip()
        kubectl_bin(
            "create",
            "svc",
            "-n",
            operator_ns,
            "externalname",
            service_name,
            f"--external-name={service_name}.{namespace}.svc.cluster.local",
            "--tcp=9000",
        )

    logger.info(f"Creating MinIO bucket: {bucket}")
    kubectl_bin(
        "run",
        "-i",
        "--rm",
        "aws-cli",
        "--image=perconalab/awscli",
        "--restart=Never",
        "--",
        "bash",
        "-c",
        "AWS_ACCESS_KEY_ID=some-access-key "
        "AWS_SECRET_ACCESS_KEY=some-secret-key "
        "AWS_DEFAULT_REGION=us-east-1 "
        f"/usr/bin/aws --no-verify-ssl --endpoint-url {endpoint} s3 mb s3://{bucket}",
    )

    yield

    try:
        subprocess.run(
            ["helm", "uninstall", service_name, "--wait", "--timeout", "60s"],
            check=True,
        )
    except subprocess.CalledProcessError as e:
        logger.warning(f"Failed to cleanup minio: {e}")


@pytest.fixture(scope="class")
def psmdb_client(test_paths: Dict[str, str]) -> MongoManager:
    """Deploy and get the client pod name."""
    kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/client-70.yml")

    result = retry(
        lambda: kubectl_bin(
            "get",
            "pods",
            "--selector=name=psmdb-client",
            "-o",
            "jsonpath={.items[].metadata.name}",
        ),
        condition=lambda result: "container not found" not in result,
    )

    pod_name = result.strip()
    wait_pod(pod_name)
    return MongoManager(pod_name)
