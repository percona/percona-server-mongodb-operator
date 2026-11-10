#!/usr/bin/env python3

import logging
import os
import time
import uuid
from collections.abc import Callable, Generator
from dataclasses import dataclass, field
from datetime import datetime
from pathlib import Path
from typing import Any

import pytest
from lib.backup import wait_backup, wait_restore
from lib.kubectl import kubectl_bin, wait_for_running
from lib.mongo import MongoManager
from lib.utils import Paths, env_bool
from olm import (
    LOCAL_S3,
    SUBSCRIPTION,
    CatalogTarget,
    apply,
    approve_until_installed,
    assert_operator_healthy,
    channel_of_csv,
    check_members,
    create_app_user,
    csv_names,
    csv_operator_image,
    delete_crds,
    delete_pending_plans,
    event_count,
    example_backup,
    example_cluster,
    example_restore,
    get_csv,
    get_json,
    insert,
    install,
    installplan_phase,
    olm_available,
    olm_test_paths,
    operator_deployment,
    operator_pod,
    resolve_target,
    subscription_status,
    teardown,
    version,
    wait_reconciled,
    wait_until,
    warning_events,
)

logger = logging.getLogger(__name__)

# Warning events OLM emits while a CSV is being replaced.
ALLOWED_CSV_WARNINGS = {"RequirementsNotMet", "InstallWaiting"}
# InstallPlan "AppliedWithWarnings" is fine when every warning is an API deprecation notice:
# the CRD still serves the old psmdb.percona.com/v1-x-0 versions.
DEPRECATION_WARNING = "PerconaServerMongoDB is deprecated and will be removed in"

# A brief "initializing" while the new operator takes over is tolerated; pods must not change.
ALLOWED_STATES = {"ready", "initializing"}
SOAK_SECONDS = 180
# A single failed read of the CR state is skipped; this many in a row fail the test.
MAX_EMPTY_READS = 6

# Taken by the starting operator, restored by the target one.
BACKUP = "backup-before-upgrade"
RESTORE = "restore-after-upgrade"


@dataclass(frozen=True)
class OlmUpgradeConfig:
    namespace: str
    cluster: str
    target: CatalogTarget
    from_channel: str
    from_csv: str


@dataclass
class UpgradeState:
    """Cluster snapshot taken before the upgrade, plus what was observed after it started."""

    pods: dict[str, tuple[str, int]] = field(default_factory=dict)
    sts_revisions: dict[str, tuple[str, str]] = field(default_factory=dict)
    events: dict[str, int] = field(default_factory=dict)
    states: list[str] = field(default_factory=list)
    empty_reads: int = 0
    operator_pod_uid: str = ""


def _default_from_csv(manifest: dict[str, Any], target_csv: str) -> str:
    """Newest CSV in the catalog older than the target."""
    older = [
        entry["name"]
        for channel in manifest["status"]["channels"]
        for entry in channel.get("entries", [])
        if version(entry["name"]) < version(target_csv)
    ]
    assert older, f"Catalog has no version older than {target_csv}"
    return str(max(older, key=version))


def _from_channel(manifest: dict[str, Any], from_csv: str, target_channel: str) -> str:
    """The target channel when it holds the starting CSV, as in published catalogs."""
    target = next(c for c in manifest["status"]["channels"] if c["name"] == target_channel)
    if any(entry["name"] == from_csv for entry in target.get("entries", [])):
        return target_channel
    return channel_of_csv(manifest, from_csv)


@pytest.fixture(scope="class")
def test_paths(request: pytest.FixtureRequest) -> Paths:
    """Overrides the shared fixture: this test lives in installers/olm/e2e-tests/."""
    return olm_test_paths(request.path)


