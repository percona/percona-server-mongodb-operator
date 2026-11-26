#!/usr/bin/env python3

import logging
from collections.abc import Callable

import pytest
import yaml
from lib.backup import (
    check_backup_in_s3_storage,
    run_backup,
    run_restore,
    wait_backup,
    wait_physical_restore,
    wait_restore,
)
from lib.config import compare_kubectl, render_cluster_config
from lib.kubectl import kubectl_bin, wait_cluster_consistency, wait_for_running, wait_pod
from lib.mongo import MongoManager, data_members, member_uri, service_uri
from lib.secrets import apply_s3_storage_secrets
from lib.utils import Paths, retry

logger = logging.getLogger(__name__)

CLUSTER = "some-name"
CLUSTER_ARBITER_NV = "some-name-arbiter-nv"
STORAGE = "s3-compatible"

BACKUP_BASE = "backup-s3-compatible"
BACKUP_INCREMENTAL = ["backup-s3-compatible-not-base-1", "backup-s3-compatible-not-base-2"]
BACKUP_ARBITER_NV_BASE = "backup-s3-compatible-arbiter-nv"
BACKUP_ARBITER_NV_INCREMENTAL = "backup-s3-compatible-arbiter-nv-not-base"

BACKUP_TIMEOUT = 600
RESTORE_REQUESTED_TIMEOUT = 1200


@pytest.fixture(scope="class")
def namespace(create_infra: Callable[[str], str]) -> str:
    return create_infra("demand-backup-incremental-s3-compatible")


@pytest.fixture(scope="class", autouse=True)
def apply_storage_secrets(test_paths: Paths, namespace: str, deploy_s3_storage: None) -> None:
    apply_s3_storage_secrets(test_paths["conf_dir"])


def _replset(cluster: str) -> str:
    return f"{cluster}-rs0"


def _insert(client: MongoManager, namespace: str, cluster: str, value: int) -> None:
    retry(
        lambda: client.run_mongosh(
            f"db.getSiblingDB('myApp').test.insertOne({{ x: {value} }})",
            service_uri(_replset(cluster), namespace),
        ),
        condition=lambda result: "acknowledged: true" in result,
    )


def _check_data(
    client: MongoManager, namespace: str, cluster: str, test_dir: str, postfix: str = ""
) -> None:
    """Read the test collection from each data member, not only the primary."""
    for member in data_members(cluster):
        client.compare_mongo_cmd(
            "find({}, { _id: 0 }).toArray()",
            member_uri(_replset(cluster), member, namespace),
            test_file=f"{test_dir}/compare/find{postfix}.json",
            replica_set="",  # direct connection to the member
        )


def _write_data(client: MongoManager, namespace: str, cluster: str, test_dir: str) -> None:
    """Create the app user, write the first document and read it from every member."""
    client.run_mongosh(
        'db.createUser({user:"myApp",pwd:"myPass",roles:[{db:"myApp",role:"readWrite"}]})',
        service_uri(_replset(cluster), namespace, "userAdmin", "userAdmin123456"),
    )
    _insert(client, namespace, cluster, 100500)
    _check_data(client, namespace, cluster, test_dir)


def _backup(test_dir: str, cluster: str, name: str, backup_type: str) -> None:
    run_backup(test_dir, STORAGE, name, backup_type, cluster)
    wait_backup(name, timeout=BACKUP_TIMEOUT)
    check_backup_in_s3_storage(name, "rs0")


def _restore(
    client: MongoManager,
    namespace: str,
    cluster: str,
    test_dir: str,
    backup_name: str,
    compare_postfix: str,
    data_postfix: str = "",
) -> None:
    """Drop the data, restore the backup and check the cluster and the data."""
    replset = _replset(cluster)
    client.run_mongosh("db.getSiblingDB('myApp').test.drop()", service_uri(replset, namespace))
    restore_name = run_restore(test_dir, backup_name, cluster)

    wait_restore(
        restore_name,
        cluster,
        "requested",
        wait_consistency=False,
        timeout=RESTORE_REQUESTED_TIMEOUT,
    )
    compare_kubectl(test_dir, f"statefulset/{replset}", namespace, compare_postfix)

    # Incremental restores are physical: the cluster is rebuilt and PBM resynced.
    wait_physical_restore(restore_name, cluster)
    wait_cluster_consistency(cluster)
    _check_data(client, namespace, cluster, test_dir, data_postfix)


def _apply_cluster(
    test_dir: str, namespace: str, cluster: str = CLUSTER, manifest: str = f"{CLUSTER}.yml"
) -> None:
    """Apply a cluster CR named `cluster`, filling in the namespace used by backup priorities."""
    rendered = render_cluster_config(f"{test_dir}/conf/{manifest}").replace("NAMESPACE", namespace)
    cr = yaml.safe_load(rendered)
    cr["metadata"]["name"] = cluster
    kubectl_bin("apply", "-f", "-", input_data=yaml.dump(cr))


def _backup_pod(backup_name: str) -> str:
    pod = kubectl_bin(
        "get", "psmdb-backup", backup_name, "-o", "jsonpath={.status.pbmPods.rs0}"
    ).strip()
    assert pod, f"psmdb-backup/{backup_name} has no status.pbmPods.rs0"
    return pod


