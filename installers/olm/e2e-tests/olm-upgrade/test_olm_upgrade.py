#!/usr/bin/env python3

import logging
import os
import re
import time
import uuid
from collections.abc import Callable, Generator
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

import pytest
import yaml
from lib.kubectl import kubectl_bin, wait_for_running
from lib.mongo import MongoManager
from lib.operator import check_crd_for_deletion, delete_crd_rbac
from lib.secrets import get_user_data
from lib.utils import Paths, env_bool, retry
from olm import (
    SUBSCRIPTION,
    approve_until_installed,
    channel_of_csv,
    create_operator_group,
    create_subscription,
    csv_example,
    csv_names,
    csv_operator_image,
    delete_olm_install,
    delete_pending_plans,
    get_csv,
    get_json,
    get_package_manifest,
    installplan_phase,
    olm_available,
    subscription_status,
    wait_catalog_ready,
    wait_until,
)

logger = logging.getLogger(__name__)

# Operator log lines matching these are expected while controllers settle after a restart.
# Calibrate on real runs; keep each entry specific.
ALLOWED_OPERATOR_ERRORS = [
    re.compile(r"the object has been modified; please apply your changes to the latest version"),
]
OPERATOR_ERROR = re.compile(r"\tERROR\t|\"level\":\"error\"")

# Warning events OLM emits while a CSV is being replaced.
ALLOWED_CSV_WARNINGS = {"RequirementsNotMet", "InstallWaiting"}
# InstallPlan "AppliedWithWarnings" is fine when every warning is an API deprecation notice:
# the CRD still serves the old psmdb.percona.com/v1-x-0 versions.
DEPRECATION_WARNING = re.compile(r"\S+ PerconaServerMongoDB is deprecated and will be removed in")

# A brief "initializing" while the new operator takes over is tolerated; pods must not change.
ALLOWED_STATES = {"ready", "initializing"}
SOAK_SECONDS = 180
# Only read by the version-service cron, which upgradeOptions.apply=disabled and
# DISABLE_TELEMETRY leave idle: changing it bumps the CR generation without touching pods.
RECONCILE_PROBE = '{"spec":{"upgradeOptions":{"schedule":"0 3 * * *"}}}'


@dataclass(frozen=True)
class OlmUpgradeConfig:
    namespace: str
    cluster: str
    package: str
    source: str
    source_namespace: str
    from_channel: str
    from_csv: str
    target_channel: str
    target_csv: str


@dataclass
class UpgradeState:
    """Cluster snapshot taken before the upgrade, plus what was observed after it started."""

    pods: dict[str, tuple[str, int]] = field(default_factory=dict)
    sts_revisions: dict[str, tuple[str, str]] = field(default_factory=dict)
    events: dict[str, int] = field(default_factory=dict)
    states: list[str] = field(default_factory=list)


def _version(csv: str) -> tuple[int, ...]:
    """1.22.0 from percona-server-mongodb-operator.v1.22.0 (also covers -certified names)."""
    match = re.search(r"v?(\d+)\.(\d+)\.(\d+)$", csv)
    assert match, f"Cannot parse a version from CSV name {csv}"
    return tuple(int(part) for part in match.groups())


def _default_from_csv(manifest: dict[str, Any], target_channel: str) -> str:
    """Head of the newest channel older than the target one."""
    heads = [
        channel["currentCSV"]
        for channel in manifest["status"]["channels"]
        if channel["name"] != target_channel
    ]
    assert heads, f"Catalog has no channel to upgrade from besides {target_channel}"
    return str(max(heads, key=_version))


def _delete_crds(src_dir: str) -> None:
    """OLM leaves CRDs behind when a CSV is deleted; a stale newer CRD blocks older installs."""
    delete_crd_rbac(Path(src_dir))
    check_crd_for_deletion(Path(src_dir) / "deploy" / "crd.yaml")


