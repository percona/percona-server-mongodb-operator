import json
import logging
import os
import random
import subprocess
import time
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
from typing import Dict

import pytest
import yaml
from lib import k8s_collector, tools

logging.getLogger("pytest_dependency").setLevel(logging.WARNING)
logger = logging.getLogger(__name__)

_current_namespace: str | None = None


@pytest.hookimpl(tryfirst=True, hookwrapper=True)
def pytest_runtest_makereport(item: pytest.Item, call: pytest.CallInfo) -> None:
    """Collect K8s resources when a test fails."""
    outcome = yield
    report = outcome.get_result()

    if report.when == "call" and report.failed and _current_namespace:
        if os.environ.get("COLLECT_K8S_ON_FAILURE") == "1":
            logger.info(f"Test failed, collecting K8s resources from {_current_namespace}")
            try:
                custom_resources = ["psmdb", "psmdb-backup", "psmdb-restore"]
                k8s_collector.collect_resources(_current_namespace, custom_resources)
            except Exception as e:
                logger.warning(f"Failed to collect K8s resources: {e}")


@pytest.fixture(scope="session", autouse=True)
def setup_env_vars() -> None:
    """Setup environment variables for the test session."""
    git_branch = tools.get_git_branch()
    git_version, kube_version = tools.get_k8s_versions()

    defaults = {
        "KUBE_VERSION": kube_version,
        "EKS": "1" if "eks" in git_version else "0",
        "GKE": "1" if "gke" in git_version else "0",
        "OPENSHIFT": "1" if tools.is_openshift() else "0",
        "MINIKUBE": "1" if tools.is_minikube() else "0",
        "API": "psmdb.percona.com/v1",
        "GIT_COMMIT": tools.get_git_commit(),
        "GIT_BRANCH": git_branch,
        "OPERATOR_VERSION": tools.get_cr_version(),
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
        "SKIP_BACKUPS_TO_AWS_GCP_AZURE": tools.get_cloud_secret_default(),
        "UPDATE_COMPARE_FILES": "0",
        "COLLECT_K8S_ON_FAILURE": "1",
    }

    for key, value in defaults.items():
        os.environ.setdefault(key, value)

    env_lines = [f"{key}={os.environ.get(key)}" for key in defaults]
    logger.info("Environment variables:\n" + "\n".join(env_lines))


@pytest.fixture(scope="class")
def test_paths(request: pytest.FixtureRequest) -> Dict[str, str]:
    """Fixture to provide paths relative to the test file."""
    test_file = Path(request.fspath)
    test_dir = test_file.parent
    conf_dir = test_dir.parent / "conf"
    src_dir = test_dir.parent.parent

    return {
        "test_file": str(test_file),
        "test_dir": str(test_dir),
        "conf_dir": str(conf_dir),
        "src_dir": str(src_dir),
    }


@pytest.fixture(scope="class")
def create_namespace() -> callable:
    def _create_namespace(namespace: str) -> str:
        """Create kubernetes namespace and clean up if exists."""
        operator_ns = os.environ.get("OPERATOR_NS")

        if int(os.environ.get("CLEAN_NAMESPACE")):
            tools.clean_all_namespaces()

        if int(os.environ.get("OPENSHIFT")):
            logger.info("Cleaning up all old namespaces from openshift")

            if operator_ns:
                try:
                    result = subprocess.run(
                        ["oc", "get", "project", operator_ns, "-o", "json"],
                        capture_output=True,
                        text=True,
                        check=False,
                    )

                    if result.returncode == 0:
                        project_data = json.loads(result.stdout)
                        if project_data.get("metadata", {}).get("name"):
                            subprocess.run(
                                [
                                    "oc",
                                    "delete",
                                    "--grace-period=0",
                                    "--force=true",
                                    "project",
                                    namespace,
                                ],
                                check=False,
                            )
                            time.sleep(120)
                    else:
                        subprocess.run(["oc", "delete", "project", namespace], check=False)
                        time.sleep(40)
                except Exception:
                    pass

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
                tools.kubectl_bin("delete", "namespace", namespace, "--ignore-not-found")
                tools.kubectl_bin("wait", "--for=delete", f"namespace/{namespace}")
            except subprocess.CalledProcessError:
                pass

            logger.info(f"Create namespace {namespace}")
            tools.kubectl_bin("create", "namespace", namespace)
            tools.kubectl_bin("config", "set-context", "--current", f"--namespace={namespace}")
        return namespace

    return _create_namespace


