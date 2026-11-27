#!/usr/bin/env python3

import json
import logging
import time
from collections.abc import Callable
from dataclasses import dataclass, field

import pytest
from lib.config import apply_cluster
from lib.kubectl import kubectl_bin, wait_for_running
from lib.utils import Paths

logger = logging.getLogger(__name__)

CLUSTER = "some-name"

# (replset name, expose path in CR spec, service name, headless service name)
SERVICES = (
    ("rs0", "/spec/replsets/0/expose", f"{CLUSTER}-rs0-0", f"{CLUSTER}-rs0"),
    ("cfg", "/spec/sharding/configsvrReplSet/expose", f"{CLUSTER}-cfg-0", f"{CLUSTER}-cfg"),
    # mongos has no headless counterpart, nothing to check there
    ("mongos", "/spec/sharding/mongos/expose", f"{CLUSTER}-mongos-0", None),
)


@dataclass(frozen=True)
class IgnoreLabelsAnnotationsConfig:
    namespace: str
    cluster: str = CLUSTER
    services: tuple[tuple[str, str, str, str | None], ...] = field(
        default_factory=lambda: SERVICES
    )


@pytest.fixture(scope="class", autouse=True)
def config(create_infra: Callable[[str], str]) -> IgnoreLabelsAnnotationsConfig:
    """Configuration for tests"""
    return IgnoreLabelsAnnotationsConfig(namespace=create_infra("ignore-labels-annotations"))


def _patch_psmdb(cluster: str, patch: list[dict[str, object]]) -> None:
    kubectl_bin("patch", "psmdb", cluster, "--type=json", "-p", json.dumps(patch))


def _patch_service(svc_name: str, patch: list[dict[str, object]]) -> None:
    kubectl_bin("patch", f"service/{svc_name}", "--type=json", "-p", json.dumps(patch))


def _headless_svc_annotation(headless_svc: str, key: str) -> str:
    out = kubectl_bin(
        "get",
        f"service/{headless_svc}",
        "-o",
        f"jsonpath={{.metadata.annotations.{key}}}",
        check=True,
    )
    return out.strip()


def _assert_headless_svc_has_no_expose_annotations(
    headless_svc: str | None, keys: tuple[str, ...]
) -> None:
    """Headless service must never get expose.annotations keys applied."""
    if not headless_svc:
        return
    for key in keys:
        actual = _headless_svc_annotation(headless_svc, key)
        assert not actual, (
            f"headless service {headless_svc} must never get expose.annotations "
            f'key "{key}" applied, but got "{actual}"'
        )


# Label and annotation keys the test sets: "<prefix>Label" and "<prefix>Annotation".
META_PREFIXES = ("ignored", "notIgnored", "cr", "otherCr", "secondCr")


def _service_test_meta(svc_name: str) -> tuple[dict[str, str], dict[str, str]]:
    meta = json.loads(kubectl_bin("get", f"service/{svc_name}", "-o", "json"))["metadata"]
    labels = meta.get("labels") or {}
    annotations = meta.get("annotations") or {}
    return (
        {f"{p}Label": labels[f"{p}Label"] for p in META_PREFIXES if f"{p}Label" in labels},
        {
            f"{p}Annotation": annotations[f"{p}Annotation"]
            for p in META_PREFIXES
            if f"{p}Annotation" in annotations
        },
    )


def _wait_service_meta(svc_name: str, prefixes: tuple[str, ...], retries: int = 12) -> None:
    """Wait until the service has exactly the test labels and annotations for `prefixes`.

    Operator restores service metadata on its next reconcile, which may be
    delayed (e.g. by an update conflict), so retry instead of relying on sleep."""
    expected = (
        {f"{p}Label": "true" for p in prefixes},
        {f"{p}Annotation": "true" for p in prefixes},
    )
    actual = _service_test_meta(svc_name)
    for _ in range(retries):
        if actual == expected:
            return
        time.sleep(5)
        actual = _service_test_meta(svc_name)
    assert actual == expected, (
        f"service/{svc_name} labels, annotations: expected {expected}, got {actual}"
    )


