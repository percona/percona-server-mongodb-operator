#!/usr/bin/env python3
"""In-place resize of VPA-committed resources.

With spec.vpa.inPlaceResize the operator applies a committed recommendation by
resizing running pods instead of recreating them. Two things have to hold for
that to be worth anything, and this suite checks both:

  * the pod really is the same pod — same uid, same restart count — and the
    kubelet has applied the new resources to it;
  * mongod really is running with the new WiredTiger cache size, not just the
    one it computed at startup. The cache is derived from the memory limit, so
    the fixture uses controlledValues RequestsAndLimits to make limits move.

Scenarios:
  1. scale up   - pod resized in place, cache grows
  2. scale down - pod resized in place, cache shrinks
  3. fallback   - a change beyond the container's resources recreates the pod
"""

import json
import logging
import time
from collections.abc import Callable
from dataclasses import dataclass

import pytest
from lib.config import apply_cluster
from lib.kubectl import kubectl_bin, wait_cluster_consistency, wait_for_running
from lib.mongo import MongoManager
from lib.utils import Paths, retry
from lib.vpa import (
    GIB,
    create_vpa_object,
    expected_wiredtiger_cache_bytes,
    get_last_applied_at,
    install_vpa_crd,
    pod_identity,
    pod_resources,
    set_vpa_recommendation,
    wait_for_new_apply,
    wait_for_pod_resources,
    wait_for_requests,
    wait_for_wiredtiger_cache,
    wiredtiger_cache_bytes,
)

logger = logging.getLogger(__name__)

CLUSTER = "some-name"
VPA_OBJECT = f"{CLUSTER}-rs0-vpa"
POD = f"{CLUSTER}-rs0-0"
CACHE_RATIO = 0.5
STABILIZATION_WINDOW = 30

# Declared in conf/some-name.yml: requests 1Gi / limits 2Gi, a 2:1 ratio that
# RequestsAndLimits preserves, so a recommendation of N gives a limit of 2N.
DECLARED_MEMORY_LIMIT = 2 * GIB

# Chosen so every expected cache size is distinct and none hits the 0.25 GB floor.
#   recommend 2Gi -> limit 4Gi -> cache floor(0.5 * 3Gi) = 1.5 GB
#   recommend 1536Mi -> limit 3Gi -> cache floor(0.5 * 2Gi) = 1.0 GB
SCALE_UP_MEMORY = "2Gi"
SCALE_UP_LIMIT = 4 * GIB
SCALE_DOWN_MEMORY = "1536Mi"
SCALE_DOWN_LIMIT = 3 * GIB


@dataclass(frozen=True)
class InPlaceConfig:
    namespace: str


@pytest.fixture(scope="class", autouse=True)
def config(create_infra: Callable[[str], str]) -> InPlaceConfig:
    return InPlaceConfig(namespace=create_infra("vpa-inplace"))


@pytest.fixture(scope="class", autouse=True)
def setup_tests(test_paths: Paths, config: InPlaceConfig) -> None:
    install_vpa_crd(f"{test_paths['src_dir']}/e2e-tests/vpa/conf/vpa-crd.yml")
    kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/secrets.yml")

    apply_cluster(f"{test_paths['test_dir']}/conf/{CLUSTER}.yml")
    wait_for_running(f"{CLUSTER}-rs0", 3)
    wait_cluster_consistency(CLUSTER, 640)

    create_vpa_object(VPA_OBJECT, f"{CLUSTER}-rs0")


def _host(config: InPlaceConfig) -> str:
    return f"{POD}.{CLUSTER}-rs0.{config.namespace}.svc.cluster.local"


def _require_inplace_supported() -> None:
    """In-place resize needs Kubernetes 1.33+; skip rather than fail below that."""
    version = kubectl_bin("version", "-o", "json")
    minor = json.loads(version)["serverVersion"]["minor"]
    if int("".join(c for c in minor if c.isdigit())) < 33:
        pytest.skip(f"in-place resize needs Kubernetes 1.33+, server is 1.{minor}")


