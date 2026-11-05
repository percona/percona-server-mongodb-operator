#!/usr/bin/env python3

import base64
import json
import logging
import os
import re
import subprocess
from collections.abc import Callable
from dataclasses import dataclass
from functools import partial

import pytest
from lib.config import apply_cluster, compare_kubectl
from lib.kubectl import (
    kubectl_bin,
    wait_cluster_consistency,
    wait_for_delete,
    wait_for_running,
    wait_pod,
)
from lib.utils import Paths, retry

logger = logging.getLogger(__name__)

HORIZON_DOMAIN = "clouddemo.xyz"


@dataclass(frozen=True)
class SplitHorizonConfig:
    namespace: str
    cluster: str

    @property
    def replset(self) -> str:
        return f"{self.cluster}-rs0"

    @property
    def uri(self) -> str:
        hosts = ",".join(f"{self.replset}-{i}.{HORIZON_DOMAIN}" for i in range(3))
        return f"mongodb://clusterAdmin:clusterAdmin123456@{hosts}/admin?replicaSet=rs0"

    @property
    def internal_uri(self) -> str:
        host = f"{self.replset}.{self.namespace}.svc.cluster.local"
        return f"mongodb+srv://clusterAdmin:clusterAdmin123456@{host}/admin?replicaSet=rs0"


@pytest.fixture(scope="class", autouse=True)
def config(create_infra: Callable[[str], str]) -> SplitHorizonConfig:
    """Configuration for tests"""
    return SplitHorizonConfig(
        namespace=create_infra("split-horizon"),
        cluster="some-name",
    )


@pytest.fixture(scope="class", autouse=True)
def setup_tests(test_paths: Paths, deploy_cert_manager: Callable[..., None]) -> None:
    """Deploy cert-manager, secrets and the TLS client"""
    deploy_cert_manager()

    kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/secrets.yml")
    kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/client-70-tls.yml")


def _client_pod() -> str:
    return kubectl_bin(
        "get", "pods", "--selector=name=psmdb-client", "-o", "jsonpath={.items[0].metadata.name}"
    ).strip()


def _configure_client_host_aliases() -> None:
    """Resolve <service>.clouddemo.xyz to the service ClusterIP inside the client pod."""
    services = json.loads(kubectl_bin("get", "svc", "-o", "json"))["items"]
    host_aliases = [
        {
            "ip": svc["spec"]["clusterIP"],
            "hostnames": [f"{svc['metadata']['name']}.{HORIZON_DOMAIN}"],
        }
        for svc in services
        if svc["spec"].get("clusterIP", "None") != "None"
    ]

    kubectl_bin("scale", "deployment", "psmdb-client", "--replicas=0")
    wait_for_delete(f"pod/{_client_pod()}")

    patch = [
        {"op": "replace", "path": "/spec/template/spec/hostAliases", "value": host_aliases},
        {"op": "replace", "path": "/spec/replicas", "value": 1},
    ]
    kubectl_bin("patch", "deployment", "psmdb-client", "--type=json", "-p", json.dumps(patch))

    pod = retry(_client_pod, max_attempts=30, delay=2, condition=lambda name: name != "")
    wait_pod(pod)


def _run_mongosh_tls(command: str, uri: str, check: bool = True) -> str:
    return kubectl_bin(
        "exec",
        _client_pod(),
        "--",
        "env",
        "HOME=/tmp",
        "mongosh",
        uri,
        "--tls",
        "--tlsCAFile",
        "/etc/mongodb-ssl/ca.crt",
        "--tlsCertificateKeyFile",
        "/tmp/tls.pem",
        "--tlsAllowInvalidHostnames",
        "--quiet",
        "--eval",
        command,
        check=check,
    ).strip()


def _compare_horizons(config: SplitHorizonConfig, expected_file: str) -> None:
    out = _run_mongosh_tls(
        "EJSON.stringify(rs.conf().members.map(m => m.horizons)"
        ".sort((a, b) => a.external.localeCompare(b.external)))",
        config.uri,
    )
    with open(expected_file) as f:
        expected = json.load(f)
    actual = json.loads(out)
    assert actual == expected, (
        f"rs.conf() horizons {actual} don't match {expected_file}: {expected}"
    )
    logger.info(f"rs.conf() horizons match {os.path.basename(expected_file)}: {actual}")


def _assert_writable_primary(config: SplitHorizonConfig) -> None:
    out = _run_mongosh_tls("db.hello().isWritablePrimary", config.uri)
    assert out == "true", "mongo client should've redirected the connection to primary"
    logger.info("Connected through horizon hostnames and landed on a writable primary")


def _cert_sans(secret: str) -> list[str]:
    cert = base64.b64decode(
        kubectl_bin("get", "secret", secret, "-o", "jsonpath={.data.tls\\.crt}")
    )
    text = subprocess.run(
        ["openssl", "x509", "-noout", "-text"], input=cert, capture_output=True, check=True
    ).stdout.decode()
    return re.findall(r"DNS:([^,\s]+)", text)


def _assert_cert_san(secret: str, san: str) -> None:
    sans = _cert_sans(secret)
    assert san in sans, f"SAN {san} not found in secret {secret}: {sans}"
    logger.info(f"Secret {secret} has SAN {san}")


