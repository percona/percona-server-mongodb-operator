#!/usr/bin/env python3

import json
import logging
import subprocess
import time
from collections.abc import Callable
from dataclasses import dataclass

import pytest
from lib.config import apply_cluster
from lib.kubectl import kubectl_bin, wait_for_cluster_state, wait_for_running
from lib.utils import Paths

logger = logging.getLogger(__name__)

LOG_DIR = "/data/db/logs"
LOGROTATE_CONFIG = "/opt/percona/logcollector/logrotate/logrotate.conf"
LOGROTATE_STATUS = "/tmp/e2e-logrotate.status"
CLIENT_PEM = "/tmp/logrotate-mongosh.pem"


@dataclass(frozen=True)
class LogCollectorConfig:
    namespace: str
    cluster: str


@pytest.fixture(scope="class", autouse=True)
def config(create_infra: Callable[[str], str]) -> LogCollectorConfig:
    """Configuration for tests"""
    return LogCollectorConfig(
        namespace=create_infra("logcollector"),
        cluster="logcollector",
    )


@pytest.fixture(scope="class", autouse=True)
def setup_tests(config: LogCollectorConfig, test_paths: Paths) -> None:
    """Setup test environment"""
    kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/secrets.yml")


def _exec_logrotate(pod: str, *command: str) -> str:
    return kubectl_bin("exec", pod, "-c", "logrotate", "--", *command)


def _check_pem_permissions(pod: str) -> None:
    permissions = _exec_logrotate(pod, "stat", "-c", "%a", CLIENT_PEM).strip()
    assert permissions == "600", (
        f"Expected permissions 600 for {CLIENT_PEM} on {pod}, got {permissions}"
    )


def _check_log_rotation(pod: str, tls_enabled: bool) -> None:
    """Force rotation and check the MongoDB log, captured output, and expired files."""
    logger.info(f"Forcing log rotation on {pod}")
    for filename in ("mongod.log", "mongod.full.log"):
        kubectl_bin("exec", pod, "-c", "mongod", "--", "test", "-f", f"{LOG_DIR}/{filename}")

    marker = f"e2e-logrotate-success-{time.time_ns()}"
    _exec_logrotate(
        pod,
        "sh",
        "-c",
        'printf "%s\\n" "$1" >> "$2"',
        "sh",
        marker,
        f"{LOG_DIR}/mongod.full.log",
    )
    old_inode = _exec_logrotate(pod, "stat", "-c", "%i", f"{LOG_DIR}/mongod.log").strip()
    expired_log = f"{LOG_DIR}/mongod.log.e2e-expired"
    _exec_logrotate(pod, "touch", "-t", "200001010000", expired_log)

    _exec_logrotate(
        pod,
        "sh",
        "-c",
        'export PATH="${PATH}:/opt/fluent-bit/bin"; exec "$@"',
        "sh",
        "logrotate",
        "-f",
        "-s",
        LOGROTATE_STATUS,
        LOGROTATE_CONFIG,
    )

    new_inode = _exec_logrotate(pod, "stat", "-c", "%i", f"{LOG_DIR}/mongod.log").strip()
    assert new_inode != old_inode, f"mongod.log was not rotated on {pod}"
    _exec_logrotate(pod, "grep", "-Fq", marker, f"{LOG_DIR}/mongod.full.log.1")
    logger.info(
        f"Checking marker is absent from mongod.full.log on {pod}; grep exit code 1 is expected"
    )
    with pytest.raises(subprocess.CalledProcessError) as error:
        _exec_logrotate(pod, "grep", "-Fq", marker, f"{LOG_DIR}/mongod.full.log")
    assert error.value.returncode == 1, f"Could not check mongod.full.log on {pod}"
    _exec_logrotate(pod, "test", "!", "-e", expired_log)
    if tls_enabled:
        _check_pem_permissions(pod)


