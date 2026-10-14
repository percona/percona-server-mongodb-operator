#!/usr/bin/env python3

import json
import logging
import time
from collections.abc import Callable
from dataclasses import dataclass
from pathlib import Path

import pytest
import yaml
from lib.config import apply_cluster, compare_kubectl
from lib.kubectl import (
    kubectl_bin,
    wait_for_cluster_state,
    wait_for_running,
)
from lib.utils import Paths

logger = logging.getLogger(__name__)

CLUSTER = "some-name"


@dataclass(frozen=True)
class TLSCertManagerConfig:
    namespace: str
    cluster: str


def _wait_all_started(cluster: str) -> None:
    wait_for_running(f"{cluster}-rs0", 3)
    wait_for_running(f"{cluster}-cfg", 3, False)
    wait_for_running(f"{cluster}-mongos", 3)


def _check_tls_secret(secret_name: str) -> None:
    data = json.loads(kubectl_bin("get", f"secrets/{secret_name}", "-o", "json"))
    for key in ("ca.crt", "tls.crt", "tls.key"):
        assert data.get("data", {}).get(key), f"Secret {secret_name} missing data key {key}"


def _deploy_cmctl(src_dir: str, conf_dir: str) -> None:
    """Deploy cmctl pod with RBAC that includes certificates/status access."""
    rbac_path = Path(src_dir) / "deploy" / "rbac.yaml"
    raw = rbac_path.read_text().replace("percona-server-mongodb-operator", "cmctl")

    docs = list(yaml.safe_load_all(raw))
    for doc in docs:
        if not doc:
            continue
        for rule in doc.get("rules", []):
            if "cert-manager.io" in rule.get("apiGroups", []):
                resources = rule.get("resources", [])
                if "certificates/status" not in resources:
                    resources.append("certificates/status")

    combined = "\n---\n".join(yaml.dump(d) for d in docs if d)
    kubectl_bin("apply", "-f", "-", input_data=combined)
    kubectl_bin("apply", "-f", f"{conf_dir}/cmctl.yml")


