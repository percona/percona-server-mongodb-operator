#!/usr/bin/env python3

import logging
import os
import re
import uuid
from collections.abc import Callable, Generator
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta
from pathlib import Path
from typing import Any

import pytest
from lib.backup import wait_backup, wait_physical_restore, wait_restore
from lib.kubectl import kubectl_bin, wait_for_running
from lib.mongo import MongoManager
from lib.secret_scan import LogRecorder, check_logs_for_secrets
from lib.utils import Paths, env_bool
from olm import (
    LOCAL_S3,
    RS0_SIZE,
    CatalogTarget,
    ErrorWindow,
    apply,
    assert_operator_healthy,
    check_members,
    create_app_user,
    csv_operator_image,
    delete_crds,
    example_backup,
    example_cluster,
    example_restore,
    get_csv,
    insert,
    install,
    olm_available,
    olm_test_paths,
    operator_pod,
    resolve_target,
    teardown,
)

logger = logging.getLogger(__name__)

# A logical restore of a few documents takes about a minute; a hang shows up early.
LOGICAL_RESTORE_TIMEOUT = 900

# A physical restore stops every mongod while PBM replaces the data files, so a
# reconcile cannot reach the replica set and the operator logs it as a full cluster
# crash before it rebuilds the StatefulSets. Allowed only during the physical
# restore: elsewhere the line means the operator force-reconfigures a replset.
PHYSICAL_RESTORE_CRASH = re.compile(r"FULL CLUSTER CRASH.*ping mongo: server selection error")
# Seen once per restore; a few more reconciles can run while the mongods are down,
# but not more than one per member.
PHYSICAL_RESTORE_CRASH_MAX = RS0_SIZE
# The window is measured on this machine and the line time on the operator's node.
CLOCK_SKEW = timedelta(seconds=60)


@dataclass(frozen=True)
class OlmBackupRestoreConfig:
    namespace: str
    cluster: str
    target: CatalogTarget


@dataclass
class State:
    """What the steps saw, for the final operator and credential checks."""

    operator_pod_uid: str = ""
    # Start and end of the physical restore, in UTC.
    physical_restore: tuple[datetime, datetime] | None = None
    # Logs of the pods the physical restore replaced.
    restore_logs: LogRecorder | None = None


@pytest.fixture(scope="class")
def test_paths(request: pytest.FixtureRequest) -> Paths:
    """Overrides the shared fixture: this test lives in installers/olm/e2e-tests/."""
    return olm_test_paths(request.path)


@pytest.fixture(scope="class")
def config(create_namespace: Callable[[str], str], test_paths: Paths) -> OlmBackupRestoreConfig:
    """Resolve the target CSV from the catalog, then create the test namespace."""
    if not olm_available():
        pytest.skip("OLM is not available on this cluster")

    target = resolve_target()
    logger.info(
        f"Target: {target.target_csv} ({target.target_channel}) from "
        f"{target.source_namespace}/{target.source}"
    )

    delete_crds(test_paths["src_dir"])
    namespace = create_namespace(f"olm-backup-restore-{uuid.uuid4().hex[:8]}")
    # Lets the failure report collect resources from this namespace.
    Path(os.environ["PYTEST_NAMESPACE_FILE"]).write_text(namespace)

    return OlmBackupRestoreConfig(namespace=namespace, cluster="olm-backup-restore", target=target)


@pytest.fixture(scope="class", autouse=True)
def cleanup(config: OlmBackupRestoreConfig, test_paths: Paths) -> Generator[None]:
    yield

    Path(os.environ["PYTEST_NAMESPACE_FILE"]).unlink(missing_ok=True)
    if env_bool("SKIP_DELETE"):
        logger.info("SKIP_DELETE is set. Skipping test environment cleanup")
        return
    teardown(config.namespace, config.target.package, test_paths["src_dir"])


@pytest.fixture(scope="class")
def storage(config: OlmBackupRestoreConfig, test_paths: Paths, deploy_s3_storage: None) -> str:
    """SeaweedFS in the test namespace (after config created it) and its secret."""
    kubectl_bin("apply", "-n", config.namespace, "-f", f"{test_paths['conf_dir']}/s3-secret.yml")
    return LOCAL_S3


@pytest.fixture(scope="class")
def state() -> State:
    return State()


@pytest.fixture(scope="class")
def csv(config: OlmBackupRestoreConfig) -> dict[str, Any]:
    """The installed target CSV; request it only after test_install_operator."""
    return get_csv(config.namespace, config.target.target_csv)


def _backup(config: OlmBackupRestoreConfig, csv: dict[str, Any], storage: str, kind: str) -> str:
    name = f"backup-{kind}"
    apply(config.namespace, example_backup(csv, name, config.cluster, storage, kind))
    wait_backup(name)
    return name


def _restore(config: OlmBackupRestoreConfig, csv: dict[str, Any], kind: str) -> None:
    name = f"restore-{kind}"
    apply(config.namespace, example_restore(csv, name, config.cluster, f"backup-{kind}"))
    if kind == "physical":
        wait_physical_restore(name, config.cluster)
    else:
        wait_restore(name, config.cluster, timeout=LOGICAL_RESTORE_TIMEOUT)