def _assert_no_horizons_in_rs_conf(config: SplitHorizonConfig) -> None:
    out = _run_mongosh_tls(
        "EJSON.stringify(rs.conf().members.filter(m => m.horizons).map(m => m.host))",
        config.internal_uri,
    )
    assert json.loads(out) == [], f"members still have horizons: {out}"
    logger.info("No replset member has horizons in rs.conf()")


def _scale(cluster: str, size: int) -> None:
    kubectl_bin(
        "patch",
        "psmdb",
        cluster,
        "--type=json",
        "-p",
        f'[{{"op": "replace", "path": "/spec/replsets/0/size", "value": {size}}}]',
    )


def _wait_cluster(config: SplitHorizonConfig, size: int) -> None:
    wait_for_running(config.replset, size)
    wait_cluster_consistency(config.cluster)


class TestSplitHorizon:
    """Test replica set split horizons with cert-manager issued certificates"""

    @pytest.mark.dependency()
    def test_create_cluster(self, config: SplitHorizonConfig, test_paths: Paths) -> None:
        """Create cluster with 3 horizons and compare certificates and issuers"""
        cluster = config.cluster
        apply_cluster(f"{test_paths['test_dir']}/conf/{cluster}-3horizons.yml")
        _wait_cluster(config, 3)

        for resource in (
            f"certificate/{cluster}-ssl",
            f"certificate/{cluster}-ssl-internal",
            f"certificate/{cluster}-ca-cert",
            f"issuer/{cluster}-psmdb-ca-issuer",
            f"issuer/{cluster}-psmdb-issuer",
        ):
            compare_kubectl(test_paths["test_dir"], resource, config.namespace)
            logger.info(f"{resource} matches expected spec")

    @pytest.mark.dependency(depends=["TestSplitHorizon::test_create_cluster"])
    def test_connect_via_horizons(self, config: SplitHorizonConfig, test_paths: Paths) -> None:
        """Connect through horizon hostnames and get redirected to the primary"""
        _configure_client_host_aliases()

        retry(
            lambda: _compare_horizons(config, f"{test_paths['test_dir']}/compare/horizons-3.json"),
            max_attempts=6,
            delay=5,
        )
        _assert_writable_primary(config)

        # step down so we know redirection isn't just because primary is pod-0
        logger.info("Stepping down primary to check redirection follows the new primary")
        _run_mongosh_tls("rs.stepDown()", config.uri, check=False)
        retry(lambda: _assert_writable_primary(config), max_attempts=12, delay=5)

    @pytest.mark.dependency(depends=["TestSplitHorizon::test_connect_via_horizons"])
    def test_certificates_updated_with_new_horizons(
        self, config: SplitHorizonConfig, test_paths: Paths
    ) -> None:
        """Add horizons for pods 3 and 4 and check certificates include them"""
        cluster = config.cluster
        apply_cluster(f"{test_paths['test_dir']}/conf/{cluster}-5horizons.yml")
        _wait_cluster(config, 3)

        for secret in (f"{cluster}-ssl", f"{cluster}-ssl-internal"):
            kubectl_bin("wait", "--for=condition=Ready", f"certificate/{secret}", "--timeout=300s")
            compare_kubectl(
                test_paths["test_dir"],
                f"certificate/{secret}",
                config.namespace,
                postfix="-5horizons",
                skip_generation_check=True,
            )
            logger.info(f"certificate/{secret} matches expected spec with 5 horizons")
            for i in (3, 4):
                san = f"{config.replset}-{i}.{HORIZON_DOMAIN}"
                retry(partial(_assert_cert_san, secret, san), max_attempts=30, delay=10)

        _wait_cluster(config, 3)

        for secret in (f"{cluster}-ssl-old", f"{cluster}-ssl-internal-old"):
            out = kubectl_bin("get", "secret", secret, "--ignore-not-found", "-o", "name")
            assert not out.strip(), f"secret {secret} should be deleted after certificate update"
            logger.info(f"Secret {secret} was deleted after certificate update")

    @pytest.mark.dependency(
        depends=["TestSplitHorizon::test_certificates_updated_with_new_horizons"]
    )
    def test_scale_up(self, config: SplitHorizonConfig, test_paths: Paths) -> None:
        """Scale up to 5 members and check all horizons are configured"""
        _scale(config.cluster, 5)
        _wait_cluster(config, 5)
        _compare_horizons(config, f"{test_paths['test_dir']}/compare/horizons-5.json")

    @pytest.mark.dependency(depends=["TestSplitHorizon::test_scale_up"])
    def test_scale_down(self, config: SplitHorizonConfig, test_paths: Paths) -> None:
        """Scale down to 3 members and check horizons of removed members are gone"""
        _scale(config.cluster, 3)
        _wait_cluster(config, 3)
        _compare_horizons(config, f"{test_paths['test_dir']}/compare/horizons-3.json")

    @pytest.mark.dependency(depends=["TestSplitHorizon::test_scale_down"])
    def test_remove_horizons(self, config: SplitHorizonConfig, test_paths: Paths) -> None:
        """Remove horizon configuration and check it's gone from rs.conf()"""
        apply_cluster(f"{test_paths['test_dir']}/conf/{config.cluster}.yml")
        _wait_cluster(config, 3)

        retry(lambda: _assert_no_horizons_in_rs_conf(config), max_attempts=12, delay=5)
