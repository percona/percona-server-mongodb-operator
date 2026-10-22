#!/usr/bin/env python3
"""In-place resize across every component of a sharded cluster.

vpa-inplace covers rs0. Each of the other components takes a different branch of
the operator's in-place path, and this suite covers those:

    cfg        container mongod          WiredTiger cache adjusted
    mongos     container mongos          no storage engine, so no cache at all
    nonvoting  container mongod-nv       WiredTiger cache adjusted
    hidden     container mongod-hidden   WiredTiger cache adjusted

Mongos is the interesting one: planInPlaceResize returns early for it, skipping
the cache logic that every other component depends on.

Components are scaled one at a time, which isolates each branch and keeps the
cluster's memory footprint reasonable.
"""

import logging
from collections.abc import Callable
from dataclasses import dataclass

import pytest
from lib.config import apply_cluster
from lib.kubectl import kubectl_bin, wait_cluster_consistency, wait_for_running, wait_pod
from lib.mongo import MongoManager
from lib.utils import Paths
from lib.vpa import (
    GIB,
    create_vpa_object,
    expected_wiredtiger_cache_bytes,
    install_vpa_crd,
    pod_identity,
    pod_ip,
    pod_resources,
    set_vpa_recommendation,
    status_key,
    wait_for_pod_resources,
    wait_for_vpa_status,
    wait_for_wiredtiger_cache,
    wiredtiger_cache_bytes,
)

logger = logging.getLogger(__name__)

CLUSTER = "some-name"
CACHE_RATIO = 0.5
CLUSTER_READY_TIMEOUT = 640

# Declared in conf/some-name.yml. Everything with a storage engine starts at
# requests 1Gi / limits 2Gi, a 2:1 ratio RequestsAndLimits preserves, so a
# recommendation of 2Gi gives a 4Gi limit:
#   2Gi limit -> cache floor(0.5 * 1Gi) = 0.5 GB
#   4Gi limit -> cache floor(0.5 * 3Gi) = 1.5 GB
DECLARED_LIMIT = 2 * GIB
SCALED_LIMIT = 4 * GIB
SCALED_MEMORY = "2Gi"
SCALED_CPU = "500m"

# Mongos has no cache, so it only needs to prove the resize itself.
MONGOS_SCALED_MEMORY = "384Mi"
MONGOS_SCALED_CPU = "400m"


@dataclass(frozen=True)
class Component:
    """One component and everything needed to drive and check it."""

    name: str  # status.vpaStatus key, and lib.vpa component name
    pod: str
    vpa_object: str
    target_sts: str
    container: str
    has_cache: bool


COMPONENTS = {
    "cfg": Component(
        "cfg", f"{CLUSTER}-cfg-0", f"{CLUSTER}-cfg-vpa", f"{CLUSTER}-cfg", "mongod", True
    ),
    "mongos": Component(
        "mongos",
        f"{CLUSTER}-mongos-0",
        f"{CLUSTER}-mongos-vpa",
        f"{CLUSTER}-mongos",
        "mongos",
        False,
    ),
    "nonvoting": Component(
        "nonvoting",
        f"{CLUSTER}-rs0-nv-0",
        f"{CLUSTER}-rs0-nv-vpa",
        f"{CLUSTER}-rs0-nv",
        "mongod-nv",
        True,
    ),
    "hidden": Component(
        "hidden",
        f"{CLUSTER}-rs0-hidden-0",
        f"{CLUSTER}-rs0-hidden-vpa",
        f"{CLUSTER}-rs0-hidden",
        "mongod-hidden",
        True,
    ),
}


@dataclass(frozen=True)
class InPlaceShardedConfig:
    namespace: str


@pytest.fixture(scope="class", autouse=True)
def config(create_infra: Callable[[str], str]) -> InPlaceShardedConfig:
    return InPlaceShardedConfig(namespace=create_infra("vpa-inplace-sharded"))


@pytest.fixture(scope="class", autouse=True)
def setup_tests(test_paths: Paths, config: InPlaceShardedConfig) -> None:
    install_vpa_crd(f"{test_paths['src_dir']}/e2e-tests/vpa/conf/vpa-crd.yml")
    kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/secrets.yml")

    apply_cluster(f"{test_paths['test_dir']}/conf/{CLUSTER}.yml")
    wait_for_running(f"{CLUSTER}-rs0", 3)
    wait_for_running(f"{CLUSTER}-cfg", 3)
    wait_pod(f"{CLUSTER}-rs0-nv-0")
    wait_pod(f"{CLUSTER}-rs0-hidden-0")
    wait_cluster_consistency(CLUSTER, CLUSTER_READY_TIMEOUT)

    for component in COMPONENTS.values():
        create_vpa_object(component.vpa_object, component.target_sts)


