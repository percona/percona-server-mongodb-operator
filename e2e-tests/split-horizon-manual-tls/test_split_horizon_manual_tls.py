#!/usr/bin/env python3

import hashlib
import json
import logging
import os
import re
import subprocess
import tempfile
from collections.abc import Callable
from dataclasses import dataclass
from functools import partial

import pytest
from lib.config import apply_cluster
from lib.kubectl import (
    kubectl_bin,
    wait_cluster_consistency,
    wait_for_delete,
    wait_for_running,
    wait_pod,
)
from lib.secrets import get_secret_data
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
    def ca_secret(self) -> str:
        return f"{self.cluster}-ca-cert"

    @property
    def tls_secrets(self) -> tuple[str, str]:
        return f"{self.cluster}-ssl", f"{self.cluster}-ssl-internal"

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
        namespace=create_infra("split-horizon-manual-tls"),
        cluster="some-name",
    )


@pytest.fixture(scope="class", autouse=True)
def setup_tests(test_paths: Paths, destroy_cert_manager: Callable[[], None]) -> None:
    """Destroy cert-manager so certs are issued by the operator, then create secrets and client"""
    destroy_cert_manager()

    kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/secrets.yml")
    kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/client-70-tls.yml")


@pytest.fixture(scope="class")
def cert_fingerprints() -> dict[str, str]:
    """Holds certificate fingerprints saved during the test class run."""
    return {}


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


def _run_mongosh_tls(command: str, uri: str) -> str:
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


def _tls_crt(secret: str) -> str:
    return get_secret_data(secret, "tls\\.crt")


def _cert_fingerprint(secret: str) -> str:
    return hashlib.sha256(_tls_crt(secret).encode()).hexdigest()


def _cert_sans(secret: str) -> list[str]:
    text = subprocess.run(
        ["openssl", "x509", "-noout", "-text"],
        input=_tls_crt(secret).encode(),
        capture_output=True,
        check=True,
    ).stdout.decode()
    return re.findall(r"DNS:([^,\s]+)", text)


def _assert_cert_san(secret: str, san: str) -> None:
    sans = _cert_sans(secret)
    assert san in sans, f"SAN {san} not found in secret {secret}: {sans}"
    logger.info(f"Secret {secret} has SAN {san}")


def _assert_no_horizon_sans(secret: str) -> None:
    sans = [san for san in _cert_sans(secret) if san.endswith(f".{HORIZON_DOMAIN}")]
    assert not sans, f"secret {secret} still has horizon SANs: {sans}"
    logger.info(f"Secret {secret} has no horizon SANs")


def _assert_no_horizons_in_rs_conf(config: SplitHorizonConfig) -> None:
    out = _run_mongosh_tls(
        "EJSON.stringify(rs.conf().members.filter(m => m.horizons).map(m => m.host))",
        config.internal_uri,
    )
    assert json.loads(out) == [], f"members still have horizons: {out}"
    logger.info("No replset member has horizons in rs.conf()")


def _assert_ca_secret(secret: str) -> None:
    for key in ("ca\\.crt", "ca\\.key"):
        assert get_secret_data(secret, key), f"secret {secret} has no {key}"
    logger.info(f"Secret {secret} has ca.crt and ca.key")


def _assert_signed_by_ca(tls_secret: str, ca_secret: str) -> None:
    with (
        tempfile.NamedTemporaryFile("w", suffix=".crt") as ca,
        tempfile.NamedTemporaryFile("w", suffix=".crt") as crt,
    ):
        ca.write(get_secret_data(ca_secret, "ca\\.crt"))
        ca.flush()
        crt.write(_tls_crt(tls_secret))
        crt.flush()
        result = subprocess.run(
            ["openssl", "verify", "-CAfile", ca.name, crt.name],
            capture_output=True,
            text=True,
            check=False,
        )
    assert result.returncode == 0, (
        f"{tls_secret} is not signed by CA in {ca_secret}: {result.stdout}{result.stderr}"
    )
    logger.info(f"Secret {tls_secret} is signed by CA in {ca_secret}")


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


