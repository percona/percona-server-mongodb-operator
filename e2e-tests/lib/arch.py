import json
import os
from typing import Any

import yaml

ARCH_NODE_SELECTOR_KEY = "kubernetes.io/arch"

_TEMPLATE = ("spec", "template", "spec")

# Kind -> path to its pod spec (where nodeSelector/tolerations live).
_POD_SPEC_PATHS: dict[str, tuple[str, ...]] = {
    "Pod": ("spec",),
    "Deployment": _TEMPLATE,
    "StatefulSet": _TEMPLATE,
    "DaemonSet": _TEMPLATE,
    "Job": _TEMPLATE,
}


def arch() -> str:
    """Target architecture for scheduling, or '' when disabled."""
    return os.environ.get("ARCH", "")


def _toleration(value: str) -> dict[str, str]:
    return {
        "key": ARCH_NODE_SELECTOR_KEY,
        "operator": "Equal",
        "value": value,
        "effect": "NoSchedule",
    }


def add_arch_scheduling(pod_spec: dict[str, Any]) -> None:
    """Pin a pod spec to the target architecture (nodeSelector + toleration)."""
    value = arch()
    if not value:
        return
    pod_spec.setdefault("nodeSelector", {})[ARCH_NODE_SELECTOR_KEY] = value
    tolerations = pod_spec.setdefault("tolerations", [])
    if _toleration(value) not in tolerations:
        tolerations.append(_toleration(value))


def _nested(node: Any, path: tuple[str, ...]) -> dict[str, Any] | None:
    for key in path:
        node = node.get(key) if isinstance(node, dict) else None
    return node if isinstance(node, dict) else None


def _patch_psmdb(spec: dict[str, Any]) -> None:
    for replset in spec.get("replsets") or []:
        add_arch_scheduling(replset)
        for component in ("arbiter", "nonvoting", "hidden"):
            if replset.get(component) is not None:
                add_arch_scheduling(replset[component])

    sharding = spec.get("sharding") or {}
    for component in ("configsvrReplSet", "mongos"):
        if sharding.get(component) is not None:
            add_arch_scheduling(sharding[component])


def _patch_doc(doc: dict[str, Any]) -> bool:
    """Add arch scheduling to a single manifest doc. Returns True if patched."""
    if doc.get("kind") == "PerconaServerMongoDB":
        _patch_psmdb(doc.get("spec") or {})
        return True

    path = _POD_SPEC_PATHS.get(doc.get("kind", ""))
    pod_spec = _nested(doc, path) if path else None
    if pod_spec is None:
        return False
    add_arch_scheduling(pod_spec)
    return True


def add_arch_to_manifest(yaml_text: str) -> str:
    """Add arch scheduling to every pod-owning doc in a (multi-doc) manifest."""
    if not arch():
        return yaml_text

    docs = list(yaml.safe_load_all(yaml_text))
    patched = [_patch_doc(doc) for doc in docs if isinstance(doc, dict)]
    if not any(patched):
        return yaml_text
    return yaml.dump_all(docs, sort_keys=False)


def helm_arch_set_args(prefixes: tuple[str, ...] = ("",)) -> list[str]:
    """`--set` args pinning helm-installed pods to the target architecture.

    Each prefix targets a pod-owning value block in the chart (e.g. "server.").
    """
    value = arch()
    if not value:
        return []

    settings = {
        "nodeSelector.kubernetes\\.io/arch": value,
        "tolerations[0].key": ARCH_NODE_SELECTOR_KEY,
        "tolerations[0].operator": "Equal",
        "tolerations[0].value": value,
        "tolerations[0].effect": "NoSchedule",
    }
    return [
        arg
        for prefix in prefixes
        for key, val in settings.items()
        for arg in ("--set", f"{prefix}{key}={val}")
    ]


def arch_run_overrides() -> str:
    """`--overrides` JSON that adds arch scheduling to a `kubectl run` pod."""
    value = arch()
    return json.dumps(
        {
            "spec": {
                "nodeSelector": {ARCH_NODE_SELECTOR_KEY: value},
                "tolerations": [_toleration(value)],
            }
        }
    )


def clean_arch(node: object) -> None:
    """Strip the arch nodeSelector/toleration so compare files stay arch-agnostic."""
    if isinstance(node, list):
        for item in node:
            clean_arch(item)
        return
    if not isinstance(node, dict):
        return

    selector = node.get("nodeSelector")
    if isinstance(selector, dict):
        selector.pop(ARCH_NODE_SELECTOR_KEY, None)
        if not selector:
            node.pop("nodeSelector")

    tolerations = node.get("tolerations")
    if isinstance(tolerations, list):
        kept = [
            t
            for t in tolerations
            if not (isinstance(t, dict) and t.get("key") == ARCH_NODE_SELECTOR_KEY)
        ]
        if kept:
            node["tolerations"] = kept
        else:
            node.pop("tolerations")

    for child in node.values():
        clean_arch(child)
