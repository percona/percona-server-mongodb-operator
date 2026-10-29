#!/usr/bin/env python3

import json
import logging
import os
import time
from collections.abc import Callable
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import pytest
import yaml
from lib.backup import wait_backup, wait_backup_agent, wait_restore
from lib.config import apply_cluster
from lib.kubectl import (
    assert_cr_state_stable,
    event_reasons,
    kubectl_bin,
    resource_exists,
    wait_cluster_consistency,
    wait_cr_state,
    wait_deployment_ready,
    wait_for_running,
)
from lib.secrets import get_secret_data
from lib.utils import Paths

logger = logging.getLogger(__name__)

SOURCE_CLUSTER = "source-cluster"
TARGET_CLUSTER = "target-cluster"
SYNC_CR = "sync"
PCSM_DEPLOYMENT = f"{SYNC_CR}-pcsm"
SOURCE_CREDS_SECRET = "clustersync-source-creds"
SHARDED_NS = "myApp.sharded"
SHARDED_DOCS = 300
INITIAL_BACKUP = "initial-backup"

USER_ADMIN = ("userAdmin", "userAdmin123456")
CLUSTER_ADMIN = ("clusterAdmin", "clusterAdmin123456")
MY_APP = ("myApp", "myPass")


@dataclass(frozen=True)
class ClusterSyncConfig:
    namespace: str
    source_user: str
    source_pass: str


def mongosh(cluster: str, credentials: tuple[str, str], script: str, check: bool = True) -> str:
    """Run a mongosh script on a cluster's mongos over its unix socket."""
    user, password = credentials
    uri = f"mongodb://{user}:{password}@%2Ftmp%2Fmongodb-27017.sock/admin"
    return kubectl_bin(
        "exec",
        f"{cluster}-mongos-0",
        "-c",
        "mongos",
        "--",
        "env",
        "HOME=/data/db",
        "mongosh",
        uri,
        "--quiet",
        "--eval",
        script,
        check=check,
    )


def create_user(cluster: str, user: str, password: str, roles: str) -> None:
    mongosh(
        cluster,
        USER_ADMIN,
        f"db.getSiblingDB('admin').createUser({{user:'{user}',pwd:'{password}',roles:{roles}}})",
    )


def insert_doc(value: int) -> None:
    mongosh(SOURCE_CLUSTER, MY_APP, f'db.getSiblingDB("myApp").test.insertOne({{x: {value}}})')


def target_doc_count(collection: str = "test") -> int | None:
    """Count documents in myApp on the target, None while mongos is unreachable."""
    out = mongosh(
        TARGET_CLUSTER,
        MY_APP,
        f'db.getSiblingDB("myApp").{collection}.countDocuments({{}})',
        check=False,
    ).strip()
    return int(out) if out.isdigit() else None


def wait_target_doc_count(expected: int, timeout: int = 300, collection: str = "test") -> None:
    logger.info(f"Waiting for target myApp.{collection} count={expected}")
    deadline = time.time() + timeout
    count = None
    while time.time() < deadline:
        count = target_doc_count(collection)
        if count == expected:
            logger.info(f"Target myApp.{collection} count={expected}")
            return
        time.sleep(5)

    raise TimeoutError(
        f"Timeout waiting for target myApp.{collection} count={expected}. "
        f"Last count: {count if count is not None else 'unknown'}"
    )


def assert_target_doc_count_stable(
    expected: int, duration: int = 30, collection: str = "test"
) -> None:
    """Assert the target count does not move, i.e. replication really is stopped."""
    logger.info(f"Asserting target myApp.{collection} stays at {expected} for {duration}s")
    deadline = time.time() + duration
    while time.time() < deadline:
        count = target_doc_count(collection)
        assert count == expected, (
            f"target myApp.{collection} count is {count}, expected it to stay at {expected}"
        )
        time.sleep(5)


def pcsm_pods() -> list[str]:
    out = kubectl_bin(
        "get",
        "pods",
        "-l",
        f"app.kubernetes.io/instance={SYNC_CR},app.kubernetes.io/component=pcsm",
        "-o",
        "jsonpath={range .items[*]}{.metadata.name}{'\\n'}{end}",
        check=False,
    )
    return out.split()


