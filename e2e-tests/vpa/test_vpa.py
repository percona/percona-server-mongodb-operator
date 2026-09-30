#!/usr/bin/env python3
"""VPA recommendation reading and application.

Validates the operator's VPA integration without a real VPA controller: the
minimal VPA CRD is installed, VPA objects are created by hand, and their status
is patched to stand in for what VPA Recommender or Goldilocks would write.

Scenarios:
  1. Off mode   - recommendation recorded in status, CR resources untouched
  2. Auto mode  - recommendation applied to the CR, SmartUpdate converges
  3. Bounds     - minAllowed/maxAllowed clamp applied before patching
  4. Window     - a second apply is blocked until stabilizationWindow passes
  5. Two clusters in one namespace   - no cross-contamination
  6. Two clusters in two namespaces  - recommendations do not cross namespaces
"""

import logging
import time
from collections.abc import Callable
from dataclasses import dataclass

import pytest
from lib.config import apply_cluster, render_cluster_config
from lib.kubectl import kubectl_bin, wait_for_running
from lib.utils import Paths, retry
from lib.vpa import (
    assert_committed,
    assert_requests,
    assert_spec_untouched,
    create_vpa_object,
    get_last_applied_at,
    install_vpa_crd,
    set_vpa_recommendation,
    wait_for_last_applied_at,
    wait_for_new_apply,
    wait_for_requests,
    wait_for_vpa_status,
)

logger = logging.getLogger(__name__)

CLUSTER = "some-name"
CLUSTER2 = "another-name"

# Matches vpa/conf/some-name.yml
INITIAL_CPU = "100m"
INITIAL_MEMORY = "100M"

# What the CR declares, and must keep saying no matter what VPA applies.
DECLARED = {"cpu": INITIAL_CPU, "memory": INITIAL_MEMORY}
MAX_ALLOWED_CPU = "2"
MAX_ALLOWED_MEMORY = "2Gi"
STABILIZATION_WINDOW = 30


@dataclass(frozen=True)
class VPAConfig:
    namespace: str


@pytest.fixture(scope="class", autouse=True)
def config(create_infra: Callable[[str], str]) -> VPAConfig:
    """Cluster-wide operator, so scenario 6 can watch a second namespace."""
    return VPAConfig(namespace=create_infra("vpa"))


@pytest.fixture(scope="class", autouse=True)
def setup_tests(test_paths: Paths, config: VPAConfig) -> None:
    install_vpa_crd(f"{test_paths['test_dir']}/conf/vpa-crd.yml")
    kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/secrets.yml")
    kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/client.yml")

    apply_cluster(f"{test_paths['test_dir']}/conf/{CLUSTER}.yml")
    wait_for_running(f"{CLUSTER}-rs0", 3)

    # The operator must not see a recommendation before the cluster is up.
    create_vpa_object(f"{CLUSTER}-rs0-vpa", f"{CLUSTER}-rs0")


def _set_update_mode(cluster: str, mode: str, namespace: str | None = None) -> None:
    args = ["patch", "psmdb", cluster, "--type=json"]
    if namespace:
        args = ["patch", "-n", namespace, "psmdb", cluster, "--type=json"]
    kubectl_bin(
        *args, "-p", f'[{{"op": "replace", "path": "/spec/vpa/updateMode", "value": "{mode}"}}]'
    )


def _assert_cluster_ready(cluster: str, namespace: str | None = None) -> None:
    args = ["get", "psmdb", cluster, "-o", "jsonpath={.status.state}"]
    if namespace:
        args = ["get", "-n", namespace, "psmdb", cluster, "-o", "jsonpath={.status.state}"]
    retry(
        lambda: kubectl_bin(*args).strip(),
        max_attempts=60,
        delay=5,
        condition=lambda s: s == "ready",
    )


