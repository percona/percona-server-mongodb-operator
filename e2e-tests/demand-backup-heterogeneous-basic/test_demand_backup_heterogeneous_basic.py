#!/usr/bin/env python3

import logging
import time
from collections.abc import Callable
from dataclasses import dataclass

import pytest
from lib import backup, instances
from lib.config import apply_cluster
from lib.instances import InstanceGroup
from lib.kubectl import kubectl_bin
from lib.mongo import MongoManager
from lib.utils import Paths

logger = logging.getLogger(__name__)

LOGICAL_BACKUP = "backup-minio-logical"
PHYSICAL_BACKUP = "backup-minio-physical"

# PBM-1265: a physical backup taken too soon after the agents come up can wedge.
# The pitr-physical bash test works around it the same way.
PBM_1265_SETTLE_SECONDS = 360


@dataclass(frozen=True)
class BackupConfig:
    namespace: str
    psmdb: str
    cluster: str


@pytest.fixture(scope="class", autouse=True)
def config(create_infra: Callable[[str], str]) -> BackupConfig:
    """Configuration for tests"""
    return BackupConfig(
        namespace=create_infra("demand-backup-heterogeneous-basic"),
        psmdb="heterogeneous",
        cluster="heterogeneous-rs0",
    )


@pytest.fixture(scope="class", autouse=True)
def setup_tests(test_paths: Paths, deploy_minio: None) -> None:
    """Deploy MinIO, then the secrets the cluster and PBM need"""
    kubectl_bin(
        "apply",
        "-f",
        f"{test_paths['conf_dir']}/secrets.yml",
        "-f",
        f"{test_paths['conf_dir']}/minio-secret.yml",
    )


class State:
    """Values one step produces and a later one consumes.

    Class attributes rather than fixtures: the PITR target is established in
    the middle of the run and read by both restores, and a fixture would either
    recompute it or need a scope that outlives the step that defines it.
    """

    pitr_target: str = ""


def app_uri(config: BackupConfig, host: str | None = None) -> str:
    return f"myApp:myPass@{host or config.cluster}.{config.namespace}"


def groups(config: BackupConfig) -> dict[str, InstanceGroup]:
    return {g.name: g for g in instances.read_instance_groups(config.psmdb)}


def data_bearing_pods(config: BackupConfig) -> list[str]:
    """Every pod that holds data, and therefore runs a pbm-agent."""
    return [pod for g in groups(config).values() if g.data_bearing for pod in g.pods]


def agent_pod(config: BackupConfig) -> str:
    """A pod to run pbm commands in.

    There is no <cluster>-rs0-0 in this topology, so the pod has to come from
    whichever data-bearing group exists rather than from a fixed name.
    """
    pods = data_bearing_pods(config)
    assert pods, "no data-bearing pod to run pbm in"
    return pods[0]


def count_documents(psmdb_client: MongoManager, config: BackupConfig) -> int:
    return int(
        psmdb_client.run_mongosh(
            "db.getSiblingDB('myApp').test.countDocuments({})", app_uri(config)
        ).strip()
    )


def read_back(
    psmdb_client: MongoManager, config: BackupConfig, test_paths: Paths, expected: str
) -> None:
    """Assert every data-bearing member holds `expected`, read from that member."""
    for group in groups(config).values():
        if not group.data_bearing:
            continue
        for pod in group.pods:
            psmdb_client.compare_mongo_cmd(
                "find({}, { _id: 0 }).toArray()",
                app_uri(config, f"{pod}.{config.cluster}"),
                test_file=f"{test_paths['test_dir']}/compare/{expected}",
                direct=True,
            )