class TestOlmBackupRestore:
    """Install the target release from the catalog, back up and restore through it."""

    @pytest.mark.dependency()
    def test_install_operator(self, config: OlmBackupRestoreConfig, state: State) -> None:
        """Install the target CSV from its channel"""
        csv = install(
            config.namespace, config.target, config.target.target_channel, config.target.target_csv
        )
        pod = operator_pod(config.namespace, csv)
        image = pod["spec"]["containers"][0]["image"]
        assert image == csv_operator_image(csv), f"Operator runs {image}"
        # A replacement pod would start with a fresh restart count and log.
        state.operator_pod_uid = pod["metadata"]["uid"]

    @pytest.mark.dependency(depends=["TestOlmBackupRestore::test_install_operator"])
    def test_deploy_cluster(
        self, config: OlmBackupRestoreConfig, csv: dict[str, Any], storage: str
    ) -> None:
        """Deploy a replica set with backups from the CSV's example CR"""
        apply(config.namespace, example_cluster(csv, config.cluster, storage))
        wait_for_running(f"{config.cluster}-rs0", RS0_SIZE)

    @pytest.mark.dependency(depends=["TestOlmBackupRestore::test_deploy_cluster"])
    def test_write_data(
        self, config: OlmBackupRestoreConfig, test_paths: Paths, psmdb_client: MongoManager
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

    @pytest.mark.dependency(depends=["TestOlmBackupRestore::test_write_data"])
    def test_logical_backup(
        self, config: OlmBackupRestoreConfig, csv: dict[str, Any], storage: str
    ) -> None:
        """A logical backup reaches ready"""
        _backup(config, csv, storage, "logical")

    @pytest.mark.dependency(depends=["TestOlmBackupRestore::test_logical_backup"])
    def test_logical_restore(
        self,
        config: OlmBackupRestoreConfig,
        csv: dict[str, Any],
        test_paths: Paths,
        psmdb_client: MongoManager,
    ) -> None:
        """A logical restore brings back the data as of the backup"""
        compare = f"{test_paths['test_dir']}/compare"
        # Written after the backup, so the restore has something to undo.
        insert(psmdb_client, config.cluster, config.namespace, 100501)
        check_members(psmdb_client, config.cluster, config.namespace, f"{compare}/find-2.json")
        _restore(config, csv, "logical")
        check_members(psmdb_client, config.cluster, config.namespace, f"{compare}/find-1.json")

    @pytest.mark.dependency(depends=["TestOlmBackupRestore::test_logical_restore"])
    def test_physical_backup(
        self, config: OlmBackupRestoreConfig, csv: dict[str, Any], storage: str
    ) -> None:
        """A physical backup reaches ready"""
        _backup(config, csv, storage, "physical")

    @pytest.mark.dependency(depends=["TestOlmBackupRestore::test_physical_backup"])
    def test_no_credentials_before_physical_restore(
        self, config: OlmBackupRestoreConfig, csv: dict[str, Any]
    ) -> None:
        """No credential in the logs and objects of the backups and the logical restore"""
        operator = operator_pod(config.namespace, csv)["metadata"]["name"]
        check_logs_for_secrets(config.namespace, config.cluster, operator)

    @pytest.mark.dependency(depends=["TestOlmBackupRestore::test_physical_backup"])
    def test_physical_restore(
        self,
        config: OlmBackupRestoreConfig,
        csv: dict[str, Any],
        test_paths: Paths,
        psmdb_client: MongoManager,
        state: State,
    ) -> None:
        """A physical restore rebuilds the cluster with the data as of the backup"""
        compare = f"{test_paths['test_dir']}/compare"
        insert(psmdb_client, config.cluster, config.namespace, 100501)
        check_members(psmdb_client, config.cluster, config.namespace, f"{compare}/find-2.json")
        # The operator replaces the replset pods when the restore completes; keep their logs.
        state.restore_logs = LogRecorder(config.namespace, config.cluster)
        start = datetime.now(UTC)
        with state.restore_logs:
            _restore(config, csv, "physical")
        state.physical_restore = (start, datetime.now(UTC))
        check_members(psmdb_client, config.cluster, config.namespace, f"{compare}/find-1.json")
        # The rebuilt cluster accepts writes.
        insert(psmdb_client, config.cluster, config.namespace, 100501)
        check_members(psmdb_client, config.cluster, config.namespace, f"{compare}/find-2.json")

    @pytest.mark.dependency(depends=["TestOlmBackupRestore::test_physical_restore"])
    def test_operator_healthy(
        self, config: OlmBackupRestoreConfig, csv: dict[str, Any], state: State
    ) -> None:
        """The operator was not replaced or restarted and logged no unexpected errors"""
        assert state.physical_restore, "The physical restore step did not record its time"
        start, end = state.physical_restore
        crash = ErrorWindow(
            PHYSICAL_RESTORE_CRASH,
            start - CLOCK_SKEW,
            end + CLOCK_SKEW,
            PHYSICAL_RESTORE_CRASH_MAX,
        )
        assert_operator_healthy(config.namespace, csv, state.operator_pod_uid, (crash,))

    # Last, and only after the deploy step, so it also runs when a backup or restore fails.
    @pytest.mark.dependency(depends=["TestOlmBackupRestore::test_deploy_cluster"])
    def test_no_credentials_in_logs(
        self, config: OlmBackupRestoreConfig, csv: dict[str, Any], state: State
    ) -> None:
        """No storage credential or credential-shaped string in the logs and objects"""
        operator = operator_pod(config.namespace, csv)["metadata"]["name"]
        recorded = state.restore_logs.sources() if state.restore_logs else {}
        check_logs_for_secrets(config.namespace, config.cluster, operator, extra_sources=recorded)