def pcsm_state() -> str | None:
    """State reported by the active PCSM instance, None when no instance is active."""
    for pod in pcsm_pods():
        out = kubectl_bin("exec", pod, "-c", "pcsm", "--", "pcsm", "status", check=False)
        try:
            status = json.loads(out)
        except ValueError:
            continue
        if status.get("error") == "not_active":
            continue
        if state := status.get("state"):
            return str(state)
    return None


def wait_pcsm_state(expected: str, timeout: int = 300) -> None:
    logger.info(f"Waiting for pcsm state={expected}")
    deadline = time.time() + timeout
    state = None
    while time.time() < deadline:
        state = pcsm_state()
        if state == expected:
            logger.info(f"pcsm reached state={expected}")
            return
        time.sleep(5)

    kubectl_bin("logs", f"deploy/{PCSM_DEPLOYMENT}", "-c", "pcsm", "--tail=200", check=False)
    raise TimeoutError(
        f"Timeout waiting for pcsm state={expected}. Last state: {state or 'unknown'}"
    )


def cr_status() -> dict[str, Any]:
    """Parsed .status of the ClusterSync CR, empty while it has none."""
    out = kubectl_bin("get", "psmdb-clustersync", SYNC_CR, "-o", "jsonpath={.status}", check=False)
    try:
        return dict(json.loads(out))
    except ValueError:
        return {}


def active_instance() -> str:
    return str(cr_status().get("activeInstance", ""))


def wait_cr_replicas(expected: int, timeout: int = 300) -> None:
    """Wait for the CR to report the expected instance count as ready."""
    logger.info(f"Waiting for {SYNC_CR} status.readyReplicas={expected}")
    deadline = time.time() + timeout
    status: dict[str, Any] = {}
    while time.time() < deadline:
        status = cr_status()
        if status.get("size") == expected and status.get("readyReplicas") == expected:
            logger.info(f"{SYNC_CR} reports {expected} ready instances")
            return
        time.sleep(5)

    raise TimeoutError(
        f"Timeout waiting for {SYNC_CR} readyReplicas={expected}. "
        f"Last status: size={status.get('size')}, readyReplicas={status.get('readyReplicas')}"
    )


def assert_cr_condition(cond_type: str, cond_status: str, timeout: int = 60) -> None:
    """Assert the CR reports a condition, allowing for reconcile lag."""
    deadline = time.time() + timeout
    conditions: list[dict[str, Any]] = []
    while time.time() < deadline:
        conditions = cr_status().get("conditions", [])
        if any(c.get("type") == cond_type and c.get("status") == cond_status for c in conditions):
            logger.info(f"{SYNC_CR} condition {cond_type}={cond_status}")
            return
        time.sleep(5)

    reported = {c.get("type"): c.get("status") for c in conditions}
    raise AssertionError(
        f"Expected {SYNC_CR} condition {cond_type}={cond_status}, got: {reported or 'none'}"
    )


def wait_active_instance(previous: str = "", timeout: int = 300) -> str:
    """Wait for an active instance, different from `previous` when one is given."""
    logger.info(
        f"Waiting for active instance to move away from {previous}"
        if previous
        else "Waiting for an active instance"
    )
    deadline = time.time() + timeout
    while time.time() < deadline:
        active = active_instance()
        if active and active != previous:
            logger.info(f"Active instance: {active}")
            return active
        time.sleep(5)

    raise TimeoutError(f"Timeout waiting for active instance, still {active_instance() or 'none'}")


def patch_sync(patch: str) -> None:
    kubectl_bin("patch", "psmdb-clustersync", SYNC_CR, "--type=merge", "-p", patch)


