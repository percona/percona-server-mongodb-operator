import logging
import os
import subprocess
from collections.abc import Callable
from pathlib import Path
from typing import Any

import yaml

from .kubectl import kubectl_bin
from .utils import env_bool, render_yaml_diff

logger = logging.getLogger(__name__)

ARCH_NODE_SELECTOR_KEY = "kubernetes.io/arch"


def _arch() -> str:
    """Target architecture for scheduling, or '' when disabled."""
    return os.environ.get("ARCH", "")


# Keys removed wherever they appear in the tree.
_DELETE_KEYS_ANYWHERE = {
    "creationTimestamp",
    "namespace",
    "uid",
    "image",
    "clusterIP",
    "clusterIPs",
    "dataSource",
    "procMount",
    "storageClassName",
    "finalizers",
    "volumeName",
    "kubernetes.io/pvc-protection",
    "volume.beta.kubernetes.io/storage-provisioner",
    "volume.kubernetes.io/storage-provisioner",
    "volume.kubernetes.io/selected-node",
    "percona.com/last-config-hash",
    "percona.com/configuration-hash",
    "percona.com/ssl-hash",
    "percona.com/ssl-internal-hash",
    "healthCheckNodePort",
    "nodePort",
}

# Scalars replaced wherever they appear (exact match).
_VALUE_REPLACEMENTS = {"extensions/v1beta1": "apps/v1", "batch/v1beta1": "batch/v1"}

_EACH = object()  # list-wildcard sentinel for path navigation

# Fixed paths removed (segment tuples so dotted annotation keys are safe).
_DELETE_PATHS = [
    ("metadata", "ownerReferences", _EACH, "apiVersion"),
    ("metadata", "managedFields"),
    ("metadata", "resourceVersion"),
    ("metadata", "selfLink"),
    ("metadata", "annotations", "cloud.google.com/neg"),
    ("metadata", "annotations", "field.cattle.io/publicEndpoints"),
    ("metadata", "annotations", "metallb.io/ip-allocated-from-pool"),
    ("metadata", "annotations", "networking.gke.io/target-pool"),
    ("metadata", "annotations", "kubectl.kubernetes.io/last-applied-configuration"),
    ("status",),
    ("spec", "volumeMode"),
    ("spec", "volumeClaimTemplates", _EACH, "apiVersion"),
    ("spec", "volumeClaimTemplates", _EACH, "kind"),
    ("spec", "ipFamilies"),
    ("spec", "ipFamilyPolicy"),
    ("spec", "persistentVolumeClaimRetentionPolicy"),
    ("spec", "internalTrafficPolicy"),
    ("spec", "allocateLoadBalancerNodePorts"),
]


def _add_arch_scheduling(target: dict[str, Any]) -> None:
    """Add the arch nodeSelector and toleration to a pod-owning spec."""
    arch = _arch()
    target.setdefault("nodeSelector", {})[ARCH_NODE_SELECTOR_KEY] = arch

    toleration = {
        "key": ARCH_NODE_SELECTOR_KEY,
        "operator": "Equal",
        "value": arch,
        "effect": "NoSchedule",
    }
    tolerations = target.setdefault("tolerations", [])
    if toleration not in tolerations:
        tolerations.append(toleration)


def _add_arch_to_psmdb(spec: dict[str, Any]) -> None:
    """Pin every PSMDB component to the target architecture."""
    if not _arch():
        return

    for replset in spec.get("replsets") or []:
        _add_arch_scheduling(replset)
        for component in ("arbiter", "nonvoting", "hidden"):
            if replset.get(component) is not None:
                _add_arch_scheduling(replset[component])

    sharding = spec.get("sharding") or {}
    for component in ("configsvrReplSet", "mongos"):
        if sharding.get(component) is not None:
            _add_arch_scheduling(sharding[component])


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

        _add_arch_to_psmdb(spec)

    return yaml.dump(config)


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


def _delete_path(
    node: object, path: tuple[Any, ...], when: Callable[[Any], bool] | None = None
) -> None:
    """Delete the key at the end of `path`, expanding `_EACH` over lists.

    With `when`, the final key is deleted only if `when(value)` is true.
    """
    *parents, last = path
    for i, segment in enumerate(parents):
        if segment is _EACH:
            if isinstance(node, list):
                for item in node:
                    _delete_path(item, path[i + 1 :], when)
            return
        if not isinstance(node, dict) or segment not in node:
            return
        node = node[segment]

    if isinstance(node, dict) and last in node and (when is None or when(node[last])):
        del node[last]


