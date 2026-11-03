import json
import logging
import os
from collections.abc import Callable
from dataclasses import dataclass
from typing import Any, ClassVar

import pytest
from lib.config import apply_cluster
from lib.kubectl import detect_platform, kubectl_bin, wait_cluster_consistency, wait_for_running
from lib.mongo import MongoManager
from lib.operator import get_operator_pod
from lib.utils import Paths, retry

logger = logging.getLogger(__name__)

CLUSTER = "some-name"
RS = "rs0"
JOURNAL_DIR = "/data/db/journal"
DATA_DIR = "/data/db"
JOURNAL_CLAIM = "mongod-journal"
DATA_CLAIM = "mongod-data"
JOURNAL_FILLER = f"{JOURNAL_DIR}/e2e-autoscaling-filler"
SNAPSHOT_CLASS_FILES = {
    "aks": "volume-snapshot-class-aks.yaml",
    "eks": "volume-snapshot-class-ebs.yaml",
    "openshift": "volume-snapshot-class-ebs.yaml",
    "gke": "volume-snapshot-class-gke.yaml",
}


def _pods() -> list[str]:
    return [f"{CLUSTER}-{RS}-{i}" for i in range(3)]


def _journal_pvcs() -> list[str]:
    return [f"{JOURNAL_CLAIM}-{pod}" for pod in _pods()]


def _data_pvcs() -> list[str]:
    return [f"{DATA_CLAIM}-{pod}" for pod in _pods()]


def _pvc_exists(pvc: str) -> bool:
    return (
        kubectl_bin("get", "pvc", pvc, "--ignore-not-found", "-o", "name", check=False).strip()
        != ""
    )


def _pvc_field(pvc: str, jsonpath: str) -> str:
    return kubectl_bin("get", "pvc", pvc, "-o", f"jsonpath={jsonpath}").strip()


def _pvc_capacity(pvc: str) -> str:
    return _pvc_field(pvc, "{.status.capacity.storage}")


def _pvc_request(pvc: str) -> str:
    return _pvc_field(pvc, "{.spec.resources.requests.storage}")


def _sts_claims() -> dict[str, str]:
    raw = kubectl_bin(
        "get",
        "sts",
        f"{CLUSTER}-{RS}",
        "-o",
        "jsonpath={range .spec.volumeClaimTemplates[*]}{.metadata.name}={.spec.resources.requests.storage}{'\\n'}{end}",
    ).strip()
    return dict(line.split("=", 1) for line in raw.splitlines())


def _mount_device(pod: str, path: str) -> str:
    output = kubectl_bin("exec", pod, "-c", "mongod", "--", "df", "-P", path).strip()
    return output.splitlines()[-1].split()[0]


def _container_mounts(pod: str, container: str) -> dict[str, str]:
    spec = json.loads(kubectl_bin("get", "pod", pod, "-o", "json"))["spec"]
    target = next(c for c in spec["containers"] if c["name"] == container)
    return {m["name"]: m["mountPath"] for m in target["volumeMounts"]}


def _journal_files(pod: str) -> list[str]:
    output = kubectl_bin(
        "exec", pod, "-c", "mongod", "--", "sh", "-c", f"ls {JOURNAL_DIR}"
    ).strip()
    return output.split()


def _journal_df_kb(pod: str) -> tuple[int, int]:
    output = kubectl_bin("exec", pod, "-c", "mongod", "--", "df", "-P", "-k", JOURNAL_DIR).strip()
    fields = output.splitlines()[-1].split()
    return int(fields[1]), int(fields[2])


def _journal_used_bytes(pod: str) -> int:
    _, used_kb = _journal_df_kb(pod)
    return used_kb * 1024


