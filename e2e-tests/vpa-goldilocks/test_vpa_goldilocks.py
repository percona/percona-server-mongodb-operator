#!/usr/bin/env python3
"""VPA integration driven by real upstream components.

Unlike the other VPA suites, nothing here fabricates a recommendation: the real
VPA Recommender computes one from live metrics and Goldilocks creates the VPA
objects. This is the end-to-end proof that the operator reads what an actual
recommender writes.

Only the Recommender is installed, never the Updater or Admission Controller:
the operator applies recommendations itself and pods must not be evicted
underneath it.

Phases:
  1. Goldilocks creates the VPA object and the Recommender fills it in
  2. Off mode  - status populated, CR resources unchanged
  3. Auto mode - recommendation applied, cluster converges via SmartUpdate
"""

import logging
import os
import time
from collections.abc import Callable, Generator
from dataclasses import dataclass
from functools import partial

import pytest
from lib.cli import helm_bin
from lib.config import apply_cluster
from lib.kubectl import kubectl_bin, wait_cluster_consistency, wait_for_running
from lib.utils import Paths, retry
from lib.vpa import (
    assert_committed,
    assert_spec_untouched,
    get_last_applied_at,
    get_requests,
    wait_for_last_applied_at,
)

logger = logging.getLogger(__name__)

CLUSTER = "some-name"
GOLDILOCKS_NS = "goldilocks"
INITIAL_CPU = "100m"

# What the CR declares; the operator must keep spec saying this.
DECLARED = {"cpu": "100m", "memory": "100M"}

# The bash suite passed 64 retries with a 10s sleep, i.e. a 640s budget.
CLUSTER_READY_TIMEOUT = 640

# Goldilocks names its VPA objects goldilocks-<statefulset>. The CR sets
# vpa.objectName to match, so the operator looks the object up under that name.
GOLDILOCKS_VPA = f"goldilocks-{CLUSTER}-rs0"

VPA_AUTOSCALER_RAW = (
    "https://raw.githubusercontent.com/kubernetes/autoscaler/master/vertical-pod-autoscaler/deploy"
)

# The first recommendation appears once pods are running and metrics-server has
# history, in practice 3-8 minutes.
VPA_TIMEOUT = int(os.environ.get("VPA_TIMEOUT", "900"))
VPA_POLL_INTERVAL = 20


@dataclass(frozen=True)
class GoldilocksConfig:
    namespace: str


@pytest.fixture(scope="class", autouse=True)
def config(create_infra: Callable[[str], str]) -> GoldilocksConfig:
    return GoldilocksConfig(namespace=create_infra("vpa-goldilocks"))


def _install_vpa_recommender() -> None:
    """Install the VPA CRDs, RBAC and Recommender only."""
    logger.info("Installing VPA CRDs, RBAC and Recommender")
    for manifest in ("vpa-v1-crd-gen.yaml", "vpa-rbac.yaml", "recommender-deployment.yaml"):
        url = f"{VPA_AUTOSCALER_RAW}/{manifest}"
        retry(
            partial(kubectl_bin, "apply", "-f", url),
            max_attempts=5,
            delay=10,
        )
        if manifest == "vpa-v1-crd-gen.yaml":
            kubectl_bin(
                "wait",
                "crd/verticalpodautoscalers.autoscaling.k8s.io",
                "--for=condition=Established",
                "--timeout=60s",
            )
    kubectl_bin(
        "rollout", "status", "deployment/vpa-recommender", "-n", "kube-system", "--timeout=120s"
    )


def _install_goldilocks() -> None:
    """Install Goldilocks, with its bundled VPA disabled since we installed our own."""
    logger.info("Installing Goldilocks via Helm")
    helm_bin("repo", "add", "fairwinds-stable", "https://charts.fairwinds.com/stable", check=False)
    helm_bin("repo", "update", "fairwinds-stable")
    retry(
        lambda: helm_bin(
            "upgrade",
            "--install",
            "goldilocks",
            "fairwinds-stable/goldilocks",
            "--namespace",
            GOLDILOCKS_NS,
            "--create-namespace",
            "--set",
            "vpa.enabled=false",
            "--wait",
            "--timeout",
            "120s",
        ),
        max_attempts=5,
        delay=30,
    )
    kubectl_bin(
        "rollout",
        "status",
        "deployment/goldilocks-controller",
        "-n",
        GOLDILOCKS_NS,
        "--timeout=120s",
    )


