import logging
import os
from pathlib import Path
from typing import Any

import yaml

from .allure_support import step
from .kubectl import kubectl_bin
from .utils import env_bool, render_yaml_diff

logger = logging.getLogger(__name__)


def render_cluster_config(config_file: str) -> str:
    """Render a cluster manifest with test image and scheduling overrides."""
    with open(config_file, "r") as f:
        config = yaml.safe_load(f)

    if "spec" in config:
        spec = config["spec"]

        if "image" not in spec or spec["image"] is None:
            spec["image"] = os.environ.get("IMAGE_MONGOD")

        if "pmm" in spec:
            spec["pmm"]["image"] = os.environ.get("IMAGE_PMM_CLIENT")

        if "initImage" in spec:
            spec["initImage"] = os.environ.get("IMAGE")

        if "backup" in spec:
            spec["backup"]["image"] = os.environ.get("IMAGE_BACKUP")

        if "logcollector" in spec and os.environ.get("IMAGE_LOGCOLLECTOR"):
            spec["logcollector"]["image"] = os.environ.get("IMAGE_LOGCOLLECTOR")

        if "search" in spec:
            spec["search"]["image"] = os.environ.get("IMAGE_SEARCH")

        if "upgradeOptions" not in spec:
            spec["upgradeOptions"] = {}
        spec["upgradeOptions"]["apply"] = "Never"

    return yaml.dump(config)


@step("Apply cluster CR {config_file}")
def apply_cluster(config_file: str) -> None:
    logger.info("Creating PSMDB cluster")
    config_yaml = render_cluster_config(config_file)

    if env_bool("SKIP_BACKUPS_TO_AWS_GCP_AZURE"):
        config = yaml.safe_load(config_yaml)
        backup = config.get("spec", {}).get("backup", {})
        if tasks := backup.get("tasks"):
            backup["tasks"] = tasks[:1]
        config_yaml = yaml.dump(config)
    kubectl_bin("apply", "-f", "-", input_data=config_yaml)


def _expected_compare_path(test_dir: str, resource: str, postfix: str = "") -> Path:
    expected_path = Path(test_dir) / "compare" / f"{resource.replace('/', '_')}{postfix}.yml"
    if not os.environ.get("OPENSHIFT"):
        return expected_path

    oc_path = expected_path.with_name(f"{expected_path.stem}-oc{expected_path.suffix}")
    if not oc_path.exists():
        return expected_path

    oc4_path = expected_path.with_name(f"{expected_path.stem}-4-oc{expected_path.suffix}")
    if os.environ.get("OPENSHIFT") == "4" and oc4_path.exists():
        return oc4_path

    return oc_path


def _assert_yaml_equal(
    expected_data: Any, actual_data: Any, expected_label: str, actual_label: str
) -> None:
    """Assert two parsed YAML structures match, showing a unified diff on failure."""
    message = render_yaml_diff(expected_data, actual_data, expected_label, actual_label)
    assert not message, message


def compare_metadata(test_dir: str, resource: str, postfix: str = "") -> None:
    """Compare only the .metadata of a resource with the expected compare file."""
    expected_result = _expected_compare_path(test_dir, resource, postfix)

    actual_meta = (yaml.safe_load(kubectl_bin("get", resource, "-o", "yaml")) or {}).get(
        "metadata", {}
    ) or {}

    annotations = actual_meta.get("annotations")
    if isinstance(annotations, dict):
        annotations.pop("kubectl.kubernetes.io/last-applied-configuration", None)
    for key in ("namespace", "resourceVersion", "uid", "creationTimestamp", "managedFields"):
        actual_meta.pop(key, None)

    if env_bool("UPDATE_COMPARE_FILES"):
        # Preserve the non-metadata fields (apiVersion/kind/type) and refresh .metadata.
        doc = (
            yaml.safe_load(expected_result.read_text()) if expected_result.exists() else {}
        ) or {}
        doc["metadata"] = actual_meta
        expected_result.write_text(yaml.dump(doc, sort_keys=False))
        return

    expected_meta = (yaml.safe_load(expected_result.read_text()) or {}).get("metadata", {}) or {}

    _assert_yaml_equal(expected_meta, actual_meta, str(expected_result), resource)


def apply_runtime_class(test_dir: str) -> None:
    """Create RuntimeClass with the runc handler."""
    kubectl_bin("delete", "runtimeclass", "container-rc", "--ignore-not-found")

    with open(f"{test_dir}/../conf/container-rc.yaml", "r") as f:
        config = yaml.safe_load(f)

    handler = "runc"
    logger.info(f"Create RuntimeClass with handler {handler}")
    config["handler"] = handler
    kubectl_bin("apply", "-f", "-", input_data=yaml.dump(config))
