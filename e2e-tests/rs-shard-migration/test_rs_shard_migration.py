#!/usr/bin/env python3

import json
import logging
from collections.abc import Callable
from dataclasses import dataclass
from typing import Any

import pytest
from lib.arch import add_arch_scheduling
from lib.backup import run_backup, wait_backup
from lib.config import apply_cluster, compare_kubectl
from lib.kubectl import kubectl_bin, wait_cluster_consistency, wait_for_running, wait_pod
from lib.mongo import MongoManager
from lib.utils import Paths

logger = logging.getLogger(__name__)

CLUSTER = "some-name"
REPLSET = f"{CLUSTER}-rs0"
CONFIGSVR = f"{CLUSTER}-cfg"
MONGOS = f"{CLUSTER}-mongos"
CLUSTER_SIZE = 3
HIDDEN_POD = f"{REPLSET}-hidden-0"


@dataclass(frozen=True)
class MigrationConfig:
    namespace: str


@pytest.fixture(scope="class", autouse=True)
def config(create_infra: Callable[[str], str]) -> MigrationConfig:
    """Configuration for tests"""
    return MigrationConfig(namespace=create_infra("rs-shard-migration"))


@pytest.fixture(scope="class", autouse=True)
def setup_tests(config: MigrationConfig, deploy_s3_storage: None, test_paths: Paths) -> None:
    """Setup test environment"""
    kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/secrets.yml")
    kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/s3-secret.yml")


def _sharding_spec() -> dict[str, Any]:
    """Build the sharding block, pinned to the target arch like apply_cluster would.

    kubectl_bin only injects arch scheduling into `apply` and `run`, so a patch has to do
    it itself or the new cfg/mongos pods go unscheduled on a mixed-arch cluster.
    """
    configsvr: dict[str, Any] = {
        "size": CLUSTER_SIZE,
        "volumeSpec": {"persistentVolumeClaim": {"resources": {"requests": {"storage": "3Gi"}}}},
    }
    mongos: dict[str, Any] = {"size": 1}
    add_arch_scheduling(configsvr)
    add_arch_scheduling(mongos)

    return {"enabled": True, "configsvrReplSet": configsvr, "mongos": mongos}


def _sts_field(name: str, path: str) -> str:
    return kubectl_bin(
        "get", "statefulset", name, "-o", f"jsonpath={{{path}}}", check=False
    ).strip()


def _exists(kind: str, name: str) -> bool:
    return bool(
        kubectl_bin("get", kind, name, "-o", "name", "--ignore-not-found", check=False).strip()
    )


