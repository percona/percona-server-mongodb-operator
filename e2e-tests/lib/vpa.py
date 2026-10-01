"""Helpers for the VPA e2e suites.

The operator never creates VerticalPodAutoscaler objects; it only reads
status.recommendation from them. These helpers therefore stand in for whatever
external tool would normally write that status (VPA Recommender, Goldilocks, ...)
by creating VPA objects and patching their status directly.

The operator never writes resources into the CR spec. It commits the effective
values to status.vpaStatus.<component>.resources and generates the StatefulSet
from them, so assertions here read the StatefulSet (what the cluster runs) and
status (what the operator decided), and check that spec still holds what the user
declared.

Components map to the StatefulSet that carries them:

    rs0        -> <cluster>-rs0          container mongod
    cfg        -> <cluster>-cfg          container mongod
    mongos     -> <cluster>-mongos       container mongos
    nonvoting  -> <cluster>-rs0-nv       container mongod-nv
    hidden     -> <cluster>-rs0-hidden   container mongod-hidden
"""

import logging
import math

from lib.kubectl import kubectl_bin
from lib.utils import retry

logger = logging.getLogger(__name__)

# Component name -> (StatefulSet name suffix, container name)
COMPONENT_STATEFULSETS = {
    "rs0": ("rs0", "mongod"),
    "cfg": ("cfg", "mongod"),
    "mongos": ("mongos", "mongos"),
    "nonvoting": ("rs0-nv", "mongod-nv"),
    "hidden": ("rs0-hidden", "mongod-hidden"),
}


# Waiting for a value to reach the StatefulSet means waiting for a rollout, not
# for an API write: the operator commits to status, regenerates the StatefulSet,
# and SmartUpdate rolls the pods, and on a sharded cluster mongos only starts once
# rs0 and cfg are done. These waits return as soon as the value lands, so a
# generous budget costs nothing on success and only bounds how long a genuine
# failure takes to report.
CONVERGE_ATTEMPTS = 120
CONVERGE_DELAY = 5


def _ns_args(namespace: str | None) -> list[str]:
    return ["-n", namespace] if namespace else []


def install_vpa_crd(crd_file: str) -> None:
    """Install the minimal VPA CRD. No VPA controller is needed: the operator only reads."""
    logger.info("Installing the VerticalPodAutoscaler CRD")
    kubectl_bin("apply", "-f", crd_file)
    kubectl_bin(
        "wait",
        "crd/verticalpodautoscalers.autoscaling.k8s.io",
        "--for=condition=Established",
        "--timeout=60s",
    )


def create_vpa_object(name: str, target: str, namespace: str | None = None) -> None:
    """Create a VPA object targeting a StatefulSet, with updateMode Off.

    updateMode Off means a real VPA controller would never evict pods, which is
    what the operator expects: it reads recommendations and applies them itself.
    """
    logger.info(f"Creating VPA object {name} targeting statefulset/{target}")
    manifest = f"""
apiVersion: autoscaling.k8s.io/v1
kind: VerticalPodAutoscaler
metadata:
  name: {name}
spec:
  targetRef:
    apiVersion: apps/v1
    kind: StatefulSet
    name: {target}
  updatePolicy:
    updateMode: "Off"
"""
    kubectl_bin("apply", *_ns_args(namespace), "-f", "-", input_data=manifest)


def set_vpa_recommendation(
    name: str,
    cpu: str,
    memory: str,
    container: str = "mongod",
    namespace: str | None = None,
) -> None:
    """Write a target recommendation into the VPA object's status subresource."""
    logger.info(f"Setting recommendation on {name}: cpu={cpu} memory={memory} ({container})")
    bounds = f'{{"cpu": "{cpu}", "memory": "{memory}"}}'
    patch = (
        '{"status": {"recommendation": {"containerRecommendations": [{'
        f'"containerName": "{container}",'
        f'"target": {bounds},'
        f'"lowerBound": {bounds},'
        f'"upperBound": {bounds}'
        "}]}}}"
    )
    kubectl_bin(
        "patch",
        *_ns_args(namespace),
        f"vpa/{name}",
        "--type=merge",
        "--subresource=status",
        "-p",
        patch,
    )