def _resize_and_check(
    component: Component,
    psmdb_client: MongoManager,
    cpu: str,
    memory: str,
) -> None:
    """Drive one component through an in-place resize and verify it end to end."""
    before_identity = pod_identity(component.pod, container=component.container)

    if component.has_cache:
        before_cache = wiredtiger_cache_bytes(psmdb_client.client, pod_ip(component.pod))
        assert before_cache == expected_wiredtiger_cache_bytes(DECLARED_LIMIT, CACHE_RATIO), (
            f"{component.name} did not start with the cache implied by its declared memory limit"
        )

    set_vpa_recommendation(component.vpa_object, cpu, memory, container=component.container)
    # The operator keys status by the replset-qualified name (rs0-nv, rs0-hidden),
    # which is not the component name. status_key() maps between the two.
    wait_for_vpa_status(f"{status_key(component.name)}.cpu", cpu, CLUSTER)
    wait_for_pod_resources(component.pod, "memory", memory, container=component.container)

    # Same pod: resized, not replaced.
    assert pod_identity(component.pod, container=component.container) == before_identity, (
        f"{component.name} pod was recreated — uid or restart count changed, "
        f"so this was not an in-place resize"
    )
    assert pod_resources(component.pod, "cpu", container=component.container) == cpu

    if component.has_cache:
        wait_for_wiredtiger_cache(
            psmdb_client.client,
            pod_ip(component.pod),
            expected_wiredtiger_cache_bytes(SCALED_LIMIT, CACHE_RATIO),
        )
        assert pod_identity(component.pod, container=component.container) == before_identity, (
            f"{component.name} pod restarted while its cache was being adjusted"
        )


class TestVPAInPlaceSharded:
    """Every sharded component resizes in place."""

    @pytest.mark.dependency()
    def test_config_server(self, config: InPlaceShardedConfig, psmdb_client: MongoManager) -> None:
        """Config server: same container as rs0, but a separate component path."""
        _resize_and_check(COMPONENTS["cfg"], psmdb_client, SCALED_CPU, SCALED_MEMORY)

    @pytest.mark.dependency(depends=["TestVPAInPlaceSharded::test_config_server"])
    def test_mongos(self, config: InPlaceShardedConfig, psmdb_client: MongoManager) -> None:
        """Mongos: planInPlaceResize returns early, so no cache work happens.

        Worth covering precisely because it skips the logic every other component
        relies on — a regression there would show up here and nowhere else.
        """
        _resize_and_check(
            COMPONENTS["mongos"], psmdb_client, MONGOS_SCALED_CPU, MONGOS_SCALED_MEMORY
        )

    @pytest.mark.dependency(depends=["TestVPAInPlaceSharded::test_mongos"])
    def test_non_voting(self, config: InPlaceShardedConfig, psmdb_client: MongoManager) -> None:
        """Non-voting members: own StatefulSet, own container name."""
        _resize_and_check(COMPONENTS["nonvoting"], psmdb_client, SCALED_CPU, SCALED_MEMORY)

    @pytest.mark.dependency(depends=["TestVPAInPlaceSharded::test_non_voting"])
    def test_hidden(self, config: InPlaceShardedConfig, psmdb_client: MongoManager) -> None:
        """Hidden members: own StatefulSet, own container name."""
        _resize_and_check(COMPONENTS["hidden"], psmdb_client, SCALED_CPU, SCALED_MEMORY)

    @pytest.mark.dependency(depends=["TestVPAInPlaceSharded::test_hidden"])
    def test_components_stayed_isolated(self, config: InPlaceShardedConfig) -> None:
        """Each resize touched only its own component.

        rs0 was never given a recommendation, so it must still be running exactly
        what the CR declares.
        """
        assert pod_resources(f"{CLUSTER}-rs0-0", "memory") == "1Gi", (
            "rs0 resources changed although it was never given a recommendation"
        )
        wait_cluster_consistency(CLUSTER, CLUSTER_READY_TIMEOUT)