class TestRsShardMigration:
    """Convert a replica set with a hidden member into a sharded cluster and back.
    """

    @pytest.mark.dependency()
    def test_create_replset_cluster(self, config: MigrationConfig, test_paths: Paths) -> None:
        """Create an unsharded cluster with a hidden member and write data to it"""
        apply_cluster(f"{test_paths['test_dir']}/conf/{CLUSTER}.yml")

        wait_pod(HIDDEN_POD)
        wait_for_running(REPLSET, CLUSTER_SIZE)

        compare_kubectl(test_paths["test_dir"], f"statefulset/{REPLSET}", config.namespace)

    @pytest.mark.dependency(depends=["TestRsShardMigration::test_create_replset_cluster"])
    def test_hidden_member_joined_replset(
        self, config: MigrationConfig, psmdb_client: MongoManager
    ) -> None:
        """The hidden member has to be in the replset config"""
        members = psmdb_client.run_mongosh(
            "rs.conf().members.map(m => m.host).join('\\n')",
            f"clusterAdmin:clusterAdmin123456@{REPLSET}.{config.namespace}",
        )
        assert HIDDEN_POD in members, f"Hidden member not found in replset config: {members}"

    @pytest.mark.dependency(depends=["TestRsShardMigration::test_hidden_member_joined_replset"])
    def test_write_and_read_data(
        self, config: MigrationConfig, test_paths: Paths, psmdb_client: MongoManager
    ) -> None:
        """Create user, write data and read it back from every data node"""
        psmdb_client.run_mongosh(
            'db.createUser({user:"myApp",pwd:"myPass",roles:[{db:"myApp",role:"readWrite"}]})',
            f"userAdmin:userAdmin123456@{REPLSET}.{config.namespace}",
        )
        psmdb_client.run_mongosh(
            "db.getSiblingDB('myApp').test.insertOne({ x: 100500 })",
            f"myApp:myPass@{REPLSET}.{config.namespace}",
        )

        for i in range(CLUSTER_SIZE):
            psmdb_client.compare_mongo_cmd(
                "find({}, { _id: 0 }).toArray()",
                f"myApp:myPass@{REPLSET}-{i}.{REPLSET}.{config.namespace}",
                test_file=f"{test_paths['test_dir']}/compare/find.json",
            )

    @pytest.mark.dependency(depends=["TestRsShardMigration::test_write_and_read_data"])
    def test_backup_before_migration(self, test_paths: Paths) -> None:
        """Back up the replica set before converting it"""
        wait_backup(run_backup(test_paths["test_dir"], "local-s3", "backup-local-s3-1"))

    @pytest.mark.dependency(depends=["TestRsShardMigration::test_backup_before_migration"])
    def test_migrate_to_sharded(self) -> None:
        """Enable sharding and wait for the conversion to complete"""
        kubectl_bin(
            "patch",
            f"psmdb/{CLUSTER}",
            "--type=merge",
            "-p",
            json.dumps({"spec": {"sharding": _sharding_spec()}}),
        )

        wait_for_running(REPLSET, CLUSTER_SIZE, False)
        wait_for_running(CONFIGSVR, CLUSTER_SIZE, False)
        wait_cluster_consistency(CLUSTER)

        ready = _sts_field(MONGOS, ".status.readyReplicas")
        assert ready and int(ready) >= 1, (
            f"mongos hasn't started properly (readyReplicas={ready!r})"
        )

        replicas = _sts_field(CONFIGSVR, ".status.replicas")
        ready_replicas = _sts_field(CONFIGSVR, ".status.readyReplicas")
        assert replicas == ready_replicas, (
            f"cfg pods haven't started properly ({ready_replicas}/{replicas} ready)"
        )

    @pytest.mark.dependency(depends=["TestRsShardMigration::test_migrate_to_sharded"])
    def test_hidden_member_survived_migration(
        self, config: MigrationConfig, psmdb_client: MongoManager
    ) -> None:
        """The hidden member has to come back as part of the shard"""
        wait_pod(HIDDEN_POD)

        members = psmdb_client.run_mongosh(
            "rs.conf().members.map(m => m.host).join('\\n')",
            f"clusterAdmin:clusterAdmin123456@{REPLSET}.{config.namespace}",
        )
        assert HIDDEN_POD in members, (
            f"Hidden member missing from replset config after migration: {members}"
        )

    @pytest.mark.dependency(
        depends=["TestRsShardMigration::test_hidden_member_survived_migration"]
    )
    def test_sharded_cluster_serves_data(
        self, config: MigrationConfig, test_paths: Paths, psmdb_client: MongoManager
    ) -> None:
        """Migration to shards wipes out rs users, so recreate them and read through mongos"""
        psmdb_client.run_mongos(
            'db.createUser({user:"myApp",pwd:"myPass",roles:[{db:"myApp",role:"readWrite"}]})',
            f"userAdmin:userAdmin123456@{MONGOS}.{config.namespace}",
        )

        psmdb_client.compare_mongo_cmd(
            "find({}, { _id: 0 }).toArray()",
            f"myApp:myPass@{MONGOS}.{config.namespace}",
            test_file=f"{test_paths['test_dir']}/compare/find.json",
            sharded=True,
        )

        majority_ts = psmdb_client.run_mongosh(
            "EJSON.stringify(db.hello().lastWrite.majorityOpTime.ts)",
            f"clusterAdmin:clusterAdmin123456@{REPLSET}-0.{REPLSET}.{config.namespace}",
            "mongodb",
        ).strip()
        assert majority_ts and majority_ts != "null", (
            f"Sharded cluster does not work properly, majorityOpTime is {majority_ts!r}"
        )

    @pytest.mark.dependency(depends=["TestRsShardMigration::test_sharded_cluster_serves_data"])
    def test_backup_after_migration(self, test_paths: Paths) -> None:
        """Toggling sharding drops PBM collections, so backups have to work again afterwards"""
        wait_backup(run_backup(test_paths["test_dir"], "local-s3", "backup-local-s3-2"))

    @pytest.mark.dependency(depends=["TestRsShardMigration::test_backup_after_migration"])
    def test_migrate_back_to_replset(
        self, config: MigrationConfig, test_paths: Paths, psmdb_client: MongoManager
    ) -> None:
        """Disable sharding and check the cluster returns to a plain replica set"""
        kubectl_bin(
            "patch",
            f"psmdb/{CLUSTER}",
            "--type=json",
            "-p",
            '[{"op":"remove","path":"/spec/sharding"}]',
        )

        wait_pod(HIDDEN_POD)
        wait_for_running(REPLSET, CLUSTER_SIZE)
        wait_cluster_consistency(CLUSTER)

        for i in range(CLUSTER_SIZE):
            psmdb_client.compare_mongo_cmd(
                "find({}, { _id: 0 }).toArray()",
                f"myApp:myPass@{REPLSET}-{i}.{REPLSET}.{config.namespace}",
                test_file=f"{test_paths['test_dir']}/compare/find.json",
            )

        leftovers = [
            f"{kind}/{name}"
            for kind, name in (
                ("statefulset", MONGOS),
                ("statefulset", CONFIGSVR),
                ("service", MONGOS),
                ("service", CONFIGSVR),
            )
            if _exists(kind, name)
        ]
        assert not leftovers, f"Sharding leftovers still exist: {', '.join(leftovers)}"

    @pytest.mark.dependency(depends=["TestRsShardMigration::test_migrate_back_to_replset"])
    def test_backup_after_rollback(self, test_paths: Paths) -> None:
        """Backups have to work again once the cluster is a plain replica set"""
        wait_backup(run_backup(test_paths["test_dir"], "local-s3", "backup-local-s3-3"))
