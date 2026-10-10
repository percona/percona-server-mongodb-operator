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
from lib.kubectl import kubectl_bin, wait_for_delete, wait_for_running
from lib.utils import Paths, retry

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


class TestUserManagedCertManager:
    """Test cert-manager with user-managed issuers and certificates.

    Step 1: operator issues certs using user-provided issuerConf and leaves issuers untouched.
    Step 2: operator preserves user-created certificates.
    """

    @pytest.fixture(scope="class", autouse=True)
    def config(self, create_infra: Callable[[str], str]) -> TLSCertManagerConfig:
        return TLSCertManagerConfig(
            namespace=create_infra("tls-issue-cert-manager"),
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

        test_dir = test_paths["test_dir"]
        for resource in ("some-name-psmdb-ca-issuer", "some-name-psmdb-issuer", "some-name-ca-cert"):
            retry(
                lambda r=resource: kubectl_bin("apply", "-f", f"{test_dir}/conf/{r}.yml"),
                max_attempts=10,
                delay=10,
            )

        _deploy_cmctl(test_paths["src_dir"], test_paths["conf_dir"])
        time.sleep(60)

    @pytest.mark.dependency()
    def test_user_issuer_not_overwritten(
        self, config: TLSCertManagerConfig, test_paths: Paths
    ) -> None:
        """Deploy with issuerConf, validate operator preserves user issuers."""
        cluster = config.cluster
        test_dir = test_paths["test_dir"]
        ns = config.namespace

        apply_cluster(f"{test_dir}/conf/{cluster}-user-issuer.yml")
        _wait_all_started(cluster)

        compare_kubectl(test_dir, f"issuer/{cluster}-psmdb-ca-issuer", ns, postfix="-custom")
        compare_kubectl(test_dir, f"issuer/{cluster}-psmdb-issuer", ns, postfix="-custom")
        compare_kubectl(test_dir, f"certificate/{cluster}-ca-cert", ns, postfix="-custom")

        compare_kubectl(test_dir, f"certificate/{cluster}-ssl", ns)
        compare_kubectl(test_dir, f"certificate/{cluster}-ssl-internal", ns)

        _check_tls_secret(f"{cluster}-ssl")

    @pytest.mark.dependency(depends=["TestUserManagedCertManager::test_user_issuer_not_overwritten"])
    def test_user_certificates_preserved(
        self, config: TLSCertManagerConfig, test_paths: Paths
    ) -> None:
        """Pre-create certs, deploy cluster, validate operator preserves them."""
        cluster = config.cluster
        test_dir = test_paths["test_dir"]
        ns = config.namespace

        kubectl_bin("delete", "psmdb", "--all")
        wait_for_delete(f"psmdb/{cluster}", 180)
        kubectl_bin("delete", "pvc", "--all")
        kubectl_bin(
            "delete", "secret", f"{cluster}-ssl", f"{cluster}-ssl-internal",
            "--ignore-not-found",
        )

        kubectl_bin("apply", "-f", f"{test_dir}/conf/some-name-ssl-internal.yml")
        kubectl_bin("apply", "-f", f"{test_dir}/conf/some-name-ssl.yml")
        time.sleep(60)

        apply_cluster(f"{test_dir}/conf/{cluster}.yml")
        _wait_all_started(cluster)

        compare_kubectl(test_dir, f"certificate/{cluster}-ssl", ns, postfix="-custom")
        compare_kubectl(test_dir, f"certificate/{cluster}-ssl-internal", ns, postfix="-custom")
        compare_kubectl(test_dir, f"certificate/{cluster}-ca-cert", ns, postfix="-custom")
        compare_kubectl(test_dir, f"issuer/{cluster}-psmdb-ca-issuer", ns, postfix="-custom")
        compare_kubectl(test_dir, f"issuer/{cluster}-psmdb-issuer", ns, postfix="-custom")