def _delete_list_items(
    node: object, path: tuple[Any, ...], predicate: Callable[[Any], bool]
) -> None:
    """Filter out items matching `predicate` from the list at `path`."""
    *parents, last = path
    for i, segment in enumerate(parents):
        if segment is _EACH:
            if isinstance(node, list):
                for item in node:
                    _delete_list_items(item, path[i + 1 :], predicate)
            return
        if not isinstance(node, dict) or segment not in node:
            return
        node = node[segment]

    if isinstance(node, dict) and isinstance(node.get(last), list):
        node[last] = [item for item in node[last] if not predicate(item)]


def _walk(node: object, namespace: str) -> None:
    """Single pass: drop keys anywhere, replace scalar values, sub namespace."""
    if isinstance(node, dict):
        for key in [k for k in node if k in _DELETE_KEYS_ANYWHERE]:
            del node[key]
        for key, value in node.items():
            if isinstance(value, str):
                node[key] = _filter_scalar(value, namespace)
            else:
                _walk(value, namespace)
    elif isinstance(node, list):
        for i, item in enumerate(node):
            if isinstance(item, str):
                node[i] = _filter_scalar(item, namespace)
            else:
                _walk(item, namespace)


def _filter_scalar(value: str, namespace: str) -> str:
    if value in _VALUE_REPLACEMENTS:
        return _VALUE_REPLACEMENTS[value]
    return value.replace(namespace, "NAME_SPACE")


def _clean_arch(node: object) -> None:
    """Remove the arch nodeSelector/toleration so compare files stay arch-agnostic."""
    arch = _arch()
    if isinstance(node, dict):
        selector = node.get("nodeSelector")
        if isinstance(selector, dict):
            selector.pop(ARCH_NODE_SELECTOR_KEY, None)
            if not selector:
                node.pop("nodeSelector", None)

        tolerations = node.get("tolerations")
        if isinstance(tolerations, list):
            node["tolerations"] = [
                t
                for t in tolerations
                if not (
                    isinstance(t, dict)
                    and t.get("key") == ARCH_NODE_SELECTOR_KEY
                    and t.get("value") == arch
                )
            ]
            if not node["tolerations"]:
                node.pop("tolerations", None)

        for value in node.values():
            _clean_arch(value)
    elif isinstance(node, list):
        for item in node:
            _clean_arch(item)


def filter_yaml(
    yaml_content: str, namespace: str, resource: str = "", skip_generation_check: bool = False
) -> str:
    """Filter YAML content for stable resource comparisons."""
    data = yaml.safe_load(yaml_content)
    if data is None:
        return yaml_content

    for path in _DELETE_PATHS:
        _delete_path(data, path)
    _delete_path(
        data,
        ("spec", "volumeClaimTemplates", _EACH, "spec", "volumeMode"),
        when=lambda v: v == "Filesystem",
    )
    _delete_list_items(
        data,
        ("spec", "template", "spec", "containers", _EACH, "env"),
        lambda e: isinstance(e, dict) and e.get("name") == "NAMESPACE",
    )

    if "cronjob" in resource.lower() or skip_generation_check:
        _delete_path(data, ("metadata", "generation"))

    _walk(data, namespace)

    if _arch():
        _clean_arch(data)

    return yaml.dump(data, sort_keys=False)


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


def compare_kubectl(
    test_dir: str,
    resource: str,
    namespace: str,
    postfix: str = "",
    skip_generation_check: bool = False,
) -> None:
    """Compare kubectl resource with expected output using yq filtering"""
    expected_result = _expected_compare_path(test_dir, resource, postfix)

    try:
        actual_yaml = kubectl_bin("get", resource, "-o", "yaml")
        filtered_actual = filter_yaml(actual_yaml, namespace, resource, skip_generation_check)

        if env_bool("UPDATE_COMPARE_FILES"):
            expected_result.write_text(filtered_actual)
            return

        if not expected_result.exists():
            raise FileNotFoundError(
                f"No compare file for {resource}: {expected_result} "
                f"(run with UPDATE_COMPARE_FILES=1 to create it)"
            )
        filtered_expected = filter_yaml(
            expected_result.read_text(), namespace, resource, skip_generation_check
        )

        actual_data = yaml.safe_load(filtered_actual)
        expected_data = yaml.safe_load(filtered_expected)

        _assert_yaml_equal(expected_data, actual_data, str(expected_result), resource)

    except subprocess.CalledProcessError as e:
        raise ValueError(f"Failed to process resource {resource}: {e}") from e


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