def _wait_certificate(cert_name: str, timeout: int = 600) -> None:
    """Wait for a cert-manager Certificate to be ready."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            kubectl_bin(
                "wait",
                "--for=condition=Ready",
                f"certificate/{cert_name}",
                "--timeout=60s",
            )
            return
        except Exception:
            time.sleep(1)
    raise TimeoutError(f"Certificate {cert_name} not ready within {timeout}s")


def _renew_certificate(cert_name: str) -> None:
    """Renew a cert-manager certificate using cmctl."""
    _wait_certificate(cert_name)
    logger.info(f"Renewing certificate {cert_name}")

    pod_name = kubectl_bin(
        "get", "pods", "--selector=name=cmctl", "-o", "jsonpath={.items[].metadata.name}"
    ).strip()

    revision = kubectl_bin(
        "get", "certificate", cert_name, "-o", "jsonpath={.status.revision}"
    ).strip()

    kubectl_bin("exec", pod_name, "--", "/tmp/cmctl", "renew", cert_name)

    expected_revision = str(int(revision) + 1)
    for _ in range(10):
        new_revision = kubectl_bin(
            "get", "certificate", cert_name, "-o", "jsonpath={.status.revision}"
        ).strip()
        if new_revision == expected_revision:
            return
        time.sleep(1)


def _pause_cluster(cluster_name: str) -> None:
    logger.info(f"Pausing cluster {cluster_name}")
    kubectl_bin(
        "patch", "psmdb", cluster_name,
        "--type", "merge", "-p", '{"spec": {"pause": true}}',
    )


def _unpause_cluster(cluster_name: str) -> None:
    logger.info(f"Unpausing cluster {cluster_name}")
    kubectl_bin(
        "patch", "psmdb", cluster_name,
        "--type", "merge", "-p", '{"spec": {"pause": false}}',
    )


def _disable_tls(cluster_name: str) -> None:
    logger.info(f"Disabling TLS for cluster {cluster_name}")
    kubectl_bin(
        "patch", "psmdb", cluster_name,
        "--type", "merge",
        "-p", '{"spec": {"unsafeFlags": {"tls": true}, "tls": {"mode": "disabled"}}}',
    )


class TestOperatorManagedCertManager:
    """Test operator-managed cert-manager certificates, renewal, and TLS disable.

    Deploys without custom issuers/certs so the operator creates its own,
    then renews certificates and disables TLS.
    """

    @pytest.fixture(scope="class", autouse=True)
    def config(self, create_infra: Callable[[str], str]) -> TLSCertManagerConfig:
        return TLSCertManagerConfig(
            namespace=create_infra("tls-issue-cert-mgr"),
            cluster=CLUSTER,
        )

    @pytest.fixture(scope="class", autouse=True)
    def setup_tests(
        self,
        config: TLSCertManagerConfig,
        test_paths: Paths,
        deploy_cert_manager: Callable[..., None],
    ) -> None:
        deploy_cert_manager()
        kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/secrets.yml")
        kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/client_with_tls.yml")
        _deploy_cmctl(test_paths["src_dir"], test_paths["conf_dir"])

    @pytest.mark.dependency()
    def test_operator_creates_certificates(
        self, config: TLSCertManagerConfig, test_paths: Paths
    ) -> None:
        """Operator creates its own certs when no custom ones exist."""
        cluster = config.cluster
        test_dir = test_paths["test_dir"]
        ns = config.namespace

        apply_cluster(f"{test_dir}/conf/{cluster}.yml")
        _wait_all_started(cluster)

        for component in ("rs0", "cfg", "mongos"):
            compare_kubectl(test_dir, f"statefulset/{cluster}-{component}", ns)

        _check_tls_secret(f"{cluster}-ssl")

        compare_kubectl(test_dir, f"issuer/{cluster}-psmdb-ca-issuer", ns)
        compare_kubectl(test_dir, f"issuer/{cluster}-psmdb-issuer", ns)
        compare_kubectl(test_dir, f"certificate/{cluster}-ssl", ns)
        compare_kubectl(test_dir, f"certificate/{cluster}-ssl-internal", ns)

    @pytest.mark.dependency(
        depends=["TestOperatorManagedCertManager::test_operator_creates_certificates"]
    )
    def test_certificate_renewal(
        self, config: TLSCertManagerConfig, test_paths: Paths
    ) -> None:
        """Renew both certificates and validate the cluster recovers."""
        cluster = config.cluster
        test_dir = test_paths["test_dir"]
        ns = config.namespace

        _renew_certificate(f"{cluster}-ssl")
        time.sleep(10)
        _wait_all_started(cluster)

        _renew_certificate(f"{cluster}-ssl-internal")
        time.sleep(10)
        _wait_all_started(cluster)

        compare_kubectl(test_dir, f"issuer/{cluster}-psmdb-ca-issuer", ns)
        compare_kubectl(test_dir, f"issuer/{cluster}-psmdb-issuer", ns)
        compare_kubectl(test_dir, f"certificate/{cluster}-ssl", ns)
        compare_kubectl(test_dir, f"certificate/{cluster}-ssl-internal", ns)

    @pytest.mark.dependency(
        depends=["TestOperatorManagedCertManager::test_certificate_renewal"]
    )
    def test_disable_tls(
        self, config: TLSCertManagerConfig, test_paths: Paths
    ) -> None:
        """Pause cluster, disable TLS, unpause, validate statefulsets."""
        cluster = config.cluster
        test_dir = test_paths["test_dir"]
        ns = config.namespace

        _pause_cluster(cluster)
        wait_for_cluster_state(cluster, "paused")

        _disable_tls(cluster)

        _unpause_cluster(cluster)
        wait_for_cluster_state(cluster, "ready")

        for component in ("rs0", "cfg", "mongos"):
            compare_kubectl(
                test_dir,
                f"statefulset/{cluster}-{component}",
                ns,
                postfix="-tls-disabled",
                skip_generation_check=True,
            )
