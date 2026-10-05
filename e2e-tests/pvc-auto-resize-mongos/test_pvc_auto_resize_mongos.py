#!/usr/bin/env python3

import json
import logging
from collections.abc import Callable
from dataclasses import dataclass

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
MONGOS_POD = f"{CLUSTER}-mongos-0"
MONGOS_CONTAINER = "mongos"
MONGOS_LOG_DIR = "/data/db/logs"
MONGOS_PVC = f"mongos-logs-{MONGOS_POD}"
RS_PVCS = [f"mongod-data-{CLUSTER}-rs0-{i}" for i in range(3)]
CFG_PVCS = [f"mongod-data-{CLUSTER}-cfg-{i}" for i in range(3)]

INITIAL_SIZE = "1Gi"
# triggerThresholdPercent is 50 and growthStep is 2Gi, so one fill grows 1Gi to 3Gi
GROWN_SIZE = "3Gi"
FILL_MB = 600


@dataclass(frozen=True)
class AutoResizeConfig:
    namespace: str
    cluster: str
    platform: str


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


def _pvc_size(pvc: str) -> str:
    return kubectl_bin("get", "pvc", pvc, "-o", "jsonpath={.status.capacity.storage}").strip()


def _wait_pvc_size(pvc: str, expected: str, max_attempts: int = 60, delay: int = 10) -> None:
    logger.info(f"Waiting for PVC {pvc} to reach {expected}")
    try:
        retry(
            lambda: _pvc_size(pvc),
            max_attempts=max_attempts,
            delay=delay,
            condition=lambda size: size == expected,
        )
    except RuntimeError:
        pytest.fail(f"PVC {pvc} is {_pvc_size(pvc)}, expected {expected}")


def _autoscaling_status(cluster: str) -> dict[str, dict[str, object]]:
    raw = kubectl_bin(
        "get", "psmdb", cluster, "-o", "jsonpath={.status.storageAutoscaling}"
    ).strip()
    return json.loads(raw) if raw else {}


def _fill_disk(pod: str, container: str, path: str, size_mb: int) -> str:
    """Write a file under `path` and return the resulting usage percentage."""
    logger.info(f"Filling {path} on {pod} with a {size_mb}MB file")
    kubectl_bin(
        "exec",
        pod,
        "-c",
        container,
        "--",
        "bash",
        "-c",
        f"dd if=/dev/zero of={path}/fillfile bs=1M count={size_mb} 2>/dev/null || true",
    )
    usage = kubectl_bin(
        "exec",
        pod,
        "-c",
        container,
        "--",
        "bash",
        "-c",
        f"df {path} | tail -1 | awk '{{print $5}}' | sed 's/%//'",
    ).strip()
    logger.info(f"Usage of {path} on {pod} is {usage}%")
    return usage


@pytest.fixture(scope="class", autouse=True)
def config(create_infra: Callable[[str], str]) -> AutoResizeConfig:
    """Configuration for tests"""
    platform = detect_platform()
    return AutoResizeConfig(
        namespace=create_infra("pvc-auto-resize-mongos"),
        cluster=CLUSTER,
        platform=platform,
    )


@pytest.fixture(scope="class", autouse=True)
def setup_tests(config: AutoResizeConfig, test_paths: Paths) -> None:
    """Setup test environment"""
    kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/secrets.yml")

    if config.platform == "eks":
        logger.info("EKS detected, creating a storageclass for expandable EBS volumes")
        kubectl_bin("apply", "-f", f"{test_paths['test_dir']}/conf/eks-storageclass.yml")
        return

    storageclass = _default_storageclass()
    if not _allows_volume_expansion(storageclass):
        pytest.skip(f"default storageclass {storageclass} does not allow volume expansion")


class TestMongosPVCAutoResize:
    """Storage autoscaling of the mongos log volume

    The mongos log volume is the only PVC mongos owns, and it is a PVC only when
    sharding.mongos.logs.persistentVolumeClaim is set. It also differs from every
    replset volume: another claim name, another mount path and another container.
    """

    @pytest.mark.dependency()
    def test_create_cluster(self, config: AutoResizeConfig, test_paths: Paths) -> None:
        """Create a sharded cluster with autoscaling enabled, on 1Gi volumes"""
        manifest = "some-name-eks.yml" if config.platform == "eks" else "some-name.yml"
        apply_cluster(f"{test_paths['test_dir']}/conf/{manifest}")

        wait_for_running(f"{config.cluster}-rs0", 3)
        wait_for_running(f"{config.cluster}-cfg", 3, False)
        wait_pod(MONGOS_POD)

        for pvc in [MONGOS_PVC, *RS_PVCS, *CFG_PVCS]:
            assert _pvc_size(pvc) == INITIAL_SIZE, f"PVC {pvc} did not start at {INITIAL_SIZE}"

    @pytest.mark.dependency(depends=["TestMongosPVCAutoResize::test_create_cluster"])
    def test_mongos_logs_land_on_the_volume(self) -> None:
        """mongos has to write its log to the volume, otherwise the PVC is an empty disk"""
        kubectl_bin(
            "exec",
            MONGOS_POD,
            "-c",
            MONGOS_CONTAINER,
            "--",
            "test",
            "-s",
            f"{MONGOS_LOG_DIR}/mongos.log",
        )

    @pytest.mark.dependency(
        depends=["TestMongosPVCAutoResize::test_mongos_logs_land_on_the_volume"]
    )
    def test_autoscale_mongos_log_pvc(self, config: AutoResizeConfig) -> None:
        """Filling the log volume past the threshold grows it"""
        usage = _fill_disk(MONGOS_POD, MONGOS_CONTAINER, MONGOS_LOG_DIR, FILL_MB)
        assert int(usage) >= 50, f"log volume is only {usage}% full, autoscaling won't trigger"

        _wait_pvc_size(MONGOS_PVC, GROWN_SIZE)

        status = retry(
            lambda: _autoscaling_status(config.cluster),
            max_attempts=30,
            delay=10,
            condition=lambda s: MONGOS_PVC in s,
        )[MONGOS_PVC]

        assert not status.get("lastError"), f"autoscaling reported an error: {status}"
        assert status.get("resizeCount") == 1, f"unexpected resize count: {status}"

        wait_cluster_consistency(config.cluster, 600)

    @pytest.mark.dependency(depends=["TestMongosPVCAutoResize::test_autoscale_mongos_log_pvc"])
    def test_replset_volumes_untouched(self) -> None:
        """Only the volume that filled up grows: every component has its own spec"""
        for pvc in [*RS_PVCS, *CFG_PVCS]:
            assert _pvc_size(pvc) == INITIAL_SIZE, f"PVC {pvc} grew without filling up"