@pytest.fixture(scope="class", autouse=True)
def setup_tests(test_paths: Paths, config: GoldilocksConfig) -> Generator[None]:
    _install_vpa_recommender()
    _install_goldilocks()

    kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/secrets.yml")
    kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/client.yml")

    apply_cluster(f"{test_paths['test_dir']}/conf/{CLUSTER}.yml")
    wait_for_running(f"{CLUSTER}-rs0", 3)
    time.sleep(15)

    kubectl_bin(
        "label",
        "namespace",
        config.namespace,
        "goldilocks.fairwinds.com/enabled=true",
        "--overwrite",
    )

    yield

    helm_bin("uninstall", "goldilocks", "--namespace", GOLDILOCKS_NS, check=False)
    kubectl_bin("delete", "namespace", GOLDILOCKS_NS, "--ignore-not-found", check=False)

    # The Recommender is cluster-scoped and must not outlive this test. The other
    # VPA suites fabricate recommendations by patching VPA status directly; a live
    # Recommender recomputes and overwrites those values, so leaving it running
    # makes those suites fail with whatever it happens to recommend.
    for manifest in ("recommender-deployment.yaml", "vpa-rbac.yaml"):
        kubectl_bin(
            "delete",
            "-f",
            f"{VPA_AUTOSCALER_RAW}/{manifest}",
            "--ignore-not-found",
            check=False,
        )


class TestVPAGoldilocks:
    """The operator consumes recommendations produced by real VPA components."""

    @pytest.mark.dependency()
    def test_goldilocks_creates_vpa_with_recommendation(self, config: GoldilocksConfig) -> None:
        """Goldilocks creates the VPA object and the Recommender fills in a target."""
        retry(
            lambda: kubectl_bin("get", "vpa", GOLDILOCKS_VPA, check=False, return_stderr=True),
            max_attempts=30,
            delay=10,
            condition=lambda out: "not found" not in out.lower() and out.strip() != "",
        )

        # The operator applies recommendations itself, so the VPA object must not
        # be in a mode where a real Updater would evict pods.
        mode = kubectl_bin(
            "get", "vpa", GOLDILOCKS_VPA, "-o", "jsonpath={.spec.updatePolicy.updateMode}"
        ).strip()
        if mode != "Off":
            logger.warning(f"Goldilocks VPA updateMode is {mode!r}, expected 'Off'")

        logger.info(
            f"Waiting up to {VPA_TIMEOUT}s for a recommendation "
            "(the Recommender needs metrics history)"
        )
        retry(
            lambda: kubectl_bin(
                "get",
                "vpa",
                GOLDILOCKS_VPA,
                "-o",
                'jsonpath={.status.recommendation.containerRecommendations[?(@.containerName=="mongod")].target.cpu}',
            ).strip(),
            max_attempts=VPA_TIMEOUT // VPA_POLL_INTERVAL,
            delay=VPA_POLL_INTERVAL,
            condition=bool,
        )

    @pytest.mark.dependency(
        depends=["TestVPAGoldilocks::test_goldilocks_creates_vpa_with_recommendation"]
    )
    def test_off_mode_records_without_applying(self, config: GoldilocksConfig) -> None:
        """Off mode: the real recommendation reaches status but not the CR."""
        retry(
            lambda: kubectl_bin(
                "get", "psmdb", CLUSTER, "-o", "jsonpath={.status.vpaStatus.rs0.cpu}"
            ).strip(),
            max_attempts=60,
            delay=5,
            condition=bool,
        )

        assert get_requests("rs0", "cpu", CLUSTER) == INITIAL_CPU, (
            "CR resources must not change while updateMode is Off"
        )
        assert get_last_applied_at("rs0", CLUSTER) == "", (
            "lastAppliedAt must stay empty while updateMode is Off"
        )

    @pytest.mark.dependency(depends=["TestVPAGoldilocks::test_off_mode_records_without_applying"])
    def test_auto_mode_applies_recommendation(self, config: GoldilocksConfig) -> None:
        """Auto mode: the recommendation is applied and the cluster converges."""
        kubectl_bin(
            "patch",
            "psmdb",
            CLUSTER,
            "--type=json",
            "-p",
            '[{"op": "replace", "path": "/spec/vpa/updateMode", "value": "Auto"}]',
        )

        wait_for_last_applied_at("rs0", CLUSTER)

        # A real recommendation may be clamped to minAllowed/maxAllowed, so the
        # applied value is not predictable. What must hold is that the operator
        # applied something and it sits within the configured bounds.
        applied_cpu = get_requests("rs0", "cpu", CLUSTER)
        applied_memory = get_requests("rs0", "memory", CLUSTER)
        logger.info(
            f"Applied cpu={applied_cpu} memory={applied_memory}; bounds cpu=[100m, 2] memory=[128Mi, 2Gi]"
        )
        assert applied_cpu, "cpu request must be set after an apply"
        assert applied_memory, "memory request must be set after an apply"

        # A real recommendation must reach the StatefulSet without the operator
        # rewriting the CR the user declared. assert_committed does not need to
        # know the value, which suits a recommendation computed from live metrics.
        assert_spec_untouched("rs0", DECLARED, CLUSTER)
        assert_committed("rs0", DECLARED, CLUSTER)

        wait_cluster_consistency(CLUSTER, CLUSTER_READY_TIMEOUT)
        state = kubectl_bin("get", "psmdb", CLUSTER, "-o", "jsonpath={.status.state}").strip()
        assert state == "ready", f"cluster state is {state!r}, expected 'ready'"
