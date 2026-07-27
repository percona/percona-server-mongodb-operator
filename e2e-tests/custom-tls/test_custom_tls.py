#!/usr/bin/env python3

import logging
from collections.abc import Callable
from dataclasses import dataclass

import pytest
import yaml
from lib.config import apply_cluster, compare_kubectl, compare_metadata
from lib.kubectl import kubectl_bin, wait_for_delete, wait_for_running
from lib.utils import Paths

logger = logging.getLogger(__name__)


@dataclass(frozen=True)
class CustomTLSConfig:
    namespace: str
    cluster: str


@pytest.fixture(scope="class", autouse=True)
def config(create_infra: Callable[[str], str]) -> CustomTLSConfig:
    """Configuration for tests"""
    return CustomTLSConfig(
        namespace=create_infra("custom-tls"),
        cluster="some-name",
    )


@pytest.fixture(scope="class", autouse=True)
def setup_tests(test_paths: Paths, destroy_cert_manager: Callable[[], None]) -> None:
    """Destroy cert-manager so certs are issued by the operator, then create secrets and client."""
    destroy_cert_manager()

    kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/secrets.yml")
    kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/client_with_tls.yml")


def _save_operator_secret(secret_name: str) -> str:
    """Return a secret manifest based on the operator-created one with a custom annotation."""
    data = yaml.safe_load(kubectl_bin("get", "secret", secret_name, "-o", "yaml"))
    data.pop("metadata", None)
    data["metadata"] = {"name": secret_name, "annotations": {"my-custom-annotation": "true"}}
    return yaml.dump(data)


def _wait_all_started(cluster: str) -> None:
    wait_for_running(f"{cluster}-rs0", 3)
    wait_for_running(f"{cluster}-cfg", 3, False)
    wait_for_running(f"{cluster}-mongos", 3)


def _compare_statefulsets(test_dir: str, cluster: str, namespace: str) -> None:
    for component in ("rs0", "cfg", "mongos"):
        compare_kubectl(test_dir, f"statefulset/{cluster}-{component}", namespace)


@pytest.fixture(scope="class")
def saved_secrets() -> dict[str, str]:
    """Holds the operator-issued secrets saved during the test class run."""
    return {}


class TestCustomTLS:
    """Test PSMDB cluster with operator-issued and custom TLS certificates"""

    @pytest.mark.dependency()
    def test_create_cluster(
        self, config: CustomTLSConfig, test_paths: Paths, saved_secrets: dict[str, str]
    ) -> None:
        """Create cluster and save the certificates issued by the operator"""
        cluster = config.cluster
        apply_cluster(f"{test_paths['test_dir']}/conf/{cluster}.yml")
        _wait_all_started(cluster)

        saved_secrets["ssl"] = _save_operator_secret(f"{cluster}-ssl")
        saved_secrets["ssl-internal"] = _save_operator_secret(f"{cluster}-ssl-internal")

    @pytest.mark.dependency(depends=["TestCustomTLS::test_create_cluster"])
    def test_custom_non_internal_cert(
        self,
        config: CustomTLSConfig,
        test_paths: Paths,
        saved_secrets: dict[str, str],
        deploy_cert_manager: Callable[..., None],
    ) -> None:
        """Recreate the cluster with a single custom non-internal certificate"""
        cluster = config.cluster

        kubectl_bin("delete", "psmdb", cluster)
        wait_for_delete(f"psmdb/{cluster}", 180)

        kubectl_bin("apply", "-f", "-", input_data=saved_secrets["ssl"])

        # cert-manager must not overwrite the user-provided secret
        deploy_cert_manager()

        apply_cluster(f"{test_paths['test_dir']}/conf/{cluster}.yml")
        _wait_all_started(cluster)

        _compare_statefulsets(test_paths["test_dir"], cluster, config.namespace)
        compare_metadata(test_paths["test_dir"], f"secret/{cluster}-ssl")

    @pytest.mark.dependency(depends=["TestCustomTLS::test_custom_non_internal_cert"])
    def test_custom_internal_cert(
        self, config: CustomTLSConfig, test_paths: Paths, saved_secrets: dict[str, str]
    ) -> None:
        """Recreate the cluster with both custom internal and non-internal certificates"""
        cluster = config.cluster

        kubectl_bin("delete", "psmdb", cluster)
        wait_for_delete(f"psmdb/{cluster}", 180)

        kubectl_bin("apply", "-f", "-", input_data=saved_secrets["ssl-internal"])

        apply_cluster(f"{test_paths['test_dir']}/conf/{cluster}.yml")
        _wait_all_started(cluster)

        _compare_statefulsets(test_paths["test_dir"], cluster, config.namespace)
        compare_metadata(test_paths["test_dir"], f"secret/{cluster}-ssl")
        compare_metadata(test_paths["test_dir"], f"secret/{cluster}-ssl-internal")
