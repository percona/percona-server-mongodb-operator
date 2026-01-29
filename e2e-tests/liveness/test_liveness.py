#!/usr/bin/env python3

import logging
import re
from typing import Callable, Dict

import pytest
from lib import tools

logger = logging.getLogger(__name__)


@pytest.fixture(scope="class", autouse=True)
def config(create_infra: Callable[[str], str]) -> Dict[str, str]:
    """Configuration for tests"""
    return {
        "namespace": create_infra("liveness"),
        "cluster": "liveness",
    }


@pytest.fixture(scope="class", autouse=True)
def setup_tests(test_paths: Dict[str, str], deploy_minio) -> None:
    """Setup test environment"""
    tools.kubectl_bin(
        "apply",
        "-f",
        f"{test_paths['conf_dir']}/secrets.yml",
        "-f",
        f"{test_paths['conf_dir']}/minio-secret.yml",
    )


class TestLiveness:
    @pytest.mark.dependency()
    def test_create_first_cluster(self, config, test_paths):
        """Create first PSMDB cluster"""
        tools.apply_cluster(f"{test_paths['test_dir']}/conf/{config['cluster']}-rs0.yml")
        tools.wait_for_running(f"{config['cluster']}-rs0", 3)

        tools.compare_kubectl(
            test_paths["test_dir"], f"statefulset/{config['cluster']}-rs0", config["namespace"]
        )

    @pytest.mark.dependency(depends=["TestLiveness::test_create_first_cluster"])
    def test_liveness_check_fails_with_invalid_ssl_option(self, config: Dict[str, str]) -> None:
        tools.kubectl_bin(
            "exec",
            f"{config['cluster']}-rs0-0",
            "-c",
            "mongod",
            "--",
            "bash",
            "-c",
            "/opt/percona/mongodb-healthcheck k8s liveness --ssl",
            check=False,
        )

        logs_output = tools.kubectl_bin(
            "exec",
            f"{config['cluster']}-rs0-0",
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
    def test_change_liveness_config(
        self, config: Dict[str, str], test_paths: Dict[str, str]
    ) -> None:
        tools.apply_cluster(f"{test_paths['test_dir']}/conf/{config['cluster']}-rs0-changed.yml")

        tools.wait_for_running(f"{config['cluster']}-rs0", 3)

        tools.compare_kubectl(
            test_paths["test_dir"],
            f"statefulset/{config['cluster']}-rs0",
            config["namespace"],
            "-changed",
        )
