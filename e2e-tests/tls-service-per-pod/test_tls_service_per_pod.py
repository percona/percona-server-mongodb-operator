#!/usr/bin/env python3

import base64
import logging
import subprocess
import time
from collections.abc import Callable
from dataclasses import dataclass

import pytest
from lib.config import apply_cluster
from lib.kubectl import kubectl_bin, wait_cluster_consistency, wait_for_running, wait_pod
from lib.utils import Paths, retry

logger = logging.getLogger(__name__)

MONGOS_SIZE = 3
SSL_HASH_JSONPATH = r"jsonpath={.spec.template.metadata.annotations.percona\.com/ssl-hash}"


@dataclass(frozen=True)
class TLSServicePerPodConfig:
    namespace: str
    cluster: str


@pytest.fixture(scope="class", autouse=True)
def config(create_infra: Callable[[str], str]) -> TLSServicePerPodConfig:
    """Configuration for tests"""
    return TLSServicePerPodConfig(
        namespace=create_infra("tls-service-per-pod"),
        cluster="some-name",
    )


@pytest.fixture(scope="class", autouse=True)
def setup_tests(deploy_cert_manager: Callable[..., None], test_paths: Paths) -> None:
    """Setup test environment"""
    deploy_cert_manager()
    kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/secrets.yml")


@pytest.fixture(scope="class")
def state() -> dict[str, object]:
    """Holds ssl hashes and pod UIDs captured between test steps."""
    return {}


def _mongos_host(cluster: str, namespace: str, idx: int) -> str:
    return f"{cluster}-mongos-{idx}.{namespace}.svc.cluster.local"


def _ssl_hash(sts: str) -> str:
    return kubectl_bin("get", "sts", sts, "-o", SSL_HASH_JSONPATH).strip()


def _ssl_hashes(cluster: str) -> dict[str, str]:
    return {f"{cluster}-{c}": _ssl_hash(f"{cluster}-{c}") for c in ("rs0", "cfg", "mongos")}


def _pod_uids(cluster: str) -> dict[str, str]:
    out = kubectl_bin(
        "get",
        "pods",
        "-l",
        f"app.kubernetes.io/instance={cluster}",
        "-o",
        'jsonpath={range .items[*]}{.metadata.name}={.metadata.uid}{"\\n"}{end}',
    )
    return dict(line.split("=", 1) for line in out.split() if "=" in line)


def _cert_sans(cluster: str) -> set[str]:
    crt = kubectl_bin(
        "get", "secret", f"{cluster}-ssl", "-o", r"jsonpath={.data.tls\.crt}"
    ).strip()
    out = subprocess.run(
        ["openssl", "x509", "-noout", "-ext", "subjectAltName"],
        input=base64.b64decode(crt),
        capture_output=True,
        check=True,
    ).stdout.decode()
    return {
        entry.strip().removeprefix("DNS:")
        for line in out.splitlines()
        for entry in line.split(",")
        if entry.strip().startswith("DNS:")
    }


def _expected_per_pod_sans(cluster: str, namespace: str, size: int) -> set[str]:
    return {_mongos_host(cluster, namespace, i) for i in range(size)}


def _service_exists(name: str) -> bool:
    return bool(
        kubectl_bin(
            "get", "service", name, "--ignore-not-found", "-o", "name", check=False
        ).strip()
    )


def _wait_for_service(name: str, present: bool) -> None:
    retry(
        lambda: _service_exists(name),
        max_attempts=60,
        delay=5,
        condition=lambda exists: exists is present,
    )
    assert _service_exists(name) is present, f"service {name} present={_service_exists(name)}"


def _scale_mongos(cluster: str, size: int) -> None:
    kubectl_bin(
        "patch",
        "psmdb",
        cluster,
        "--type=merge",
        "--patch",
        f'{{"spec":{{"sharding":{{"mongos":{{"size":{size}}}}}}}}}',
    )
    wait_for_running(f"{cluster}-mongos", size)
    wait_cluster_consistency(cluster)


def _deploy_tls_client(conf_dir: str) -> str:
    kubectl_bin("apply", "-f", f"{conf_dir}/client-70-tls.yml")
    pod: str = retry(
        lambda: kubectl_bin(
            "get",
            "pods",
            "--selector=name=psmdb-client",
            "-o",
            "jsonpath={range .items[*]}{.metadata.name}{end}",
        ).strip(),
        max_attempts=30,
        delay=2,
        condition=lambda result: result != "",
    )
    wait_pod(pod)
    return pod


def _check_tls_connection(client: str, host: str) -> None:
    """Connect with hostname verification on; run_mongosh passes --tlsAllowInvalidHostnames."""
    out = kubectl_bin(
        "exec",
        client,
        "--",
        "env",
        "HOME=/tmp",
        "mongosh",
        f"mongodb://{host}:27017/admin",
        "--tls",
        "--tlsCAFile",
        "/etc/mongodb-ssl/ca.crt",
        "--tlsCertificateKeyFile",
        "/tmp/tls.pem",
        "--quiet",
        "--eval",
        "JSON.stringify(db.runCommand({ping:1}))",
        check=False,
        return_stderr=True,
    )
    assert '"ok":1' in out.replace(" ", ""), f"TLS connection to {host} failed: {out}"