class TestDemandBackupHeterogeneousBasic:
    """Logical and physical demand backups, and PITR restores of both, on a
    replica set built from instances[]"""

    @pytest.mark.dependency()
    def test_create_cluster(self, config: BackupConfig, test_paths: Paths) -> None:
        """Create the cluster and wait for a pbm-agent on every data-bearing pod"""
        apply_cluster(f"{test_paths['test_dir']}/conf/{config.cluster}.yml")
        instances.wait_for_instances_running(config.psmdb)

        declared = groups(config)
        assert sorted(declared) == ["arbiter", "hidden", "inst1", "inst2", "nonVoting"]

        # No PBM agent on arbiter
        arbiter = declared["arbiter"]
        containers = kubectl_bin(
            "get",
            "sts",
            arbiter.statefulset,
            "-o",
            "jsonpath={.spec.template.spec.containers[*].name}",
        ).split()
        assert "backup-agent" not in containers, (
            f"the arbiter runs a backup agent it cannot use: {containers}"
        )

        for pod in data_bearing_pods(config):
            backup.wait_backup_agent(pod)

    @pytest.mark.dependency(depends=["TestDemandBackupHeterogeneousBasic::test_create_cluster"])
    def test_write_initial_data(
        self, config: BackupConfig, test_paths: Paths, psmdb_client: MongoManager
    ) -> None:
        """Create the app user and write the document both backups must capture"""
        psmdb_client.run_mongosh(
            'db.createUser({user:"myApp",pwd:"myPass",roles:[{db:"myApp",role:"readWrite"}]})',
            f"userAdmin:userAdmin123456@{config.cluster}.{config.namespace}",
        )
        psmdb_client.run_mongosh(
            "db.getSiblingDB('myApp').test.insertOne({ x: 100500 })", app_uri(config)
        )
        read_back(psmdb_client, config, test_paths, "find-1.json")

    @pytest.mark.dependency(
        depends=["TestDemandBackupHeterogeneousBasic::test_write_initial_data"]
    )
    def test_logical_backup(self, config: BackupConfig) -> None:
        """Take a logical backup off whichever group PBM elected"""
        backup.create_backup(LOGICAL_BACKUP, config.psmdb)
        backup.wait_backup(LOGICAL_BACKUP)

        assert backup.backup_field(LOGICAL_BACKUP, ".status.type") in ("", "logical")
        destination = backup.backup_field(LOGICAL_BACKUP, ".status.destination")
        assert destination, "the backup reported no destination"
        logger.info(f"logical backup destination: {destination}")

    @pytest.mark.dependency(depends=["TestDemandBackupHeterogeneousBasic::test_logical_backup"])
    def test_physical_backup(self, config: BackupConfig) -> None:
        """Take a physical backup of the same cluster"""
        logger.info(f"Sleeping {PBM_1265_SETTLE_SECONDS}s before the physical backup (PBM-1265)")
        time.sleep(PBM_1265_SETTLE_SECONDS)

        backup.create_backup(PHYSICAL_BACKUP, config.psmdb, backup_type="physical")
        backup.wait_backup(PHYSICAL_BACKUP)

        assert backup.backup_field(PHYSICAL_BACKUP, ".status.type") == "physical"

    @pytest.mark.dependency(depends=["TestDemandBackupHeterogeneousBasic::test_physical_backup"])
    def test_write_more_data_and_pin_pitr_target(
        self, config: BackupConfig, test_paths: Paths, psmdb_client: MongoManager
    ) -> None:
        """Write the document only PITR can recover, then fix the restore target

        The target is taken after the write and after the oplog has been
        uploaded past it, and it is shared by both restores below. That is
        valid for each: the operator rejects a target at or before a backup's
        last write, and this one is after both backups.
        """
        psmdb_client.run_mongosh(
            "db.getSiblingDB('myApp').test.insertOne({ x: 100501 })", app_uri(config)
        )
        read_back(psmdb_client, config, test_paths, "find-2.json")

        target = backup.now_ts()
        backup.wait_for_oplog_past(agent_pod(config), target)
        State.pitr_target = backup.format_pitr_date(target)
        logger.info(f"PITR target pinned at {State.pitr_target}")

        for name in (LOGICAL_BACKUP, PHYSICAL_BACKUP):
            last_write = backup.backup_field(name, ".status.lastWriteAt")
            logger.info(f"{name} lastWriteAt={last_write}, target={State.pitr_target}")

    @pytest.mark.dependency(
        depends=["TestDemandBackupHeterogeneousBasic::test_write_more_data_and_pin_pitr_target"]
    )
    def test_restore_logical_with_pitr(
        self, config: BackupConfig, test_paths: Paths, psmdb_client: MongoManager
    ) -> None:
        """Drop everything, then bring it back from the logical backup plus oplog"""
        assert State.pitr_target, "no PITR target was pinned"

        psmdb_client.run_mongosh("db.getSiblingDB('myApp').test.deleteMany({})", app_uri(config))
        assert count_documents(psmdb_client, config) == 0

        restore = f"restore-{LOGICAL_BACKUP}"
        backup.create_restore(
            restore,
            config.psmdb,
            LOGICAL_BACKUP,
            pitr_type="date",
            pitr_date=State.pitr_target,
        )
        backup.wait_restore(restore)
        instances.wait_for_instances_running(config.psmdb)

        # Both documents: the first from the backup itself, the second only
        # from the oplog replayed up to the target.
        read_back(psmdb_client, config, test_paths, "find-2.json")

    @pytest.mark.dependency(
        depends=["TestDemandBackupHeterogeneousBasic::test_restore_logical_with_pitr"]
    )
    def test_restore_physical_with_pitr(
        self, config: BackupConfig, test_paths: Paths, psmdb_client: MongoManager
    ) -> None:
        """Drop everything again, then recover from the physical backup plus oplog

        A physical restore stops every group's workload, replaces the data
        path and brings the cluster back, so this is the step that exercises
        prepareStatefulSetsForPhysicalRestore across four data-bearing groups
        and an arbiter rather than the legacy three.
        """
        assert State.pitr_target, "no PITR target was pinned"

        psmdb_client.run_mongosh("db.getSiblingDB('myApp').test.deleteMany({})", app_uri(config))
        assert count_documents(psmdb_client, config) == 0

        restore = f"restore-{PHYSICAL_BACKUP}"
        backup.create_restore(
            restore,
            config.psmdb,
            PHYSICAL_BACKUP,
            pitr_type="date",
            pitr_date=State.pitr_target,
        )
        # Physical restores go through requested before running, and take long
        # enough that failing at the first gate is worth a separate wait.
        backup.wait_restore(restore, target_state="requested", timeout=1200)
        backup.wait_restore(restore, target_state="ready", timeout=2400)

        instances.wait_for_instances_running(config.psmdb)
        read_back(psmdb_client, config, test_paths, "find-2.json")