class TestVPA:
    """VPA recommendation reading and application."""

    @pytest.mark.dependency()
    def test_off_mode_records_without_applying(self, config: VPAConfig) -> None:
        """Off mode: the recommendation reaches status but never the CR."""
        vpa_name = f"{CLUSTER}-rs0-vpa"
        set_vpa_recommendation(vpa_name, "350m", "350Mi")

        wait_for_vpa_status("rs0.cpu", "350m", CLUSTER, max_attempts=60)
        wait_for_vpa_status("rs0.memory", "350Mi", CLUSTER, max_attempts=60)

        assert_requests("rs0", "cpu", INITIAL_CPU, CLUSTER)
        assert_requests("rs0", "memory", INITIAL_MEMORY, CLUSTER)

        assert get_last_applied_at("rs0", CLUSTER) == "", (
            "lastAppliedAt must stay empty while updateMode is Off"
        )

    @pytest.mark.dependency(depends=["TestVPA::test_off_mode_records_without_applying"])
    def test_auto_mode_applies_recommendation(self, config: VPAConfig) -> None:
        """Auto mode: the recommendation is patched into the CR and the cluster converges."""
        _set_update_mode(CLUSTER, "Auto")

        wait_for_last_applied_at("rs0", CLUSTER)
        wait_for_requests("rs0", "cpu", "350m", CLUSTER)
        wait_for_requests("rs0", "memory", "350Mi", CLUSTER)

        # The whole point of committing to status: spec stays the user's
        # declaration, so a GitOps controller owning this CR sees no drift.
        assert_spec_untouched("rs0", DECLARED, CLUSTER)
        assert_committed("rs0", DECLARED, CLUSTER)

        _assert_cluster_ready(CLUSTER)

    @pytest.mark.dependency(depends=["TestVPA::test_auto_mode_applies_recommendation"])
    def test_recommendation_clamped_to_max_allowed(self, config: VPAConfig) -> None:
        """A recommendation above maxAllowed is clamped before it reaches the CR."""
        previous_apply = get_last_applied_at("rs0", CLUSTER)

        time.sleep(STABILIZATION_WINDOW + 5)
        set_vpa_recommendation(f"{CLUSTER}-rs0-vpa", "8000m", "8Gi")

        wait_for_new_apply("rs0", previous_apply, CLUSTER)
        wait_for_requests("rs0", "cpu", MAX_ALLOWED_CPU, CLUSTER)
        wait_for_requests("rs0", "memory", MAX_ALLOWED_MEMORY, CLUSTER)

        _assert_cluster_ready(CLUSTER)

    @pytest.mark.dependency(depends=["TestVPA::test_recommendation_clamped_to_max_allowed"])
    def test_stabilization_window_delays_next_apply(self, config: VPAConfig) -> None:
        """A recommendation arriving inside the window waits; it is applied once the window passes."""
        previous_apply = get_last_applied_at("rs0", CLUSTER)
        set_vpa_recommendation(f"{CLUSTER}-rs0-vpa", "500m", "500Mi")

        # Still inside the window: the clamped values from the previous scenario must hold.
        time.sleep(5)
        assert_requests("rs0", "cpu", MAX_ALLOWED_CPU, CLUSTER)
        assert_requests("rs0", "memory", MAX_ALLOWED_MEMORY, CLUSTER)

        time.sleep(STABILIZATION_WINDOW)
        wait_for_new_apply("rs0", previous_apply, CLUSTER)
        wait_for_requests("rs0", "cpu", "500m", CLUSTER)
        wait_for_requests("rs0", "memory", "500Mi", CLUSTER)

        _assert_cluster_ready(CLUSTER)

    @pytest.mark.dependency(depends=["TestVPA::test_stabilization_window_delays_next_apply"])
    def test_two_clusters_one_namespace_do_not_interfere(
        self, config: VPAConfig, test_paths: Paths
    ) -> None:
        """Two clusters in one namespace each track only their own VPA object."""
        apply_cluster(f"{test_paths['test_dir']}/conf/{CLUSTER2}.yml")
        wait_for_running(f"{CLUSTER2}-rs0", 3)
        create_vpa_object(f"{CLUSTER2}-rs0-vpa", f"{CLUSTER2}-rs0")

        previous_apply = get_last_applied_at("rs0", CLUSTER)
        time.sleep(STABILIZATION_WINDOW + 5)

        set_vpa_recommendation(f"{CLUSTER}-rs0-vpa", "450m", "450Mi")
        set_vpa_recommendation(f"{CLUSTER2}-rs0-vpa", "600m", "600Mi")

        wait_for_new_apply("rs0", previous_apply, CLUSTER)
        wait_for_requests("rs0", "cpu", "450m", CLUSTER)
        wait_for_requests("rs0", "memory", "450Mi", CLUSTER)

        wait_for_last_applied_at("rs0", CLUSTER2)
        wait_for_requests("rs0", "cpu", "600m", CLUSTER2)
        wait_for_requests("rs0", "memory", "600Mi", CLUSTER2)

        # Neither cluster picked up the other's recommendation.
        assert_requests("rs0", "cpu", "450m", CLUSTER)
        assert_requests("rs0", "memory", "450Mi", CLUSTER)
        assert_requests("rs0", "cpu", "600m", CLUSTER2)
        assert_requests("rs0", "memory", "600Mi", CLUSTER2)

    @pytest.mark.dependency(depends=["TestVPA::test_two_clusters_one_namespace_do_not_interfere"])
    def test_two_namespaces_do_not_interfere(self, config: VPAConfig, test_paths: Paths) -> None:
        """A same-named cluster and VPA object in another namespace stay isolated."""
        ns2 = f"{config.namespace}-2"
        kubectl_bin("create", "namespace", ns2)
        try:
            kubectl_bin("apply", "-n", ns2, "-f", f"{test_paths['conf_dir']}/secrets.yml")

            cr = render_cluster_config(f"{test_paths['test_dir']}/conf/{CLUSTER}.yml")
            kubectl_bin("apply", "-n", ns2, "-f", "-", input_data=cr)
            wait_for_running(f"{CLUSTER}-rs0", 3)

            _set_update_mode(CLUSTER, "Auto", namespace=ns2)

            # Same VPA object name as in the first namespace.
            create_vpa_object(f"{CLUSTER}-rs0-vpa", f"{CLUSTER}-rs0", namespace=ns2)
            set_vpa_recommendation(f"{CLUSTER}-rs0-vpa", "700m", "700Mi", namespace=ns2)

            time.sleep(STABILIZATION_WINDOW + 5)
            wait_for_last_applied_at("rs0", CLUSTER, namespace=ns2)
            wait_for_requests("rs0", "cpu", "700m", CLUSTER, namespace=ns2)
            wait_for_requests("rs0", "memory", "700Mi", CLUSTER, namespace=ns2)

            # The cluster in the original namespace kept its own values.
            assert_requests("rs0", "cpu", "450m", CLUSTER, namespace=config.namespace)
            assert_requests("rs0", "memory", "450Mi", CLUSTER, namespace=config.namespace)
        finally:
            kubectl_bin(
                "delete",
                "namespace",
                ns2,
                "--grace-period=0",
                "--force=true",
                "--ignore-not-found",
                check=False,
            )
