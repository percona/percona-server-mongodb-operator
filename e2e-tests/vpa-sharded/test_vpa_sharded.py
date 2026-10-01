#!/usr/bin/env python3
"""VPA recommendations across every component of a sharded cluster.

Each component reads its own VPA object and applies only its own recommendation:
rs0, the config server replset, mongos, and the non-voting and hidden members of
rs0 (which live in their own StatefulSets with their own containers).

Scenarios:
  1. Off mode   - rs0, cfg and mongos all recorded, no CR resources changed
  2. Auto mode  - each of the three applies its own recommendation
  3. Isolation  - distinct recommendations, no cross-contamination
  4. Non-voting - recorded and applied independently of rs0
  5. Hidden     - recorded and applied independently of non-voting
"""

import logging
import time
from collections.abc import Callable
from dataclasses import dataclass

import pytest
from lib.config import apply_cluster
from lib.kubectl import kubectl_bin, wait_cluster_consistency, wait_for_running, wait_pod
from lib.utils import Paths
from lib.vpa import (
    assert_committed,
    assert_requests,
    assert_spec_untouched,
    create_vpa_object,
    get_last_applied_at,
    install_vpa_crd,
    set_vpa_recommendation,
    wait_for_last_applied_at,
    wait_for_requests,
    wait_for_vpa_status,
)

logger = logging.getLogger(__name__)

CLUSTER = "some-name"
INITIAL_CPU = "100m"

# Every component in this fixture declares the same requests, and must keep
# declaring them however the recommendations move.
DECLARED = {"cpu": "100m", "memory": "100M"}
STABILIZATION_WINDOW = 30

# The bash suite passed 64 retries with a 10s sleep to wait_cluster_consistency.
# The Python helper takes seconds, so the equivalent budget is 640s. A sharded
# SmartUpdate restarts rs0, cfg, mongos, non-voting and hidden in sequence.
CLUSTER_READY_TIMEOUT = 640

# VPA object names follow the <cluster>-<component>-vpa convention.
VPA_RS0 = f"{CLUSTER}-rs0-vpa"
VPA_CFG = f"{CLUSTER}-cfg-vpa"
VPA_MONGOS = f"{CLUSTER}-mongos-vpa"
VPA_NV = f"{CLUSTER}-rs0-nv-vpa"
VPA_HIDDEN = f"{CLUSTER}-rs0-hidden-vpa"


@dataclass(frozen=True)
class VPAShardedConfig:
    namespace: str


@pytest.fixture(scope="class", autouse=True)
def config(create_infra: Callable[[str], str]) -> VPAShardedConfig:
    return VPAShardedConfig(namespace=create_infra("vpa-sharded"))


@pytest.fixture(scope="class", autouse=True)
def setup_tests(test_paths: Paths, config: VPAShardedConfig) -> None:
    install_vpa_crd(f"{test_paths['src_dir']}/e2e-tests/vpa/conf/vpa-crd.yml")
    kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/secrets.yml")
    kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/client.yml")

    apply_cluster(f"{test_paths['test_dir']}/conf/{CLUSTER}.yml")

    # wait_for_running parses <cluster>-<replset>, so it covers top-level replsets
    # only; the non-voting and hidden StatefulSets are waited on directly.
    wait_for_running(f"{CLUSTER}-rs0", 3)
    wait_for_running(f"{CLUSTER}-cfg", 3)
    wait_pod(f"{CLUSTER}-rs0-nv-0")
    wait_pod(f"{CLUSTER}-rs0-hidden-0")
    wait_cluster_consistency(CLUSTER, CLUSTER_READY_TIMEOUT)

    create_vpa_object(VPA_RS0, f"{CLUSTER}-rs0")
    create_vpa_object(VPA_CFG, f"{CLUSTER}-cfg")
    create_vpa_object(VPA_MONGOS, f"{CLUSTER}-mongos")
    create_vpa_object(VPA_NV, f"{CLUSTER}-rs0-nv")
    create_vpa_object(VPA_HIDDEN, f"{CLUSTER}-rs0-hidden")


