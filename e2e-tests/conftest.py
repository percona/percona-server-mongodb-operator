import logging
import os
import subprocess
import time
import uuid
from collections.abc import Callable, Generator
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
from typing import Any

import pytest
import yaml
from lib.arch import helm_arch_set_args
from lib.cli import helm_bin, oc_bin
from lib.kubectl import (
    clean_all_namespaces,
    detect_arch,
    detect_platform,
    get_k8s_versions,
    kubectl_bin,
    wait_pod,
)
from lib.mongo import MongoManager
from lib.operator import check_crd_for_deletion, delete_crd_rbac, deploy_operator
from lib.secrets import get_cloud_secret_default
from lib.utils import (
    K8sHighlighter,
    Paths,
    env_bool,
    get_cr_version,
    get_git_branch,
    get_git_commit,
    k8s_theme,
    qualify_image,
    retry,
)
from rich.console import Console
from rich.logging import RichHandler

pytest_plugins = ["lib.report_generator"]

_console = Console(theme=k8s_theme, highlighter=K8sHighlighter())

logging.basicConfig(
    level=os.environ.get("LOG_LEVEL", "INFO").upper(),
    format="%(message)s",
    handlers=[
        RichHandler(
            console=_console,
            highlighter=K8sHighlighter(),
            keywords=[],
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

# Shared with the bash harness (e2e-tests/functions). Override via
# PYTEST_NAMESPACE_FILE so parallel Jenkins clusters don't race on one path.
_NAMESPACE_FILE = os.environ.get("PYTEST_NAMESPACE_FILE", "/tmp/pytest_current_namespace")


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
            # Relies on the private _nodeid attribute; may need revisiting on
            # pytest upgrades (no public API for renaming a collected item).
            item._nodeid = item._nodeid.replace(
                "test_bash_wrapper", f"test_bash_wrapper[{test_name}]"
            )


_NODES_READY_TIMEOUT = 300
_NODES_READY_INTERVAL = 10


def _wait_for_nodes_ready() -> None:
    """Wait until K8s nodes are ready, failing the test if they aren't in time."""
    from lib import report_generator

    deadline = time.monotonic() + _NODES_READY_TIMEOUT
    while True:
        status = report_generator.check_nodes_ready()
        if status["ok"]:
            return
        if time.monotonic() >= deadline:
            pytest.fail(
                f"K8s nodes not ready: {status['ready']}/{status['total']} "
                f"(need >= {report_generator.MIN_READY_NODES}) "
                f"after {_NODES_READY_TIMEOUT}s",
                pytrace=False,
            )
        logger.warning(f"Waiting for nodes ready: {status['ready']}/{status['total']}")
        time.sleep(_NODES_READY_INTERVAL)


@pytest.hookimpl(tryfirst=True)
def pytest_runtest_setup(item: pytest.Item) -> None:
    """Print newline after pytest's verbose test name output and gate on node readiness."""
    print()
    _wait_for_nodes_ready()


@pytest.hookimpl(tryfirst=True)
def pytest_report_teststatus(
    report: pytest.TestReport, config: pytest.Config
) -> tuple[str, str, str] | None:
    """Suppress pytest's default PASSED/FAILED/SKIPPED word; we print our own banner."""
    if report.when != "call":
        return None
    if report.passed:
        return "passed", ".", ""
    if report.failed:
        return "failed", "F", ""
    if report.skipped:
        return "skipped", "s", ""
    return None


@pytest.hookimpl(trylast=True)
def pytest_runtest_logreport(report: pytest.TestReport) -> None:
    """Print a clear separator around call-phase results so they aren't buried in logs."""
    if report.when != "call":
        return
    if report.passed:
        status = "PASSED"
    elif report.failed:
        status = "FAILED"
    elif report.skipped:
        status = "SKIPPED"
    else:
        return
    sep = "=" * 100
    _console.print(f"\n{sep}\n[{status}] {report.nodeid}\n{sep}\n")


def _get_current_namespace() -> str | None:
    """Get namespace from global or temp file."""
    if _current_namespace:
        return _current_namespace
    try:
        with open(_NAMESPACE_FILE) as f:
            return f.read().strip() or None
    except Exception:
        return None


@pytest.hookimpl(tryfirst=True, hookwrapper=True)
def pytest_runtest_makereport(item: pytest.Item, call: pytest.CallInfo[None]) -> Generator[None]:
    """Collect K8s resources when a test fails and add to HTML report."""
    outcome: Any = yield
    report = outcome.get_result()

    # Handle the call phase, and setup failures (where infra fixtures break and
    # there is no call phase). Skip passing setup/teardown to avoid duplicate work.
    if not (report.when == "call" or (report.when == "setup" and report.failed)):
        return

    # Imported here so pytest can assert-rewrite it when loading it as a plugin.
    from lib import report_generator

    try:
        report.nodes_status = report_generator.check_nodes_ready()
    except Exception as e:
        logger.debug(f"Failed to check node readiness: {e}")

    if report.failed:
        namespace = _get_current_namespace()
        if not namespace:
            return

        try:
            report.extras = report_generator.generate_report(namespace)
        except Exception as e:
            logger.warning(f"Failed to generate HTML report extras: {e}")


@pytest.fixture(scope="session", autouse=True)
def setup_env_vars() -> None:
    """Setup environment variables for the test session."""
    git_branch = get_git_branch()
    platform = detect_platform()

    defaults = {
        "KUBE_VERSION": get_k8s_versions()[1],
        "PLATFORM": platform,
        "ARCH": detect_arch(),
        "EKS": "1" if platform == "eks" else "",
        "GKE": "1" if platform == "gke" else "",
        "AKS": "1" if platform == "aks" else "",
        "OPENSHIFT": "1" if platform == "openshift" else "",
        "KIND": "1" if platform == "kind" else "",
        "MINIKUBE": "1" if platform == "minikube" else "",
        "RANCHER": "1" if platform == "rancher" else "",
        "API": "psmdb.percona.com/v1",
        "REGISTRY_NAME": "docker.io",
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
        "IMAGE_SEARCH": "perconalab/percona-server-mongodb-operator:main-mongot",
        "IMAGE_BACKUP": "perconalab/percona-server-mongodb-operator:main-backup",
        "IMAGE_PMM_CLIENT": "percona/pmm-client:2.44.1-1",
        "IMAGE_PMM_SERVER": "perconalab/pmm-server:dev-latest",
        "IMAGE_PMM3_CLIENT": "perconalab/pmm-client:3-dev-latest",
        "IMAGE_PMM3_SERVER": "perconalab/pmm-server:3-dev-latest",
        "IMAGE_LOGCOLLECTOR": "perconalab/fluentbit:main-logcollector",
        "IMAGE_CLUSTERSYNC": "percona/percona-clustersync-mongodb:0.9.0",
        "IMAGE_AWS_CLI": "docker.io/amazon/aws-cli:2.34.60",
        "CERT_MANAGER_VER": "1.21.0",
        "CHAOS_MESH_VER": "2.7.1",
        "MINIO_VER": "5.4.0",
        "PMM_SERVER_VER": "9.9.9",
        "CLEAN_NAMESPACE": "0",
        "DELETE_CRD_ON_START": "1",
        "SKIP_DELETE": "1",
        "SKIP_BACKUPS_TO_AWS_GCP_AZURE": get_cloud_secret_default(),
        "UPDATE_COMPARE_FILES": "0",
    }

    for key, value in defaults.items():
        if not os.environ.get(key):
            os.environ[key] = value

    registry = os.environ.get("REGISTRY_NAME", "").rstrip("/") + "/"
    image_vars = [key for key in defaults if key.startswith("IMAGE")]
    image_vars.append("IMAGE_OPERATOR")
    for var in image_vars:
        images = os.environ.get(var, "").splitlines()
        if images:
            os.environ[var] = "\n".join(qualify_image(i, registry) for i in images if i)

    logger.info(
        "Environment variables:\n" + "\n".join(f"{key}={os.environ.get(key)}" for key in defaults)
    )


@pytest.fixture(scope="class")
def test_paths(request: pytest.FixtureRequest) -> Paths:
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
    result = oc_bin(
        "wait",
        "--for=delete",
        f"project/{project}",
        f"--timeout={timeout}s",
        check=False,
        capture=True,
    )
    if result.returncode == 0 or "NotFound" in result.stderr:
        return
    logger.warning(f"Project {project} not deleted within {timeout}s, continuing anyway")


@pytest.fixture(scope="class")
def create_namespace() -> Callable[[str], str]:
    def _create_namespace(namespace: str) -> str:
        """Create kubernetes namespace and clean up if exists."""

        if env_bool("CLEAN_NAMESPACE"):
            clean_all_namespaces()

        if env_bool("OPENSHIFT"):
            logger.info("Cleaning up existing OpenShift project if exists")
            oc_bin("delete", "project", namespace, "--ignore-not-found", check=False)
            _wait_for_project_delete(namespace)

            logger.info(f"Create namespace {namespace}")
            oc_bin("new-project", namespace)
            oc_bin("project", namespace)
            oc_bin("adm", "policy", "add-scc-to-user", "hostaccess", "-z", "default", check=False)
        else:
            logger.info("Cleaning up existing namespace")
            kubectl_bin("delete", "namespace", namespace, "--ignore-not-found", check=False)
            kubectl_bin("wait", "--for=delete", f"namespace/{namespace}", check=False)

            logger.info(f"Create namespace {namespace}")
            kubectl_bin("create", "namespace", namespace)
            kubectl_bin("config", "set-context", "--current", f"--namespace={namespace}")
        return namespace

    return _create_namespace


def _kubectl_quietly(*args: str) -> None:
    """Run kubectl, logging instead of raising: cleanup must never fail a test."""
    try:
        kubectl_bin(*args, check=False)
    except OSError as e:
        logger.debug(f"Command failed (continuing cleanup): {' '.join(args)}, error: {e}")


def _cleanup_crd(src_dir: str) -> None:
    """Best-effort CRD delete: never wait on controllers."""
    crd_file = Path(src_dir) / "deploy" / "crd.yaml"
    _kubectl_quietly("delete", "-f", str(crd_file), "--ignore-not-found", "--wait=false")

    try:
        docs = list(yaml.safe_load_all(crd_file.read_text()))
    except (OSError, yaml.YAMLError) as e:
        logger.debug(f"CRD cleanup failed (continuing): {e}")
        return

    for doc in docs:
        name = doc.get("metadata", {}).get("name") if isinstance(doc, dict) else None
        if not name:
            continue
        _kubectl_quietly(
            "patch", "crd", name, "--type=merge", "-p", '{"metadata":{"finalizers":[]}}'
        )


def _clear_namespace_finalizers(ns: str) -> None:
    """Strip finalizers on operator resources so the namespace does not hang in
    Terminating when the operator is already gone (or gke-mcs stops clearing them)."""
    for kind in ("psmdb", "psmdb-backup", "psmdb-restore", "serviceexport", "serviceimport"):
        names = kubectl_bin("get", kind, "-n", ns, "-o", "name", "--ignore-not-found", check=False)
        for name in (names or "").split():
            _kubectl_quietly(
                "patch",
                name,
                "-n",
                ns,
                "--type=merge",
                "-p",
                '{"metadata":{"finalizers":[]}}',
            )


def _delete_namespace_crs(ns: str) -> None:
    """Issue non-blocking deletes for operator CRs after finalizers are cleared."""
    for kind in ("psmdb-backup", "psmdb-restore", "psmdb"):
        _kubectl_quietly("delete", kind, "--all", "-n", ns, "--ignore-not-found", "--wait=false")


def _cleanup_infra(test_paths: Paths, namespaces: list[str]) -> None:
    """Best-effort teardown: strip finalizers, fire-and-forget deletes, never hang."""
    logger.info("Cleaning up test environment")
    src_dir = test_paths["src_dir"]
    rbac = f"{src_dir}/deploy/{'cw-' if os.environ.get('OPERATOR_NS') else ''}rbac.yaml"

    # Finalizers first, while CRD APIs still exist. Do not wait on operator/storage.
    if namespaces:
        with ThreadPoolExecutor(max_workers=len(namespaces)) as pool:
            for ns in namespaces:
                pool.submit(_clear_namespace_finalizers, ns)
        with ThreadPoolExecutor(max_workers=len(namespaces)) as pool:
            for ns in namespaces:
                pool.submit(_delete_namespace_crs, ns)

    deletes = [
        [
            "delete",
            "-f",
            f"{test_paths['test_dir']}/../conf/container-rc.yaml",
            "--ignore-not-found",
            "--wait=false",
        ],
        ["delete", "-f", rbac, "--ignore-not-found", "--wait=false"],
    ]

    with ThreadPoolExecutor(max_workers=len(deletes) + 1) as pool:
        for args in deletes:
            pool.submit(_kubectl_quietly, *args)
        pool.submit(_cleanup_crd, src_dir)

    if namespaces:
        with ThreadPoolExecutor(max_workers=len(namespaces)) as pool:
            for ns in namespaces:
                pool.submit(
                    _kubectl_quietly,
                    "delete",
                    "namespace",
                    ns,
                    "--ignore-not-found",
                    "--wait=false",
                )


@pytest.fixture(scope="class")
def create_infra(
    test_paths: Paths, create_namespace: Callable[[str], str]
) -> Generator[Callable[[str], str]]:
    global _current_namespace
    created_namespaces: list[str] = []

    def _create_infra(test_name: str) -> str:
        """Create the necessary infrastructure for the tests."""
        global _current_namespace
        logger.info("Creating test environment")
        if env_bool("DELETE_CRD_ON_START"):
            delete_crd_rbac(Path(test_paths["src_dir"]))
            check_crd_for_deletion(Path(test_paths["src_dir"]) / "deploy" / "crd.yaml")

        namespace = f"{test_name}-{uuid.uuid4().hex[:8]}"
        operator_ns = os.environ.get("OPERATOR_NS")
        # Cluster-wide: operator lives in its own namespace, deployed first.
        # Namespaced: operator lives in the test namespace, which must exist first.
        if operator_ns:
            create_namespace(operator_ns)
            deploy_operator(test_paths["test_dir"], test_paths["src_dir"])
            create_namespace(namespace)
        else:
            create_namespace(namespace)
            deploy_operator(test_paths["test_dir"], test_paths["src_dir"])

        # Track created namespace for cleanup and failure collection
        created_namespaces.append(namespace)
        _current_namespace = namespace
        Path(_NAMESPACE_FILE).write_text(namespace)
        return namespace

    yield _create_infra

    _current_namespace = None
    Path(_NAMESPACE_FILE).unlink(missing_ok=True)

    if env_bool("SKIP_DELETE"):
        logger.info("SKIP_DELETE is set. Skipping test environment cleanup")
        return

    operator_ns = os.environ.get("OPERATOR_NS")
    _cleanup_infra(test_paths, created_namespaces + ([operator_ns] if operator_ns else []))


@pytest.fixture(scope="class")
def deploy_chaos_mesh() -> Generator[Callable[[str], None]]:
    """Deploy Chaos Mesh and clean up after tests."""
    deployed_namespaces = []

    def _deploy(namespace: str) -> None:
        helm_bin("repo", "add", "chaos-mesh", "https://charts.chaos-mesh.org")
        helm_bin("repo", "update")
        helm_bin(
            "install",
            "chaos-mesh",
            "chaos-mesh/chaos-mesh",
            "--namespace",
            namespace,
            "--version",
            os.environ.get("CHAOS_MESH_VER", ""),
            "--set",
            "dashboard.create=false",
            "--set",
            "chaosDaemon.runtime=containerd",
            "--set",
            "chaosDaemon.socketPath=/run/containerd/containerd.sock",
            *helm_arch_set_args(("controllerManager.", "chaosDaemon.")),
            "--wait",
        )
        deployed_namespaces.append(namespace)

    yield _deploy

    for ns in deployed_namespaces:
        try:
            helm_bin("uninstall", "chaos-mesh", "--namespace", ns, "--wait", "--timeout", "60s")
        except subprocess.CalledProcessError as e:
            logger.error(f"Failed to cleanup chaos-mesh in {ns}: {e}")


def _cert_manager_url() -> str:
    return f"https://github.com/cert-manager/cert-manager/releases/download/v{os.environ.get('CERT_MANAGER_VER')}/cert-manager.yaml"


def _delete_cert_manager() -> None:
    """Best-effort removal of cert-manager; safe to call when it isn't installed."""
    if env_bool("RANCHER"):
        logger.info("Rancher cluster detected, skipping cert-manager destroy")
        return
    logger.info("Deleting cert-manager")
    kubectl_bin("delete", "-f", _cert_manager_url(), "--ignore-not-found", check=False)


@pytest.fixture(scope="class")
def destroy_cert_manager() -> Callable[[], None]:
    """Delete cert-manager so certificates are issued by the operator, not cert-manager."""
    return _delete_cert_manager


@pytest.fixture(scope="class")
def deploy_cert_manager() -> Generator[Callable[..., None]]:
    """Deploy Cert Manager and clean up after tests."""

    def _deploy(*extra_args: str) -> None:
        if env_bool("RANCHER"):
            logger.info("Rancher cluster detected, skipping cert-manager deployment")
            return
        logger.info("Deploying cert-manager")
        try:
            kubectl_bin("create", "namespace", "cert-manager")
            kubectl_bin(
                "label", "namespace", "cert-manager", "certmanager.k8s.io/disable-validation=true"
            )
            kubectl_bin("apply", "-f", _cert_manager_url(), "--validate=false")
            for arg in extra_args:
                kubectl_bin(
                    "patch",
                    "deployment",
                    "cert-manager",
                    "-n",
                    "cert-manager",
                    "--type=json",
                    "-p",
                    f'[{{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"{arg}"}}]',
                )
            kubectl_bin(
                "wait",
                "pod",
                "-l",
                "app.kubernetes.io/instance=cert-manager",
                "--for=condition=ready",
                "-n",
                "cert-manager",
            )
        except Exception:
            _delete_cert_manager()
            raise

    yield _deploy

    _delete_cert_manager()


def aws_cli_minio(command: str, endpoint: str) -> str:
    """Run an aws-cli command against MinIO from a throwaway pod."""
    return kubectl_bin(
        "run",
        "-i",
        "--rm",
        "aws-cli",
        f"--image={os.environ.get('IMAGE_AWS_CLI')}",
        "--restart=Never",
        "--env=AWS_ACCESS_KEY_ID=some-access-key",
        "--env=AWS_SECRET_ACCESS_KEY=some-secret-key",
        "--env=AWS_DEFAULT_REGION=us-east-1",
        "--",
        "--no-verify-ssl",
        "--endpoint-url",
        endpoint,
        *command.split(),
    )


@pytest.fixture(scope="class")
def deploy_minio() -> Generator[None]:
    """Deploy MinIO and clean up after tests."""
    service_name = "minio-service"
    bucket = "operator-testing"

    logger.info(f"Installing MinIO: {service_name}")

    helm_bin("uninstall", service_name, check=False, capture=True)
    helm_bin("repo", "remove", "minio", check=False, capture=True)
    helm_bin("repo", "add", "minio", "https://charts.min.io/")

    endpoint = f"http://{service_name}:9000"
    minio_ver = os.environ.get("MINIO_VER", "")
    settings = {
        "replicas": "1",
        "mode": "standalone",
        "resources.requests.memory": "256Mi",
        "rootUser": "rootuser",
        "rootPassword": "rootpass123",
        "users[0].accessKey": "some-access-key",
        "users[0].secretKey": "some-secret-key",
        "users[0].policy": "consoleAdmin",
        "service.type": "ClusterIP",
        "configPathmc": "/tmp/",
        "securityContext.enabled": "false",
        "persistence.size": "2G",
        "fullnameOverride": service_name,
        "serviceAccount.create": "true",
        "serviceAccount.name": f"{service_name}-sa",
    }
    set_args = [arg for k, v in settings.items() for arg in ("--set", f"{k}={v}")]
    set_args += helm_arch_set_args(("", "postJob."))
    install_args = ["install", service_name, "minio/minio", "--version", minio_ver, *set_args]

    retry(lambda: helm_bin(*install_args), max_attempts=6, delay=5, backoff=2)

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
    aws_cli_minio(f"s3 mb s3://{bucket}", endpoint)

    yield

    try:
        helm_bin("uninstall", service_name, "--wait", "--timeout", "60s")
    except subprocess.CalledProcessError as e:
        logger.warning(f"Failed to cleanup minio: {e}")


@pytest.fixture(scope="class")
def psmdb_client(test_paths: Paths) -> MongoManager:
    """Deploy and get the client pod name."""
    kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/client-70.yml")

    result = retry(
        lambda: kubectl_bin(
            "get",
            "pods",
            "--selector=name=psmdb-client-mongosh",
            "-o",
            "jsonpath={range .items[*]}{.metadata.name}{end}",
        ),
        max_attempts=30,
        delay=2,
        condition=lambda result: result.strip() != "",
    )

    pod_name = result.strip()
    wait_pod(pod_name)
    return MongoManager(pod_name)


@pytest.fixture
def bash_test_cleanup(test_paths: Paths) -> Generator[None]:
    """Tear down a bash-wrapped test's namespace after diagnostics are collected."""
    yield

    if env_bool("SKIP_DELETE"):
        logger.info("SKIP_DELETE is set. Skipping bash test cleanup")
        return

    ns = _get_current_namespace()
    namespaces = [ns] if ns else []
    operator_ns = os.environ.get("OPERATOR_NS")
    if operator_ns:
        namespaces.append(operator_ns)
    _cleanup_infra(test_paths, namespaces)