def get_vpa_status(field: str, cluster: str, namespace: str | None = None) -> str:
    """Read a field under .status.vpaStatus, e.g. "rs0.cpu" or "mongos.memory"."""
    return kubectl_bin(
        "get",
        *_ns_args(namespace),
        "psmdb",
        cluster,
        "-o",
        f"jsonpath={{.status.vpaStatus.{field}}}",
    ).strip()


def wait_for_vpa_status(
    field: str,
    expected: str,
    cluster: str,
    namespace: str | None = None,
    max_attempts: int = 30,
    delay: int = 5,
) -> None:
    """Wait until .status.vpaStatus.<field> equals expected."""
    logger.info(f"Waiting for {cluster}.status.vpaStatus.{field} == {expected}")
    retry(
        lambda: get_vpa_status(field, cluster, namespace),
        max_attempts=max_attempts,
        delay=delay,
        condition=lambda value: value == expected,
    )


def get_requests(component: str, resource: str, cluster: str, namespace: str | None = None) -> str:
    """Read a resource request from the StatefulSet the component actually runs."""
    suffix, container = COMPONENT_STATEFULSETS[component]
    return kubectl_bin(
        "get",
        *_ns_args(namespace),
        "sts",
        f"{cluster}-{suffix}",
        "-o",
        f'jsonpath={{.spec.template.spec.containers[?(@.name=="{container}")]'
        f".resources.requests.{resource}}}",
    ).strip()


def get_spec_requests(
    component: str, resource: str, cluster: str, namespace: str | None = None
) -> str:
    """Read a resource request from the CR spec, which the operator must never write."""
    paths = {
        "rs0": "replsets[0]",
        "cfg": "sharding.configsvrReplSet",
        "mongos": "sharding.mongos",
        "nonvoting": "replsets[0].nonvoting",
        "hidden": "replsets[0].hidden",
    }
    return kubectl_bin(
        "get",
        *_ns_args(namespace),
        "psmdb",
        cluster,
        "-o",
        f"jsonpath={{.spec.{paths[component]}.resources.requests.{resource}}}",
    ).strip()


def status_key(component: str) -> str:
    """Return the status.vpaStatus key the operator uses for a component.

    The operator keys status by the replset-qualified component name — "rs0-nv"
    and "rs0-hidden" rather than "nonvoting" and "hidden" — which is the same
    string as the StatefulSet suffix.
    """
    return COMPONENT_STATEFULSETS[component][0]


def get_committed_requests(
    component: str, resource: str, cluster: str, namespace: str | None = None
) -> str:
    """Read the effective request the operator committed to status for a component."""
    return get_vpa_status(
        f"{status_key(component)}.resources.requests.{resource}", cluster, namespace
    )


def assert_requests(
    component: str, resource: str, expected: str, cluster: str, namespace: str | None = None
) -> None:
    """Assert a resource request equals expected right now, without waiting."""
    actual = get_requests(component, resource, cluster, namespace)
    suffix, container = COMPONENT_STATEFULSETS[component]
    assert actual == expected, (
        f"{cluster}-{suffix} container {container} requests.{resource} "
        f"is {actual!r}, expected {expected!r}"
    )
    logger.info(f"OK: {cluster}/{component} requests.{resource} == {expected}")


def assert_spec_untouched(
    component: str, declared: dict[str, str], cluster: str, namespace: str | None = None
) -> None:
    """Assert the CR spec still holds what the user declared.

    The operator commits effective resources to status and generates the
    StatefulSet from them, so spec must stay single-writer. This is the assertion
    that would catch a regression back to patching the CR.
    """
    for resource, expected in declared.items():
        actual = get_spec_requests(component, resource, cluster, namespace)
        assert actual == expected, (
            f"{cluster} spec for {component} requests.{resource} is {actual!r}, "
            f"expected the declared {expected!r} — the operator must not write "
            f"resources into the CR spec"
        )
    logger.info(f"OK: {cluster}/{component} spec still holds the declared resources")


