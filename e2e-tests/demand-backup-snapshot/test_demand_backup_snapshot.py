#!/usr/bin/env python3

import logging
import time
from collections.abc import Callable
from dataclasses import dataclass
from pathlib import Path

import pytest
import yaml
from lib.backup import (
    deploy_volume_snapshot_class,
    get_backup_snapshots,
    get_volume_snapshot_class,
    wait_backup,
    wait_for_pbm_operations,
    wait_restore,
)
from lib.config import apply_cluster
from lib.kubectl import kubectl_bin, wait_cluster_consistency, wait_for_delete, wait_for_running
from lib.mongo import MongoManager
from lib.secrets import apply_s3_storage_secrets
from lib.utils import Paths, retry

logger = logging.getLogger(__name__)

# The config server keeps the spec name "cfg", but runs under this replSetName, so
# PBM records its snapshot under "csReplSet". Restore has to resolve the snapshot by
# the MongoDB name; looking it up by the spec name finds nothing.
CONFIGSVR_REPLSET_NAME = "csReplSet"

# Agents need a moment after startup before their resync shows up as a PBM operation.
RESYNC_START_DELAY = 60


@dataclass(frozen=True)
class DemandBackupSnapshotConfig:
    namespace: str
    cluster: str
    new_cluster: str
    backup: str
    encrypted_backup: str


@pytest.fixture(scope="class", autouse=True)
def config(create_infra: Callable[[str], str]) -> DemandBackupSnapshotConfig:
    """Configuration for tests"""
    return DemandBackupSnapshotConfig(
        namespace=create_infra("demand-backup-snapshot"),
        cluster="some-name",
        new_cluster="some-name-new",
        backup="backup-snapshot",
        encrypted_backup="backup-snapshot-encrypted",
    )


@pytest.fixture(scope="class", autouse=True)
def setup_tests(
    test_paths: Paths, config: DemandBackupSnapshotConfig, deploy_s3_storage: None
) -> None:
    """Setup test environment"""
    apply_s3_storage_secrets(test_paths["conf_dir"])
    deploy_volume_snapshot_class(test_paths["conf_dir"], config.namespace)
    kubectl_bin("apply", "-f", f"{test_paths['test_dir']}/conf/secrets.yml")


def _wait_cluster_started(cluster: str) -> None:
    """Wait for every component, then for the PBM resync to settle."""
    wait_for_running(f"{cluster}-rs0", 3)
    wait_for_running(f"{cluster}-cfg", 3, False)
    wait_for_running(f"{cluster}-mongos", 1)
    wait_cluster_consistency(cluster)

    time.sleep(RESYNC_START_DELAY)
    wait_for_pbm_operations(cluster)


def _mongos_uri(user: str, password: str, cluster: str, namespace: str) -> str:
    return f"{user}:{password}@{cluster}-mongos.{namespace}"


def _write_test_data(client: MongoManager, cluster: str, namespace: str) -> None:
    """Create the app user and insert the test document through mongos."""
    client.run_mongosh(
        'db.createUser({user:"myApp",pwd:"myPass",roles:[{db:"myApp",role:"readWrite"}]})',
        _mongos_uri("userAdmin", "userAdmin123456", cluster, namespace),
        driver="mongodb",
        replica_set="",
    )

    retry(
        lambda: client.run_mongosh(
            "db.getSiblingDB('myApp').test.insertOne({ x: 100500 })",
            _mongos_uri("myApp", "myPass", cluster, namespace),
            driver="mongodb",
            replica_set="",
        ),
        condition=lambda result: "acknowledged: true" in result,
    )


def _check_data(client: MongoManager, cluster: str, namespace: str, test_dir: str) -> None:
    """Read the test document through mongos and from each shard member."""
    expected = f"{test_dir}/compare/find.json"

    client.compare_mongo_cmd(
        "find({}, { _id: 0 }).toArray()",
        _mongos_uri("myApp", "myPass", cluster, namespace),
        test_file=expected,
        replica_set="",
    )

    # myApp lives on the config servers, so shard members are read as a system user.
    for i in range(3):
        client.compare_mongo_cmd(
            "find({}, { _id: 0 }).toArray()",
            f"databaseAdmin:databaseAdmin123456@{cluster}-rs0-{i}.{cluster}-rs0.{namespace}",
            test_file=expected,
        )


