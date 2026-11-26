#!/usr/bin/env python3

import json
import logging
import time
from collections.abc import Callable
from dataclasses import dataclass
from typing import Any

import pytest
import yaml
from lib.config import apply_cluster, render_cluster_config
from lib.kubectl import kubectl_bin, wait_for_running
from lib.utils import Paths

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


def _mongod_resources(sts: str) -> dict[str, Any]:
    data = json.loads(kubectl_bin("get", "statefulset", sts, "-o", "json"))
    for container in data["spec"]["template"]["spec"]["containers"]:
        if container["name"] == "mongod":
            return container.get("resources") or {}
    raise AssertionError(f"statefulset/{sts} has no mongod container")


def _wait_mongod_resources(sts: str, expected: dict[str, Any], attempts: int = 10) -> None:
    actual: dict[str, Any] = {}
    for _ in range(attempts):
        actual = _mongod_resources(sts)
        if actual == expected:
            return
        time.sleep(2)
    raise AssertionError(f"statefulset/{sts} mongod resources: expected {expected}, got {actual}")


class TestLimits:
    """Test PSMDB cluster creation with various CPU/Memory limits and requests"""

    @pytest.mark.parametrize(
        "cluster",
        ["no-limits-rs0", "no-requests-rs0", "no-requests-no-limits-rs0"],
    )
    def test_cr_config(self, test_paths: Paths, cluster: str) -> None:
        """Create cluster, increase resources and check the statefulset picks them up"""
        conf_file = f"{test_paths['test_dir']}/conf/{cluster}.yml"

        apply_cluster(conf_file)
        wait_for_running(cluster, 1, False)

        increased = (
            render_cluster_config(conf_file)
            .replace("300m", "600m")
            .replace("500M", "1G")
            .replace("0.5G", "1G")
        )
        kubectl_bin("apply", "-f", "-", input_data=increased)
        expected = yaml.safe_load(increased)["spec"]["replsets"][0].get("resources") or {}
        _wait_mongod_resources(cluster, expected)

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