class TestDemandBackupIncrementalS3Compatible:
    """Incremental backup chain and restores on a three-member replica set"""

    @pytest.mark.dependency()
    def test_create_cluster(self, namespace: str, test_paths: Paths) -> None:
        kubectl_bin("apply", "-f", f"{test_paths['test_dir']}/conf/secrets.yml")
        _apply_cluster(test_paths["test_dir"], namespace)
        wait_for_running(_replset(CLUSTER), 3)
        wait_cluster_consistency(CLUSTER)

    @pytest.mark.dependency(
        depends=["TestDemandBackupIncrementalS3Compatible::test_create_cluster"]
    )
    def test_base_backup(
        self, namespace: str, test_paths: Paths, psmdb_client_tls: MongoManager
    ) -> None:
        """Write the first document, then take the base backup"""
        test_dir = test_paths["test_dir"]
        _write_data(psmdb_client_tls, namespace, CLUSTER, test_dir)
        _backup(test_dir, CLUSTER, BACKUP_BASE, "incremental-base")

    @pytest.mark.dependency(depends=["TestDemandBackupIncrementalS3Compatible::test_base_backup"])
    def test_incremental_backups(
        self, namespace: str, test_paths: Paths, psmdb_client_tls: MongoManager
    ) -> None:
        """One more document, then two incremental backups on the base backup's node"""
        test_dir = test_paths["test_dir"]
        _insert(psmdb_client_tls, namespace, CLUSTER, 100501)
        _check_data(psmdb_client_tls, namespace, CLUSTER, test_dir, "-not-base")

        for name in BACKUP_INCREMENTAL:
            _backup(test_dir, CLUSTER, name, "incremental")

        # Incremental backups must run on the same pod as their base.
        base_pod = _backup_pod(BACKUP_BASE)
        logger.info(f"Base backup {BACKUP_BASE} ran on {base_pod}")
        for name in BACKUP_INCREMENTAL:
            pod = _backup_pod(name)
            assert pod == base_pod, f"{name} ran on {pod}, its base ran on {base_pod}"
            logger.info(f"Incremental backup {name} ran on {pod}, same as its base")

    @pytest.mark.dependency(
        depends=["TestDemandBackupIncrementalS3Compatible::test_incremental_backups"]
    )
    def test_restore_incremental(
        self, namespace: str, test_paths: Paths, psmdb_client_tls: MongoManager
    ) -> None:
        """Both documents come back"""
        _restore(
            psmdb_client_tls,
            namespace,
            CLUSTER,
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
        _restore(
            psmdb_client_tls, namespace, CLUSTER, test_paths["test_dir"], BACKUP_BASE, "_restore"
        )


class TestDemandBackupIncrementalArbiterNonVoting:
    """Incremental backup and restore on a replica set with an arbiter and a non-voting member"""

    @pytest.mark.dependency()
    def test_create_cluster(self, namespace: str, test_paths: Paths) -> None:
        test_dir = test_paths["test_dir"]
        replset = _replset(CLUSTER_ARBITER_NV)
        kubectl_bin("apply", "-f", f"{test_dir}/conf/secrets.yml")
        _apply_cluster(test_dir, namespace, CLUSTER_ARBITER_NV, "some-name-arbiter-nv.yml")
        for member in data_members(CLUSTER_ARBITER_NV):
            wait_pod(f"{replset}-{member}")
        wait_pod(f"{replset}-arbiter-0")
        wait_cluster_consistency(CLUSTER_ARBITER_NV)

    @pytest.mark.dependency(
        depends=["TestDemandBackupIncrementalArbiterNonVoting::test_create_cluster"]
    )
    def test_base_and_incremental_backups(
        self, namespace: str, test_paths: Paths, psmdb_client: MongoManager
    ) -> None:
        """Base backup, one more document, then an incremental backup"""
        test_dir = test_paths["test_dir"]
        _write_data(psmdb_client, namespace, CLUSTER_ARBITER_NV, test_dir)
        _backup(test_dir, CLUSTER_ARBITER_NV, BACKUP_ARBITER_NV_BASE, "incremental-base")

        _insert(psmdb_client, namespace, CLUSTER_ARBITER_NV, 100501)
        _check_data(psmdb_client, namespace, CLUSTER_ARBITER_NV, test_dir, "-not-base")
        _backup(test_dir, CLUSTER_ARBITER_NV, BACKUP_ARBITER_NV_INCREMENTAL, "incremental")

    @pytest.mark.dependency(
        depends=["TestDemandBackupIncrementalArbiterNonVoting::test_base_and_incremental_backups"]
    )
    def test_restore_incremental(
        self, namespace: str, test_paths: Paths, psmdb_client: MongoManager
    ) -> None:
        """Both documents come back on every data member, the non-voting one included"""
        _restore(
            psmdb_client,
            namespace,
            CLUSTER_ARBITER_NV,
            test_paths["test_dir"],
            BACKUP_ARBITER_NV_INCREMENTAL,
            "_restore",
            "-not-base",
        )
