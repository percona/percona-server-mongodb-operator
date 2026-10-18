#!/usr/bin/env python3

import json
import logging
import subprocess
import threading
import time
from collections.abc import Callable
from dataclasses import dataclass

import pytest
from lib.config import apply_cluster
from lib.kubectl import (
    detect_platform,
    kubectl_bin,
    wait_cluster_consistency,
    wait_for_running,
    wait_pod,
)
from lib.utils import Paths, retry

logger = logging.getLogger(__name__)

CLUSTER = "some-name"
SHRINK_ANNOTATION_PREFIX = "percona.com/pvc-shrink-in-progress."


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def _pvc_size(pvc: str) -> str:
    return kubectl_bin("get", "pvc", pvc, "-o", "jsonpath={.status.capacity.storage}").strip()


def _wait_pvc_size(pvc: str, expected: str, max_attempts: int = 120, delay: int = 10) -> None:
    logger.info(f"Waiting for PVC {pvc} to reach size {expected}")
    try:
        retry(
            lambda: _pvc_size(pvc),
            max_attempts=max_attempts,
            delay=delay,
            condition=lambda size: size == expected,
        )
    except RuntimeError:
        actual = _pvc_size(pvc)
        pytest.fail(f"PVC {pvc} is {actual}, expected {expected}")


def _cr_storage(replset: str) -> str:
    jsonpath = f'{{.spec.replsets[?(@.name=="{replset}")].volumeSpec.persistentVolumeClaim.resources.requests.storage}}'
    return kubectl_bin("get", "psmdb", CLUSTER, "-o", f"jsonpath={jsonpath}").strip()


def _cfg_storage() -> str:
    return kubectl_bin(
        "get", "psmdb", CLUSTER, "-o",
        "jsonpath={.spec.sharding.configsvrReplSet.volumeSpec.persistentVolumeClaim.resources.requests.storage}",
    ).strip()


def _has_shrink_annotation() -> bool:
    annotations = kubectl_bin(
        "get", "psmdb", CLUSTER, "-o", "jsonpath={.metadata.annotations}"
    ).strip()
    return SHRINK_ANNOTATION_PREFIX in annotations


def _wait_no_shrink_annotation(max_attempts: int = 180, delay: int = 10) -> None:
    logger.info("Waiting for shrink annotations to be removed")
    try:
        retry(
            _has_shrink_annotation,
            max_attempts=max_attempts,
            delay=delay,
            condition=lambda has: not has,
        )
    except RuntimeError:
        pytest.fail("Shrink annotation still present after timeout")


def _patch_replset_storage(replset: str, size: str) -> None:
    idx_raw = kubectl_bin(
        "get", "psmdb", CLUSTER, "-o",
        "jsonpath={range .spec.replsets[*]}{.name}{'\\n'}{end}",
    ).strip()
    names = idx_raw.splitlines()
    if replset not in names:
        pytest.fail(f"Replset {replset} not found in CR, available: {names}")
    idx = names.index(replset)
    kubectl_bin(
        "patch", "psmdb", CLUSTER, "--type=json", "-p",
        json.dumps([{
            "op": "replace",
            "path": f"/spec/replsets/{idx}/volumeSpec/persistentVolumeClaim/resources/requests/storage",
            "value": size,
        }]),
    )


def _patch_cfg_storage(size: str) -> None:
    kubectl_bin(
        "patch", "psmdb", CLUSTER, "--type=merge", "-p",
        json.dumps({
            "spec": {
                "sharding": {
                    "configsvrReplSet": {
                        "volumeSpec": {"persistentVolumeClaim": {"resources": {"requests": {"storage": size}}}},
                    },
                },
            },
        }),
    )


def _patch_shrink_enabled(enabled: bool) -> None:
    kubectl_bin(
        "patch", "psmdb", CLUSTER, "--type=merge", "-p",
        json.dumps({"spec": {"storageScaling": {"enableVolumeShrink": enabled}}}),
    )


