#!/usr/bin/env python3

import json
import logging
import secrets
import subprocess
import time
from collections.abc import Callable
from dataclasses import dataclass
from urllib.parse import quote_plus

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


def _capture_restart_state(cluster: str) -> dict[str, object]:
    pods = json.loads(
        kubectl_bin("get", "pods", *(f"{cluster}-rs0-{i}" for i in range(3)), "-o", "json")
    )
    sts = json.loads(kubectl_bin("get", "statefulset", f"{cluster}-rs0", "-o", "json"))
    return {
        "pods": {
            pod["metadata"]["name"]: {
                "uid": pod["metadata"]["uid"],
                "restarts": {
                    c["name"]: c["restartCount"] for c in pod["status"]["containerStatuses"]
                },
            }
            for pod in pods["items"]
        },
        "currentRevision": sts["status"]["currentRevision"],
        "updateRevision": sts["status"]["updateRevision"],
    }


def _check_password_rotation(cluster: str, tls_enabled: bool) -> None:
    """Reload clusterAdmin credentials without restarting pods or containers."""
    before = _capture_restart_state(cluster)
    password = secrets.token_urlsafe(24) + "@:/%?#"
    kubectl_bin(
        "patch",
        "secret",
        "some-users",
        "--type=merge",
        "-p",
        json.dumps({"stringData": {"MONGODB_CLUSTER_ADMIN_PASSWORD": password}}),
    )

    expected = quote_plus(password)
    deadline = time.monotonic() + 180
    pending = [f"{cluster}-rs0-{i}" for i in range(3)]
    while pending and time.monotonic() < deadline:
        status = json.loads(kubectl_bin("get", "psmdb", cluster, "-o", "json"))["status"]
        assert status.get("state", "").lower() not in {"error", "failed"}, (
            f"Cluster failed during password rotation: {status}"
        )
        pending = [
            pod
            for pod in pending
            if _exec_logrotate(
                pod,
                "cat",
                "/etc/users-secret/MONGODB_CLUSTER_ADMIN_PASSWORD_ESCAPED",
            )
            != expected
        ]
        if pending:
            time.sleep(2)
    assert not pending, f"Updated clusterAdmin password was not projected on: {pending}"

    for i in range(3):
        _check_log_rotation(f"{cluster}-rs0-{i}", tls_enabled=tls_enabled)
    assert _capture_restart_state(cluster) == before, "Password rotation caused a restart"


def _check_log_rotation(pod: str, tls_enabled: bool) -> str:
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
    return marker


def _check_log_rotation_failure(pod: str, tls_enabled: bool) -> None:
    """Report authentication failure while still rotating captured output."""
    logger.info(f"Checking log rotation with an invalid password on {pod}")
    error_log = "/tmp/e2e-logrotate-error"
    failure_config = "/tmp/e2e-logrotate-failure.conf"
    _exec_logrotate(
        pod,
        "sh",
        "-c",
        "mkdir -p /tmp/e2e-logrotate-users && "
        "cp /etc/users-secret/MONGODB_CLUSTER_ADMIN_USER_ESCAPED /tmp/e2e-logrotate-users/ && "
        'printf "%s" e2e-invalid-password '
        "> /tmp/e2e-logrotate-users/MONGODB_CLUSTER_ADMIN_PASSWORD_ESCAPED && "
        'sed "s|/etc/users-secret|/tmp/e2e-logrotate-users|g" "$1" > "$2"',
        "sh",
        LOGROTATE_CONFIG,
        failure_config,
    )
    marker = f"e2e-logrotate-failure-{time.time_ns()}"
    _exec_logrotate(
        pod,
        "sh",
        "-c",
        'printf "%s\\n" "$1" >> "$2"',
        "sh",
        marker,
        f"{LOG_DIR}/mongod.full.log",
    )
    logger.info(
        f"Running logrotate with an invalid password on {pod}; a nonzero exit code is expected"
    )
    with pytest.raises(subprocess.CalledProcessError):
        _exec_logrotate(
            pod,
            "sh",
            "-c",
            'export PATH="${PATH}:/opt/fluent-bit/bin"; '
            'exec logrotate -f -s /tmp/e2e-logrotate.status "$1" > "$2" 2>&1',
            "sh",
            failure_config,
            error_log,
        )
    _exec_logrotate(
        pod, "grep", "-Fq", "ERROR: failed to rotate mongod.log with mongosh", error_log
    )
    _exec_logrotate(pod, "grep", "-Fq", "Authentication failed", error_log)
    _exec_logrotate(pod, "grep", "-Fq", marker, f"{LOG_DIR}/mongod.full.log.1")
    logger.info(
        f"Checking marker is absent from mongod.full.log on {pod}; grep exit code 1 is expected"
    )
    with pytest.raises(subprocess.CalledProcessError) as error:
        _exec_logrotate(pod, "grep", "-Fq", marker, f"{LOG_DIR}/mongod.full.log")
    assert error.value.returncode == 1, f"Could not check mongod.full.log on {pod}"
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
        markers = {}
        for i in range(3):
            pod = f"{config.cluster}-rs0-{i}"
            markers[pod] = _check_log_rotation(pod, tls_enabled=True)

        time.sleep(1)
        for pod, marker in markers.items():
            _check_log_rotation(pod, tls_enabled=True)
            older_logs = _exec_logrotate(
                pod, "sh", "-c", 'exec gzip -cd "$1"/mongod.full.log.*.gz', "sh", LOG_DIR
            )
            assert marker in older_logs, f"Rotation marker missing from compressed logs on {pod}"

    @pytest.mark.dependency(depends=["TestLogCollector::test_tls_rotation"])
    def test_tls_rotation_failure(self, config: LogCollectorConfig) -> None:
        """Check authentication failure propagates when TLS is enabled"""
        _check_log_rotation_failure(f"{config.cluster}-rs0-0", tls_enabled=True)

    @pytest.mark.dependency(depends=["TestLogCollector::test_tls_rotation_failure"])
    def test_tls_password_rotation(self, config: LogCollectorConfig) -> None:
        """Rotate logs after a password change while TLS is enabled."""
        _check_password_rotation(config.cluster, tls_enabled=True)

    @pytest.mark.dependency(depends=["TestLogCollector::test_tls_password_rotation"])
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

    @pytest.mark.dependency(depends=["TestLogCollector::test_plaintext_rotation_failure"])
    def test_plaintext_password_rotation(self, config: LogCollectorConfig) -> None:
        """Rotate logs after a password change while TLS is disabled."""
        _check_password_rotation(config.cluster, tls_enabled=False)
