#!/usr/bin/env python3

import logging
from collections.abc import Callable

import pytest
from lib.backup import (
    check_backup_in_s3_storage,
    run_backup,
    run_restore,
    wait_backup,
    wait_physical_restore,
    wait_restore,
)
from lib.config import apply_cluster, compare_kubectl
from lib.kubectl import kubectl_bin, wait_cluster_consistency, wait_for_running
from lib.mongo import MongoManager, member_uri, service_uri
from lib.secrets import apply_s3_storage_secrets
from lib.utils import Paths, retry

logger = logging.getLogger(__name__)

CLUSTER = "some-name"
REPLSET = f"{CLUSTER}-rs0"
STORAGE = "s3-compatible"

BACKUP_BASE = "backup-s3-compatible"
BACKUP_INCREMENTAL = ["backup-s3-compatible-not-base-1", "backup-s3-compatible-not-base-2"]
BACKUP_ARBITER_NV = "backup-s3-compatible-arbiter-nv"

BACKUP_TIMEOUT = 600
# Same bound the bash test used for the restore to be requested.
RESTORE_REQUESTED_TIMEOUT = 3000


@pytest.fixture(scope="class")
def namespace(create_infra: Callable[[str], str]) -> str:
    return create_infra("demand-backup-incremental-s3-compatible")


@pytest.fixture(scope="class", autouse=True)
def apply_storage_secrets(test_paths: Paths, namespace: str, deploy_s3_storage: None) -> None:
    apply_s3_storage_secrets(test_paths["conf_dir"])


def _insert(client: MongoManager, namespace: str, value: int) -> None:
    retry(
        lambda: client.run_mongosh(
            f"db.getSiblingDB('myApp').test.insertOne({{ x: {value} }})",
            service_uri(REPLSET, namespace),
        ),
        condition=lambda result: "acknowledged: true" in result,
    )


def _check_data(client: MongoManager, namespace: str, test_dir: str, postfix: str = "") -> None:
    """Read the test collection from each data member, not only the primary."""
    for i in range(3):
        client.compare_mongo_cmd(
            "find({}, { _id: 0 }).toArray()",
            member_uri(REPLSET, i, namespace),
            test_file=f"{test_dir}/compare/find{postfix}.json",
            replica_set="",  # direct connection to the member
        )


def _backup(test_dir: str, name: str, backup_type: str) -> None:
    run_backup(test_dir, STORAGE, name, backup_type)
    wait_backup(name, timeout=BACKUP_TIMEOUT)
    check_backup_in_s3_storage(name, "rs0")


def _restore(
    client: MongoManager,
    namespace: str,
    test_dir: str,
    backup_name: str,
    compare_postfix: str,
    data_postfix: str = "",
) -> None:
    """Drop the data, restore the backup and check the cluster and the data."""
    client.run_mongosh("db.getSiblingDB('myApp').test.drop()", service_uri(REPLSET, namespace))
    restore_name = run_restore(test_dir, backup_name, CLUSTER)

    wait_restore(
        restore_name,
        CLUSTER,
        "requested",
        wait_consistency=False,
        timeout=RESTORE_REQUESTED_TIMEOUT,
    )
    compare_kubectl(test_dir, f"statefulset/{REPLSET}", namespace, compare_postfix)

    # Incremental restores are physical: the cluster is rebuilt and PBM resynced.
    wait_physical_restore(restore_name, CLUSTER)
    wait_cluster_consistency(CLUSTER)
    _check_data(client, namespace, test_dir, data_postfix)


def _backup_pod(backup_name: str) -> str:
    pod = kubectl_bin(
        "get", "psmdb-backup", backup_name, "-o", "jsonpath={.status.pbmPods.rs0}"
    ).strip()
    assert pod, f"psmdb-backup/{backup_name} has no status.pbmPods.rs0"
    return pod