def assert_committed(
    component: str,
    declared: dict[str, str],
    cluster: str,
    namespace: str | None = None,
    max_attempts: int = CONVERGE_ATTEMPTS,
    delay: int = CONVERGE_DELAY,
) -> None:
    """Assert status carries the committed values and the StatefulSet agrees with them.

    Together with assert_spec_untouched this pins the whole path: recommendation
    -> status.vpaStatus.<component>.resources -> StatefulSet, with spec untouched.

    The StatefulSet converges after the commit rather than with it, so this waits
    for agreement. Mongos lags the most: reconcileMongosStatefulset returns early
    until the replsets are up to date, so its StatefulSet is only regenerated once
    rs0 and cfg have finished rolling.
    """
    for resource in declared:
        committed = get_committed_requests(component, resource, cluster, namespace)
        assert committed, (
            f"{cluster} status.vpaStatus.{status_key(component)}.resources.requests."
            f"{resource} is empty; the operator should have committed an effective value"
        )

        def read(res: str = resource) -> str:
            return get_requests(component, res, cluster, namespace)

        def agrees(value: str, expected: str = committed) -> bool:
            return value == expected

        running: str = retry(read, max_attempts=max_attempts, delay=delay, condition=agrees)
        assert committed == running, (
            f"{cluster}/{component} {resource}: status committed {committed!r} but the "
            f"StatefulSet runs {running!r}"
        )
    logger.info(f"OK: {cluster}/{component} status and StatefulSet agree")


def wait_for_requests(
    component: str,
    resource: str,
    expected: str,
    cluster: str,
    namespace: str | None = None,
    max_attempts: int = CONVERGE_ATTEMPTS,
    delay: int = CONVERGE_DELAY,
) -> None:
    """Wait until a resource request reaches expected in the StatefulSet."""
    logger.info(f"Waiting for {cluster}/{component} requests.{resource} == {expected}")
    retry(
        lambda: get_requests(component, resource, cluster, namespace),
        max_attempts=max_attempts,
        delay=delay,
        condition=lambda value: value == expected,
    )


def get_last_applied_at(component: str, cluster: str, namespace: str | None = None) -> str:
    """Read .status.vpaStatus.<component>.lastAppliedAt, empty when never applied."""
    return get_vpa_status(f"{component}.lastAppliedAt", cluster, namespace)


def wait_for_last_applied_at(
    component: str,
    cluster: str,
    namespace: str | None = None,
    max_attempts: int = 30,
    delay: int = 5,
) -> str:
    """Wait until lastAppliedAt is set for a component, and return it."""
    logger.info(f"Waiting for {cluster}/{component} lastAppliedAt to be set")
    applied: str = retry(
        lambda: get_last_applied_at(component, cluster, namespace),
        max_attempts=max_attempts,
        delay=delay,
        condition=bool,
    )
    return applied


def wait_for_new_apply(
    component: str,
    after: str,
    cluster: str,
    namespace: str | None = None,
    max_attempts: int = 30,
    delay: int = 5,
) -> str:
    """Wait until lastAppliedAt advances past `after`, and return the new value.

    Timestamps are RFC 3339 in UTC and fixed width, so lexicographic comparison
    matches chronological order.
    """
    logger.info(f"Waiting for {cluster}/{component} lastAppliedAt > {after}")
    applied: str = retry(
        lambda: get_last_applied_at(component, cluster, namespace),
        max_attempts=max_attempts,
        delay=delay,
        condition=lambda value: bool(value) and value > after,
    )
    return applied


# ---------------------------------------------------------------------------
# in-place resize helpers
#
# An in-place resize changes a running pod instead of replacing it, so the proof
# it happened is that the pod's identity is unchanged: same uid, same container
# restart count. A recreate changes both.
# ---------------------------------------------------------------------------

# floor(ratio * (limit - 1GiB)) / 1GiB, floored at MinWiredTigerCacheSizeGB.
# Mirrors getWiredTigerCacheSizeGB in pkg/psmdb/container.go.
GIB = 1024**3
MIN_WT_CACHE_GB = 0.25


def expected_wiredtiger_cache_bytes(limit_bytes: int, ratio: float) -> int:
    """The cache size mongod should end up with for a given memory limit."""
    size_gb = math.floor(ratio * (limit_bytes - GIB)) / GIB
    size_gb = max(size_gb, MIN_WT_CACHE_GB)
    # The operator passes whole megabytes to setParameter.
    return int(size_gb * 1024) * 1024 * 1024


