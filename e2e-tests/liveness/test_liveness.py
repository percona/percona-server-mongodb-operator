#!/usr/bin/env python3

import logging
import re
from collections.abc import Callable
from dataclasses import dataclass
from typing import Any

import pytest
from lib.config import apply_cluster, compare_kubectl
from lib.kubectl import kubectl_bin, wait_for_running
from lib.utils import Paths

logger = logging.getLogger(__name__)


@dataclass(frozen=True)
class LivenessConfig:
    namespace: str
    cluster: str


@pytest.fixture(scope="class", autouse=True)
def config(create_infra: Callable[[str], str]) -> LivenessConfig:
    """Configuration for tests"""
    return LivenessConfig(
        namespace=create_infra("liveness"),
        cluster="liveness",
    )


@pytest.fixture(scope="class", autouse=True)
def setup_tests(test_paths: Paths, deploy_minio: Any) -> None:
    """Setup test environment"""
    kubectl_bin(
        "apply",
        "-f",
        f"{test_paths['conf_dir']}/secrets.yml",
        "-f",
        f"{test_paths['conf_dir']}/minio-secret.yml",
    )


class TestLiveness:
    @pytest.mark.dependency()
    def test_create_first_cluster(self, config: LivenessConfig, test_paths: Paths) -> None:
        """Create first PSMDB cluster"""
        apply_cluster(f"{test_paths['test_dir']}/conf/{config.cluster}-rs0.yml")
        wait_for_running(f"{config.cluster}-rs0", 3)

        compare_kubectl(
            test_paths["test_dir"], f"statefulset/{config.cluster}-rs0", config.namespace
        )

    @pytest.mark.dependency(depends=["TestLiveness::test_create_first_cluster"])
    def test_liveness_check_fails_with_invalid_ssl_option(self, config: LivenessConfig) -> None:
        kubectl_bin(
            "exec",
            f"{config.cluster}-rs0-0",
            "-c",
            "mongod",
            "--",
            "bash",
            "-c",
            "/opt/percona/mongodb-healthcheck k8s liveness --ssl",
            check=False,
        )

        wait_for_running(f"{config.cluster}-rs0", 3)

        logs_output = kubectl_bin(
            "exec",
            f"{config.cluster}-rs0-0",
            "-c",
            "mongod",
            "--",
            "bash",
            "-c",
            "ls /data/db/mongod-data/logs",
        )
        log_count = logs_output.count("mongodb-healthcheck.log")
        assert log_count == 1, f"Expected 1 healthcheck log file, got {log_count}"

        rotated_count = len(re.findall(r"mongodb-healthcheck-.*\.log\.gz", logs_output))
        assert rotated_count >= 1, f"Expected >=1 rotated logs, got {rotated_count}"

    @pytest.mark.dependency(
        depends=["TestLiveness::test_liveness_check_fails_with_invalid_ssl_option"]
    )
    def test_change_liveness_config(self, config: LivenessConfig, test_paths: Paths) -> None:
        apply_cluster(f"{test_paths['test_dir']}/conf/{config.cluster}-rs0-changed.yml")

        wait_for_running(f"{config.cluster}-rs0", 3)

        compare_kubectl(
            test_paths["test_dir"],
            f"statefulset/{config.cluster}-rs0",
            config.namespace,
            "-changed",
        )
