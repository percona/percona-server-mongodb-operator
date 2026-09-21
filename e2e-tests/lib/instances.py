"""Helpers for replica sets whose topology comes from spec.replsets[].instances[].

The legacy helpers assume the fixed shape: pods numbered off replsets[].size,
plus the arbiter/nonVoting/hidden blocks, each with its own well-known suffix.
A replica set built from instances[] has none of that. Every group is named by
the user, the member count lives on the instance, and the base StatefulSet does
not exist at all unless a group is literally called "mongod". So the layout has
to be read back from the CR rather than assumed.
"""

import json
import logging
import time
from collections.abc import Callable
from dataclasses import dataclass
from typing import Any

from .kubectl import kubectl_bin, wait_pod

logger = logging.getLogger(__name__)

LABEL_INSTANCE = "app.kubernetes.io/instance"
LABEL_REPLSET = "app.kubernetes.io/replset"

# Reserved group names reproduce the objects the legacy blocks produced, so
# their component label and container name are not derived from the group name.
# Mirrors pkg/naming/group.go.
_RESERVED: dict[str, tuple[str, str]] = {
    # group name: (component label, container name)
    "mongod": ("mongod", "mongod"),
    "nonVoting": ("nonVoting", "mongod-nv"),
    "hidden": ("hidden", "mongod-hidden"),
    "arbiter": ("arbiter", "mongod-arbiter"),
}


def statefulset_name(cluster: str, rs: str, group: str) -> str:
    """Mirrors api.DerivedStatefulSetName: mongod keeps the bare name, nonVoting
    is abbreviated to -nv, and everything else is suffixed with the group name."""
    base = f"{cluster}-{rs}"
    if group == "mongod":
        return base
    if group == "nonVoting":
        return f"{base}-nv"
    return f"{base}-{group}"


@dataclass(frozen=True)
class InstanceGroup:
    """One entry of spec.replsets[].instances[] and the objects it produces."""

    name: str
    replicas: int
    statefulset: str
    component: str
    container: str
    data_bearing: bool

    @property
    def pods(self) -> list[str]:
        return [f"{self.statefulset}-{i}" for i in range(self.replicas)]

    @property
    def pvcs(self) -> list[str]:
        if not self.data_bearing:
            return []
        return [f"mongod-data-{pod}" for pod in self.pods]


def read_cr(cluster: str) -> dict[str, Any]:
    cr: dict[str, Any] = json.loads(kubectl_bin("get", "psmdb", cluster, "-o", "json"))
    return cr


def _replset(cr: dict[str, Any], rs: str) -> dict[str, Any]:
    for replset in cr["spec"]["replsets"]:
        if replset["name"] == rs:
            return dict(replset)
    raise KeyError(f"replset {rs} not found in {cr['metadata']['name']}")


def read_instance_groups(cluster: str, rs: str = "rs0") -> list[InstanceGroup]:
    """Read the declared instances[] back from the live CR.

    Read rather than hard-coded so a test that scales or removes a group does
    not have to restate the topology at every step.
    """
    instances = _replset(read_cr(cluster), rs).get("instances") or []

    groups = []
    for inst in instances:
        name = inst["name"]
        arbiter_only = bool((inst.get("rsConfig") or {}).get("arbiterOnly", False))
        component, container = _RESERVED.get(
            name, (name, "mongod-arbiter" if arbiter_only else "mongod")
        )
        groups.append(
            InstanceGroup(
                name=name,
                replicas=int(inst["replicas"]),
                statefulset=statefulset_name(cluster, rs, name),
                component=component,
                container=container,
                data_bearing=not arbiter_only,
            )
        )
    return sorted(groups, key=lambda g: g.name)


def wait_until(
    what: str,
    predicate: Callable[[], bool],
    timeout: int = 600,
    interval: int = 5,
) -> None:
    """Poll predicate until it holds, else raise TimeoutError."""
    logger.info(f"Waiting for {what}")
    deadline = time.monotonic() + timeout
    while True:
        if predicate():
            logger.info(f"{what}: done")
            return
        if time.monotonic() >= deadline:
            raise TimeoutError(f"Timeout after {timeout}s waiting for {what}")
        time.sleep(interval)


def _status(cluster: str) -> dict[str, Any]:
    out = kubectl_bin("get", "psmdb", cluster, "-o", "jsonpath={.status}", check=False)
    if not out.strip():
        return {}
    status: dict[str, Any] = json.loads(out)
    return status


def wait_replset_ready(cluster: str, rs: str = "rs0", timeout: int = 900) -> None:
    """Wait until every declared member is ready and the cluster reports ready.

    status.replsets[rs].size is the sum of every instance's replicas, so this
    covers all the groups at once without naming them.
    """

    def ready() -> bool:
        status = _status(cluster)
        replset = (status.get("replsets") or {}).get(rs) or {}
        size, count = replset.get("size"), replset.get("ready")
        logger.info(
            f"cluster={status.get('state')} {rs}={replset.get('status')} ready={count}/{size}"
        )
        return bool(size) and size == count and status.get("state") == "ready"

    wait_until(f"replset {rs} of {cluster} to be ready", ready, timeout=timeout)


def wait_for_instances_running(
    cluster: str, rs: str = "rs0", timeout: int = 900, pod_timeout: int = 360
) -> None:
    """Wait for every pod of every declared group, then for the cluster itself.

    The per-pod budget is separate and much smaller than the cluster one: a pod
    that is not ready within it is a real failure, and a heterogeneous replica
    set has enough groups that sharing one long budget across all of them would
    outlast the job's own timeout.
    """
    for group in read_instance_groups(cluster, rs):
        for pod in group.pods:
            wait_pod(pod, timeout=pod_timeout)
    wait_replset_ready(cluster, rs, timeout)


