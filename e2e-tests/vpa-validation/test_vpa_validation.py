#!/usr/bin/env python3
"""CRD-level validation of VPA resource bounds.

spec.vpa.minAllowed must not exceed spec.vpa.maxAllowed for cpu or memory. The
rule is a CEL x-kubernetes-validations expression on the CRD, enforced by the API
server, so every case here uses a server-side dry run: no cluster is created and
nothing is persisted, which makes the whole suite run in seconds.

Two cases assert that a bad configuration is ACCEPTED. They are not endorsements
of that configuration; they pin down the deliberate boundary of this validation
so a future change to either side fails loudly here:

  * Per-component bounds (test_per_replset_inverted_bounds_are_not_caught) carry
    no rule. A matching rule on the per-component spec does not fit this CRD's
    schema-wide CEL cost budget: replsets is an unbounded list, so the API server
    multiplies the per-item rule cost by an unbounded length and rejects the
    whole CRD at install time.

  * Cross-layer inversion (test_cross_layer_inversion_is_not_caught) is not
    expressible in CEL at any cost. effectiveBounds() overrides minAllowed and
    maxAllowed independently, so a global ceiling plus a per-replset floor above
    it yields inverted effective bounds while each document on its own is valid.
    Only the merged result is wrong, and CEL cannot see across that merge.
"""

import logging
from collections.abc import Callable
from dataclasses import dataclass
from typing import Any

import pytest
import yaml
from lib.kubectl import kubectl_bin
from lib.utils import get_cr_version

logger = logging.getLogger(__name__)

EXPECTED_ERROR = "minAllowed must not exceed maxAllowed"


@dataclass(frozen=True)
class ValidationConfig:
    namespace: str


@pytest.fixture(scope="class", autouse=True)
def config(create_infra: Callable[[str], str]) -> ValidationConfig:
    """create_infra deploys the operator, which installs the CRD under test."""
    return ValidationConfig(namespace=create_infra("vpa-validation"))


def _build_cr(
    name: str,
    vpa: dict[str, Any] | None = None,
    replset_vpa: dict[str, Any] | None = None,
) -> str:
    """A minimal but schema-valid PerconaServerMongoDB, optionally carrying VPA config."""
    replset: dict[str, Any] = {
        "name": "rs0",
        "size": 3,
        "volumeSpec": {"persistentVolumeClaim": {"resources": {"requests": {"storage": "3Gi"}}}},
    }
    if replset_vpa is not None:
        replset["vpa"] = replset_vpa

    spec: dict[str, Any] = {
        "crVersion": get_cr_version(),
        "image": "perconalab/percona-server-mongodb-operator:main-mongod8.0",
        "replsets": [replset],
    }
    if vpa is not None:
        spec["vpa"] = vpa

    return yaml.dump(
        {
            "apiVersion": "psmdb.percona.com/v1",
            "kind": "PerconaServerMongoDB",
            "metadata": {"name": name},
            "spec": spec,
        }
    )


def _dry_run_apply(manifest: str) -> tuple[bool, str]:
    """Server-side dry run. Returns (accepted, output)."""
    out = kubectl_bin(
        "apply",
        "--dry-run=server",
        "-f",
        "-",
        input_data=manifest,
        check=False,
        return_stderr=True,
    )
    return "created (server dry run)" in out or "configured (server dry run)" in out, out


def _assert_accepted(manifest: str) -> None:
    accepted, out = _dry_run_apply(manifest)
    assert accepted, f"expected the API server to accept this CR, but it was rejected:\n{out}"


def _assert_rejected(manifest: str, expected: str = EXPECTED_ERROR) -> None:
    accepted, out = _dry_run_apply(manifest)
    assert not accepted, f"expected the API server to reject this CR, but it was accepted:\n{out}"
    assert expected in out, (
        f"the CR was rejected, but not for the expected reason.\n"
        f"  expected the error to contain: {expected}\n  actual: {out}"
    )


class TestVPAValidation:
    """spec.vpa.minAllowed must not exceed spec.vpa.maxAllowed."""

    @pytest.mark.parametrize(
        ("case", "vpa"),
        [
            ("no vpa section at all", None),
            ("vpa enabled with no bounds", {"enabled": True}),
            (
                "minAllowed only, no maxAllowed",
                {"enabled": True, "minAllowed": {"cpu": "4", "memory": "4Gi"}},
            ),
            (
                "maxAllowed only, no minAllowed",
                {"enabled": True, "maxAllowed": {"cpu": "2", "memory": "2Gi"}},
            ),
            (
                "cpu and memory bounds both sane",
                {
                    "enabled": True,
                    "minAllowed": {"cpu": "100m", "memory": "256Mi"},
                    "maxAllowed": {"cpu": "8", "memory": "16Gi"},
                },
            ),
            (
                "cpu and memory bounds exactly equal",
                {
                    "enabled": True,
                    "minAllowed": {"cpu": "2", "memory": "2Gi"},
                    "maxAllowed": {"cpu": "2", "memory": "2Gi"},
                },
            ),
        ],
    )
    def test_valid_bounds_accepted(
        self, config: ValidationConfig, case: str, vpa: dict[str, Any] | None
    ) -> None:
        """Bounds that are absent, one-sided, ordered or equal are all valid."""
        logger.info(f"case: {case}")
        _assert_accepted(_build_cr("vpa-valid", vpa=vpa))

    @pytest.mark.parametrize(
        ("case", "vpa"),
        [
            (
                "cpu minAllowed above cpu maxAllowed",
                {"enabled": True, "minAllowed": {"cpu": "4"}, "maxAllowed": {"cpu": "2"}},
            ),
            (
                "memory minAllowed above memory maxAllowed",
                {
                    "enabled": True,
                    "minAllowed": {"memory": "16Gi"},
                    "maxAllowed": {"memory": "2Gi"},
                },
            ),
            (
                "cpu sane but memory inverted",
                {
                    "enabled": True,
                    "minAllowed": {"cpu": "100m", "memory": "16Gi"},
                    "maxAllowed": {"cpu": "8", "memory": "2Gi"},
                },
            ),
        ],
    )
    def test_inverted_bounds_rejected(
        self, config: ValidationConfig, case: str, vpa: dict[str, Any]
    ) -> None:
        """A floor above the ceiling is rejected, for cpu and for memory alike."""
        logger.info(f"case: {case}")
        _assert_rejected(_build_cr("vpa-invalid", vpa=vpa))

    def test_per_replset_inverted_bounds_are_not_caught(self, config: ValidationConfig) -> None:
        """Documented gap: per-component bounds carry no rule (CEL cost budget)."""
        _assert_accepted(
            _build_cr(
                "vpa-rs-inverted",
                vpa={"enabled": True},
                replset_vpa={"minAllowed": {"cpu": "4"}, "maxAllowed": {"cpu": "2"}},
            )
        )

    def test_cross_layer_inversion_is_not_caught(self, config: ValidationConfig) -> None:
        """Documented gap: a global ceiling with a higher per-replset floor.

        Each document is individually valid; only the merged effective bounds are
        inverted, which CEL cannot observe.
        """
        _assert_accepted(
            _build_cr(
                "vpa-cross-layer",
                vpa={
                    "enabled": True,
                    "minAllowed": {"cpu": "100m"},
                    "maxAllowed": {"cpu": "2"},
                },
                replset_vpa={"minAllowed": {"cpu": "4"}},
            )
        )