def _default_storageclass() -> str:
    return kubectl_bin(
        "get", "sc", "-o",
        'jsonpath={.items[?(@.metadata.annotations.storageclass\\.kubernetes\\.io/is-default-class=="true")].metadata.name}',
    ).strip()


def _allows_volume_expansion(storageclass: str) -> bool:
    allowed = kubectl_bin(
        "get", "sc", storageclass, "-o", "jsonpath={.allowVolumeExpansion}",
        check=False,
    ).strip()
    return allowed == "true"


def _setup_storageclass(platform: str, test_paths: Paths) -> None:
    if platform == "eks":
        logger.info("EKS detected, creating storageclass for expandable EBS volumes")
        kubectl_bin("apply", "-f", f"{test_paths['test_dir']}/conf/eks-storageclass.yml")
        return
    storageclass = _default_storageclass()
    if not _allows_volume_expansion(storageclass):
        pytest.skip(f"default storageclass {storageclass} does not allow volume expansion")


class LiveTrafficWriter:
    """Runs a background thread that continuously inserts documents via mongosh.

    Each document has a sequential `seq` field. After stopping, the caller can
    verify that every seq from 0..written-1 is present in the collection,
    proving no data was lost and no write was silently dropped.
    """

    def __init__(self, client_pod: str, uri: str, namespace: str,
                 db: str = "liveTraffic", col: str = "writes"):
        self.client_pod = client_pod
        self.uri = uri
        self.namespace = namespace
        self.db = db
        self.col = col
        self._stop = threading.Event()
        self._thread: threading.Thread | None = None
        self.written = 0
        self.errors: list[str] = []

    def start(self) -> None:
        self._thread = threading.Thread(target=self._run, daemon=True)
        self._thread.start()
        logger.info("Live traffic writer started")

    def stop(self) -> int:
        self._stop.set()
        if self._thread:
            self._thread.join(timeout=60)
        logger.info(f"Live traffic writer stopped: {self.written} docs written, {len(self.errors)} errors")
        return self.written

    def _run(self) -> None:
        seq = 0
        while not self._stop.is_set():
            try:
                write_cmd = (
                    f"db.getSiblingDB('{self.db}').{self.col}.insertOne("
                    f"{{seq: {seq}, ts: new Date(), data: 'shrink-test-payload-' + '{seq}'.padStart(6, '0')}})"
                )
                kubectl_bin(
                    "exec", self.client_pod, "--",
                    "timeout", "15",
                    "env", "HOME=/tmp",
                    "mongosh",
                    f"mongodb://{self.uri}.{self.namespace}.svc.cluster.local/admin?ssl=false&replicaSet=rs0",
                    "--eval", write_cmd, "--quiet",
                    check=True,
                )
                seq += 1
                self.written = seq
            except subprocess.CalledProcessError as e:
                self.errors.append(f"seq={seq}: {e}")
                logger.warning(f"Live writer error at seq={seq}: {e}")
                time.sleep(2)
            time.sleep(0.5)


def _verify_live_traffic(client_pod: str, uri: str, namespace: str,
                         expected_count: int,
                         db: str = "liveTraffic", col: str = "writes") -> None:
    """Verify all documents written by LiveTrafficWriter are present."""
    conn = f"mongodb://{uri}.{namespace}.svc.cluster.local/admin?ssl=false&replicaSet=rs0"
    count_cmd = f"db.getSiblingDB('{db}').{col}.countDocuments({{}})"
    result = kubectl_bin(
        "exec", client_pod, "--",
        "timeout", "15",
        "env", "HOME=/tmp",
        "mongosh", conn,
        "--eval", count_cmd, "--quiet",
    ).strip()
    actual_count = int(result)
    logger.info(f"Live traffic verification: expected={expected_count}, actual={actual_count}")
    assert actual_count == expected_count, (
        f"Data loss detected: expected {expected_count} documents, found {actual_count}"
    )

    gaps_cmd = (
        f"EJSON.stringify(db.getSiblingDB('{db}').{col}.aggregate(["
        f"{{$group: {{_id: null, seqs: {{$addToSet: '$seq'}}, count: {{$sum: 1}}}}}},"
        f"{{$project: {{_id: 0, count: 1, max: {{$max: '$seqs'}}, min: {{$min: '$seqs'}}}}}}])"
        f".toArray())"
    )
    agg_result = kubectl_bin(
        "exec", client_pod, "--",
        "timeout", "15",
        "env", "HOME=/tmp",
        "mongosh", conn,
        "--eval", gaps_cmd, "--quiet",
    ).strip()
    logger.info(f"Live traffic aggregation: {agg_result}")
    agg = json.loads(agg_result)
    if agg:
        assert agg[0]["min"] == 0, f"First seq should be 0, got {agg[0]['min']}"
        assert agg[0]["max"] == expected_count - 1, (
            f"Last seq should be {expected_count - 1}, got {agg[0]['max']}"
        )


