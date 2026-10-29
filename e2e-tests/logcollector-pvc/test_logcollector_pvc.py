import json
import logging
import subprocess
import time
from collections.abc import Callable
from dataclasses import dataclass
from typing import ClassVar

import pytest
from lib.config import apply_cluster
from lib.kubectl import kubectl_bin, wait_cluster_consistency, wait_for_running
from lib.mongo import MongoManager
from lib.utils import Paths, retry

logger = logging.getLogger(__name__)

CLUSTER = "some-name"
RS = "rs0"
LOG_DIR = "/data/db/logs"
DATA_DIR = "/data/db"
LOG_CLAIM = "mongod-logs"
DATA_CLAIM = "mongod-data"
LOGROTATE_CONFIG = "/opt/percona/logcollector/logrotate/logrotate.conf"
LOGROTATE_STATUS = "/tmp/e2e-logrotate.status"


def _pods() -> list[str]:
    return [f"{CLUSTER}-{RS}-{i}" for i in range(3)]


def _log_pvcs() -> list[str]:
    return [f"{LOG_CLAIM}-{pod}" for pod in _pods()]


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


def _file_size(pod: str, container: str, path: str) -> int:
    return int(kubectl_bin("exec", pod, "-c", container, "--", "stat", "-c", "%s", path).strip())


def _exec_logrotate(pod: str, *command: str) -> str:
    return kubectl_bin("exec", pod, "-c", "logrotate", "--", *command)


def _log_storage_request() -> str:
    return kubectl_bin(
        "get",
        "psmdb",
        CLUSTER,
        "-o",
        "jsonpath={.spec.logcollector.storage.persistentVolumeClaim.resources.requests.storage}",
    ).strip()


def _patch_log_storage(size: str) -> None:
    kubectl_bin(
        "patch",
        "psmdb",
        CLUSTER,
        "--type=merge",
        "-p",
        json.dumps(
            {
                "spec": {
                    "logcollector": {
                        "storage": {
                            "persistentVolumeClaim": {
                                "accessModes": ["ReadWriteOnce"],
                                "resources": {"requests": {"storage": size}},
                            }
                        }
                    }
                }
            }
        ),
    )