@pytest.fixture(scope="class")
def config(create_namespace: Callable[[str], str], test_paths: Paths) -> OlmUpgradeConfig:
    """Resolve the upgrade path from the catalog, then create the test namespace.

    OLM_FROM_CSV pins the starting CSV; by default it is the newest one older than the
    target (see resolve_target for the catalog and target variables).
    """
    if not olm_available():
        pytest.skip("OLM is not available on this cluster")

    target = resolve_target()
    from_csv = os.environ.get("OLM_FROM_CSV") or _default_from_csv(
        target.manifest, target.target_csv
    )
    from_channel = _from_channel(target.manifest, from_csv, target.target_channel)
    assert version(from_csv) < version(target.target_csv), (
        f"Starting CSV {from_csv} is not older than target {target.target_csv}"
    )
    logger.info(
        f"Upgrade path: {from_csv} ({from_channel}) -> {target.target_csv} "
        f"({target.target_channel}) from {target.source_namespace}/{target.source}"
    )

    delete_crds(test_paths["src_dir"])
    namespace = create_namespace(f"olm-upgrade-{uuid.uuid4().hex[:8]}")
    # Lets the failure report collect resources from this namespace.
    Path(os.environ["PYTEST_NAMESPACE_FILE"]).write_text(namespace)

    return OlmUpgradeConfig(
        namespace=namespace,
        cluster="olm-upgrade",
        target=target,
        from_channel=from_channel,
        from_csv=from_csv,
    )


@pytest.fixture(scope="class", autouse=True)
def cleanup(config: OlmUpgradeConfig, test_paths: Paths) -> Generator[None]:
    yield

    Path(os.environ["PYTEST_NAMESPACE_FILE"]).unlink(missing_ok=True)
    if env_bool("SKIP_DELETE"):
        logger.info("SKIP_DELETE is set. Skipping test environment cleanup")
        return
    teardown(config.namespace, config.target.package, test_paths["src_dir"])


@pytest.fixture(scope="class")
def storage(config: OlmUpgradeConfig, test_paths: Paths, deploy_s3_storage: None) -> str:
    """SeaweedFS in the test namespace (after config created it) and its secret."""
    kubectl_bin("apply", "-n", config.namespace, "-f", f"{test_paths['conf_dir']}/s3-secret.yml")
    return LOCAL_S3


@pytest.fixture(scope="class")
def state() -> UpgradeState:
    return UpgradeState()


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


def _sample_state(config: OlmUpgradeConfig, state: UpgradeState) -> None:
    """Record the CR state; a state that stays unreadable is recorded as such."""
    if current := _psmdb_state(config):
        state.states.append(current)
        state.empty_reads = 0
        return
    state.empty_reads += 1
    if state.empty_reads >= MAX_EMPTY_READS:
        state.states.append("unreadable")


def _assert_operator_clean(config: OlmUpgradeConfig, state: UpgradeState) -> None:
    """The target operator never restarted; its log and the namespace events show no errors."""
    target = get_csv(config.namespace, config.target.target_csv)
    deployment = operator_deployment(target)
    # A replacement pod starts with a fresh restart count and log, hiding what happened.
    pod = assert_operator_healthy(config.namespace, target, state.operator_pod_uid)
    name = pod["metadata"]["name"]
    ready = next(c for c in pod["status"]["conditions"] if c["type"] == "Ready")
    ready_since = datetime.fromisoformat(ready["lastTransitionTime"])

    def allowed(ev: dict[str, Any]) -> bool:
        obj = ev["involvedObject"]
        if obj["kind"] == "ClusterServiceVersion":
            return ev.get("reason") in ALLOWED_CSV_WARNINGS
        if obj["kind"] == "InstallPlan" and ev.get("reason") == "AppliedWithWarnings":
            warnings = ev.get("message", "").split("):", 1)[-1].split(", psmdb.")
            return all(DEPRECATION_WARNING in w for w in warnings)
        if obj["kind"] != "Pod" or ev.get("reason") != "Unhealthy":
            return False
        # Readiness probe noise of the operator pods being replaced.
        if obj["name"] != name:
            return bool(obj["name"].startswith(f"{deployment}-"))
        # The probe has no initial delay, so the target pod can fail it before it listens.
        last = (
            ev.get("lastTimestamp")
            or ev.get("series", {}).get("lastObservedTime")
            or ev["eventTime"]
        )
        return datetime.fromisoformat(last) <= ready_since

    new_warnings = [
        f"{ev['involvedObject']['kind']}/{ev['involvedObject']['name']}: "
        f"{ev.get('reason')}: {ev.get('message')}"
        for uid, ev in warning_events(config.namespace).items()
        if event_count(ev) > state.events.get(uid, 0) and not allowed(ev)
    ]
    assert not new_warnings, "Warning events during upgrade:\n" + "\n".join(new_warnings)