@dataclass(frozen=True)
class PVCShrinkConfig:
    namespace: str
    cluster: str
    platform: str


# ---------------------------------------------------------------------------
# Test class 1: basic 3-member replset shrink (non-sharded)
# ---------------------------------------------------------------------------

class TestPVCShrinkBasic:
    """Shrink a 3-member replset from 10Gi to 5Gi, then test disable/enable cycle."""

    RS = "rs0"
    INITIAL_SIZE = "10Gi"
    SHRUNK_SIZE = "5Gi"
    PVCS = [f"mongod-data-{CLUSTER}-rs0-{i}" for i in range(3)]

    @pytest.fixture(scope="class", autouse=True)
    @classmethod
    def config(cls, create_infra: Callable[[str], str]) -> PVCShrinkConfig:
        platform = detect_platform()
        return PVCShrinkConfig(
            namespace=create_infra("pvc-shrink"),
            cluster=CLUSTER,
            platform=platform,
        )

    @pytest.fixture(scope="class", autouse=True)
    @classmethod
    def setup_tests(cls, config: PVCShrinkConfig, test_paths: Paths) -> None:
        kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/secrets.yml")
        _setup_storageclass(config.platform, test_paths)

    # -- Shrink basic replset --

    @pytest.mark.dependency()
    def test_01_create_cluster(self, config: PVCShrinkConfig, test_paths: Paths) -> None:
        apply_cluster(f"{test_paths['test_dir']}/conf/some-name.yml")
        wait_for_running(f"{config.cluster}-{self.RS}", 3)
        for pvc in self.PVCS:
            assert _pvc_size(pvc) == self.INITIAL_SIZE, f"PVC {pvc} did not start at {self.INITIAL_SIZE}"

    @pytest.mark.dependency(depends=["TestPVCShrinkBasic::test_01_create_cluster"])
    def test_02_seed_data(self, config: PVCShrinkConfig, psmdb_client) -> None:
        """Insert data before shrink to verify it survives."""
        admin_uri = f"userAdmin:userAdmin123456@{CLUSTER}-rs0.{config.namespace}"
        psmdb_client.run_mongosh(
            "db.createUser({user:'testWriter',pwd:'testWriter123456',"
            "roles:[{db:'shrinkTest',role:'readWrite'},{db:'liveTraffic',role:'readWrite'}]})",
            admin_uri, driver="mongodb",
        )
        uri = f"testWriter:testWriter123456@{CLUSTER}-rs0.{config.namespace}"
        for i in range(100):
            psmdb_client.run_mongosh(
                f"db.getSiblingDB('shrinkTest').seedData.insertOne({{idx: {i}, payload: 'pre-shrink-{i}'}})",
                uri, driver="mongodb",
            )
        count = psmdb_client.run_mongosh(
            "db.getSiblingDB('shrinkTest').seedData.countDocuments({})",
            uri, driver="mongodb",
        ).strip()
        assert count == "100", f"Expected 100 seed documents, got {count}"

    @pytest.mark.dependency(depends=["TestPVCShrinkBasic::test_02_seed_data"])
    def test_03_shrink_with_live_traffic(self, config: PVCShrinkConfig, psmdb_client) -> None:
        """Shrink from 10Gi to 5Gi while a background writer inserts documents.

        Proves the replica set stays available throughout the rolling PVC
        delete+recreate cycle: no write downtime, no data loss.
        """
        uri_raw = f"testWriter:testWriter123456@{CLUSTER}-rs0"
        writer = LiveTrafficWriter(psmdb_client.client, uri_raw, config.namespace)
        writer.start()

        time.sleep(5)
        assert writer.written > 0, "Writer should have inserted at least one document before shrink"

        _patch_replset_storage(self.RS, self.SHRUNK_SIZE)
        time.sleep(10)

        assert _has_shrink_annotation(), "Shrink annotation was not set"

        _wait_no_shrink_annotation()

        for pvc in self.PVCS:
            _wait_pvc_size(pvc, self.SHRUNK_SIZE)

        wait_cluster_consistency(config.cluster, 900)

        total_written = writer.stop()
        assert total_written > 0, "Writer should have inserted documents during shrink"
        logger.info(f"Writer inserted {total_written} docs, {len(writer.errors)} transient errors")

        _verify_live_traffic(psmdb_client.client, uri_raw, config.namespace, total_written)

        assert len(writer.errors) == 0, (
            f"Write errors during shrink (potential downtime): {writer.errors}"
        )

    @pytest.mark.dependency(depends=["TestPVCShrinkBasic::test_03_shrink_with_live_traffic"])
    def test_04_cr_storage_updated(self) -> None:
        """The CR should reflect the new (smaller) storage size."""
        assert _cr_storage(self.RS) == self.SHRUNK_SIZE

    @pytest.mark.dependency(depends=["TestPVCShrinkBasic::test_03_shrink_with_live_traffic"])
    def test_05_seed_data_intact(self, config: PVCShrinkConfig, psmdb_client) -> None:
        """Verify the 100 documents inserted before shrink are still present."""
        uri = f"testWriter:testWriter123456@{CLUSTER}-rs0.{config.namespace}"
        count = psmdb_client.run_mongosh(
            "db.getSiblingDB('shrinkTest').seedData.countDocuments({})",
            uri, driver="mongodb",
        ).strip()
        assert count == "100", f"Pre-shrink data lost: expected 100, got {count}"

    # -- Expand after shrink --

    @pytest.mark.dependency(depends=["TestPVCShrinkBasic::test_03_shrink_with_live_traffic"])
    def test_06_expand_after_shrink(self, config: PVCShrinkConfig) -> None:
        """Expanding the PVC after a shrink should work normally (no shrink triggered)."""
        expand_size = "8Gi"
        _patch_replset_storage(self.RS, expand_size)
        time.sleep(10)

        assert not _has_shrink_annotation(), "Shrink annotation should NOT be set for expand"

        for pvc in self.PVCS:
            _wait_pvc_size(pvc, expand_size, max_attempts=60)

        wait_cluster_consistency(config.cluster, 600)

    # -- Disable shrink, try to reduce, verify revert --

    @pytest.mark.dependency(depends=["TestPVCShrinkBasic::test_06_expand_after_shrink"])
    def test_07_disable_shrink_then_reduce(self, config: PVCShrinkConfig) -> None:
        """With shrink disabled, reducing storage should revert the CR, not shrink."""
        current_size = "8Gi"
        _patch_shrink_enabled(False)
        time.sleep(5)

        _patch_replset_storage(self.RS, "4Gi")
        time.sleep(15)

        assert not _has_shrink_annotation(), "Shrink annotation should NOT be set when disabled"

        try:
            retry(
                lambda: _cr_storage(self.RS),
                max_attempts=30,
                delay=5,
                condition=lambda s: s == current_size,
            )
        except RuntimeError:
            actual = _cr_storage(self.RS)
            pytest.fail(f"CR storage was not reverted: got {actual}, expected {current_size}")

        for pvc in self.PVCS:
            assert _pvc_size(pvc) == current_size, f"PVC {pvc} should still be {current_size}"

    # -- Expand after disabled shrink attempt --

    @pytest.mark.dependency(depends=["TestPVCShrinkBasic::test_07_disable_shrink_then_reduce"])
    def test_08_expand_after_disabled_shrink(self, config: PVCShrinkConfig) -> None:
        """Expansion should still work after a rejected shrink attempt."""
        expand_size = "9Gi"
        _patch_replset_storage(self.RS, expand_size)
        time.sleep(10)

        for pvc in self.PVCS:
            _wait_pvc_size(pvc, expand_size, max_attempts=60)

        wait_cluster_consistency(config.cluster, 600)

    # -- Re-enable shrink and verify it works --

    @pytest.mark.dependency(depends=["TestPVCShrinkBasic::test_08_expand_after_disabled_shrink"])
    def test_09_reenable_and_shrink(self, config: PVCShrinkConfig) -> None:
        """Re-enabling shrink and reducing storage should trigger the shrink flow."""
        _patch_shrink_enabled(True)
        time.sleep(5)

        shrink_to = "6Gi"
        _patch_replset_storage(self.RS, shrink_to)
        time.sleep(10)

        assert _has_shrink_annotation(), "Shrink annotation should be set after re-enabling"

        _wait_no_shrink_annotation()

        for pvc in self.PVCS:
            _wait_pvc_size(pvc, shrink_to)

        wait_cluster_consistency(config.cluster, 900)

    # -- Edge cases: expand after shrink, disable/enable cycles --

    @pytest.mark.dependency(depends=["TestPVCShrinkBasic::test_09_reenable_and_shrink"])
    def test_10_expand_after_reenable_shrink(self, config: PVCShrinkConfig) -> None:
        """Expanding after a successful re-enable-and-shrink should not trigger shrink."""
        expand_size = "10Gi"
        _patch_replset_storage(self.RS, expand_size)
        time.sleep(10)

        assert not _has_shrink_annotation(), "Shrink annotation should NOT be set for expand"

        for pvc in self.PVCS:
            _wait_pvc_size(pvc, expand_size, max_attempts=60)

        wait_cluster_consistency(config.cluster, 600)

    @pytest.mark.dependency(depends=["TestPVCShrinkBasic::test_10_expand_after_reenable_shrink"])
    def test_11_consecutive_shrink(self, config: PVCShrinkConfig) -> None:
        """Shrink again immediately after an expand — back-to-back shrink cycles."""
        shrink_to = "7Gi"
        _patch_replset_storage(self.RS, shrink_to)
        time.sleep(10)

        assert _has_shrink_annotation(), "Shrink annotation should be set for second shrink"

        _wait_no_shrink_annotation()

        for pvc in self.PVCS:
            _wait_pvc_size(pvc, shrink_to)

        wait_cluster_consistency(config.cluster, 900)

    @pytest.mark.dependency(depends=["TestPVCShrinkBasic::test_11_consecutive_shrink"])
    def test_12_disable_reduce_enable_expand(self, config: PVCShrinkConfig) -> None:
        """Disable shrink, reduce (revert), re-enable, then expand past original.

        Verifies no stale shrink target leaks: the operator should treat the
        expand purely as an expand, with no shrink annotation set.
        """
        _patch_shrink_enabled(False)
        time.sleep(5)

        _patch_replset_storage(self.RS, "3Gi")
        time.sleep(15)

        assert not _has_shrink_annotation(), "Shrink annotation should NOT appear while disabled"

        try:
            retry(
                lambda: _cr_storage(self.RS),
                max_attempts=30,
                delay=5,
                condition=lambda s: s == "7Gi",
            )
        except RuntimeError:
            actual = _cr_storage(self.RS)
            pytest.fail(f"CR storage was not reverted: got {actual}, expected 7Gi")

        _patch_shrink_enabled(True)
        time.sleep(5)

        expand_size = "12Gi"
        _patch_replset_storage(self.RS, expand_size)
        time.sleep(10)

        assert not _has_shrink_annotation(), "Shrink annotation should NOT be set for expand after re-enable"

        for pvc in self.PVCS:
            _wait_pvc_size(pvc, expand_size, max_attempts=60)

        wait_cluster_consistency(config.cluster, 600)