def assert_event_present(name: str, reason: str, timeout: int = 60) -> None:
    """Assert an event with the reason was recorded, allowing for reporting lag."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        if reason in event_reasons(name):
            logger.info(f"Event reason={reason} present on {name}")
            return
        time.sleep(5)

    raise AssertionError(
        f"Expected event reason={reason} on {name}, got: {event_reasons(name) or 'no events'}"
    )


def apply_backup(test_paths: Paths, name: str) -> None:
    backup = yaml.safe_load(Path(f"{test_paths['test_dir']}/conf/backup-local-s3.yml").read_text())
    backup["metadata"]["name"] = name
    kubectl_bin("apply", "-f", "-", input_data=yaml.dump(backup))


def apply_restore(test_paths: Paths, name: str, backup_name: str) -> None:
    restore = yaml.safe_load(Path(f"{test_paths['test_dir']}/conf/restore.yml").read_text())
    restore["metadata"]["name"] = name
    restore["spec"]["backupName"] = backup_name
    kubectl_bin("apply", "-f", "-", input_data=yaml.dump(restore))


@pytest.fixture(scope="class", autouse=True)
def config(
    create_infra: Callable[[str], str], test_paths: Paths, request: pytest.FixtureRequest
) -> ClusterSyncConfig:
    """Create the namespace, S3 storage, secrets and both clusters."""
    namespace = create_infra("clustersync")
    # Requested lazily: SeaweedFS must land in the namespace create_infra just made.
    request.getfixturevalue("deploy_s3_storage")

    kubectl_bin(
        "apply",
        "-f",
        f"{test_paths['test_dir']}/conf/secrets.yml",
        "-f",
        f"{test_paths['conf_dir']}/s3-secret.yml",
    )

    apply_cluster(f"{test_paths['test_dir']}/conf/source-cluster.yml")
    apply_cluster(f"{test_paths['test_dir']}/conf/target-cluster.yml")

    wait_for_running(f"{SOURCE_CLUSTER}-rs0", 1)
    wait_for_running(f"{SOURCE_CLUSTER}-cfg", 1, False)
    wait_for_running(f"{SOURCE_CLUSTER}-mongos", 1, False)
    wait_cluster_consistency(SOURCE_CLUSTER)

    # The target has two shards where the source has one.
    wait_for_running(f"{TARGET_CLUSTER}-rs0", 1)
    wait_for_running(f"{TARGET_CLUSTER}-rs1", 1, False)
    wait_for_running(f"{TARGET_CLUSTER}-cfg", 1, False)
    wait_for_running(f"{TARGET_CLUSTER}-mongos", 1, False)
    wait_cluster_consistency(TARGET_CLUSTER)

    return ClusterSyncConfig(
        namespace=namespace,
        source_user=get_secret_data(SOURCE_CREDS_SECRET, "username"),
        source_pass=get_secret_data(SOURCE_CREDS_SECRET, "password"),
    )


class TestClusterSync:
    """Test syncing a sharded PSMDB cluster into another one with PCSM"""

    @pytest.mark.dependency()
    def test_create_users(self, config: ClusterSyncConfig) -> None:
        """Create the PCSM source user and the workload users on both clusters"""
        create_user(
            SOURCE_CLUSTER,
            config.source_user,
            config.source_pass,
            "[{role:'backup',db:'admin'},{role:'clusterMonitor',db:'admin'},"
            "{role:'readAnyDatabase',db:'admin'}]",
        )
        auth_info = mongosh(
            SOURCE_CLUSTER,
            (config.source_user, config.source_pass),
            "db.runCommand({connectionStatus: 1}).authInfo",
        )
        assert config.source_user in auth_info, (
            f"user {config.source_user} did not authenticate: {auth_info}"
        )

        create_user(SOURCE_CLUSTER, *MY_APP, "[{db:'myApp',role:'readWrite'}]")
        create_user(TARGET_CLUSTER, *MY_APP, "[{db:'myApp',role:'readWrite'}]")

    @pytest.mark.dependency(depends=["TestClusterSync::test_create_users"])
    def test_write_source_data(self) -> None:
        """Write the unsharded document and fill a hash-sharded collection"""
        insert_doc(100500)

        mongosh(
            SOURCE_CLUSTER,
            CLUSTER_ADMIN,
            f'sh.enableSharding("myApp"); sh.shardCollection("{SHARDED_NS}", {{k: "hashed"}})',
        )
        mongosh(
            SOURCE_CLUSTER,
            MY_APP,
            f"const docs = []; for (let i = 0; i < {SHARDED_DOCS}; i++) "
            '{ docs.push({k: i, payload: "payload-" + i}) }; '
            'db.getSiblingDB("myApp").sharded.insertMany(docs)',
        )

    @pytest.mark.dependency(depends=["TestClusterSync::test_write_source_data"])
    def test_initial_backup(self, test_paths: Paths) -> None:
        """Back up the target before clustersync starts, for the restore lease test"""
        for shard in ("rs0", "rs1", "cfg"):
            wait_backup_agent(f"{TARGET_CLUSTER}-{shard}-0")

        apply_backup(test_paths, INITIAL_BACKUP)
        wait_backup(INITIAL_BACKUP)

    @pytest.mark.dependency(depends=["TestClusterSync::test_initial_backup"])
    def test_start_clustersync(self, config: ClusterSyncConfig, test_paths: Paths) -> None:
        """Apply the ClusterSync CR with mode=running and wait for the initial sync"""
        manifest = Path(f"{test_paths['test_dir']}/conf/clustersync.yml").read_text()
        manifest = manifest.replace(
            "CLUSTERSYNC_IMAGE", os.environ.get("IMAGE_CLUSTERSYNC", "")
        ).replace("NAME_SPACE", config.namespace)
        kubectl_bin("apply", "-f", "-", input_data=manifest)

        kubectl_bin(
            "wait", "--for=condition=Available", "--timeout=5m", f"deployment/{PCSM_DEPLOYMENT}"
        )
        wait_pcsm_state("running", 600)

    @pytest.mark.dependency(depends=["TestClusterSync::test_start_clustersync"])
    def test_initial_data_replicated(self) -> None:
        """The document written before the sync started reaches the target"""
        wait_target_doc_count(1, 300)

    @pytest.mark.dependency(depends=["TestClusterSync::test_initial_data_replicated"])
    def test_sharded_collection_replicated(self) -> None:
        """The sharded collection keeps its shard key and spreads over both target shards"""
        wait_target_doc_count(SHARDED_DOCS, 600, "sharded")

        key = mongosh(
            TARGET_CLUSTER,
            CLUSTER_ADMIN,
            f'const c = db.getSiblingDB("config").collections.findOne('
            f'{{_id: "{SHARDED_NS}", dropped: {{$ne: true}}}});'
            'print(c ? JSON.stringify(c.key) : "")',
            check=False,
        ).strip()
        assert key == '{"k":"hashed"}', (
            f"target {SHARDED_NS} shard key is {key or '<unsharded>'}, expected hashed on k"
        )

        logger.info(f"Waiting for {SHARDED_NS} chunks on both shards of {TARGET_CLUSTER}")
        deadline = time.time() + 300
        shards: list[str] = []
        while time.time() < deadline:
            shards = self._target_chunk_shards()
            if len(shards) >= 2:
                logger.info(f"{SHARDED_NS} chunks live on {shards}")
                return
            time.sleep(5)

        raise TimeoutError(f"{SHARDED_NS} chunks live on {shards or '<none>'}, expected 2 shards")

    @staticmethod
    def _target_chunk_shards() -> list[str]:
        out = mongosh(
            TARGET_CLUSTER,
            CLUSTER_ADMIN,
            'const config = db.getSiblingDB("config");'
            f'const c = config.collections.findOne({{_id: "{SHARDED_NS}", dropped: {{$ne: true}}}});'
            'if (!c) { print("[]"); quit(); }'
            f'const q = c.uuid ? {{uuid: c.uuid}} : {{ns: "{SHARDED_NS}"}};'
            'print(JSON.stringify(config.chunks.distinct("shard", q).sort()))',
            check=False,
        ).strip()
        try:
            return list(json.loads(out))
        except ValueError:
            return []

    @pytest.mark.dependency(depends=["TestClusterSync::test_sharded_collection_replicated"])
    def test_live_replication(self) -> None:
        """Writes made while the sync runs show up on the target"""
        insert_doc(100501)
        wait_target_doc_count(2, 180)

        insert_doc(100502)
        wait_target_doc_count(3, 180)

    @pytest.mark.dependency(depends=["TestClusterSync::test_live_replication"])
    def test_pause_stops_replication(self) -> None:
        """running -> paused: writes to the source do not reach the target"""
        patch_sync('{"spec":{"mode":"paused"}}')
        wait_pcsm_state("paused", 180)
        wait_cr_state("psmdb-clustersync", SYNC_CR, "paused", 120)
        assert_cr_condition("Running", "False")

        insert_doc(100503)
        assert_target_doc_count_stable(3)

    @pytest.mark.dependency(depends=["TestClusterSync::test_pause_stops_replication"])
    def test_resume_replicates_backlog(self) -> None:
        """paused -> running: the write made while paused is replicated too"""
        patch_sync('{"spec":{"mode":"running"}}')
        wait_pcsm_state("running", 300)
        wait_cr_state("psmdb-clustersync", SYNC_CR, "running", 120)
        assert_cr_condition("Running", "True")

        insert_doc(100504)
        wait_target_doc_count(5, 180)

    @pytest.mark.dependency(depends=["TestClusterSync::test_resume_replicates_backlog"])
    def test_scale_up_keeps_active_instance(self) -> None:
        """HA: scaling to 3 instances must not hand over the leader"""
        active_before = active_instance()

        patch_sync('{"spec":{"size":3}}')
        wait_deployment_ready(PCSM_DEPLOYMENT, 3)
        assert resource_exists("pdb", PCSM_DEPLOYMENT), (
            f"no pod disruption budget created for {PCSM_DEPLOYMENT}"
        )

        # The pod template must not vary with spec.size, or the Deployment rolls the
        # whole group and the active instance hands over on every scale. The template
        # itself is pinned by TestPodTemplateIsInvariantAcrossSize; what only a live
        # cluster can show is that PCSM kept its leader through it.
        assert not active_before or active_instance() == active_before, (
            f"scaling up moved the active instance from {active_before} to {active_instance()}"
        )

        wait_active_instance()
        wait_cr_replicas(3)
        wait_cr_state("psmdb-clustersync", SYNC_CR, "running", 120)

    @pytest.mark.dependency(depends=["TestClusterSync::test_scale_up_keeps_active_instance"])
    def test_failover_on_active_instance_loss(self) -> None:
        """HA: deleting the active instance hands over to a standby"""
        previous_active = wait_active_instance()
        kubectl_bin("delete", "pod", previous_active, "--wait=false")

        wait_active_instance(previous_active)
        wait_deployment_ready(PCSM_DEPLOYMENT, 3)
        wait_pcsm_state("running", 300)
        wait_cr_state("psmdb-clustersync", SYNC_CR, "running", 120)
        assert_event_present(SYNC_CR, "PCSMFailover")

        insert_doc(100506)
        wait_target_doc_count(6, 300)

    @pytest.mark.dependency(depends=["TestClusterSync::test_failover_on_active_instance_loss"])
    def test_scale_down_removes_pdb(self) -> None:
        """HA: scaling back to one instance removes the disruption budget"""
        patch_sync('{"spec":{"size":1}}')
        wait_deployment_ready(PCSM_DEPLOYMENT, 1)

        deadline = time.time() + 120
        while resource_exists("pdb", PCSM_DEPLOYMENT):
            assert time.time() < deadline, f"pdb/{PCSM_DEPLOYMENT} was not removed within 120s"
            time.sleep(5)

        wait_cr_replicas(1)
        wait_pcsm_state("running", 300)

    @pytest.mark.dependency(depends=["TestClusterSync::test_scale_down_removes_pdb"])
    def test_backup_blocked_while_syncing(self, test_paths: Paths) -> None:
        """A backup requested while the sync holds the lease stays waiting"""
        apply_backup(test_paths, "backup-during-sync")
        wait_cr_state("psmdb-backup", "backup-during-sync", "waiting", 120)
        assert_cr_state_stable("psmdb-backup", "backup-during-sync", "waiting")
        assert_event_present("backup-during-sync", "ClusterSyncActive")

        kubectl_bin(
            "delete", "psmdb-backup", "backup-during-sync", "--ignore-not-found", "--wait=true"
        )

    @pytest.mark.dependency(depends=["TestClusterSync::test_backup_blocked_while_syncing"])
    def test_finalize_stops_replication(self) -> None:
        """running -> finalized: the target stops following the source"""
        patch_sync('{"spec":{"mode":"finalized"}}')
        wait_pcsm_state("finalized", 300)
        wait_cr_state("psmdb-clustersync", SYNC_CR, "finalized", 120)
        assert_cr_condition("Finalized", "True")
        assert_cr_condition("Running", "False")

        insert_doc(100505)
        assert_target_doc_count_stable(6)

    @pytest.mark.dependency(depends=["TestClusterSync::test_finalize_stops_replication"])
    def test_backup_after_finalize(self, test_paths: Paths) -> None:
        """The clustersync lease is released, so a backup completes"""
        apply_backup(test_paths, "backup-after-finalize")
        wait_backup("backup-after-finalize")

    @pytest.mark.dependency(depends=["TestClusterSync::test_backup_after_finalize"])
    def test_restore_after_finalize(self, test_paths: Paths) -> None:
        """The clustersync lease is released, so a restore completes"""
        apply_restore(test_paths, "restore-after-finalize", INITIAL_BACKUP)
        wait_restore("restore-after-finalize")

        # initial-backup was taken before the sync started, so it holds an empty
        # target: both collections must be gone once the restore lands.
        wait_target_doc_count(0, 300)
        wait_target_doc_count(0, 120, "sharded")