@pytest.fixture(scope="class")
def config(create_namespace: Callable[[str], str], test_paths: Paths) -> OlmUpgradeConfig:
    """Resolve the upgrade path from the catalog, then create the test namespace.

    OLM_BUNDLE_TYPE selects the dev catalog (community|certified, see installers/olm).
    OLM_FROM_CSV / OLM_TARGET_CHANNEL pin the path; by default it is the head of the
    newest previous channel -> the catalog's default channel.
    The target must be OLM_TARGET_VERSION, which for a dev catalog defaults to
    OPERATOR_VERSION (crVersion of deploy/cr.yaml), so a stale catalog fails fast.
    """
    if not olm_available():
        pytest.skip("OLM is not available on this cluster")

    bundle_type = os.environ.get("OLM_BUNDLE_TYPE", "community")
    package = os.environ.get("OLM_PACKAGE") or (
        "percona-server-mongodb-operator" + ("-certified" if bundle_type == "certified" else "")
    )
    source = os.environ.get("OLM_CATALOG_SOURCE") or f"{bundle_type}-dev"
    source_namespace = os.environ.get("OLM_CATALOG_NAMESPACE") or "openshift-marketplace"

    wait_catalog_ready(source, source_namespace)
    manifest = get_package_manifest(package, source, source_namespace)
    target_channel = os.environ.get("OLM_TARGET_CHANNEL") or manifest["status"]["defaultChannel"]
    target_csv = next(
        channel["currentCSV"]
        for channel in manifest["status"]["channels"]
        if channel["name"] == target_channel
    )
    # Only dev catalogs (installers/olm, CATALOG_ENV=dev) are built from this checkout,
    # and only their default channel must hold the version under test.
    expected = os.environ.get("OLM_TARGET_VERSION") or (
        os.environ.get("OPERATOR_VERSION", "")
        if source.endswith("-dev") and not os.environ.get("OLM_TARGET_CHANNEL")
        else ""
    )
    if expected:
        assert _version(target_csv) == _version(expected), (
            f"Catalog {source} upgrades to {target_csv}, but the version under test is "
            f"{expected}. Rebuild the catalog (make deploy/<type>) or set OLM_TARGET_VERSION."
        )
    from_csv = os.environ.get("OLM_FROM_CSV") or _default_from_csv(manifest, target_channel)
    from_channel = channel_of_csv(manifest, from_csv)
    assert _version(from_csv) < _version(target_csv), (
        f"Starting CSV {from_csv} is not older than target {target_csv}"
    )
    logger.info(
        f"Upgrade path: {from_csv} ({from_channel}) -> {target_csv} ({target_channel}) "
        f"from {source_namespace}/{source}"
    )

    _delete_crds(test_paths["src_dir"])
    namespace = create_namespace(f"olm-upgrade-{uuid.uuid4().hex[:8]}")
    # Lets the failure report collect resources from this namespace.
    Path(os.environ["PYTEST_NAMESPACE_FILE"]).write_text(namespace)

    return OlmUpgradeConfig(
        namespace=namespace,
        cluster="olm-upgrade",
        package=package,
        source=source,
        source_namespace=source_namespace,
        from_channel=from_channel,
        from_csv=from_csv,
        target_channel=target_channel,
        target_csv=target_csv,
    )


@pytest.fixture(scope="class", autouse=True)
def cleanup(config: OlmUpgradeConfig, test_paths: Paths) -> Generator[None]:
    yield

    Path(os.environ["PYTEST_NAMESPACE_FILE"]).unlink(missing_ok=True)
    if env_bool("SKIP_DELETE"):
        logger.info("SKIP_DELETE is set. Skipping test environment cleanup")
        return

    # Operator first, so OLM cannot reinstall anything; CRD deletion then strips
    # the psmdb finalizers the operator is no longer there to handle.
    delete_olm_install(config.namespace, config.package)
    _delete_crds(test_paths["src_dir"])
    kubectl_bin(
        "delete", "namespace", config.namespace, "--ignore-not-found", "--wait=false", check=False
    )


@pytest.fixture(scope="class")
def state() -> UpgradeState:
    return UpgradeState()


def _cluster_uri(
    config: OlmUpgradeConfig, user: str, password: str, pod: int | None = None
) -> str:
    host = f"{config.cluster}-rs0" + (f"-{pod}.{config.cluster}-rs0" if pod is not None else "")
    return f"{user}:{password}@{host}.{config.namespace}"


def _db_pods(config: OlmUpgradeConfig) -> dict[str, tuple[str, int]]:
    """pod name -> (uid, total container restarts) for every pod of the cluster."""
    pods = get_json(
        "pods", "-n", config.namespace, "-l", f"app.kubernetes.io/instance={config.cluster}"
    )
    return {
        pod["metadata"]["name"]: (
            pod["metadata"]["uid"],
            sum(c.get("restartCount", 0) for c in pod["status"].get("containerStatuses", [])),
        )
        for pod in pods["items"]
    }