def _remove_log_storage() -> None:
    kubectl_bin(
        "patch",
        "psmdb",
        CLUSTER,
        "--type=json",
        "-p",
        json.dumps([{"op": "remove", "path": "/spec/logcollector/storage"}]),
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


def _wait_log_growth(pod: str, path: str, max_attempts: int = 30, delay: int = 5) -> None:
    """Wait until the log collector appends to path."""
    initial = _file_size(pod, "logs", path)
    logger.info(f"Waiting for {path} on {pod} to grow past {initial} bytes")
    try:
        retry(
            lambda: _file_size(pod, "logs", path),
            max_attempts=max_attempts,
            delay=delay,
            condition=lambda size: size > initial,
        )
    except RuntimeError:
        pytest.fail(f"{path} on {pod} stopped growing at {initial} bytes")


def _generate_mongod_logs(psmdb_client: MongoManager, namespace: str) -> None:
    """Produce mongod log lines so fluent-bit has something to collect."""
    uri = f"testWriter:testWriter123456@{CLUSTER}-{RS}.{namespace}"
    for i in range(20):
        psmdb_client.run_mongosh(
            f"db.getSiblingDB('logStorage').noise.insertOne({{idx: {i}}})",
            uri,
            driver="mongodb",
        )


def _check_log_collection(pod: str) -> None:
    """fluent-bit tails mongod.log and writes mongod.full.log on the log volume."""
    for filename in ("mongod.log", "mongod.full.log"):
        kubectl_bin("exec", pod, "-c", "mongod", "--", "test", "-s", f"{LOG_DIR}/{filename}")
    _wait_log_growth(pod, f"{LOG_DIR}/mongod.full.log")


def _check_log_rotation(pod: str) -> None:
    """Force rotation and check the mongod log and the collected output are rotated."""
    logger.info(f"Forcing log rotation on {pod}")
    marker = f"e2e-logrotate-success-{time.time_ns()}"
    _exec_logrotate(
        pod,
        "sh",
        "-c",
        'printf "%s\\n" "$1" >> "$2"',
        "sh",
        marker,
        f"{LOG_DIR}/mongod.full.log",
    )
    old_inode = _exec_logrotate(pod, "stat", "-c", "%i", f"{LOG_DIR}/mongod.log").strip()

    _exec_logrotate(
        pod,
        "sh",
        "-c",
        'export PATH="${PATH}:/opt/fluent-bit/bin"; exec "$@"',
        "sh",
        "logrotate",
        "-f",
        "-s",
        LOGROTATE_STATUS,
        LOGROTATE_CONFIG,
    )

    new_inode = _exec_logrotate(pod, "stat", "-c", "%i", f"{LOG_DIR}/mongod.log").strip()
    assert new_inode != old_inode, f"mongod.log was not rotated on {pod}"
    _exec_logrotate(pod, "grep", "-Fq", marker, f"{LOG_DIR}/mongod.full.log.1")
    logger.info(
        f"Checking marker is absent from mongod.full.log on {pod}; grep exit code 1 is expected"
    )
    with pytest.raises(subprocess.CalledProcessError) as error:
        _exec_logrotate(pod, "grep", "-Fq", marker, f"{LOG_DIR}/mongod.full.log")
    assert error.value.returncode == 1, f"Could not check mongod.full.log on {pod}"


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


@dataclass(frozen=True)
class LogCollectorPVCConfig:
    namespace: str
    cluster: str


class TestLogCollectorPVC:
    """Move mongod logs from the data PVC to a dedicated log PVC and back."""

    INITIAL_SIZE = "2Gi"
    GROWN_SIZE = "3Gi"
    DATA_SIZE = "3Gi"
    DATA_UIDS: ClassVar[dict[str, str]] = {}

    @pytest.fixture(scope="class", autouse=True)
    @classmethod
    def config(cls, create_infra: Callable[[str], str]) -> LogCollectorPVCConfig:
        return LogCollectorPVCConfig(namespace=create_infra("logcollector-pvc"), cluster=CLUSTER)

    @pytest.fixture(scope="class", autouse=True)
    @classmethod
    def setup_tests(cls, config: LogCollectorPVCConfig, test_paths: Paths) -> None:
        kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/secrets.yml")

    @pytest.mark.dependency()
    def test_01_create_cluster_without_log_pvc(self, test_paths: Paths) -> None:
        """A cluster created without log storage keeps logs on the data volume."""
        apply_cluster(f"{test_paths['test_dir']}/conf/some-name.yml")
        wait_for_running(f"{CLUSTER}-{RS}", 3)

        assert sorted(_sts_claims()) == [DATA_CLAIM]
        for pvc in _log_pvcs():
            assert not _pvc_exists(pvc), (
                f"PVC {pvc} must not exist before log storage is configured"
            )

        for pod in _pods():
            assert _mount_device(pod, LOG_DIR) == _mount_device(pod, DATA_DIR), (
                f"{pod} must write logs to the data volume"
            )

    @pytest.mark.dependency(
        depends=["TestLogCollectorPVC::test_01_create_cluster_without_log_pvc"]
    )
    def test_02_seed_data(self, config: LogCollectorPVCConfig, psmdb_client: MongoManager) -> None:
        """Insert data and check log collection works on the data volume."""
        admin_uri = f"userAdmin:userAdmin123456@{CLUSTER}-{RS}.{config.namespace}"
        psmdb_client.run_mongosh(
            "db.createUser({user:'testWriter',pwd:'testWriter123456',"
            "roles:[{db:'logStorage',role:'readWrite'}]})",
            admin_uri,
            driver="mongodb",
        )

        uri = f"testWriter:testWriter123456@{CLUSTER}-{RS}.{config.namespace}"
        for i in range(100):
            psmdb_client.run_mongosh(
                f"db.getSiblingDB('logStorage').seedData.insertOne({{idx: {i}}})",
                uri,
                driver="mongodb",
            )

        for pod in _pods():
            _check_log_collection(pod)

        for pvc in _data_pvcs():
            self.DATA_UIDS[pvc] = _pvc_field(pvc, "{.metadata.uid}")

    @pytest.mark.dependency(depends=["TestLogCollectorPVC::test_02_seed_data"])
    def test_03_enable_log_pvc(
        self, config: LogCollectorPVCConfig, psmdb_client: MongoManager
    ) -> None:
        """Adding log storage recreates the statefulset and keeps the data PVCs."""
        _patch_log_storage(self.INITIAL_SIZE)
        _wait_sts_claims([DATA_CLAIM, LOG_CLAIM])
        wait_for_running(f"{CLUSTER}-{RS}", 3)
        wait_cluster_consistency(CLUSTER)

        for pvc in _log_pvcs():
            assert _pvc_field(pvc, "{.status.phase}") == "Bound", f"PVC {pvc} is not bound"
            assert _pvc_capacity(pvc) == self.INITIAL_SIZE

        for pvc, uid in self.DATA_UIDS.items():
            assert _pvc_field(pvc, "{.metadata.uid}") == uid, f"data PVC {pvc} was recreated"
            assert _pvc_capacity(pvc) == self.DATA_SIZE

        uri = f"testWriter:testWriter123456@{CLUSTER}-{RS}.{config.namespace}"
        count = psmdb_client.run_mongosh(
            "db.getSiblingDB('logStorage').seedData.countDocuments({})",
            uri,
            driver="mongodb",
        ).strip()
        assert count == "100", f"Expected 100 documents after the move, got {count}"

    @pytest.mark.dependency(depends=["TestLogCollectorPVC::test_03_enable_log_pvc"])
    def test_04_logs_land_on_the_log_pvc(self) -> None:
        """mongod and both sidecars mount the log volume and nothing else moved."""
        for pod in _pods():
            assert _mount_device(pod, LOG_DIR) != _mount_device(pod, DATA_DIR), (
                f"{pod} still writes logs to the data volume"
            )

            for container in ("mongod", "logs", "logrotate"):
                mounts = _container_mounts(pod, container)
                assert mounts.get(LOG_CLAIM) == LOG_DIR, (
                    f"container {container} in {pod} does not mount {LOG_CLAIM} at {LOG_DIR}"
                )

            assert _container_mounts(pod, "mongod").get(DATA_CLAIM) == DATA_DIR

    @pytest.mark.dependency(depends=["TestLogCollectorPVC::test_04_logs_land_on_the_log_pvc"])
    def test_05_log_collection_on_the_log_pvc(
        self, config: LogCollectorPVCConfig, psmdb_client: MongoManager
    ) -> None:
        """Collection keeps working on the dedicated PVC exactly as on the data PVC."""
        _generate_mongod_logs(psmdb_client, config.namespace)

        for pod in _pods():
            _check_log_collection(pod)

    @pytest.mark.dependency(depends=["TestLogCollectorPVC::test_05_log_collection_on_the_log_pvc"])
    def test_06_log_rotation_on_the_log_pvc(self) -> None:
        """logrotate rotates both the mongod log and the collected output."""
        for pod in _pods():
            _check_log_rotation(pod)

    @pytest.mark.dependency(depends=["TestLogCollectorPVC::test_06_log_rotation_on_the_log_pvc"])
    def test_07_resize_log_pvc(
        self, config: LogCollectorPVCConfig, psmdb_client: MongoManager
    ) -> None:
        """A larger request resizes the log PVCs and leaves the data PVCs alone."""
        if not _allows_volume_expansion():
            pytest.skip(
                f"default storageclass {_default_storageclass()} does not allow volume expansion"
            )

        _patch_log_storage(self.GROWN_SIZE)

        for pvc in _log_pvcs():
            _wait_pvc_capacity(pvc, self.GROWN_SIZE)
            assert _pvc_request(pvc) == self.GROWN_SIZE

        _wait_claim_size(LOG_CLAIM, self.GROWN_SIZE)
        wait_for_running(f"{CLUSTER}-{RS}", 3)
        wait_cluster_consistency(CLUSTER)

        assert _sts_claims()[DATA_CLAIM] == self.DATA_SIZE
        for pvc, uid in self.DATA_UIDS.items():
            assert _pvc_capacity(pvc) == self.DATA_SIZE, f"data PVC {pvc} was resized"
            assert _pvc_field(pvc, "{.metadata.uid}") == uid, f"data PVC {pvc} was recreated"

        _generate_mongod_logs(psmdb_client, config.namespace)
        for pod in _pods():
            _check_log_collection(pod)

    @pytest.mark.dependency(depends=["TestLogCollectorPVC::test_07_resize_log_pvc"])
    def test_08_shrink_is_rejected(self) -> None:
        """A smaller request is reverted instead of deleting log PVCs."""
        current = _log_storage_request()
        _patch_log_storage("1Gi")

        logger.info("Waiting for the operator to revert the log storage request")
        try:
            retry(
                _log_storage_request,
                max_attempts=30,
                delay=5,
                condition=lambda size: size == current,
            )
        except RuntimeError:
            pytest.fail(f"log storage request is {_log_storage_request()}, expected {current}")

        for pvc in _log_pvcs():
            assert _pvc_exists(pvc), f"PVC {pvc} must survive a rejected shrink"

    @pytest.mark.dependency(depends=["TestLogCollectorPVC::test_08_shrink_is_rejected"])
    def test_09_disable_log_pvc(
        self, config: LogCollectorPVCConfig, psmdb_client: MongoManager
    ) -> None:
        """Removing log storage moves logs back to the data volume."""
        _remove_log_storage()
        _wait_sts_claims([DATA_CLAIM])
        wait_for_running(f"{CLUSTER}-{RS}", 3)
        wait_cluster_consistency(CLUSTER)

        for pod in _pods():
            assert _mount_device(pod, LOG_DIR) == _mount_device(pod, DATA_DIR), (
                f"{pod} must write logs back to the data volume"
            )
            assert LOG_CLAIM not in _container_mounts(pod, "mongod")

        for pvc, uid in self.DATA_UIDS.items():
            assert _pvc_field(pvc, "{.metadata.uid}") == uid, f"data PVC {pvc} was recreated"

        _generate_mongod_logs(psmdb_client, config.namespace)
        for pod in _pods():
            _check_log_collection(pod)

        uri = f"testWriter:testWriter123456@{CLUSTER}-{RS}.{config.namespace}"
        count = psmdb_client.run_mongosh(
            "db.getSiblingDB('logStorage').seedData.countDocuments({})",
            uri,
            driver="mongodb",
        ).strip()
        assert count == "100", f"Expected 100 documents after disabling log storage, got {count}"