def _check_log_rotation_failure(pod: str, tls_enabled: bool) -> None:
    """Authentication failure must fail rotation and retain the restricted client PEM."""
    logger.info(f"Checking log rotation with an invalid password on {pod}")
    error_log = "/tmp/e2e-logrotate-error"
    _exec_logrotate(
        pod,
        "sh",
        "-c",
        'printf "%s\\n" "$1" >> "$2"',
        "sh",
        "e2e-logrotate-failure",
        f"{LOG_DIR}/mongod.full.log",
    )
    logger.info(
        f"Running logrotate with an invalid password on {pod}; a nonzero exit code is expected"
    )
    with pytest.raises(subprocess.CalledProcessError):
        _exec_logrotate(
            pod,
            "env",
            "MONGODB_PASSWORD=e2e-invalid-password",
            "sh",
            "-c",
            'export PATH="${PATH}:/opt/fluent-bit/bin"; '
            'exec logrotate -f -s /tmp/e2e-logrotate.status "$1" > "$2" 2>&1',
            "sh",
            LOGROTATE_CONFIG,
            error_log,
        )
    _exec_logrotate(
        pod, "grep", "-Fq", "ERROR: failed to rotate mongod.log with mongosh", error_log
    )
    if tls_enabled:
        _check_pem_permissions(pod)


class TestLogCollector:
    """Test log rotation with TLS, then disable TLS on the same cluster."""

    @pytest.mark.dependency()
    def test_create_cluster(self, config: LogCollectorConfig, test_paths: Paths) -> None:
        """Create a cluster with TLS and log collection enabled"""
        apply_cluster(f"{test_paths['test_dir']}/conf/{config.cluster}.yml")
        wait_for_running(f"{config.cluster}-rs0", 3)

    @pytest.mark.dependency(depends=["TestLogCollector::test_create_cluster"])
    def test_tls_rotation(self, config: LogCollectorConfig) -> None:
        """Rotate logs on all replica set pods with TLS enabled"""
        for i in range(3):
            _check_log_rotation(f"{config.cluster}-rs0-{i}", tls_enabled=True)

    @pytest.mark.dependency(depends=["TestLogCollector::test_tls_rotation"])
    def test_tls_rotation_failure(self, config: LogCollectorConfig) -> None:
        """Check authentication failure propagates when TLS is enabled"""
        _check_log_rotation_failure(f"{config.cluster}-rs0-0", tls_enabled=True)

    @pytest.mark.dependency(depends=["TestLogCollector::test_tls_rotation_failure"])
    def test_disable_tls(self, config: LogCollectorConfig) -> None:
        """Pause, disable TLS, and restart so plaintext checks use the updated pods."""
        cluster = config.cluster
        kubectl_bin("patch", "psmdb", cluster, "--type=merge", "-p", '{"spec":{"pause":true}}')
        wait_for_cluster_state(cluster, "paused")
        kubectl_bin(
            "patch",
            "psmdb",
            cluster,
            "--type=merge",
            "-p",
            '{"spec":{"unsafeFlags":{"tls":true},"tls":{"mode":"disabled"}}}',
        )
        kubectl_bin("patch", "psmdb", cluster, "--type=merge", "-p", '{"spec":{"pause":false}}')
        wait_for_cluster_state(cluster, "ready")
        wait_for_running(f"{cluster}-rs0", 3)

        for i in range(3):
            pod = json.loads(kubectl_bin("get", "pod", f"{cluster}-rs0-{i}", "-o", "json"))
            rotate = next(c for c in pod["spec"]["containers"] if c["name"] == "logrotate")
            env = {item["name"]: item.get("value") for item in rotate["env"]}
            assert env["MONGODB_TLS_ENABLED"] == "false"
            assert "MONGODB_TLS_INSECURE" not in env
            assert all(mount["name"] != "ssl" for mount in rotate["volumeMounts"])

    @pytest.mark.dependency(depends=["TestLogCollector::test_disable_tls"])
    def test_plaintext_rotation(self, config: LogCollectorConfig) -> None:
        """Rotate logs on all replica set pods after disabling TLS"""
        for i in range(3):
            _check_log_rotation(f"{config.cluster}-rs0-{i}", tls_enabled=False)

    @pytest.mark.dependency(depends=["TestLogCollector::test_plaintext_rotation"])
    def test_plaintext_rotation_failure(self, config: LogCollectorConfig) -> None:
        """Check authentication failure propagates after disabling TLS"""
        _check_log_rotation_failure(f"{config.cluster}-rs0-0", tls_enabled=False)