def pod_identities(cluster: str, rs: str = "rs0") -> dict[str, str]:
    """Map every member pod to an identity that changes when it is rolled.

    Both halves matter. The uid changes when the pod object is replaced, and
    controller-revision-hash changes when the StatefulSet template changes, so
    a group that was left alone has to match on both.
    """
    out = kubectl_bin(
        "get",
        "pods",
        "-l",
        f"{LABEL_INSTANCE}={cluster},{LABEL_REPLSET}={rs}",
        "-o",
        "json",
    )
    items = json.loads(out).get("items", [])
    return {
        item["metadata"]["name"]: "{}/{}".format(
            item["metadata"]["uid"],
            (item["metadata"].get("labels") or {}).get("controller-revision-hash", ""),
        )
        for item in items
    }


def wait_for_gone(kind: str, names: list[str], timeout: int = 900) -> None:
    """Wait until none of the named resources exist.

    Polling rather than `kubectl wait --for=delete`, which errors out when the
    resource is already gone -- and a retired workload may well be deleted
    before the test gets round to waiting for it.
    """

    def gone() -> bool:
        listed = kubectl_bin(
            "get",
            kind,
            "-o",
            "jsonpath={range .items[*]}{.metadata.name}{'\\n'}{end}",
            check=False,
        ).split()
        remaining = [name for name in names if name in listed]
        if remaining:
            logger.info(f"still waiting on {kind}: {remaining}")
        return not remaining

    wait_until(f"{kind} {names} to be deleted", gone, timeout=timeout)


def statefulset_names(cluster: str, rs: str = "rs0") -> set[str]:
    """Every StatefulSet the operator owns for this replica set."""
    out = kubectl_bin(
        "get",
        "sts",
        "-l",
        f"{LABEL_INSTANCE}={cluster},{LABEL_REPLSET}={rs}",
        "-o",
        "jsonpath={range .items[*]}{.metadata.name}{'\\n'}{end}",
    )
    return {name for name in out.split() if name}


def _instance_index(cluster: str, instance: str, rs: str = "rs0") -> tuple[int, int]:
    """Locate (replset index, instance index) for a JSON patch path."""
    cr = read_cr(cluster)
    for rs_index, replset in enumerate(cr["spec"]["replsets"]):
        if replset["name"] != rs:
            continue
        for inst_index, inst in enumerate(replset.get("instances") or []):
            if inst["name"] == instance:
                return rs_index, inst_index
    raise KeyError(f"no instance {instance!r} in replset {rs} of {cluster}")


def _patch_instance(
    cluster: str, instance: str, rs: str, op: str, sub_path: str, **kw: Any
) -> None:
    rs_index, inst_index = _instance_index(cluster, instance, rs)
    patch: dict[str, Any] = {
        "op": op,
        "path": f"/spec/replsets/{rs_index}/instances/{inst_index}{sub_path}",
    }
    patch.update(kw)
    kubectl_bin("patch", "psmdb", cluster, "--type=json", "-p", json.dumps([patch]))


def scale_instance(cluster: str, instance: str, replicas: int, rs: str = "rs0") -> None:
    logger.info(f"Scaling instance {instance} of {cluster} to {replicas}")
    _patch_instance(cluster, instance, rs, "replace", "/replicas", value=replicas)


def set_instance_configuration(
    cluster: str, instance: str, configuration: str, rs: str = "rs0"
) -> None:
    """Set an instance's own mongod configuration.

    `add` rather than `replace`: JSON patch add creates the member when the
    instance has no configuration yet and overwrites it when it does.
    """
    logger.info(f"Setting configuration on instance {instance} of {cluster}")
    _patch_instance(cluster, instance, rs, "add", "/configuration", value=configuration)


def remove_instance(cluster: str, instance: str, rs: str = "rs0") -> None:
    logger.info(f"Removing instance {instance} from {cluster}")
    _patch_instance(cluster, instance, rs, "remove", "")


def pause(cluster: str, paused: bool) -> None:
    logger.info(f"{'Pausing' if paused else 'Unpausing'} cluster {cluster}")
    kubectl_bin(
        "patch",
        "psmdb",
        cluster,
        "--type=merge",
        "-p",
        json.dumps({"spec": {"pause": paused}}),
    )


def wait_for_paused(cluster: str, rs: str = "rs0", timeout: int = 900) -> None:
    """Wait until no member pods are left and the cluster reports paused."""

    def stopped() -> bool:
        status = _status(cluster)
        replset = (status.get("replsets") or {}).get(rs) or {}
        pods = pod_identities(cluster, rs)
        logger.info(f"cluster={status.get('state')} {rs}={replset.get('status')} pods={len(pods)}")
        return not pods and status.get("state") == "paused"

    wait_until(f"cluster {cluster} to be paused", stopped, timeout=timeout)


def member_config(mongosh_output: str) -> dict[str, dict[str, Any]]:
    """Index an rs.conf().members array by the pod name in its host.

    Members are keyed by pod rather than by the _id MongoDB assigns, because
    _id is an implementation detail of the order members were added.
    """
    members = json.loads(mongosh_output)
    return {str(member["host"]).split(".")[0]: member for member in members}