def _drop_test_collection(client: MongoManager, cluster: str, namespace: str) -> None:
    client.run_mongosh(
        "db.getSiblingDB('myApp').test.drop()",
        _mongos_uri("myApp", "myPass", cluster, namespace),
        driver="mongodb",
        replica_set="",
    )


def _run_snapshot_backup(test_dir: str, backup_name: str, cluster: str) -> None:
    manifest = yaml.safe_load(Path(f"{test_dir}/conf/backup.yml").read_text())
    manifest["metadata"]["name"] = backup_name
    manifest["spec"]["clusterName"] = cluster
    manifest["spec"]["volumeSnapshotClass"] = get_volume_snapshot_class()

    logger.info(f"Running snapshot backup {backup_name}")
    kubectl_bin("apply", "-f", "-", input_data=yaml.dump(manifest))


def _run_restore(test_dir: str, backup_name: str, cluster: str) -> str:
    manifest = yaml.safe_load(Path(f"{test_dir}/conf/restore.yml").read_text())
    restore_name = f"restore-{backup_name}"
    manifest["metadata"]["name"] = restore_name
    manifest["spec"]["clusterName"] = cluster
    manifest["spec"]["backupName"] = backup_name

    logger.info(f"Running restore {restore_name}")
    kubectl_bin("apply", "-f", "-", input_data=yaml.dump(manifest))
    return restore_name


def _run_restore_backupsource(test_dir: str, backup_name: str, target_cluster: str) -> str:
    manifest = yaml.safe_load(Path(f"{test_dir}/conf/restore-backupsource.yml").read_text())
    restore_name = f"restore-backupsource-{backup_name}"
    manifest["metadata"]["name"] = restore_name
    manifest["spec"]["clusterName"] = target_cluster
    manifest["spec"]["backupSource"]["snapshots"] = get_backup_snapshots(backup_name)

    logger.info(f"Running cross-cluster restore {restore_name} to {target_cluster}")
    kubectl_bin("apply", "-f", "-", input_data=yaml.dump(manifest))
    return restore_name


def _check_recovery(
    client: MongoManager, restore_name: str, cluster: str, namespace: str, test_dir: str
) -> None:
    """Wait for the restore, then check the operator resynced PBM and data is back."""
    wait_restore(restore_name, cluster, "ready", True, 3000)

    resync = kubectl_bin(
        "get",
        "psmdb",
        cluster,
        "-o",
        "jsonpath={.metadata.annotations.percona\\.com/resync-pbm}",
    ).strip()
    assert resync == "true", (
        f"psmdb/{cluster} should be annotated with percona.com/resync-pbm "
        f"after a snapshot restore, got {resync or 'no annotation'}"
    )

    wait_cluster_consistency(cluster)
    wait_for_pbm_operations(cluster)

    _check_data(client, cluster, namespace, test_dir)


