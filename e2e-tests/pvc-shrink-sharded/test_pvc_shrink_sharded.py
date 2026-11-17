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


def _cr_storage(replset: str) -> str:
    jsonpath = f'{{.spec.replsets[?(@.name=="{replset}")].volumeSpec.persistentVolumeClaim.resources.requests.storage}}'
    return kubectl_bin("get", "psmdb", CLUSTER, "-o", f"jsonpath={jsonpath}").strip()


def _cfg_storage() -> str:
    return kubectl_bin(
        "get",
        "psmdb",
        CLUSTER,
        "-o",
        "jsonpath={.spec.sharding.configsvrReplSet.volumeSpec.persistentVolumeClaim.resources.requests.storage}",
    ).strip()


def _has_shrink_in_progress() -> bool:
    raw = kubectl_bin("get", "psmdb", CLUSTER, "-o", "jsonpath={.status.pvcShrink}").strip()
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


def _replset_index(replset: str) -> int:
    idx_raw = kubectl_bin(
        "get",
        "psmdb",
        CLUSTER,
        "-o",
        "jsonpath={range .spec.replsets[*]}{.name}{'\\n'}{end}",
    ).strip()
    names = idx_raw.splitlines()
    if replset not in names:
        pytest.fail(f"Replset {replset} not found in CR, available: {names}")
    return names.index(replset)


def _patch_replset_storage(replset: str, size: str) -> None:
    idx = _replset_index(replset)
    kubectl_bin(
        "patch",
        "psmdb",
        CLUSTER,
        "--type=json",
        "-p",
        json.dumps(
            [
                {
                    "op": "replace",
                    "path": f"/spec/replsets/{idx}/volumeSpec/persistentVolumeClaim/resources/requests/storage",
                    "value": size,
                }
            ]
        ),
    )


def _patch_cfg_storage(size: str) -> None:
    kubectl_bin(
        "patch",
        "psmdb",
        CLUSTER,
        "--type=merge",
        "-p",
        json.dumps(
            {
                "spec": {
                    "sharding": {
                        "configsvrReplSet": {
                            "volumeSpec": {
                                "persistentVolumeClaim": {
                                    "resources": {"requests": {"storage": size}}
                                }
                            },
                        },
                    },
                },
            }
        ),
    )


def _default_storageclass() -> str:
    return kubectl_bin(
        "get",
        "sc",
        "-o",
        'jsonpath={.items[?(@.metadata.annotations.storageclass\\.kubernetes\\.io/is-default-class=="true")].metadata.name}',
    ).strip()


def _allows_volume_expansion(storageclass: str) -> bool:
    allowed = kubectl_bin(
        "get",
        "sc",
        storageclass,
        "-o",
        "jsonpath={.allowVolumeExpansion}",
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
# Test: parallel shard shrink (2 shards + configsvr)
# ---------------------------------------------------------------------------


class TestPVCShrinkSharded:
    """Shrink two shards in parallel -- both rs0 and rs1 process simultaneously."""

    INITIAL_SIZE = "10Gi"
    SHRUNK_SIZE = "5Gi"
    RS0_PVCS: ClassVar[list[str]] = [f"mongod-data-{CLUSTER}-rs0-{i}" for i in range(3)]
    RS1_PVCS: ClassVar[list[str]] = [f"mongod-data-{CLUSTER}-rs1-{i}" for i in range(3)]
    CFG_PVCS: ClassVar[list[str]] = [f"mongod-data-{CLUSTER}-cfg-{i}" for i in range(3)]

    @pytest.fixture(scope="class", autouse=True)
    @classmethod
    def config(cls, create_infra: Callable[[str], str]) -> PVCShrinkConfig:
        platform = detect_platform()
        return PVCShrinkConfig(
            namespace=create_infra("pvc-shrink-sharded"),
            cluster=CLUSTER,
            platform=platform,
        )

    @pytest.fixture(scope="class", autouse=True)
    @classmethod
    def setup_tests(cls, config: PVCShrinkConfig, test_paths: Paths) -> None:
        kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/secrets.yml")
        _setup_storageclass(config.platform, test_paths)

    @pytest.mark.dependency()
    def test_01_create_sharded_cluster(self, config: PVCShrinkConfig, test_paths: Paths) -> None:
        apply_cluster(f"{test_paths['test_dir']}/conf/some-name-sharded.yml")
        wait_for_running(f"{config.cluster}-rs0", 3)
        wait_for_running(f"{config.cluster}-rs1", 3)
        wait_for_running(f"{config.cluster}-cfg", 3, False)
        wait_pod(f"{config.cluster}-mongos-0")
        wait_pod(f"{config.cluster}-mongos-1")

        for pvc in self.RS0_PVCS + self.RS1_PVCS + self.CFG_PVCS:
            assert _pvc_size(pvc) == self.INITIAL_SIZE, (
                f"PVC {pvc} did not start at {self.INITIAL_SIZE}"
            )

    @pytest.mark.dependency(depends=["TestPVCShrinkSharded::test_01_create_sharded_cluster"])
    def test_02_shrink_both_shards(self, config: PVCShrinkConfig) -> None:
        """Patch both rs0 and rs1 storage to 5Gi simultaneously."""
        _patch_replset_storage("rs0", self.SHRUNK_SIZE)
        _patch_replset_storage("rs1", self.SHRUNK_SIZE)
        time.sleep(10)

        assert _has_shrink_in_progress(), "pvcShrink status should be set"

        _wait_no_shrink_in_progress(max_attempts=240)

        for pvc in self.RS0_PVCS + self.RS1_PVCS:
            _wait_pvc_size(pvc, self.SHRUNK_SIZE)

        wait_cluster_consistency(config.cluster, 900)

    @pytest.mark.dependency(depends=["TestPVCShrinkSharded::test_02_shrink_both_shards"])
    def test_03_cfg_untouched(self) -> None:
        """Config server PVCs should not be affected by shard shrink."""
        for pvc in self.CFG_PVCS:
            assert _pvc_size(pvc) == self.INITIAL_SIZE, (
                f"Config PVC {pvc} should still be {self.INITIAL_SIZE}"
            )

    @pytest.mark.dependency(depends=["TestPVCShrinkSharded::test_02_shrink_both_shards"])
    def test_04_shrink_configsvr(self, config: PVCShrinkConfig) -> None:
        """Shrink config server PVCs after shard shrink completed."""
        _patch_cfg_storage(self.SHRUNK_SIZE)
        time.sleep(10)

        _wait_no_shrink_in_progress()

        for pvc in self.CFG_PVCS:
            _wait_pvc_size(pvc, self.SHRUNK_SIZE)

        wait_cluster_consistency(config.cluster, 900)
