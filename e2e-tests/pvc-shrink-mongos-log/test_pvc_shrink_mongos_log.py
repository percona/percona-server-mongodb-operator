import json
import logging
import time
from collections.abc import Callable
from dataclasses import dataclass
from typing import ClassVar

import pytest
from lib.config import apply_cluster
from lib.kubectl import (
    detect_platform,
    kubectl_bin,
    wait_cluster_consistency,
    wait_for_running,
    wait_pod,
)
from lib.utils import Paths, retry

logger = logging.getLogger(__name__)

CLUSTER = "some-name"


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def _pvc_size(pvc: str) -> str:
    return kubectl_bin("get", "pvc", pvc, "-o", "jsonpath={.status.capacity.storage}").strip()


def _wait_pvc_size(pvc: str, expected: str, max_attempts: int = 120, delay: int = 10) -> None:
    logger.info(f"Waiting for PVC {pvc} to reach size {expected}")
    try:
        retry(
            lambda: _pvc_size(pvc),
            max_attempts=max_attempts,
            delay=delay,
            condition=lambda size: size == expected,
        )
    except RuntimeError:
        actual = _pvc_size(pvc)
        pytest.fail(f"PVC {pvc} is {actual}, expected {expected}")


def _has_shrink_in_progress() -> bool:
    raw = kubectl_bin(
        "get", "psmdb", CLUSTER, "-o", "jsonpath={.status.pvcShrink}"
    ).strip()
    return raw != "" and raw != "{}"


def _wait_no_shrink_in_progress(max_attempts: int = 180, delay: int = 10) -> None:
    logger.info("Waiting for pvcShrink status to be cleared")
    try:
        retry(
            _has_shrink_in_progress,
            max_attempts=max_attempts,
            delay=delay,
            condition=lambda has: not has,
        )
    except RuntimeError:
        pytest.fail("pvcShrink status still present after timeout")


def _patch_mongos_log_storage(size: str) -> None:
    kubectl_bin(
        "patch", "psmdb", CLUSTER, "--type=merge", "-p",
        json.dumps({
            "spec": {
                "sharding": {
                    "mongos": {
                        "logs": {"persistentVolumeClaim": {"resources": {"requests": {"storage": size}}}},
                    },
                },
            },
        }),
    )


def _mongos_log_storage() -> str:
    return kubectl_bin(
        "get", "psmdb", CLUSTER, "-o",
        "jsonpath={.spec.sharding.mongos.logs.persistentVolumeClaim.resources.requests.storage}",
    ).strip()


def _default_storageclass() -> str:
    return kubectl_bin(
        "get", "sc", "-o",
        'jsonpath={.items[?(@.metadata.annotations.storageclass\\.kubernetes\\.io/is-default-class=="true")].metadata.name}',
    ).strip()


def _allows_volume_expansion(storageclass: str) -> bool:
    allowed = kubectl_bin(
        "get", "sc", storageclass, "-o", "jsonpath={.allowVolumeExpansion}",
        check=False,
    ).strip()
    return allowed == "true"


def _setup_storageclass(platform: str, test_paths: Paths) -> None:
    if platform == "eks":
        logger.info("EKS detected, creating storageclass for expandable EBS volumes")
        kubectl_bin("apply", "-f", f"{test_paths['test_dir']}/conf/eks-storageclass.yml")
        return
    storageclass = _default_storageclass()
    if not _allows_volume_expansion(storageclass):
        pytest.skip(f"default storageclass {storageclass} does not allow volume expansion")


@dataclass(frozen=True)
class PVCShrinkConfig:
    namespace: str
    cluster: str
    platform: str


# ---------------------------------------------------------------------------
# Test: mongos log PVC shrink (logcollector with PVC)
# ---------------------------------------------------------------------------

class TestPVCShrinkMongosLog:
    """Shrink mongos log PVCs (disposable data, no initial sync)."""

    INITIAL_SIZE = "3Gi"
    SHRUNK_SIZE = "2Gi"
    MONGOS_LOG_PVCS: ClassVar[list[str]] = [f"mongos-logs-{CLUSTER}-mongos-{i}" for i in range(2)]

    @pytest.fixture(scope="class", autouse=True)
    @classmethod
    def config(cls, create_infra: Callable[[str], str]) -> PVCShrinkConfig:
        platform = detect_platform()
        return PVCShrinkConfig(
            namespace=create_infra("pvc-shrink-mongos-log"),
            cluster=CLUSTER,
            platform=platform,
        )

    @pytest.fixture(scope="class", autouse=True)
    @classmethod
    def setup_tests(cls, config: PVCShrinkConfig, test_paths: Paths) -> None:
        kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/secrets.yml")
        _setup_storageclass(config.platform, test_paths)

    @pytest.mark.dependency()
    def test_01_create_cluster(self, config: PVCShrinkConfig, test_paths: Paths) -> None:
        apply_cluster(f"{test_paths['test_dir']}/conf/some-name-sharded-logcollector.yml")
        wait_for_running(f"{config.cluster}-rs0", 3)
        wait_for_running(f"{config.cluster}-cfg", 3, False)
        wait_pod(f"{config.cluster}-mongos-0")
        wait_pod(f"{config.cluster}-mongos-1")

        wait_cluster_consistency(config.cluster, 900)

        for pvc in self.MONGOS_LOG_PVCS:
            assert _pvc_size(pvc) == self.INITIAL_SIZE, f"PVC {pvc} did not start at {self.INITIAL_SIZE}"

    @pytest.mark.dependency(depends=["TestPVCShrinkMongosLog::test_01_create_cluster"])
    def test_02_shrink_mongos_log(self, config: PVCShrinkConfig) -> None:
        """Shrink mongos log PVCs from 3Gi to 2Gi."""
        _patch_mongos_log_storage(self.SHRUNK_SIZE)
        time.sleep(10)

        _wait_no_shrink_in_progress(max_attempts=120)

        for pvc in self.MONGOS_LOG_PVCS:
            _wait_pvc_size(pvc, self.SHRUNK_SIZE)

        wait_pod(f"{config.cluster}-mongos-0")
        wait_pod(f"{config.cluster}-mongos-1")
        wait_cluster_consistency(config.cluster, 900)

    @pytest.mark.dependency(depends=["TestPVCShrinkMongosLog::test_02_shrink_mongos_log"])
    def test_03_expand_mongos_log(self, config: PVCShrinkConfig) -> None:
        """Expand mongos log PVCs back to 3Gi after shrink."""
        _patch_mongos_log_storage(self.INITIAL_SIZE)
        time.sleep(10)

        assert not _has_shrink_in_progress(), "Shrink status should NOT be set for expand"

        for pvc in self.MONGOS_LOG_PVCS:
            _wait_pvc_size(pvc, self.INITIAL_SIZE)

        wait_cluster_consistency(config.cluster, 900)