# ---------------------------------------------------------------------------
# Test class 2: parallel shard shrink (2 shards + configsvr)
# ---------------------------------------------------------------------------

class TestPVCShrinkSharded:
    """Shrink two shards in parallel — both rs0 and rs1 process simultaneously."""

    INITIAL_SIZE = "10Gi"
    SHRUNK_SIZE = "5Gi"
    RS0_PVCS = [f"mongod-data-{CLUSTER}-rs0-{i}" for i in range(3)]
    RS1_PVCS = [f"mongod-data-{CLUSTER}-rs1-{i}" for i in range(3)]
    CFG_PVCS = [f"mongod-data-{CLUSTER}-cfg-{i}" for i in range(3)]

    @pytest.fixture(scope="class", autouse=True)
    @classmethod
    def config(cls, create_infra: Callable[[str], str]) -> PVCShrinkConfig:
        platform = detect_platform()
        return PVCShrinkConfig(
            namespace=create_infra("pvc-shrink-sharded"),
            cluster=CLUSTER,
            platform=platform,
        )

    @pytest.fixture(scope="class", autouse=True)
    @classmethod
    def setup_tests(cls, config: PVCShrinkConfig, test_paths: Paths) -> None:
        kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/secrets.yml")
        _setup_storageclass(config.platform, test_paths)

    @pytest.mark.dependency()
    def test_01_create_sharded_cluster(self, config: PVCShrinkConfig, test_paths: Paths) -> None:
        apply_cluster(f"{test_paths['test_dir']}/conf/some-name-sharded.yml")
        wait_for_running(f"{config.cluster}-rs0", 3)
        wait_for_running(f"{config.cluster}-rs1", 3)
        wait_for_running(f"{config.cluster}-cfg", 3, False)
        wait_pod(f"{config.cluster}-mongos-0")
        wait_pod(f"{config.cluster}-mongos-1")

        for pvc in self.RS0_PVCS + self.RS1_PVCS + self.CFG_PVCS:
            assert _pvc_size(pvc) == self.INITIAL_SIZE, f"PVC {pvc} did not start at {self.INITIAL_SIZE}"

    @pytest.mark.dependency(depends=["TestPVCShrinkSharded::test_01_create_sharded_cluster"])
    def test_02_shrink_both_shards(self, config: PVCShrinkConfig) -> None:
        """Patch both rs0 and rs1 storage to 5Gi simultaneously."""
        _patch_replset_storage("rs0", self.SHRUNK_SIZE)
        _patch_replset_storage("rs1", self.SHRUNK_SIZE)
        time.sleep(10)

        assert _has_shrink_annotation(), "At least one shrink annotation should be set"

        _wait_no_shrink_annotation(max_attempts=240)

        for pvc in self.RS0_PVCS + self.RS1_PVCS:
            _wait_pvc_size(pvc, self.SHRUNK_SIZE)

        wait_cluster_consistency(config.cluster, 900)

    @pytest.mark.dependency(depends=["TestPVCShrinkSharded::test_02_shrink_both_shards"])
    def test_03_cfg_untouched(self) -> None:
        """Config server PVCs should not be affected by shard shrink."""
        for pvc in self.CFG_PVCS:
            assert _pvc_size(pvc) == self.INITIAL_SIZE, f"Config PVC {pvc} should still be {self.INITIAL_SIZE}"

    @pytest.mark.dependency(depends=["TestPVCShrinkSharded::test_02_shrink_both_shards"])
    def test_04_shrink_configsvr(self, config: PVCShrinkConfig) -> None:
        """Shrink config server PVCs after shard shrink completed."""
        _patch_cfg_storage(self.SHRUNK_SIZE)
        time.sleep(10)

        _wait_no_shrink_annotation()

        for pvc in self.CFG_PVCS:
            _wait_pvc_size(pvc, self.SHRUNK_SIZE)

        wait_cluster_consistency(config.cluster, 900)