def _sts_revisions(config: OlmUpgradeConfig) -> dict[str, tuple[str, str]]:
    """StatefulSet name -> (currentRevision, updateRevision); a template change moves them."""
    sts = get_json(
        "statefulset", "-n", config.namespace, "-l", f"app.kubernetes.io/instance={config.cluster}"
    )
    return {
        s["metadata"]["name"]: (
            s["status"].get("currentRevision"),
            s["status"].get("updateRevision"),
        )
        for s in sts["items"]
    }


def _warning_events(namespace: str) -> dict[str, dict[str, Any]]:
    events = get_json("events", "-n", namespace, "--field-selector=type=Warning")
    return {ev["metadata"]["uid"]: ev for ev in events["items"]}


def _event_count(event: dict[str, Any]) -> int:
    return int(event.get("count") or event.get("series", {}).get("count") or 1)


def _psmdb_state(config: OlmUpgradeConfig) -> str:
    return kubectl_bin(
        "get",
        "psmdb",
        config.cluster,
        "-n",
        config.namespace,
        "-o",
        "jsonpath={.status.state}",
        check=False,
    ).strip()


def _operator_pod(config: OlmUpgradeConfig, csv: dict[str, Any]) -> dict[str, Any]:
    """The single pod of the CSV's operator Deployment, found by its own selector."""
    deployment = csv["spec"]["install"]["spec"]["deployments"][0]
    labels = deployment["spec"]["selector"]["matchLabels"]
    selector = ",".join(f"{key}={value}" for key, value in labels.items())
    pods = wait_until(
        f"a single {deployment['name']} pod",
        lambda: get_json("pods", "-n", config.namespace, "-l", selector)["items"],
        lambda items: len(items) == 1,
        timeout=300,
    )
    return dict(pods[0])


def _assert_operator_clean(config: OlmUpgradeConfig, state: UpgradeState) -> None:
    """The target operator never restarted; its log and the namespace events show no errors."""
    target = get_csv(config.namespace, config.target_csv)
    deployment = target["spec"]["install"]["spec"]["deployments"][0]["name"]
    pod = _operator_pod(config, target)
    name = pod["metadata"]["name"]

    restarts = sum(c.get("restartCount", 0) for c in pod["status"]["containerStatuses"])
    if restarts:
        previous = kubectl_bin("logs", name, "-n", config.namespace, "--previous", check=False)
        pytest.fail(f"Operator restarted {restarts} times. Previous logs:\n{previous}")

    errors = [
        line
        for line in kubectl_bin("logs", name, "-n", config.namespace).splitlines()
        if OPERATOR_ERROR.search(line)
        and not any(allowed.search(line) for allowed in ALLOWED_OPERATOR_ERRORS)
    ]
    assert not errors, "Operator logged errors after upgrade:\n" + "\n".join(errors)

    def allowed(ev: dict[str, Any]) -> bool:
        obj = ev["involvedObject"]
        if obj["kind"] == "ClusterServiceVersion":
            return ev.get("reason") in ALLOWED_CSV_WARNINGS
        if obj["kind"] == "InstallPlan" and ev.get("reason") == "AppliedWithWarnings":
            warnings = ev.get("message", "").split("):", 1)[-1].split(", psmdb.")
            return all(DEPRECATION_WARNING.search(w) for w in warnings)
        # Readiness probe noise of the operator pods being replaced.
        return (
            obj["kind"] == "Pod"
            and obj["name"].startswith(f"{deployment}-")
            and ev.get("reason") == "Unhealthy"
        )

    new_warnings = [
        f"{ev['involvedObject']['kind']}/{ev['involvedObject']['name']}: "
        f"{ev.get('reason')}: {ev.get('message')}"
        for uid, ev in _warning_events(config.namespace).items()
        if _event_count(ev) > state.events.get(uid, 0) and not allowed(ev)
    ]
    assert not new_warnings, "Warning events during upgrade:\n" + "\n".join(new_warnings)