@pytest.fixture(scope="class")
def create_infra(test_paths: Dict[str, str], create_namespace):
    global _current_namespace
    created_namespaces = []

    def _create_infra(test_name):
        """Create the necessary infrastructure for the tests."""
        global _current_namespace
        logger.info("Creating test environment")
        if os.environ.get("DELETE_CRD_ON_START") == "1":
            tools.delete_crd_rbac(test_paths["src_dir"])
            tools.check_crd_for_deletion(f"{test_paths['src_dir']}/deploy/crd.yaml")

        if os.environ.get("OPERATOR_NS"):
            create_namespace(os.environ.get("OPERATOR_NS"))
            tools.deploy_operator(test_paths["test_dir"], test_paths["src_dir"])
            namespace = create_namespace(f"{test_name}-{random.randint(0, 32767)}")
        else:
            namespace = create_namespace(f"{test_name}-{random.randint(0, 32767)}")
            tools.deploy_operator(test_paths["test_dir"], test_paths["src_dir"])

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
            tools.kubectl_bin(*cmd)
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
    if os.environ.get("OPERATOR_NS"):
        namespaces_to_delete.append(os.environ.get("OPERATOR_NS"))

    for ns in namespaces_to_delete:
        run_cmd(["delete", "--grace-period=0", "--force", "namespace", ns, "--ignore-not-found"])


@pytest.fixture(scope="class")
def deploy_chaos_mesh(namespace: str) -> None:
    """Deploy Chaos Mesh and clean up after tests."""
    try:
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

    except subprocess.CalledProcessError as e:
        try:
            subprocess.run(
                [
                    "helm",
                    "uninstall",
                    "chaos-mesh",
                    "--namespace",
                    namespace,
                    "--ignore-not-found",
                    "--wait",
                    "--timeout",
                    "60s",
                ]
            )
        except (subprocess.CalledProcessError, FileNotFoundError, OSError) as cleanup_error:
            logger.warning(f"Failed to cleanup chaos-mesh during error handling: {cleanup_error}")
        raise e

    yield

    try:
        subprocess.run(
            [
                "helm",
                "uninstall",
                "chaos-mesh",
                "--namespace",
                namespace,
                "--wait",
                "--timeout",
                "60s",
            ],
            check=True,
        )
    except subprocess.CalledProcessError as e:
        logger.error(f"Failed to cleanup chaos-mesh: {e}")


@pytest.fixture(scope="class")
def deploy_cert_manager() -> None:
    """Deploy Cert Manager and clean up after tests."""
    logger.info("Deploying cert-manager")
    cert_manager_url = f"https://github.com/cert-manager/cert-manager/releases/download/v{os.environ.get('CERT_MANAGER_VER')}/cert-manager.yaml"
    try:
        tools.kubectl_bin("create", "namespace", "cert-manager")
        tools.kubectl_bin(
            "label", "namespace", "cert-manager", "certmanager.k8s.io/disable-validation=true"
        )
        tools.kubectl_bin("apply", "-f", cert_manager_url, "--validate=false")
        tools.kubectl_bin(
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
            tools.kubectl_bin("delete", "-f", cert_manager_url, "--ignore-not-found")
        except (subprocess.CalledProcessError, FileNotFoundError, OSError) as cleanup_error:
            logger.warning(
                f"Failed to cleanup cert-manager during error handling: {cleanup_error}"
            )
        raise e

    yield

    try:
        tools.kubectl_bin("delete", "-f", cert_manager_url, "--ignore-not-found")
    except Exception as e:
        logger.error(f"Failed to cleanup cert-manager: {e}")


@pytest.fixture(scope="class")
def deploy_minio() -> None:
    """Deploy MinIO and clean up after tests."""
    service_name = "minio-service"
    bucket = "operator-testing"

    logger.info(f"Installing MinIO: {service_name}")

    subprocess.run(["helm", "uninstall", service_name], capture_output=True, check=False)
    subprocess.run(["helm", "repo", "remove", "minio"], capture_output=True, check=False)
    subprocess.run(["helm", "repo", "add", "minio", "https://charts.min.io/"], check=True)

    endpoint = f"http://{service_name}:9000"
    minio_args = [
        "helm",
        "install",
        service_name,
        "minio/minio",
        "--version",
        os.environ.get("MINIO_VER"),
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

    tools.retry(lambda: subprocess.run(minio_args, check=True), max_attempts=10, delay=60)

    minio_pod = tools.kubectl_bin(
        "get",
        "pods",
        f"--selector=release={service_name}",
        "-o",
        "jsonpath={.items[].metadata.name}",
    ).strip()
    tools.wait_pod(minio_pod)

    operator_ns = os.environ.get("OPERATOR_NS")
    if operator_ns:
        namespace = tools.kubectl_bin(
            "config", "view", "--minify", "-o", "jsonpath={..namespace}"
        ).strip()
        tools.kubectl_bin(
            "create",
            "svc",
            "-n",
            operator_ns,
            "externalname",
            service_name,
            f"--external-name={service_name}.{namespace}.svc.cluster.local",
            "--tcp=9000",
            check=False,
        )

    logger.info(f"Creating MinIO bucket: {bucket}")
    tools.kubectl_bin(
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
def psmdb_client(test_paths: Dict[str, str]) -> tools.MongoManager:
    """Deploy and get the client pod name."""
    tools.kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/client-70.yml")

    result = tools.retry(
        lambda: tools.kubectl_bin(
            "get",
            "pods",
            "--selector=name=psmdb-client",
            "-o",
            "jsonpath={.items[].metadata.name}",
        ),
        condition=lambda result: "container not found" not in result,
    )

    pod_name = result.strip()
    tools.wait_pod(pod_name)
    return tools.MongoManager(pod_name)