class TestDemandBackupSnapshot:
    """Snapshot backup and restore of a sharded cluster with a custom configsvr name"""

    @pytest.mark.dependency()
    def test_create_cluster(self, config: DemandBackupSnapshotConfig, test_paths: Paths) -> None:
        """Create the unencrypted cluster and wait for every component"""
        apply_cluster(f"{test_paths['test_dir']}/conf/{config.cluster}.yml")
        _wait_cluster_started(config.cluster)

    @pytest.mark.dependency(depends=["TestDemandBackupSnapshot::test_create_cluster"])
    def test_write_data(
        self,
        config: DemandBackupSnapshotConfig,
        test_paths: Paths,
        psmdb_client_tls: MongoManager,
    ) -> None:
        """Write test data and read it back"""
        _write_test_data(psmdb_client_tls, config.cluster, config.namespace)
        _check_data(psmdb_client_tls, config.cluster, config.namespace, test_paths["test_dir"])

    @pytest.mark.dependency(depends=["TestDemandBackupSnapshot::test_write_data"])
    def test_backup(self, config: DemandBackupSnapshotConfig, test_paths: Paths) -> None:
        """Take a snapshot backup, keyed by the MongoDB replset names"""
        _run_snapshot_backup(test_paths["test_dir"], config.backup, config.cluster)
        wait_backup(config.backup)

        replsets = {snapshot["replsetName"] for snapshot in get_backup_snapshots(config.backup)}
        assert replsets == {"rs0", CONFIGSVR_REPLSET_NAME}, (
            f"expected snapshots for rs0 and {CONFIGSVR_REPLSET_NAME}, got {sorted(replsets)}"
        )

    @pytest.mark.dependency(depends=["TestDemandBackupSnapshot::test_backup"])
    def test_restore(
        self,
        config: DemandBackupSnapshotConfig,
        test_paths: Paths,
        psmdb_client_tls: MongoManager,
    ) -> None:
        """Drop the collection and restore it from the snapshot"""
        _drop_test_collection(psmdb_client_tls, config.cluster, config.namespace)

        restore_name = _run_restore(test_paths["test_dir"], config.backup, config.cluster)
        _check_recovery(
            psmdb_client_tls,
            restore_name,
            config.cluster,
            config.namespace,
            test_paths["test_dir"],
        )

    @pytest.mark.dependency(depends=["TestDemandBackupSnapshot::test_restore"])
    def test_cross_cluster_restore(
        self,
        config: DemandBackupSnapshotConfig,
        test_paths: Paths,
        psmdb_client_tls: MongoManager,
    ) -> None:
        """Restore the snapshot into a second cluster through backupSource"""
        apply_cluster(f"{test_paths['test_dir']}/conf/{config.new_cluster}.yml")
        _wait_cluster_started(config.new_cluster)

        restore_name = _run_restore_backupsource(
            test_paths["test_dir"], config.backup, config.new_cluster
        )
        _check_recovery(
            psmdb_client_tls,
            restore_name,
            config.new_cluster,
            config.namespace,
            test_paths["test_dir"],
        )

        kubectl_bin("delete", "psmdb", config.new_cluster)
        wait_for_delete(f"psmdb/{config.new_cluster}", 600)

    @pytest.mark.dependency(depends=["TestDemandBackupSnapshot::test_cross_cluster_restore"])
    def test_create_encrypted_cluster(
        self,
        config: DemandBackupSnapshotConfig,
        test_paths: Paths,
        psmdb_client_tls: MongoManager,
    ) -> None:
        """Recreate the cluster with encryption at rest and write test data"""
        kubectl_bin("delete", "psmdb", config.cluster)
        wait_for_delete(f"psmdb/{config.cluster}", 600)

        kubectl_bin("apply", "-f", f"{test_paths['test_dir']}/conf/secrets.yml")
        apply_cluster(f"{test_paths['test_dir']}/conf/{config.cluster}-encrypted.yml")
        _wait_cluster_started(config.cluster)

        _write_test_data(psmdb_client_tls, config.cluster, config.namespace)
        _check_data(psmdb_client_tls, config.cluster, config.namespace, test_paths["test_dir"])

    @pytest.mark.dependency(depends=["TestDemandBackupSnapshot::test_create_encrypted_cluster"])
    def test_encrypted_backup_and_restore(
        self,
        config: DemandBackupSnapshotConfig,
        test_paths: Paths,
        psmdb_client_tls: MongoManager,
    ) -> None:
        """Take and restore a snapshot backup of the encrypted cluster"""
        _run_snapshot_backup(test_paths["test_dir"], config.encrypted_backup, config.cluster)
        wait_backup(config.encrypted_backup)

        _drop_test_collection(psmdb_client_tls, config.cluster, config.namespace)

        restore_name = _run_restore(
            test_paths["test_dir"], config.encrypted_backup, config.cluster
        )
        _check_recovery(
            psmdb_client_tls,
            restore_name,
            config.cluster,
            config.namespace,
            test_paths["test_dir"],
        )