class TestOlmUpgrade:
    """Install the previous release from the catalog, upgrade it through OLM, check the DB."""

    @pytest.mark.dependency()
    def test_install_previous_version(self, config: OlmUpgradeConfig) -> None:
        """Install the starting CSV from its channel"""
        create_operator_group(config.namespace)
        create_subscription(
            config.namespace,
            config.package,
            config.source,
            config.source_namespace,
            config.from_channel,
            config.from_csv,
        )
        approve_until_installed(
            config.namespace,
            config.package,
            config.from_csv,
            allowed_csvs=lambda csvs: config.from_csv in csvs,
        )

        csv = get_csv(config.namespace, config.from_csv)
        image = _operator_pod(config, csv)["spec"]["containers"][0]["image"]
        assert image == csv_operator_image(csv), f"Operator runs {image}"

    @pytest.mark.dependency(depends=["TestOlmUpgrade::test_install_previous_version"])
    def test_deploy_cluster(self, config: OlmUpgradeConfig) -> None:
        """Deploy a replica set from the starting CSV's example CR"""
        cr = csv_example(get_csv(config.namespace, config.from_csv), "PerconaServerMongoDB")
        cr["metadata"] = {
            "name": config.cluster,
            "finalizers": cr["metadata"].get("finalizers", []),
        }
        spec = cr["spec"]
        spec["upgradeOptions"] = {"apply": "disabled"}
        spec.setdefault("secrets", {})["users"] = f"{config.cluster}-secrets"
        for section in ("sharding", "backup", "pmm"):
            spec.setdefault(section, {})["enabled"] = False
        rs0 = spec["replsets"][0]
        rs0["size"] = 3
        rs0["affinity"] = {"antiAffinityTopologyKey": "none"}
        for member in ("arbiter", "nonvoting", "hidden"):
            if member in rs0:
                rs0[member]["enabled"] = False
        spec["replsets"] = [rs0]

        kubectl_bin("apply", "-n", config.namespace, "-f", "-", input_data=yaml.dump(cr))
        wait_for_running(f"{config.cluster}-rs0", 3)

    @pytest.mark.dependency(depends=["TestOlmUpgrade::test_deploy_cluster"])
    def test_write_data(
        self, config: OlmUpgradeConfig, test_paths: Paths, psmdb_client: MongoManager
    ) -> None:
        """Write data and read it from every member"""
        secret = f"{config.cluster}-secrets"
        user_admin = _cluster_uri(
            config,
            get_user_data(secret, "MONGODB_USER_ADMIN_USER"),
            get_user_data(secret, "MONGODB_USER_ADMIN_PASSWORD"),
        )
        psmdb_client.run_mongosh(
            'db.createUser({user:"myApp",pwd:"myPass",roles:[{db:"myApp",role:"readWrite"}]})',
            user_admin,
        )
        retry(
            lambda: psmdb_client.run_mongosh(
                "db.getSiblingDB('myApp').test.insertOne({ x: 100500 })",
                _cluster_uri(config, "myApp", "myPass"),
            ),
            condition=lambda result: "acknowledged: true" in result,
        )
        for i in range(3):
            psmdb_client.compare_mongo_cmd(
                "find({}, { _id: 0 }).toArray()",
                _cluster_uri(config, "myApp", "myPass", i),
                test_file=f"{test_paths['test_dir']}/compare/find-1.json",
            )

    @pytest.mark.dependency(depends=["TestOlmUpgrade::test_write_data"])
    def test_upgrade_operator(self, config: OlmUpgradeConfig, state: UpgradeState) -> None:
        """Switch the Subscription to the target channel and upgrade in one hop"""
        state.pods = _db_pods(config)
        state.sts_revisions = _sts_revisions(config)
        state.events = {
            uid: _event_count(ev) for uid, ev in _warning_events(config.namespace).items()
        }
        assert len(state.pods) >= 3, f"Expected the DB pods before upgrade, got {state.pods}"

        kubectl_bin(
            "patch",
            SUBSCRIPTION,
            config.package,
            "-n",
            config.namespace,
            "--type=merge",
            "-p",
            f'{{"spec":{{"channel":"{config.target_channel}"}}}}',
        )

        def sample_state() -> None:
            if current := _psmdb_state(config):
                state.states.append(current)

        # Only the plan straight to the target. A Manual Subscription may still point at a
        # plan for a newer patch of the old channel, created before the channel switch;
        # delete it so OLM resolves the target. Bundles skip every older version
        # (olm.skipRange / spec.skips), so one hop is enough.
        def to_target(csvs: list[str]) -> bool:
            return csvs == [config.target_csv]

        delete_pending_plans(config.namespace, keep=to_target)
        plans = approve_until_installed(
            config.namespace,
            config.package,
            config.target_csv,
            allowed_csvs=to_target,
            on_poll=sample_state,
            delete_rejected=True,
        )
        assert len(plans) == 1, f"Expected one InstallPlan to {config.target_csv}, got {plans}"
        phase = installplan_phase(config.namespace, plans[0])
        assert phase == "Complete", f"InstallPlan {plans[0]} phase is {phase}"

        wait_until(
            f"only {config.target_csv} to be left",
            lambda: csv_names(config.namespace),
            lambda names: names == [config.target_csv],
            timeout=300,
        )
        wait_until(
            "Subscription to reach AtLatestKnown",
            lambda: subscription_status(config.namespace, config.package).get("state"),
            lambda sub_state: sub_state == "AtLatestKnown",
            timeout=300,
        )

    @pytest.mark.dependency(depends=["TestOlmUpgrade::test_upgrade_operator"])
    def test_operator_ready(self, config: OlmUpgradeConfig, state: UpgradeState) -> None:
        """The target operator runs without restarts and logs/events have no errors"""
        target = get_csv(config.namespace, config.target_csv)
        deployment = target["spec"]["install"]["spec"]["deployments"][0]["name"]
        kubectl_bin(
            "rollout",
            "status",
            f"deployment/{deployment}",
            "-n",
            config.namespace,
            "--timeout=300s",
        )
        image = _operator_pod(config, target)["spec"]["containers"][0]["image"]
        assert image == csv_operator_image(target), f"Operator runs {image} after upgrade"
        _assert_operator_clean(config, state)

    @pytest.mark.dependency(depends=["TestOlmUpgrade::test_operator_ready"])
    def test_cluster_not_restarted(self, config: OlmUpgradeConfig, state: UpgradeState) -> None:
        """The new operator reconciles the cluster without recreating or restarting DB pods"""
        # Prove the new operator acts on the CR: an idle state would also look unchanged.
        generation = int(
            kubectl_bin(
                "patch",
                "psmdb",
                config.cluster,
                "-n",
                config.namespace,
                "--type=merge",
                "-p",
                RECONCILE_PROBE,
                "-o",
                "jsonpath={.metadata.generation}",
            )
        )
        wait_until(
            f"the operator to reconcile generation {generation}",
            lambda: kubectl_bin(
                "get",
                "psmdb",
                config.cluster,
                "-n",
                config.namespace,
                "-o",
                "jsonpath={.status.observedGeneration}",
                check=False,
            ).strip(),
            lambda observed: observed == str(generation),
            timeout=300,
        )

        # The new operator may only change the StatefulSets on a later reconcile,
        # so keep watching for a while before comparing.
        deadline = time.monotonic() + SOAK_SECONDS
        while time.monotonic() < deadline:
            if current := _psmdb_state(config):
                state.states.append(current)
            time.sleep(10)

        logger.info(f"Cluster states seen during upgrade: {sorted(set(state.states))}")
        unexpected = sorted(set(state.states) - ALLOWED_STATES)
        assert not unexpected, f"Cluster reached {unexpected} during upgrade"
        assert _psmdb_state(config) == "ready"

        pods = _db_pods(config)
        assert pods == state.pods, f"DB pods changed. Before: {state.pods}, after: {pods}"
        revisions = _sts_revisions(config)
        assert revisions == state.sts_revisions, (
            f"StatefulSets changed. Before: {state.sts_revisions}, after: {revisions}"
        )
        # Errors or restarts of the operator while it reconciled during the soak.
        _assert_operator_clean(config, state)

    @pytest.mark.dependency(depends=["TestOlmUpgrade::test_cluster_not_restarted"])
    def test_data_after_upgrade(
        self, config: OlmUpgradeConfig, test_paths: Paths, psmdb_client: MongoManager
    ) -> None:
        """Data written before the upgrade is there and the cluster accepts writes"""
        for i in range(3):
            psmdb_client.compare_mongo_cmd(
                "find({}, { _id: 0 }).toArray()",
                _cluster_uri(config, "myApp", "myPass", i),
                test_file=f"{test_paths['test_dir']}/compare/find-1.json",
            )
        psmdb_client.run_mongosh(
            "db.getSiblingDB('myApp').test.insertOne({ x: 100501 })",
            _cluster_uri(config, "myApp", "myPass"),
        )
        for i in range(3):
            psmdb_client.compare_mongo_cmd(
                "find({}, { _id: 0 }).toArray()",
                _cluster_uri(config, "myApp", "myPass", i),
                test_file=f"{test_paths['test_dir']}/compare/find-2.json",
            )