def pod_identity(
    pod: str, namespace: str | None = None, container: str = "mongod"
) -> tuple[str, str]:
    """Return (uid, restartCount) — both change when a pod is recreated.

    container must name the component's own container: mongos pods have no
    container called "mongod", and a wrong name silently yields an empty restart
    count, which would compare equal and stop detecting restarts.
    """
    out = kubectl_bin(
        "get",
        *_ns_args(namespace),
        "pod",
        pod,
        "-o",
        "jsonpath={.metadata.uid}|"
        f"{{.status.containerStatuses[?(@.name=='{container}')].restartCount}}",
    ).strip()
    uid, _, restarts = out.partition("|")
    return uid, restarts


def pod_resources(
    pod: str,
    resource: str,
    kind: str = "requests",
    namespace: str | None = None,
    container: str = "mongod",
) -> str:
    """Read a resource the kubelet currently has applied to a pod's container.

    container must match the component: mongod, mongos, mongod-nv or
    mongod-hidden. A name that does not match any container returns "" rather
    than erroring, so a wrong one looks like "the value never arrived".
    """
    return kubectl_bin(
        "get",
        *_ns_args(namespace),
        "pod",
        pod,
        "-o",
        f'jsonpath={{.spec.containers[?(@.name=="{container}")].resources.{kind}.{resource}}}',
    ).strip()


def wiredtiger_cache_bytes(client_pod: str, host: str, namespace: str | None = None) -> int:
    """Ask a specific mongod what cache size it is actually running with.

    Connects directly to one member rather than through the replica set, so the
    answer is about that pod and not whichever node happens to be primary.
    """
    uri = (
        f"mongodb://clusterAdmin:clusterAdmin123456@{host}:27017"
        "/admin?ssl=false&directConnection=true"
    )
    out = kubectl_bin(
        "exec",
        *_ns_args(namespace),
        client_pod,
        "--",
        "timeout",
        "30",
        "env",
        "HOME=/tmp",
        "mongosh",
        uri,
        "--quiet",
        "--eval",
        'db.serverStatus().wiredTiger.cache["maximum bytes configured"]',
    )
    for line in reversed(out.strip().splitlines()):
        line = line.strip()
        if line.isdigit():
            return int(line)
    raise AssertionError(f"could not read WiredTiger cache size from mongosh output: {out!r}")


def wait_for_wiredtiger_cache(
    client_pod: str,
    host: str,
    expected: int,
    namespace: str | None = None,
    max_attempts: int = 60,
    delay: int = 5,
) -> None:
    """Wait until mongod reports the expected cache size.

    The operator adjusts a running mongod with setParameter after resizing it, so
    this lags the pod resize slightly.
    """
    logger.info(f"Waiting for {host} WiredTiger cache to reach {expected} bytes")

    def read() -> int:
        return wiredtiger_cache_bytes(client_pod, host, namespace)

    def matches(value: int) -> bool:
        return value == expected

    retry(read, max_attempts=max_attempts, delay=delay, condition=matches)


def wait_for_pod_resources(
    pod: str,
    resource: str,
    expected: str,
    kind: str = "requests",
    namespace: str | None = None,
    container: str = "mongod",
    max_attempts: int = CONVERGE_ATTEMPTS,
    delay: int = CONVERGE_DELAY,
) -> None:
    """Wait until the kubelet has applied a resource value to the running pod.

    The StatefulSet is updated as soon as the operator commits, but individual
    pods are moved to the new revision later, by the SmartUpdate loop. So a value
    present on the StatefulSet is not yet present on the pod.
    """
    logger.info(f"Waiting for pod {pod} {kind}.{resource} == {expected}")

    def read() -> str:
        return pod_resources(pod, resource, kind, namespace, container)

    def matches(value: str) -> bool:
        return value == expected

    retry(read, max_attempts=max_attempts, delay=delay, condition=matches)


def pod_ip(pod: str, namespace: str | None = None) -> str:
    """Pod IP, used to reach one mongod directly.

    Non-voting and hidden members sit in their own StatefulSets, so their pod DNS
    names differ from the replset's. Connecting by IP sidesteps that entirely.
    """
    return kubectl_bin(
        "get", *_ns_args(namespace), "pod", pod, "-o", "jsonpath={.status.podIP}"
    ).strip()
