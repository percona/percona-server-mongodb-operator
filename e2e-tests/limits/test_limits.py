#!/usr/bin/env python3

import logging
from collections.abc import Callable
from dataclasses import dataclass

import pytest
from lib.config import apply_cluster, compare_kubectl, render_cluster_config
from lib.kubectl import kubectl_bin, wait_for_running
from lib.utils import Paths, retry

logger = logging.getLogger(__name__)


@dataclass(frozen=True)
class LimitsConfig:
    namespace: str


@pytest.fixture(scope="class", autouse=True)
def config(create_infra: Callable[[str], str]) -> LimitsConfig:
    """Configuration for tests"""
    return LimitsConfig(namespace=create_infra("limits"))


@pytest.fixture(scope="class", autouse=True)
def setup_tests(test_paths: Paths) -> None:
    """Setup test environment"""
    kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/secrets.yml")


class TestLimits:
    """Test PSMDB cluster creation with various CPU/Memory limits and requests"""

    @pytest.mark.parametrize(
        "cluster",
        ["no-limits-rs0", "no-requests-rs0", "no-requests-no-limits-rs0"],
    )
    def test_cr_config(self, config: LimitsConfig, test_paths: Paths, cluster: str) -> None:
        """Create cluster, verify statefulset, increase resources and verify again"""
        conf_file = f"{test_paths['test_dir']}/conf/{cluster}.yml"

        apply_cluster(conf_file)
        wait_for_running(cluster, 1, False)
        compare_kubectl(test_paths["test_dir"], f"statefulset/{cluster}", config.namespace)

        increased = (
            render_cluster_config(conf_file)
            .replace("300m", "600m")
            .replace("500M", "1G")
            .replace("0.5G", "1G")
        )
        kubectl_bin("apply", "-f", "-", input_data=increased)
        retry(
            lambda: compare_kubectl(
                test_paths["test_dir"], f"statefulset/{cluster}", config.namespace, "-increased"
            ),
            max_attempts=10,
            delay=2,
        )

        kubectl_bin("delete", "-f", conf_file)

    def test_no_storage_rejected(self, test_paths: Paths) -> None:
        """A CR without volumeSpec must be rejected by the API server"""
        conf_file = f"{test_paths['test_dir']}/conf/no-storage-rs0.yml"
        result = kubectl_bin(
            "apply",
            "-f",
            "-",
            input_data=render_cluster_config(conf_file),
            check=False,
            return_stderr=True,
        )
        assert "volumeSpec: Required value" in result