# ---------------------------------------------------------------------------
# Helpers for mongos log PVC
# ---------------------------------------------------------------------------

def _patch_mongos_log_storage(size: str) -> None:
    kubectl_bin(
        "patch", "psmdb", CLUSTER, "--type=merge", "-p",
        json.dumps({
            "spec": {
                "sharding": {
                    "mongos": {
                        "logs": {"persistentVolumeClaim": {"resources": {"requests": {"storage": size}}}},
                    },
                },
            },
        }),
    )


def _mongos_log_storage() -> str:
    return kubectl_bin(
        "get", "psmdb", CLUSTER, "-o",
        "jsonpath={.spec.sharding.mongos.logs.persistentVolumeClaim.resources.requests.storage}",
    ).strip()


# ---------------------------------------------------------------------------
# Test class 3: mongos log PVC shrink (logcollector with PVC)
# ---------------------------------------------------------------------------

class TestPVCShrinkMongosLog:
    """Shrink mongos log PVCs (disposable data, no initial sync)."""

    INITIAL_SIZE = "3Gi"
    SHRUNK_SIZE = "2Gi"
    MONGOS_LOG_PVCS = [f"mongos-logs-{CLUSTER}-mongos-{i}" for i in range(2)]

    @pytest.fixture(scope="class", autouse=True)
    @classmethod
    def config(cls, create_infra: Callable[[str], str]) -> PVCShrinkConfig:
        platform = detect_platform()
        return PVCShrinkConfig(
            namespace=create_infra("pvc-shrink-mongos-log"),
            cluster=CLUSTER,
            platform=platform,
        )

    @pytest.fixture(scope="class", autouse=True)
    @classmethod
    def setup_tests(cls, config: PVCShrinkConfig, test_paths: Paths) -> None:
        kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/secrets.yml")
        _setup_storageclass(config.platform, test_paths)

    @pytest.mark.dependency()
    def test_01_create_cluster(self, config: PVCShrinkConfig, test_paths: Paths) -> None:
        apply_cluster(f"{test_paths['test_dir']}/conf/some-name-sharded-logcollector.yml")
        wait_for_running(f"{config.cluster}-rs0", 3)
        wait_for_running(f"{config.cluster}-cfg", 3, False)
        wait_pod(f"{config.cluster}-mongos-0")
        wait_pod(f"{config.cluster}-mongos-1")

        wait_cluster_consistency(config.cluster, 900)

        for pvc in self.MONGOS_LOG_PVCS:
            assert _pvc_size(pvc) == self.INITIAL_SIZE, f"PVC {pvc} did not start at {self.INITIAL_SIZE}"

    @pytest.mark.dependency(depends=["TestPVCShrinkMongosLog::test_01_create_cluster"])
    def test_02_shrink_mongos_log(self, config: PVCShrinkConfig) -> None:
        """Shrink mongos log PVCs from 3Gi to 2Gi."""
        _patch_mongos_log_storage(self.SHRUNK_SIZE)
        time.sleep(10)

        _wait_no_shrink_annotation(max_attempts=120)

        for pvc in self.MONGOS_LOG_PVCS:
            _wait_pvc_size(pvc, self.SHRUNK_SIZE)

        wait_pod(f"{config.cluster}-mongos-0")
        wait_pod(f"{config.cluster}-mongos-1")
        wait_cluster_consistency(config.cluster, 900)

    @pytest.mark.dependency(depends=["TestPVCShrinkMongosLog::test_02_shrink_mongos_log"])
    def test_03_expand_mongos_log(self, config: PVCShrinkConfig) -> None:
        """Expand mongos log PVCs back to 3Gi after shrink."""
        _patch_mongos_log_storage(self.INITIAL_SIZE)
        time.sleep(10)

        assert not _has_shrink_annotation(), "Shrink annotation should NOT be set for expand"

        for pvc in self.MONGOS_LOG_PVCS:
            _wait_pvc_size(pvc, self.INITIAL_SIZE)

        wait_cluster_consistency(config.cluster, 900)