class TestTLSServicePerPod:
    """Test TLS certificates cover the per-pod mongos services"""

    @pytest.mark.dependency()
    def test_create_cluster(
        self, config: TLSServicePerPodConfig, test_paths: Paths, state: dict[str, object]
    ) -> None:
        """Create a sharded cluster with requireTLS and no per-pod mongos services yet"""
        cluster = config.cluster
        apply_cluster(f"{test_paths['test_dir']}/conf/{cluster}.yml")
        wait_for_running(f"{cluster}-rs0", 3)
        wait_for_running(f"{cluster}-cfg", 3, False)
        wait_for_running(f"{cluster}-mongos", MONGOS_SIZE)

        state["hash_before"] = _ssl_hash(f"{cluster}-mongos")
        state["client"] = _deploy_tls_client(test_paths["conf_dir"])

    @pytest.mark.dependency(depends=["TestTLSServicePerPod::test_create_cluster"])
    def test_enable_service_per_pod(
        self, config: TLSServicePerPodConfig, state: dict[str, object]
    ) -> None:
        """Enabling servicePerPod adds the per-pod hostnames to the certificate SANs"""
        cluster, namespace = config.cluster, config.namespace

        kubectl_bin(
            "patch",
            "psmdb",
            cluster,
            "--type=merge",
            "--patch",
            '{"spec":{"sharding":{"mongos":{"expose":{"servicePerPod":true}}}}}',
        )
        wait_for_running(f"{cluster}-mongos", MONGOS_SIZE)
        _wait_for_service(f"{cluster}-mongos-0", True)
        _wait_for_service(f"{cluster}-mongos", False)

        expected = _expected_per_pod_sans(cluster, namespace, MONGOS_SIZE)
        retry(
            lambda: _cert_sans(cluster),
            max_attempts=30,
            delay=10,
            condition=lambda sans: expected <= sans,
        )
        assert expected <= _cert_sans(cluster), "per-pod mongos SANs missing from the certificate"

        retry(
            lambda: _ssl_hash(f"{cluster}-mongos"),
            max_attempts=30,
            delay=10,
            condition=lambda h: h != state["hash_before"],
        )
        assert _ssl_hash(f"{cluster}-mongos") != state["hash_before"], (
            "certificate was not re-issued for the new SANs"
        )
        wait_cluster_consistency(cluster)

    @pytest.mark.dependency(depends=["TestTLSServicePerPod::test_enable_service_per_pod"])
    def test_tls_connection_to_per_pod_services(
        self, config: TLSServicePerPodConfig, state: dict[str, object]
    ) -> None:
        """Clients can verify the certificate hostname against the per-pod services"""
        client = str(state["client"])
        for idx in (0, MONGOS_SIZE - 1):
            _check_tls_connection(client, _mongos_host(config.cluster, config.namespace, idx))

    @pytest.mark.dependency(
        depends=["TestTLSServicePerPod::test_tls_connection_to_per_pod_services"]
    )
    def test_scale_down_keeps_certificate(
        self, config: TLSServicePerPodConfig, state: dict[str, object]
    ) -> None:
        """Scaling mongos down keeps the SANs, so no certificate re-issue and no restart"""
        cluster, namespace = config.cluster, config.namespace
        removed = f"{cluster}-mongos-{MONGOS_SIZE - 1}"

        state["hashes"] = _ssl_hashes(cluster)
        uids = {n: u for n, u in _pod_uids(cluster).items() if n != removed}

        _scale_mongos(cluster, MONGOS_SIZE - 1)
        _wait_for_service(removed, False)
        time.sleep(60)  # let the operator reconcile before claiming nothing changed

        assert _expected_per_pod_sans(cluster, namespace, MONGOS_SIZE) <= _cert_sans(cluster), (
            "SANs of the removed mongos pod were dropped from the certificate"
        )
        assert _ssl_hashes(cluster) == state["hashes"], "certificate was re-issued on scale down"
        assert uids.items() <= _pod_uids(cluster).items(), "pods were restarted on scale down"

    @pytest.mark.dependency(depends=["TestTLSServicePerPod::test_scale_down_keeps_certificate"])
    def test_scale_up_reuses_certificate(
        self, config: TLSServicePerPodConfig, state: dict[str, object]
    ) -> None:
        """Scaling mongos back up reuses the kept SANs, so still no re-issue and no restart"""
        cluster, namespace = config.cluster, config.namespace
        restored = f"{cluster}-mongos-{MONGOS_SIZE - 1}"
        uids = _pod_uids(cluster)

        _scale_mongos(cluster, MONGOS_SIZE)
        _wait_for_service(restored, True)

        assert _ssl_hashes(cluster) == state["hashes"], "certificate was re-issued on scale up"
        assert uids.items() <= _pod_uids(cluster).items(), "pods were restarted on scale up"
        _check_tls_connection(
            str(state["client"]), _mongos_host(cluster, namespace, MONGOS_SIZE - 1)
        )