def _set_update_mode(mode: str) -> None:
    kubectl_bin(
        "patch",
        "psmdb",
        CLUSTER,
        "--type=json",
        "-p",
        f'[{{"op": "replace", "path": "/spec/vpa/updateMode", "value": "{mode}"}}]',
    )


class TestVPASharded:
    """VPA behaviour across rs0, cfg, mongos, non-voting and hidden members."""

    @pytest.mark.dependency()
    def test_off_mode_records_all_components(self, config: VPAShardedConfig) -> None:
        """Off mode records rs0, cfg and mongos in status without touching the CR."""
        set_vpa_recommendation(VPA_RS0, "350m", "350Mi", "mongod")
        set_vpa_recommendation(VPA_CFG, "250m", "250Mi", "mongod")
        set_vpa_recommendation(VPA_MONGOS, "150m", "150Mi", "mongos")

        wait_for_vpa_status("rs0.cpu", "350m", CLUSTER)
        wait_for_vpa_status("cfg.cpu", "250m", CLUSTER)
        wait_for_vpa_status("mongos.cpu", "150m", CLUSTER)

        for component in ("rs0", "cfg", "mongos"):
            assert_requests(component, "cpu", INITIAL_CPU, CLUSTER)
            assert get_last_applied_at(component, CLUSTER) == "", (
                f"lastAppliedAt must stay empty for {component} while updateMode is Off"
            )

    @pytest.mark.dependency(depends=["TestVPASharded::test_off_mode_records_all_components"])
    def test_auto_mode_applies_per_component(self, config: VPAShardedConfig) -> None:
        """Each of rs0, cfg and mongos applies its own recommendation."""
        _set_update_mode("Auto")

        for component in ("rs0", "cfg", "mongos"):
            wait_for_last_applied_at(component, CLUSTER)

        expected = {
            "rs0": ("350m", "350Mi"),
            "cfg": ("250m", "250Mi"),
            "mongos": ("150m", "150Mi"),
        }
        # lastAppliedAt means the value was committed to status; the StatefulSet
        # converges afterwards. Mongos lags the most, because its StatefulSet is
        # only reconciled once the replsets are up to date.
        for component, (cpu, memory) in expected.items():
            wait_for_requests(component, "cpu", cpu, CLUSTER)
            wait_for_requests(component, "memory", memory, CLUSTER)

        for component, (cpu, memory) in expected.items():
            assert_requests(component, "cpu", cpu, CLUSTER)
            assert_requests(component, "memory", memory, CLUSTER)
            # Each component's effective values come from status, not from a
            # rewritten spec.
            assert_spec_untouched(component, DECLARED, CLUSTER)
            assert_committed(component, DECLARED, CLUSTER)

        wait_cluster_consistency(CLUSTER, CLUSTER_READY_TIMEOUT)

    @pytest.mark.dependency(depends=["TestVPASharded::test_auto_mode_applies_per_component"])
    def test_components_do_not_cross_contaminate(self, config: VPAShardedConfig) -> None:
        """Distinct recommendations land on their own component and nowhere else."""
        time.sleep(STABILIZATION_WINDOW + 5)

        set_vpa_recommendation(VPA_RS0, "500m", "500Mi", "mongod")
        set_vpa_recommendation(VPA_CFG, "400m", "400Mi", "mongod")
        set_vpa_recommendation(VPA_MONGOS, "300m", "300Mi", "mongos")

        # Wait on the CR value rather than on lastAppliedAt: the operator may
        # re-apply the previous value just as the VPA object is updated.
        expected = {
            "rs0": ("500m", "500Mi"),
            "cfg": ("400m", "400Mi"),
            "mongos": ("300m", "300Mi"),
        }
        for component, (cpu, memory) in expected.items():
            wait_for_requests(component, "cpu", cpu, CLUSTER)
            wait_for_requests(component, "memory", memory, CLUSTER)

        for component, (cpu, memory) in expected.items():
            assert_requests(component, "cpu", cpu, CLUSTER)
            assert_requests(component, "memory", memory, CLUSTER)

        wait_cluster_consistency(CLUSTER, CLUSTER_READY_TIMEOUT)

    @pytest.mark.dependency(depends=["TestVPASharded::test_components_do_not_cross_contaminate"])
    def test_non_voting_applies_independently(self, config: VPAShardedConfig) -> None:
        """Non-voting members use their own VPA object and leave rs0 alone."""
        set_vpa_recommendation(VPA_NV, "220m", "220Mi", "mongod-nv")
        wait_for_vpa_status("rs0-nv.cpu", "220m", CLUSTER)

        time.sleep(STABILIZATION_WINDOW + 5)
        wait_for_requests("nonvoting", "cpu", "220m", CLUSTER)
        wait_for_requests("nonvoting", "memory", "220Mi", CLUSTER)

        assert_requests("nonvoting", "cpu", "220m", CLUSTER)
        assert_requests("nonvoting", "memory", "220Mi", CLUSTER)
        assert_spec_untouched("nonvoting", DECLARED, CLUSTER)
        assert_committed("nonvoting", DECLARED, CLUSTER)

        # rs0 keeps the value set in the previous scenario.
        assert_requests("rs0", "cpu", "500m", CLUSTER)

        # Move both at once: each must land on its own component.
        time.sleep(STABILIZATION_WINDOW + 5)
        set_vpa_recommendation(VPA_NV, "280m", "280Mi", "mongod-nv")
        set_vpa_recommendation(VPA_RS0, "600m", "600Mi", "mongod")

        wait_for_requests("nonvoting", "cpu", "280m", CLUSTER)
        wait_for_requests("rs0", "cpu", "600m", CLUSTER)

        assert_requests("nonvoting", "cpu", "280m", CLUSTER)
        assert_requests("rs0", "cpu", "600m", CLUSTER)

    @pytest.mark.dependency(depends=["TestVPASharded::test_non_voting_applies_independently"])
    def test_hidden_applies_independently(self, config: VPAShardedConfig) -> None:
        """Hidden members use their own VPA object and leave non-voting alone."""
        set_vpa_recommendation(VPA_HIDDEN, "190m", "190Mi", "mongod-hidden")
        wait_for_vpa_status("rs0-hidden.cpu", "190m", CLUSTER)

        time.sleep(STABILIZATION_WINDOW + 5)
        wait_for_requests("hidden", "cpu", "190m", CLUSTER)
        wait_for_requests("hidden", "memory", "190Mi", CLUSTER)

        assert_requests("hidden", "cpu", "190m", CLUSTER)
        assert_requests("hidden", "memory", "190Mi", CLUSTER)
        assert_spec_untouched("hidden", DECLARED, CLUSTER)
        assert_committed("hidden", DECLARED, CLUSTER)

        # Non-voting keeps the value from the previous scenario.
        assert_requests("nonvoting", "cpu", "280m", CLUSTER)

        # Move both at once: hidden and non-voting must stay independent.
        time.sleep(STABILIZATION_WINDOW + 5)
        set_vpa_recommendation(VPA_HIDDEN, "240m", "240Mi", "mongod-hidden")
        set_vpa_recommendation(VPA_NV, "330m", "330Mi", "mongod-nv")

        wait_for_requests("hidden", "cpu", "240m", CLUSTER)
        wait_for_requests("nonvoting", "cpu", "330m", CLUSTER)

        assert_requests("hidden", "cpu", "240m", CLUSTER)
        assert_requests("nonvoting", "cpu", "330m", CLUSTER)

        wait_cluster_consistency(CLUSTER, CLUSTER_READY_TIMEOUT)