def _fill_journal(pod: str, target_percent: int) -> None:
    total_kb, used_kb = _journal_df_kb(pod)
    fill_mb = (total_kb * target_percent // 100 - used_kb) // 1024
    if fill_mb <= 0:
        return
    logger.info(
        f"Filling {JOURNAL_DIR} on {pod} with {fill_mb}MiB to cross the autoscaling threshold"
    )
    kubectl_bin(
        "exec",
        pod,
        "-c",
        "mongod",
        "--",
        "dd",
        "if=/dev/zero",
        f"of={JOURNAL_FILLER}",
        "bs=1M",
        f"count={fill_mb}",
    )


def _remove_journal_filler(pod: str) -> None:
    kubectl_bin("exec", pod, "-c", "mongod", "--", "rm", "-f", JOURNAL_FILLER, check=False)


def _patch_enable_journal_storage(size: str) -> None:
    kubectl_bin(
        "patch",
        "psmdb",
        CLUSTER,
        "--type=json",
        "-p",
        json.dumps(
            [
                {
                    "op": "add",
                    "path": "/spec/replsets/0/journalStorage",
                    "value": {
                        "persistentVolumeClaim": {
                            "accessModes": ["ReadWriteOnce"],
                            "resources": {"requests": {"storage": size}},
                        }
                    },
                }
            ]
        ),
    )


def _wait_sts_claims(expected: list[str], max_attempts: int = 60, delay: int = 5) -> None:
    logger.info(f"Waiting for statefulset volume claim templates {expected}")
    try:
        retry(
            lambda: sorted(_sts_claims()),
            max_attempts=max_attempts,
            delay=delay,
            condition=lambda claims: claims == sorted(expected),
        )
    except RuntimeError:
        pytest.fail(f"Statefulset claim templates are {_sts_claims()}, expected {expected}")


def _journal_storage_request() -> str:
    return kubectl_bin(
        "get",
        "psmdb",
        CLUSTER,
        "-o",
        "jsonpath={.spec.replsets[0].journalStorage.persistentVolumeClaim.resources.requests.storage}",
    ).strip()


def _patch_journal_storage(size: str) -> None:
    kubectl_bin(
        "patch",
        "psmdb",
        CLUSTER,
        "--type=json",
        "-p",
        json.dumps(
            [
                {
                    "op": "replace",
                    "path": "/spec/replsets/0/journalStorage/persistentVolumeClaim/resources/requests/storage",
                    "value": size,
                }
            ]
        ),
    )


def _remove_journal_storage() -> None:
    kubectl_bin(
        "patch",
        "psmdb",
        CLUSTER,
        "--type=json",
        "-p",
        json.dumps([{"op": "remove", "path": "/spec/replsets/0/journalStorage"}]),
    )


def _patch_autoscaling(threshold: int, growth_step: str, max_size: str) -> None:
    kubectl_bin(
        "patch",
        "psmdb",
        CLUSTER,
        "--type=merge",
        "-p",
        json.dumps(
            {
                "spec": {
                    "storageScaling": {
                        "enableVolumeScaling": True,
                        "autoscaling": {
                            "enabled": True,
                            "triggerThresholdPercent": threshold,
                            "growthStep": growth_step,
                            "maxSize": max_size,
                        },
                    }
                }
            }
        ),
    )


def _operator_logs() -> str:
    args = ["logs", get_operator_pod(), "--tail=200"]
    operator_ns = os.environ.get("OPERATOR_NS")
    if operator_ns:
        args += ["-n", operator_ns]
    return kubectl_bin(*args, check=False)


def _wait_claim_size(claim: str, expected: str, max_attempts: int = 60, delay: int = 5) -> None:
    logger.info(f"Waiting for claim template {claim} to request {expected}")
    try:
        retry(
            lambda: _sts_claims().get(claim),
            max_attempts=max_attempts,
            delay=delay,
            condition=lambda size: size == expected,
        )
    except RuntimeError:
        pytest.fail(
            f"Claim template {claim} requests {_sts_claims().get(claim)}, expected {expected}"
        )


def _wait_pvc_capacity(pvc: str, expected: str, max_attempts: int = 120, delay: int = 10) -> None:
    logger.info(f"Waiting for PVC {pvc} to reach {expected}")
    try:
        retry(
            lambda: _pvc_capacity(pvc),
            max_attempts=max_attempts,
            delay=delay,
            condition=lambda size: size == expected,
        )
    except RuntimeError:
        pytest.fail(f"PVC {pvc} is {_pvc_capacity(pvc)}, expected {expected}")


def _default_storageclass() -> str:
    return kubectl_bin(
        "get",
        "sc",
        "-o",
        'jsonpath={.items[?(@.metadata.annotations.storageclass\\.kubernetes\\.io/is-default-class=="true")].metadata.name}',
    ).strip()


def _allows_volume_expansion() -> bool:
    storageclass = _default_storageclass()
    if storageclass == "":
        return False
    return (
        kubectl_bin(
            "get",
            "sc",
            storageclass,
            "-o",
            "jsonpath={.allowVolumeExpansion}",
            check=False,
        ).strip()
        == "true"
    )


def _cr_state(kind: str, name: str) -> str:
    return kubectl_bin("get", kind, name, "-o", "jsonpath={.status.state}", check=False).strip()


def _wait_cr_ready(kind: str, name: str, max_attempts: int = 120, delay: int = 10) -> None:
    logger.info(f"Waiting for {kind}/{name} to be ready")
    try:
        retry(
            lambda: _cr_state(kind, name),
            max_attempts=max_attempts,
            delay=delay,
            condition=lambda state: state in ("ready", "error"),
        )
    except RuntimeError:
        pytest.fail(f"{kind}/{name} is {_cr_state(kind, name)}, expected ready")
    state = _cr_state(kind, name)
    assert state == "ready", f"{kind}/{name} ended in state {state}"


def _seed_count(psmdb_client: MongoManager, namespace: str) -> str:
    uri = f"testWriter:testWriter123456@{CLUSTER}-{RS}.{namespace}"
    return psmdb_client.run_mongosh(
        "db.getSiblingDB('journalStorage').seedData.countDocuments({})",
        uri,
        driver="mongodb",
    ).strip()


def _member_seed_count(psmdb_client: MongoManager, namespace: str, pod: str) -> str:
    member = (
        f"mongodb://testWriter:testWriter123456@{pod}.{CLUSTER}-{RS}.{namespace}.svc.cluster.local:27017"
        "/admin?directConnection=true&readPreference=secondaryPreferred"
    )
    return psmdb_client.run_mongosh(
        f"connect('{member}').getSiblingDB('journalStorage').seedData.countDocuments({{}})",
        f"testWriter:testWriter123456@{CLUSTER}-{RS}.{namespace}",
        driver="mongodb",
    ).strip()


def _pbm_running_op() -> str:
    output = kubectl_bin(
        "exec",
        _pods()[0],
        "-c",
        "backup-agent",
        "--",
        "pbm",
        "status",
        "-o",
        "json",
        "-s",
        "running",
        check=False,
    )
    try:
        return str(json.loads(output).get("running", {}).get("opID"))
    except json.JSONDecodeError:
        return "unknown"


def _wait_pbm_idle(max_attempts: int = 60, delay: int = 10) -> None:
    logger.info("Waiting for PBM operations to finish")
    try:
        retry(
            _pbm_running_op,
            max_attempts=max_attempts,
            delay=delay,
            condition=lambda op: op == "None",
        )
    except RuntimeError:
        pytest.fail(f"PBM operation {_pbm_running_op()} is still running")


@dataclass(frozen=True)
class JournalPVCConfig:
    namespace: str
    cluster: str


class TestJournalPVC:
    """Run the WiredTiger journal on a dedicated PVC."""

    INITIAL_SIZE = "2Gi"
    GROWN_SIZE = "3Gi"
    AUTOSCALED_SIZE = "4Gi"
    DATA_SIZE = "3Gi"
    DATA_UIDS: ClassVar[dict[str, str]] = {}
    JOURNAL_UIDS: ClassVar[dict[str, str]] = {}

    @pytest.fixture(scope="class", autouse=True)
    @classmethod
    def config(cls, create_infra: Callable[[str], str]) -> JournalPVCConfig:
        return JournalPVCConfig(namespace=create_infra("journal-pvc"), cluster=CLUSTER)

    @pytest.fixture(scope="class", autouse=True)
    @classmethod
    def setup_tests(
        cls, config: JournalPVCConfig, test_paths: Paths, deploy_s3_storage: Any
    ) -> None:
        kubectl_bin(
            "apply",
            "-f",
            f"{test_paths['conf_dir']}/secrets.yml",
            "-f",
            f"{test_paths['conf_dir']}/s3-secret.yml",
        )

    @pytest.mark.dependency()
    def test_01_create_cluster_without_journal_pvc(self, test_paths: Paths) -> None:
        """A cluster created without journalStorage keeps the journal on the data volume."""
        apply_cluster(f"{test_paths['test_dir']}/conf/some-name.yml")
        wait_for_running(f"{CLUSTER}-{RS}", 3)
        wait_cluster_consistency(CLUSTER)

        assert sorted(_sts_claims()) == [DATA_CLAIM]
        for pvc in _journal_pvcs():
            assert not _pvc_exists(pvc), f"PVC {pvc} must not exist before journalStorage is set"

        for pod in _pods():
            assert _mount_device(pod, JOURNAL_DIR) == _mount_device(pod, DATA_DIR), (
                f"{pod} must keep the journal on the data volume"
            )

        for pvc in _data_pvcs():
            self.DATA_UIDS[pvc] = _pvc_field(pvc, "{.metadata.uid}")

    @pytest.mark.dependency(depends=["TestJournalPVC::test_01_create_cluster_without_journal_pvc"])
    def test_02_seed_data(self, config: JournalPVCConfig, psmdb_client: MongoManager) -> None:
        """Journal-committed writes land on the data volume before the migration."""
        admin_uri = f"userAdmin:userAdmin123456@{CLUSTER}-{RS}.{config.namespace}"
        psmdb_client.run_mongosh(
            "db.createUser({user:'testWriter',pwd:'testWriter123456',"
            "roles:[{db:'journalStorage',role:'readWrite'}]})",
            admin_uri,
            driver="mongodb",
        )

        uri = f"testWriter:testWriter123456@{CLUSTER}-{RS}.{config.namespace}"
        payload = "x" * 1024
        for i in range(100):
            psmdb_client.run_mongosh(
                f"db.getSiblingDB('journalStorage').seedData.insertOne("
                f"{{idx: {i}, payload: '{payload}'}}, "
                "{writeConcern: {w: 'majority', j: true}})",
                uri,
                driver="mongodb",
            )

        for pod in _pods():
            files = _journal_files(pod)
            assert any(f.startswith("WiredTigerLog") for f in files), (
                f"{pod} has no WiredTiger journal files in {JOURNAL_DIR}: {files}"
            )

    @pytest.mark.dependency(depends=["TestJournalPVC::test_02_seed_data"])
    def test_03_enable_journal_pvc_in_place(
        self, config: JournalPVCConfig, psmdb_client: MongoManager
    ) -> None:
        """Enabling journalStorage migrates every member without losing data."""
        _patch_enable_journal_storage(self.INITIAL_SIZE)
        _wait_sts_claims([DATA_CLAIM, JOURNAL_CLAIM])
        wait_for_running(f"{CLUSTER}-{RS}", 3)
        wait_cluster_consistency(CLUSTER)

        for pvc in _journal_pvcs():
            assert _pvc_field(pvc, "{.status.phase}") == "Bound", f"PVC {pvc} is not bound"
            assert _pvc_capacity(pvc) == self.INITIAL_SIZE
            self.JOURNAL_UIDS[pvc] = _pvc_field(pvc, "{.metadata.uid}")

        for pvc, uid in self.DATA_UIDS.items():
            assert _pvc_field(pvc, "{.metadata.uid}") == uid, f"data PVC {pvc} was recreated"
            assert _pvc_capacity(pvc) == self.DATA_SIZE

        uri = f"testWriter:testWriter123456@{CLUSTER}-{RS}.{config.namespace}"
        count = psmdb_client.run_mongosh(
            "db.getSiblingDB('journalStorage').seedData.countDocuments({})",
            uri,
            driver="mongodb",
        ).strip()
        assert count == "100", f"Expected 100 documents after the migration, got {count}"

    @pytest.mark.dependency(depends=["TestJournalPVC::test_03_enable_journal_pvc_in_place"])
    def test_04_journal_is_on_its_own_volume(self) -> None:
        """mongod and the backup agent mount the journal volume, carrying the old journal."""
        for pod in _pods():
            assert _mount_device(pod, JOURNAL_DIR) != _mount_device(pod, DATA_DIR), (
                f"{pod} still keeps the journal on the data volume"
            )

            for container in ("mongod", "backup-agent"):
                mounts = _container_mounts(pod, container)
                assert mounts.get(JOURNAL_CLAIM) == JOURNAL_DIR, (
                    f"container {container} in {pod} does not mount {JOURNAL_CLAIM} at {JOURNAL_DIR}"
                )

            assert _container_mounts(pod, "mongod").get(DATA_CLAIM) == DATA_DIR

            files = _journal_files(pod)
            assert any(f.startswith("WiredTigerLog") for f in files), (
                f"{pod} has no WiredTiger journal files in {JOURNAL_DIR}: {files}"
            )

    @pytest.mark.dependency(depends=["TestJournalPVC::test_04_journal_is_on_its_own_volume"])
    def test_05_resize_journal_pvc(self) -> None:
        """A larger request resizes the journal PVCs and leaves the data PVCs alone."""
        if not _allows_volume_expansion():
            pytest.skip(
                f"default storageclass {_default_storageclass()} does not allow volume expansion"
            )

        _patch_journal_storage(self.GROWN_SIZE)

        for pvc in _journal_pvcs():
            _wait_pvc_capacity(pvc, self.GROWN_SIZE)
            assert _pvc_request(pvc) == self.GROWN_SIZE

        _wait_claim_size(JOURNAL_CLAIM, self.GROWN_SIZE)
        wait_for_running(f"{CLUSTER}-{RS}", 3)
        wait_cluster_consistency(CLUSTER)

        assert _sts_claims()[DATA_CLAIM] == self.DATA_SIZE
        for pvc, uid in self.DATA_UIDS.items():
            assert _pvc_capacity(pvc) == self.DATA_SIZE, f"data PVC {pvc} was resized"
            assert _pvc_field(pvc, "{.metadata.uid}") == uid, f"data PVC {pvc} was recreated"

    @pytest.mark.dependency(depends=["TestJournalPVC::test_05_resize_journal_pvc"])
    def test_06_shrink_is_rejected(self) -> None:
        """A smaller request is reverted instead of deleting journal PVCs."""
        current = _journal_storage_request()
        _patch_journal_storage("1Gi")

        logger.info("Waiting for the operator to revert the journal storage request")
        try:
            retry(
                _journal_storage_request,
                max_attempts=30,
                delay=5,
                condition=lambda size: size == current,
            )
        except RuntimeError:
            pytest.fail(
                f"journal storage request is {_journal_storage_request()}, expected {current}"
            )

        for pvc, uid in self.JOURNAL_UIDS.items():
            assert _pvc_exists(pvc), f"PVC {pvc} must survive a rejected shrink"
            assert _pvc_field(pvc, "{.metadata.uid}") == uid, f"journal PVC {pvc} was recreated"

    @pytest.mark.dependency(depends=["TestJournalPVC::test_06_shrink_is_rejected"])
    def test_07_autoscaling_grows_the_journal_pvc(self) -> None:
        """Usage above the threshold grows the journal PVC without a manual patch."""
        if not _allows_volume_expansion():
            pytest.skip(
                f"default storageclass {_default_storageclass()} does not allow volume expansion"
            )

        _patch_autoscaling(threshold=50, growth_step="1Gi", max_size=self.AUTOSCALED_SIZE)

        try:
            for pod in _pods():
                _fill_journal(pod, target_percent=70)

            for pvc in _journal_pvcs():
                _wait_pvc_capacity(pvc, self.AUTOSCALED_SIZE)

            _wait_claim_size(JOURNAL_CLAIM, self.AUTOSCALED_SIZE)
            wait_for_running(f"{CLUSTER}-{RS}", 3)
            wait_cluster_consistency(CLUSTER)

            assert _journal_storage_request() == self.AUTOSCALED_SIZE, (
                "autoscaling must raise the journal request in the CR"
            )
            assert _sts_claims()[DATA_CLAIM] == self.DATA_SIZE, (
                "the data claim stays below the threshold and must not grow"
            )
        finally:
            for pod in _pods():
                _remove_journal_filler(pod)

    @pytest.mark.dependency(depends=["TestJournalPVC::test_07_autoscaling_grows_the_journal_pvc"])
    def test_08_disabling_is_rejected(
        self, config: JournalPVCConfig, psmdb_client: MongoManager
    ) -> None:
        """Removing journalStorage from a running cluster is rejected, not applied."""
        _remove_journal_storage()

        logger.info("Waiting for the operator to reject the removal")
        try:
            retry(
                _operator_logs,
                max_attempts=30,
                delay=5,
                condition=lambda logs: "journalStorage can't be disabled" in logs,
            )
        except RuntimeError:
            pytest.fail("operator did not reject removing journalStorage")

        assert sorted(_sts_claims()) == sorted([DATA_CLAIM, JOURNAL_CLAIM]), (
            "the journal claim template must survive a rejected removal"
        )

        for pvc, uid in self.JOURNAL_UIDS.items():
            assert _pvc_exists(pvc), f"PVC {pvc} must survive a rejected removal"
            assert _pvc_field(pvc, "{.metadata.uid}") == uid, f"journal PVC {pvc} was recreated"

        uri = f"testWriter:testWriter123456@{CLUSTER}-{RS}.{config.namespace}"
        count = psmdb_client.run_mongosh(
            "db.getSiblingDB('journalStorage').seedData.countDocuments({})",
            uri,
            driver="mongodb",
        ).strip()
        assert count == "100", f"Expected 100 documents, got {count}"

    @pytest.mark.dependency(depends=["TestJournalPVC::test_08_disabling_is_rejected"])
    def test_09_resync_clears_the_stale_journal(
        self, config: JournalPVCConfig, psmdb_client: MongoManager
    ) -> None:
        """A member whose data volume is wiped starts with a clean journal and resyncs."""
        _patch_enable_journal_storage(_journal_storage_request() or self.INITIAL_SIZE)
        wait_for_running(f"{CLUSTER}-{RS}", 3)
        wait_cluster_consistency(CLUSTER)

        pod = _pods()[-1]
        data_pvc = f"{DATA_CLAIM}-{pod}"
        journal_pvc = f"{JOURNAL_CLAIM}-{pod}"
        journal_uid = _pvc_field(journal_pvc, "{.metadata.uid}")

        kubectl_bin("delete", "pvc", data_pvc, "--wait=false")
        kubectl_bin("delete", "pod", pod)
        wait_for_running(f"{CLUSTER}-{RS}", 3)
        wait_cluster_consistency(CLUSTER)

        assert _pvc_field(journal_pvc, "{.metadata.uid}") == journal_uid, (
            "the journal PVC must be reused, not recreated"
        )
        init_logs = kubectl_bin("logs", pod, "-c", "journal-migration")
        assert "removing stale journal" in init_logs, (
            f"{pod} did not clear the journal left by the old data volume: {init_logs}"
        )
        assert any(f.startswith("WiredTigerLog") for f in _journal_files(pod))
        assert _seed_count(psmdb_client, config.namespace) == "100"

    @pytest.mark.dependency(depends=["TestJournalPVC::test_09_resync_clears_the_stale_journal"])
    def test_10_physical_backup_and_restore(
        self, config: JournalPVCConfig, test_paths: Paths, psmdb_client: MongoManager
    ) -> None:
        """A physical backup taken with the journal on its own PVC restores the data."""
        backup = "journal-physical"
        kubectl_bin(
            "apply",
            "-f",
            "-",
            input_data=json.dumps(
                {
                    "apiVersion": "psmdb.percona.com/v1",
                    "kind": "PerconaServerMongoDBBackup",
                    "metadata": {"name": backup, "finalizers": ["percona.com/delete-backup"]},
                    "spec": {
                        "type": "physical",
                        "clusterName": CLUSTER,
                        "storageName": "local-s3",
                    },
                }
            ),
        )
        _wait_cr_ready("psmdb-backup", backup)

        uri = f"testWriter:testWriter123456@{CLUSTER}-{RS}.{config.namespace}"
        psmdb_client.run_mongosh(
            "db.getSiblingDB('journalStorage').seedData.insertOne({idx: 100}, {writeConcern: {w: 'majority', j: true}})",
            uri,
            driver="mongodb",
        )
        assert _seed_count(psmdb_client, config.namespace) == "101"

        restore = f"restore-{backup}"
        kubectl_bin(
            "apply",
            "-f",
            "-",
            input_data=json.dumps(
                {
                    "apiVersion": "psmdb.percona.com/v1",
                    "kind": "PerconaServerMongoDBRestore",
                    "metadata": {"name": restore},
                    "spec": {"clusterName": CLUSTER, "backupName": backup},
                }
            ),
        )
        _wait_cr_ready("psmdb-restore", restore, max_attempts=180)
        wait_for_running(f"{CLUSTER}-{RS}", 3)
        wait_cluster_consistency(CLUSTER)

        assert _seed_count(psmdb_client, config.namespace) == "100"
        for pod in _pods():
            assert _mount_device(pod, JOURNAL_DIR) != _mount_device(pod, DATA_DIR), (
                f"{pod} lost the journal volume after the restore"
            )
            assert any(f.startswith("WiredTigerLog") for f in _journal_files(pod))

    @pytest.mark.dependency(depends=["TestJournalPVC::test_10_physical_backup_and_restore"])
    def test_11_snapshot_backup_and_restore(
        self, config: JournalPVCConfig, test_paths: Paths, psmdb_client: MongoManager
    ) -> None:
        """A snapshot restore replaces the live journal instead of replaying it on restored data."""
        platform = os.environ.get("PLATFORM") or detect_platform()
        if platform not in SNAPSHOT_CLASS_FILES:
            pytest.skip(f"volume snapshots are not configured for platform {platform}")

        kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/{SNAPSHOT_CLASS_FILES[platform]}")
        snapshot_class = kubectl_bin(
            "get", "volumesnapshotclass", "-o", "jsonpath={.items[0].metadata.name}"
        ).strip()
        _wait_pbm_idle()

        uri = f"testWriter:testWriter123456@{CLUSTER}-{RS}.{config.namespace}"
        psmdb_client.run_mongosh(
            "db.getSiblingDB('journalStorage').seedData.insertOne({idx: 101}, {writeConcern: {w: 'majority', j: true}})",
            uri,
            driver="mongodb",
        )
        assert _seed_count(psmdb_client, config.namespace) == "101"

        backup = "journal-snapshot"
        kubectl_bin(
            "apply",
            "-f",
            "-",
            input_data=json.dumps(
                {
                    "apiVersion": "psmdb.percona.com/v1",
                    "kind": "PerconaServerMongoDBBackup",
                    "metadata": {"name": backup, "finalizers": ["percona.com/delete-backup"]},
                    "spec": {
                        "type": "external",
                        "clusterName": CLUSTER,
                        "volumeSnapshotClass": snapshot_class,
                    },
                }
            ),
        )
        _wait_cr_ready("psmdb-backup", backup)

        psmdb_client.run_mongosh(
            "db.getSiblingDB('journalStorage').seedData.insertMany("
            "Array.from({length: 1000}, (_, i) => ({idx: 1000 + i})), {writeConcern: {w: 'majority', j: true}});"
            "db.getSiblingDB('journalStorage').seedData.drop()",
            uri,
            driver="mongodb",
        )
        assert _seed_count(psmdb_client, config.namespace) == "0"

        restore = f"restore-{backup}"
        kubectl_bin(
            "apply",
            "-f",
            "-",
            input_data=json.dumps(
                {
                    "apiVersion": "psmdb.percona.com/v1",
                    "kind": "PerconaServerMongoDBRestore",
                    "metadata": {"name": restore},
                    "spec": {"clusterName": CLUSTER, "backupName": backup},
                }
            ),
        )
        _wait_cr_ready("psmdb-restore", restore, max_attempts=180)
        wait_for_running(f"{CLUSTER}-{RS}", 3)
        wait_cluster_consistency(CLUSTER)

        for pod in _pods():
            init_logs = kubectl_bin("logs", pod, "-c", "journal-migration")
            assert "journal migrated" in init_logs, (
                f"{pod} did not move the restored journal to the journal volume: {init_logs}"
            )
            assert _mount_device(pod, JOURNAL_DIR) != _mount_device(pod, DATA_DIR), (
                f"{pod} lost the journal volume after the snapshot restore"
            )
            count = _member_seed_count(psmdb_client, config.namespace, pod)
            assert count == "101", (
                f"{pod} has {count} documents after the snapshot restore, expected 101"
            )