class TestOlmUpgrade:
    """Install the previous release from the catalog, upgrade it through OLM, check the DB."""

    @pytest.mark.dependency()
    def test_install_previous_version(self, config: OlmUpgradeConfig) -> None:
        """Install the starting CSV from its channel"""
        csv = install(config.namespace, config.target, config.from_channel, config.from_csv)
        image = operator_pod(config.namespace, csv)["spec"]["containers"][0]["image"]
        assert image == csv_operator_image(csv), f"Operator runs {image}"

    @pytest.mark.dependency(depends=["TestOlmUpgrade::test_install_previous_version"])
    def test_deploy_cluster(self, config: OlmUpgradeConfig, storage: str) -> None:
        """Deploy a replica set with backups from the starting CSV's example CR"""
        csv = get_csv(config.namespace, config.from_csv)
        apply(config.namespace, example_cluster(csv, config.cluster, storage))
        wait_for_running(f"{config.cluster}-rs0", 3)

    @pytest.mark.dependency(depends=["TestOlmUpgrade::test_deploy_cluster"])
    def test_write_data(
        self, config: OlmUpgradeConfig, test_paths: Paths, psmdb_client: MongoManager
    ) -> None:
        """Write data and read it from every member"""
        create_app_user(psmdb_client, config.cluster, config.namespace)
        insert(psmdb_client, config.cluster, config.namespace, 100500)
        check_members(
            psmdb_client,
            config.cluster,
            config.namespace,
            f"{test_paths['test_dir']}/compare/find-1.json",
        )

    @pytest.mark.dependency(depends=["TestOlmUpgrade::test_write_data"])
    def test_backup_before_upgrade(self, config: OlmUpgradeConfig, storage: str) -> None:
        """Take a logical backup with the starting operator"""
        csv = get_csv(config.namespace, config.from_csv)
        apply(config.namespace, example_backup(csv, BACKUP, config.cluster, storage, "logical"))
        wait_backup(BACKUP)

    @pytest.mark.dependency(depends=["TestOlmUpgrade::test_backup_before_upgrade"])
    def test_upgrade_operator(self, config: OlmUpgradeConfig, state: UpgradeState) -> None:
        """Upgrade to the target CSV in one hop"""
        target = config.target
        state.pods = _db_pods(config)
        state.sts_revisions = _sts_revisions(config)
        state.events = {
            uid: event_count(ev) for uid, ev in warning_events(config.namespace).items()
        }
        assert len(state.pods) >= 3, f"Expected the DB pods before upgrade, got {state.pods}"

        # Published catalogs have one channel, so users upgrade without changing it. Only
        # a catalog with a channel per minor needs the Subscription moved to the target.
        if config.from_channel != target.target_channel:
            kubectl_bin(
                "patch",
                SUBSCRIPTION,
                target.package,
                "-n",
                config.namespace,
                "--type=merge",
                "-p",
                f'{{"spec":{{"channel":"{target.target_channel}"}}}}',
            )

        # Only the plan straight to the target. A Manual Subscription may still point at a
        # plan for a newer patch, created before the channel switch; delete it so OLM
        # resolves the target. Bundles skip every older version (olm.skipRange /
        # spec.skips), so one hop is enough.
        def to_target(csvs: list[str]) -> bool:
            return csvs == [target.target_csv]

        delete_pending_plans(config.namespace, keep=to_target)
        plans = approve_until_installed(
            config.namespace,
            target.package,
            target.target_csv,
            allowed_csvs=to_target,
            on_poll=lambda: _sample_state(config, state),
            delete_rejected=True,
        )
        assert len(plans) == 1, f"Expected one InstallPlan to {target.target_csv}, got {plans}"
        phase = installplan_phase(config.namespace, plans[0])
        assert phase == "Complete", f"InstallPlan {plans[0]} phase is {phase}"

        wait_until(
            f"only {target.target_csv} to be left",
            lambda: csv_names(config.namespace),
            lambda names: names == [target.target_csv],
            timeout=300,
        )
        wait_until(
            "Subscription to reach AtLatestKnown",
            lambda: subscription_status(config.namespace, target.package).get("state"),
            lambda sub_state: sub_state == "AtLatestKnown",
            timeout=300,
        )

    @pytest.mark.dependency(depends=["TestOlmUpgrade::test_upgrade_operator"])
    def test_operator_ready(self, config: OlmUpgradeConfig, state: UpgradeState) -> None:
        """The target operator runs without restarts and logs/events have no errors"""
        target = get_csv(config.namespace, config.target.target_csv)
        kubectl_bin(
            "rollout",
            "status",
            f"deployment/{operator_deployment(target)}",
            "-n",
            config.namespace,
            "--timeout=300s",
        )
        pod = operator_pod(config.namespace, target)
        image = pod["spec"]["containers"][0]["image"]
        assert image == csv_operator_image(target), f"Operator runs {image} after upgrade"
        _assert_operator_clean(config, state)
        state.operator_pod_uid = pod["metadata"]["uid"]

    @pytest.mark.dependency(depends=["TestOlmUpgrade::test_operator_ready"])
    def test_cluster_not_restarted(self, config: OlmUpgradeConfig, state: UpgradeState) -> None:
        """The new operator reconciles the cluster without recreating or restarting DB pods"""
        # Prove the new operator acts on the CR: an idle state would also look unchanged.
        wait_reconciled(config.namespace, config.cluster)

        # The new operator may only change the StatefulSets on a later reconcile,
        # so keep watching for a while before comparing.
        deadline = time.monotonic() + SOAK_SECONDS
        while time.monotonic() < deadline:
            _sample_state(config, state)
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
        compare = f"{test_paths['test_dir']}/compare"
        check_members(psmdb_client, config.cluster, config.namespace, f"{compare}/find-1.json")
        insert(psmdb_client, config.cluster, config.namespace, 100501)
        check_members(psmdb_client, config.cluster, config.namespace, f"{compare}/find-2.json")

    @pytest.mark.dependency(depends=["TestOlmUpgrade::test_data_after_upgrade"])
    def test_restore_after_upgrade(
        self,
        config: OlmUpgradeConfig,
        state: UpgradeState,
        test_paths: Paths,
        psmdb_client: MongoManager,
    ) -> None:
        """The target operator restores the backup taken before the upgrade"""
        compare = f"{test_paths['test_dir']}/compare"
        csv = get_csv(config.namespace, config.target.target_csv)
        apply(config.namespace, example_restore(csv, RESTORE, config.cluster, BACKUP))
        wait_restore(RESTORE, config.cluster, timeout=900)
        # Only the pre-upgrade document is back: the restore replaced the data.
        check_members(psmdb_client, config.cluster, config.namespace, f"{compare}/find-1.json")
        insert(psmdb_client, config.cluster, config.namespace, 100501)
        check_members(psmdb_client, config.cluster, config.namespace, f"{compare}/find-2.json")
        # The restore was the operator's last job; it must have survived it cleanly too.
        _assert_operator_clean(config, state)