class TestVPAInPlaceResize:
    """Resizing pods in place instead of recreating them."""

    @pytest.mark.dependency()
    def test_scale_up_resizes_in_place(
        self, config: InPlaceConfig, psmdb_client: MongoManager
    ) -> None:
        """A larger recommendation reaches the pod and mongod without a restart."""
        _require_inplace_supported()

        before_identity = pod_identity(POD)
        before_cache = wiredtiger_cache_bytes(psmdb_client.client, _host(config))
        logger.info(f"before: identity={before_identity} cache={before_cache}")

        # Sanity: mongod started with the cache implied by the declared limit.
        assert before_cache == expected_wiredtiger_cache_bytes(
            DECLARED_MEMORY_LIMIT, CACHE_RATIO
        ), "mongod did not start with the cache size implied by its declared memory limit"

        set_vpa_recommendation(VPA_OBJECT, "500m", SCALE_UP_MEMORY)
        wait_for_requests("rs0", "memory", SCALE_UP_MEMORY, CLUSTER)
        # The StatefulSet updates on commit; the pod is moved to the new revision
        # later by the SmartUpdate loop, so wait for the pod itself.
        wait_for_pod_resources(POD, "memory", SCALE_UP_MEMORY)

        # The pod must be the same pod: in-place resize, not a replacement.
        assert pod_identity(POD) == before_identity, (
            "pod was recreated — uid or restart count changed, so this was not an in-place resize"
        )

        # The kubelet applied the new values to the running container.
        assert pod_resources(POD, "memory") == SCALE_UP_MEMORY
        assert pod_resources(POD, "cpu") == "500m"

        # And mongod is actually using the larger cache.
        wait_for_wiredtiger_cache(
            psmdb_client.client,
            _host(config),
            expected_wiredtiger_cache_bytes(SCALE_UP_LIMIT, CACHE_RATIO),
        )
        assert pod_identity(POD) == before_identity, "pod restarted while the cache was growing"

    @pytest.mark.dependency(depends=["TestVPAInPlaceResize::test_scale_up_resizes_in_place"])
    def test_scale_down_resizes_in_place(
        self, config: InPlaceConfig, psmdb_client: MongoManager
    ) -> None:
        """A smaller recommendation shrinks the cache before the limit, still without a restart."""
        before_identity = pod_identity(POD)
        previous_apply = get_last_applied_at("rs0", CLUSTER)

        time.sleep(STABILIZATION_WINDOW + 5)
        set_vpa_recommendation(VPA_OBJECT, "300m", SCALE_DOWN_MEMORY)
        wait_for_new_apply("rs0", previous_apply, CLUSTER)
        wait_for_requests("rs0", "memory", SCALE_DOWN_MEMORY, CLUSTER)
        wait_for_pod_resources(POD, "memory", SCALE_DOWN_MEMORY)

        assert pod_identity(POD) == before_identity, (
            "pod was recreated on scale down — the cache shrink should have allowed an in-place resize"
        )
        assert pod_resources(POD, "memory") == SCALE_DOWN_MEMORY

        wait_for_wiredtiger_cache(
            psmdb_client.client,
            _host(config),
            expected_wiredtiger_cache_bytes(SCALE_DOWN_LIMIT, CACHE_RATIO),
        )
        assert pod_identity(POD) == before_identity, "pod restarted while the cache was shrinking"

    @pytest.mark.dependency(depends=["TestVPAInPlaceResize::test_scale_down_resizes_in_place"])
    def test_non_resource_change_recreates_the_pod(self, config: InPlaceConfig) -> None:
        """A change beyond the container's resources must fall back to recreating pods.

        In-place resize is only valid when the two controller revisions differ in
        nothing but the managed container's resources. Anything else — here an
        added annotation on the pod template — has to go through the normal
        SmartUpdate path.
        """
        before_identity = pod_identity(POD)

        # A JSON patch targets the exact path. A merge patch would replace the
        # whole replsets list and drop required fields such as volumeSpec.
        kubectl_bin(
            "patch",
            "psmdb",
            CLUSTER,
            "--type=json",
            "-p",
            '[{"op":"add","path":"/spec/replsets/0/annotations",'
            '"value":{"vpa-inplace.percona.com/forced-restart":"1"}}]',
        )

        # The pod is replaced, so its uid changes.
        def identity_changed() -> bool:
            return pod_identity(POD) != before_identity

        retry(identity_changed, max_attempts=120, delay=5, condition=lambda changed: changed)

        wait_for_running(f"{CLUSTER}-rs0", 3)
        wait_cluster_consistency(CLUSTER, 640)
        assert pod_identity(POD) != before_identity, (
            "pod should have been recreated for a non-resource change"
        )