def _check_service(
    config: IgnoreLabelsAnnotationsConfig,
    replset_name: str,
    expose_path: str,
    svc_name: str,
    headless_svc_name: str | None,
) -> None:
    cluster = config.cluster

    logger.info(f"add labels and annotation manually to service {svc_name}")
    # `notIgnoredLabel` and `notIgnoredAnnotation` should be deleted
    _patch_service(
        svc_name,
        [
            {"op": "add", "path": "/metadata/labels/notIgnoredLabel", "value": "true"},
            {"op": "add", "path": "/metadata/labels/ignoredLabel", "value": "true"},
            {"op": "add", "path": "/metadata/annotations/notIgnoredAnnotation", "value": "true"},
            {"op": "add", "path": "/metadata/annotations/ignoredAnnotation", "value": "true"},
        ],
    )

    time.sleep(7)  # waiting for reconcile
    logger.info(f"check if annotations/labels are not deleted from service {svc_name}")
    _wait_service_meta(svc_name, ("ignored", "notIgnored"))

    logger.info("adding ignoreAnnotations and ignoreLabels")
    _patch_psmdb(
        cluster,
        [
            {"op": "add", "path": "/spec/ignoreAnnotations", "value": ["ignoredAnnotation"]},
            {"op": "add", "path": "/spec/ignoreLabels", "value": ["ignoredLabel"]},
        ],
    )
    time.sleep(7)
    logger.info(f"check if annotations/labels are not deleted from service {svc_name}")
    _wait_service_meta(svc_name, ("ignored", "notIgnored"))

    logger.info(f"adding labels and annotations to {expose_path}")
    _patch_psmdb(
        cluster,
        [
            {"op": "replace", "path": f"{expose_path}/labels", "value": {"crLabel": "true"}},
            {
                "op": "replace",
                "path": f"{expose_path}/annotations",
                "value": {"crAnnotation": "true"},
            },
        ],
    )
    time.sleep(5)
    logger.info(f"check if not ignored labels and annotations are deleted from service {svc_name}")
    _wait_service_meta(svc_name, ("ignored", "cr"))

    logger.info(
        f"check that expose.annotations are never applied to the headless service {headless_svc_name}"
    )
    _assert_headless_svc_has_no_expose_annotations(headless_svc_name, ("crAnnotation",))

    logger.info(f"removing labels and annotations from {expose_path}")
    _patch_psmdb(
        cluster,
        [
            {"op": "remove", "path": f"{expose_path}/labels"},
            {"op": "remove", "path": f"{expose_path}/annotations"},
        ],
    )
    time.sleep(5)
    logger.info(f"check if labels and annotations are not deleted from service {svc_name}")
    _wait_service_meta(svc_name, ("ignored", "cr"))

    logger.info(f"adding other labels and annotations to {expose_path}")
    _patch_psmdb(
        cluster,
        [
            {
                "op": "replace",
                "path": f"{expose_path}/labels",
                "value": {"otherCrLabel": "true", "secondCrLabel": "true"},
            },
            {
                "op": "replace",
                "path": f"{expose_path}/annotations",
                "value": {"otherCrAnnotation": "true", "secondCrAnnotation": "true"},
            },
        ],
    )
    time.sleep(5)
    logger.info(f"check if not previous service labels and annotations is deleted from {svc_name}")
    _wait_service_meta(svc_name, ("ignored", "otherCr", "secondCr"))

    logger.info(
        f"check that expose.annotations are still never applied to the headless service {headless_svc_name}"
    )
    _assert_headless_svc_has_no_expose_annotations(
        headless_svc_name, ("otherCrAnnotation", "secondCrAnnotation")
    )

    logger.info(f"adding removing one labels from {expose_path}")
    _patch_psmdb(
        cluster,
        [
            {"op": "replace", "path": f"{expose_path}/labels", "value": {"otherCrLabel": "true"}},
            {
                "op": "replace",
                "path": f"{expose_path}/annotations",
                "value": {"otherCrAnnotation": "true"},
            },
        ],
    )
    time.sleep(5)
    logger.info(f"check if removed service label is deleted from {svc_name}")
    _wait_service_meta(svc_name, ("ignored", "otherCr"))

    logger.info("removing ignoreLabels and ignoreAnnotations")
    # When `labels` and `annotations` are not set, old metadata should stay
    _patch_psmdb(
        cluster,
        [
            {"op": "remove", "path": "/spec/ignoreAnnotations"},
            {"op": "remove", "path": "/spec/ignoreLabels"},
        ],
    )
    time.sleep(5)  # waiting for reconcile
    logger.info(f"check if ignored labels and annotations are deleted from {svc_name}")
    _wait_service_meta(svc_name, ("otherCr",))


class TestIgnoreLabelsAnnotations:
    """Test that ignoreLabels/ignoreAnnotations keep manually-added service metadata,
    and that expose.annotations are never applied to headless services."""

    @pytest.mark.dependency()
    def test_create_cluster(
        self, config: IgnoreLabelsAnnotationsConfig, test_paths: Paths
    ) -> None:
        """Create cluster and check all 3 pods started"""
        apply_cluster(f"{test_paths['test_dir']}/conf/{config.cluster}.yml")
        wait_for_running(f"{config.cluster}-rs0", 3)

    @pytest.mark.dependency(depends=["TestIgnoreLabelsAnnotations::test_create_cluster"])
    @pytest.mark.parametrize("replset_name,expose_path,svc_name,headless_svc_name", SERVICES)
    def test_service_labels_annotations(
        self,
        config: IgnoreLabelsAnnotationsConfig,
        replset_name: str,
        expose_path: str,
        svc_name: str,
        headless_svc_name: str | None,
    ) -> None:
        """Check ignoreLabels/ignoreAnnotations and expose.labels/annotations behavior"""
        _check_service(config, replset_name, expose_path, svc_name, headless_svc_name)