class TestDemandBackupIncrementalS3Compatible:
    """Incremental backups and restores to S3-compatible storage"""

    @pytest.mark.dependency()
    def test_create_cluster(self, test_paths: Paths) -> None:
        kubectl_bin("apply", "-f", f"{test_paths['test_dir']}/conf/secrets.yml")
        apply_cluster(f"{test_paths['test_dir']}/conf/{CLUSTER}.yml")
        wait_for_running(REPLSET, 3)
        wait_cluster_consistency(CLUSTER)

    @pytest.mark.dependency(
        depends=["TestDemandBackupIncrementalS3Compatible::test_create_cluster"]
    )
    def test_write_data(
        self, namespace: str, test_paths: Paths, psmdb_client_tls: MongoManager
    ) -> None:
        psmdb_client_tls.run_mongosh(
            'db.createUser({user:"myApp",pwd:"myPass",roles:[{db:"myApp",role:"readWrite"}]})',
            service_uri(REPLSET, namespace, "userAdmin", "userAdmin123456"),
        )
        _insert(psmdb_client_tls, namespace, 100500)
        _check_data(psmdb_client_tls, namespace, test_paths["test_dir"])

    @pytest.mark.dependency(depends=["TestDemandBackupIncrementalS3Compatible::test_write_data"])
    def test_backups(
        self, namespace: str, test_paths: Paths, psmdb_client_tls: MongoManager
    ) -> None:
        """Base backup, one more document, then two incremental backups"""
        test_dir = test_paths["test_dir"]
        _backup(test_dir, BACKUP_BASE, "incremental-base")

        _insert(psmdb_client_tls, namespace, 100501)
        _check_data(psmdb_client_tls, namespace, test_dir, "-not-base")

        for name in BACKUP_INCREMENTAL:
            _backup(test_dir, name, "incremental")

        # Incremental backups must run on the same pod as their base.
        base_pod = _backup_pod(BACKUP_BASE)
        logger.info(f"Base backup {BACKUP_BASE} ran on {base_pod}")
        for name in BACKUP_INCREMENTAL:
            pod = _backup_pod(name)
            assert pod == base_pod, f"{name} ran on {pod}, its base ran on {base_pod}"
            logger.info(f"Incremental backup {name} ran on {pod}, same as its base")

    @pytest.mark.dependency(depends=["TestDemandBackupIncrementalS3Compatible::test_backups"])
    def test_restore_incremental(
        self, namespace: str, test_paths: Paths, psmdb_client_tls: MongoManager
    ) -> None:
        """Both documents come back"""
        _restore(
            psmdb_client_tls,
            namespace,
            test_paths["test_dir"],
            BACKUP_INCREMENTAL[0],
            "_restore",
            "-not-base",
        )

    @pytest.mark.dependency(
        depends=["TestDemandBackupIncrementalS3Compatible::test_restore_incremental"]
    )
    def test_restore_base(
        self, namespace: str, test_paths: Paths, psmdb_client_tls: MongoManager
    ) -> None:
        """Only the first document comes back"""
        _restore(psmdb_client_tls, namespace, test_paths["test_dir"], BACKUP_BASE, "_restore")

    @pytest.mark.dependency(depends=["TestDemandBackupIncrementalS3Compatible::test_restore_base"])
    def test_arbiter_nonvoting(
        self, namespace: str, test_paths: Paths, psmdb_client_tls: MongoManager
    ) -> None:
        """Back up and restore a replica set with an arbiter and a non-voting member"""
        test_dir = test_paths["test_dir"]
        apply_cluster(f"{test_dir}/conf/{CLUSTER}-arbiter-nv.yml")
        wait_for_running(REPLSET, 3)
        wait_cluster_consistency(CLUSTER)

        _backup(test_dir, BACKUP_ARBITER_NV, "incremental-base")
        _restore(psmdb_client_tls, namespace, test_dir, BACKUP_ARBITER_NV, "_restore-arbiter-nv")