class TestSplitHorizonManualTLS:
    """Test replica set split horizons with operator issued certificates"""

    @pytest.mark.dependency()
    def test_create_cluster(
        self, config: SplitHorizonConfig, test_paths: Paths, cert_fingerprints: dict[str, str]
    ) -> None:
        """Create cluster with 3 horizons and check certificates are signed by the operator CA"""
        apply_cluster(f"{test_paths['test_dir']}/conf/{config.cluster}-3horizons.yml")
        _wait_cluster(config, 3)

        _assert_ca_secret(config.ca_secret)
        for secret in config.tls_secrets:
            _assert_signed_by_ca(secret, config.ca_secret)
            for i in range(3):
                _assert_cert_san(secret, f"{config.replset}-{i}.{HORIZON_DOMAIN}")
            cert_fingerprints[secret] = _cert_fingerprint(secret)

    @pytest.mark.dependency(depends=["TestSplitHorizonManualTLS::test_create_cluster"])
    def test_connect_via_horizons(self, config: SplitHorizonConfig, test_paths: Paths) -> None:
        """Connect through horizon hostnames and check horizons in rs.conf()"""
        _configure_client_host_aliases()

        retry(
            lambda: _compare_horizons(config, f"{test_paths['test_dir']}/compare/horizons-3.json"),
            max_attempts=6,
            delay=5,
        )

    @pytest.mark.dependency(depends=["TestSplitHorizonManualTLS::test_connect_via_horizons"])
    def test_certificates_resigned_with_new_horizons(
        self, config: SplitHorizonConfig, test_paths: Paths, cert_fingerprints: dict[str, str]
    ) -> None:
        """Add horizons for pods 3 and 4 and check certificates are re-signed by the same CA"""
        apply_cluster(f"{test_paths['test_dir']}/conf/{config.cluster}-5horizons.yml")
        _wait_cluster(config, 3)

        for secret in config.tls_secrets:
            for i in (3, 4):
                san = f"{config.replset}-{i}.{HORIZON_DOMAIN}"
                retry(partial(_assert_cert_san, secret, san), max_attempts=30, delay=10)

            assert _cert_fingerprint(secret) != cert_fingerprints[secret], (
                f"certificate in {secret} was not re-signed after horizon update"
            )
            logger.info(f"Certificate in {secret} was re-signed after horizon update")
            _assert_signed_by_ca(secret, config.ca_secret)

    @pytest.mark.dependency(
        depends=["TestSplitHorizonManualTLS::test_certificates_resigned_with_new_horizons"]
    )
    def test_scale_up(self, config: SplitHorizonConfig, test_paths: Paths) -> None:
        """Scale up to 5 members and check all horizons are configured"""
        _scale(config.cluster, 5)
        _wait_cluster(config, 5)
        _compare_horizons(config, f"{test_paths['test_dir']}/compare/horizons-5.json")

    @pytest.mark.dependency(depends=["TestSplitHorizonManualTLS::test_scale_up"])
    def test_scale_down(self, config: SplitHorizonConfig, test_paths: Paths) -> None:
        """Scale down to 3 members and check horizons of removed members are gone"""
        _scale(config.cluster, 3)
        _wait_cluster(config, 3)
        _compare_horizons(config, f"{test_paths['test_dir']}/compare/horizons-3.json")

    @pytest.mark.dependency(depends=["TestSplitHorizonManualTLS::test_scale_down"])
    def test_remove_horizons(self, config: SplitHorizonConfig, test_paths: Paths) -> None:
        """Remove horizon configuration and check it's gone from rs.conf() and certificates"""
        apply_cluster(f"{test_paths['test_dir']}/conf/{config.cluster}.yml")
        _wait_cluster(config, 3)

        retry(lambda: _assert_no_horizons_in_rs_conf(config), max_attempts=12, delay=5)

        for secret in config.tls_secrets:
            retry(partial(_assert_no_horizon_sans, secret), max_attempts=30, delay=10)
            _assert_signed_by_ca(secret, config.ca_secret)
