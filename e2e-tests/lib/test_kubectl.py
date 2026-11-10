"""Unit tests of the pure kubectl helpers; run with `make py-unit` (no cluster, no conftest)."""

from typing import Any

from lib.kubectl import schedulable_nodes


def _node(name: str, unschedulable: bool = False, *taints: tuple[str, str]) -> dict[str, Any]:
    spec: dict[str, Any] = {"taints": [{"key": key, "effect": effect} for key, effect in taints]}
    if unschedulable:
        spec["unschedulable"] = True
    return {"metadata": {"name": name}, "spec": spec}


def test_schedulable_nodes() -> None:
    nodes = [
        _node("master-0", False, ("node-role.kubernetes.io/master", "NoSchedule")),
        _node("control-plane-0", False, ("node-role.kubernetes.io/control-plane", "NoSchedule")),
        _node("worker-0"),
        _node("worker-1", True),
        _node("worker-2", False, ("node.kubernetes.io/unreachable", "NoExecute")),
        _node("worker-3", False, ("example.com/spot", "PreferNoSchedule")),
        {"metadata": {"name": "worker-4"}},
    ]
    assert schedulable_nodes(nodes) == ["worker-0", "worker-3", "worker-4"]
