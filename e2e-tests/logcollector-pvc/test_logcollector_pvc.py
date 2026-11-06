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
LOG_FILLER = f"{LOG_DIR}/e2e-autoscaling.filler"


@dataclass(frozen=True)
class Component:
    sts: str
    size: int
    container: str
    patch_path: str
    jsonpath: str

    @property
    def pods(self) -> list[str]:
        return [f"{self.sts}-{i}" for i in range(self.size)]

    @property
    def log_pvcs(self) -> list[str]:
        return [f"{LOG_CLAIM}-{pod}" for pod in self.pods]

    @property
    def data_pvcs(self) -> list[str]:
        return [f"{DATA_CLAIM}-{pod}" for pod in self.pods]


MONGOD = Component(
    sts=f"{CLUSTER}-{RS}",
    size=3,
    container="mongod",
    patch_path="/spec/replsets/0",
    jsonpath=".spec.replsets[0]",
)
HIDDEN = Component(
    sts=f"{CLUSTER}-{RS}-hidden",
    size=1,
    container="mongod-hidden",
    patch_path="/spec/replsets/0/hidden",
    jsonpath=".spec.replsets[0].hidden",
)


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


def _pvc_uids(pvcs: list[str]) -> dict[str, str]:
    return {pvc: _pvc_field(pvc, "{.metadata.uid}") for pvc in pvcs}


def _pod_uids(component: Component) -> dict[str, str]:
    return {
        pod: kubectl_bin("get", "pod", pod, "-o", "jsonpath={.metadata.uid}").strip()
        for pod in component.pods
    }


def _sts_claims(component: Component) -> dict[str, str]:
    raw = kubectl_bin(
        "get",
        "sts",
        component.sts,
        "-o",
        "jsonpath={range .spec.volumeClaimTemplates[*]}{.metadata.name}={.spec.resources.requests.storage}{'\\n'}{end}",
    ).strip()
    return dict(line.split("=", 1) for line in raw.splitlines())


def _mount_device(component: Component, pod: str, path: str) -> str:
    output = kubectl_bin("exec", pod, "-c", component.container, "--", "df", "-P", path).strip()
    return output.splitlines()[-1].split()[0]


def _container_mounts(pod: str, container: str) -> dict[str, str]:
    spec = json.loads(kubectl_bin("get", "pod", pod, "-o", "json"))["spec"]
    target = next(c for c in spec["containers"] if c["name"] == container)
    return {m["name"]: m["mountPath"] for m in target["volumeMounts"]}


def _file_size(pod: str, container: str, path: str) -> int:
    return int(kubectl_bin("exec", pod, "-c", container, "--", "stat", "-c", "%s", path).strip())


def _exec_logrotate(pod: str, *command: str) -> str:
    return kubectl_bin("exec", pod, "-c", "logrotate", "--", *command)


def _log_storage_request(component: Component) -> str:
    return kubectl_bin(
        "get",
        "psmdb",
        CLUSTER,
        "-o",
        f"jsonpath={{{component.jsonpath}.logStorage.persistentVolumeClaim.resources.requests.storage}}",
    ).strip()


def _patch_log_storage(component: Component, size: str) -> None:
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
                    "path": f"{component.patch_path}/logStorage",
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


def _remove_log_storage(component: Component) -> None:
    kubectl_bin(
        "patch",
        "psmdb",
        CLUSTER,
        "--type=json",
        "-p",
        json.dumps([{"op": "remove", "path": f"{component.patch_path}/logStorage"}]),
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


def _fill_log_volume(component: Component, pod: str, target_percent: int) -> None:
    output = kubectl_bin(
        "exec", pod, "-c", component.container, "--", "df", "-P", "-k", LOG_DIR
    ).strip()
    fields = output.splitlines()[-1].split()
    total_kb, used_kb = int(fields[1]), int(fields[2])
    fill_mb = (total_kb * target_percent // 100 - used_kb) // 1024
    if fill_mb <= 0:
        return
    logger.info(f"Filling {LOG_DIR} on {pod} with {fill_mb}MiB to cross the autoscaling threshold")
    kubectl_bin(
        "exec",
        pod,
        "-c",
        component.container,
        "--",
        "dd",
        "if=/dev/zero",
        f"of={LOG_FILLER}",
        "bs=1M",
        f"count={fill_mb}",
        "status=none",
    )


def _remove_log_filler(component: Component, pod: str) -> None:
    kubectl_bin("exec", pod, "-c", component.container, "--", "rm", "-f", LOG_FILLER)


def _wait_sts_claims(
    component: Component, expected: list[str], max_attempts: int = 60, delay: int = 5
) -> None:
    logger.info(f"Waiting for {component.sts} volume claim templates {expected}")
    try:
        retry(
            lambda: sorted(_sts_claims(component)),
            max_attempts=max_attempts,
            delay=delay,
            condition=lambda claims: claims == sorted(expected),
        )
    except RuntimeError:
        pytest.fail(
            f"{component.sts} claim templates are {_sts_claims(component)}, expected {expected}"
        )


def _wait_claim_size(
    component: Component, claim: str, expected: str, max_attempts: int = 60, delay: int = 5
) -> None:
    logger.info(f"Waiting for {component.sts} claim template {claim} to request {expected}")
    try:
        retry(
            lambda: _sts_claims(component).get(claim),
            max_attempts=max_attempts,
            delay=delay,
            condition=lambda size: size == expected,
        )
    except RuntimeError:
        pytest.fail(
            f"{component.sts} claim template {claim} requests "
            f"{_sts_claims(component).get(claim)}, expected {expected}"
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


def _check_log_collection(component: Component, pod: str) -> None:
    """fluent-bit tails mongod.log and writes mongod.full.log on the log volume."""
    for filename in ("mongod.log", "mongod.full.log"):
        kubectl_bin(
            "exec", pod, "-c", component.container, "--", "test", "-s", f"{LOG_DIR}/{filename}"
        )
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


def _check_logs_on_log_pvc(component: Component) -> None:
    """mongod and both sidecars mount the log volume and nothing else moved."""
    for pod in component.pods:
        assert _mount_device(component, pod, LOG_DIR) != _mount_device(component, pod, DATA_DIR), (
            f"{pod} still writes logs to the data volume"
        )

        for container in (component.container, "logs", "logrotate"):
            mounts = _container_mounts(pod, container)
            assert mounts.get(LOG_CLAIM) == LOG_DIR, (
                f"container {container} in {pod} does not mount {LOG_CLAIM} at {LOG_DIR}"
            )

        assert _container_mounts(pod, component.container).get(DATA_CLAIM) == DATA_DIR


def _check_logs_on_data_pvc(component: Component) -> None:
    for pod in component.pods:
        assert _mount_device(component, pod, LOG_DIR) == _mount_device(component, pod, DATA_DIR), (
            f"{pod} must write logs to the data volume"
        )
        assert LOG_CLAIM not in _container_mounts(pod, component.container)


def _seed_count(psmdb_client: MongoManager, namespace: str) -> str:
    uri = f"testWriter:testWriter123456@{CLUSTER}-{RS}.{namespace}"
    return psmdb_client.run_mongosh(
        "db.getSiblingDB('logStorage').seedData.countDocuments({})",
        uri,
        driver="mongodb",
    ).strip()


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


def _skip_without_volume_expansion() -> None:
    if not _allows_volume_expansion():
        pytest.skip(
            f"default storageclass {_default_storageclass()} does not allow volume expansion"
        )


@dataclass(frozen=True)
class LogCollectorPVCConfig:
    namespace: str
    cluster: str


class TestLogCollectorPVC:
    """Move mongod logs to per-component log PVCs and back."""

    INITIAL_SIZE = "2Gi"
    GROWN_SIZE = "3Gi"
    HIDDEN_SIZE = "1Gi"
    HIDDEN_AUTOSCALED_SIZE = "2Gi"
    DATA_SIZE = "3Gi"
    DATA_UIDS: ClassVar[dict[str, str]] = {}
    HIDDEN_LOG_UIDS: ClassVar[dict[str, str]] = {}

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

        for component in (MONGOD, HIDDEN):
            assert sorted(_sts_claims(component)) == [DATA_CLAIM]
            for pvc in component.log_pvcs:
                assert not _pvc_exists(pvc), (
                    f"PVC {pvc} must not exist before log storage is configured"
                )
            _check_logs_on_data_pvc(component)

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

        for pod in MONGOD.pods:
            _check_log_collection(MONGOD, pod)

        self.DATA_UIDS.update(_pvc_uids(MONGOD.data_pvcs + HIDDEN.data_pvcs))

    @pytest.mark.dependency(depends=["TestLogCollectorPVC::test_02_seed_data"])
    def test_03_enable_log_pvc(
        self, config: LogCollectorPVCConfig, psmdb_client: MongoManager
    ) -> None:
        """Log storage on the replset recreates only its statefulset and keeps the data PVCs."""
        hidden_pods = _pod_uids(HIDDEN)

        _patch_log_storage(MONGOD, self.INITIAL_SIZE)
        _wait_sts_claims(MONGOD, [DATA_CLAIM, LOG_CLAIM])
        wait_for_running(f"{CLUSTER}-{RS}", 3)
        wait_cluster_consistency(CLUSTER)

        for pvc in MONGOD.log_pvcs:
            assert _pvc_field(pvc, "{.status.phase}") == "Bound", f"PVC {pvc} is not bound"
            assert _pvc_capacity(pvc) == self.INITIAL_SIZE

        for pvc, uid in self.DATA_UIDS.items():
            assert _pvc_field(pvc, "{.metadata.uid}") == uid, f"data PVC {pvc} was recreated"
            assert _pvc_capacity(pvc) == self.DATA_SIZE

        assert sorted(_sts_claims(HIDDEN)) == [DATA_CLAIM], "hidden must not get a log claim"
        for pvc in HIDDEN.log_pvcs:
            assert not _pvc_exists(pvc), f"PVC {pvc} must not be created for the hidden member"
        assert _pod_uids(HIDDEN) == hidden_pods, "hidden pods must not be restarted"

        count = _seed_count(psmdb_client, config.namespace)
        assert count == "100", f"Expected 100 documents after the move, got {count}"

    @pytest.mark.dependency(depends=["TestLogCollectorPVC::test_03_enable_log_pvc"])
    def test_04_logs_land_on_the_log_pvc(self) -> None:
        """mongod and both sidecars mount the log volume and nothing else moved."""
        _check_logs_on_log_pvc(MONGOD)
        _check_logs_on_data_pvc(HIDDEN)

    @pytest.mark.dependency(depends=["TestLogCollectorPVC::test_04_logs_land_on_the_log_pvc"])
    def test_05_log_collection_on_the_log_pvc(
        self, config: LogCollectorPVCConfig, psmdb_client: MongoManager
    ) -> None:
        """Collection keeps working on the dedicated PVC exactly as on the data PVC."""
        _generate_mongod_logs(psmdb_client, config.namespace)

        for pod in MONGOD.pods:
            _check_log_collection(MONGOD, pod)

    @pytest.mark.dependency(depends=["TestLogCollectorPVC::test_05_log_collection_on_the_log_pvc"])
    def test_06_log_rotation_on_the_log_pvc(self) -> None:
        """logrotate rotates both the mongod log and the collected output."""
        for pod in MONGOD.pods:
            _check_log_rotation(pod)

    @pytest.mark.dependency(depends=["TestLogCollectorPVC::test_06_log_rotation_on_the_log_pvc"])
    def test_07_enable_hidden_log_pvc(self) -> None:
        """The hidden member gets its own log PVC size without touching the replset."""
        mongod_pods = _pod_uids(MONGOD)
        mongod_log_uids = _pvc_uids(MONGOD.log_pvcs)

        _patch_log_storage(HIDDEN, self.HIDDEN_SIZE)
        _wait_sts_claims(HIDDEN, [DATA_CLAIM, LOG_CLAIM])
        wait_for_running(f"{CLUSTER}-{RS}", 3)
        wait_cluster_consistency(CLUSTER)

        assert _sts_claims(HIDDEN)[LOG_CLAIM] == self.HIDDEN_SIZE
        for pvc in HIDDEN.log_pvcs:
            assert _pvc_capacity(pvc) == self.HIDDEN_SIZE

        _check_logs_on_log_pvc(HIDDEN)
        for pod in HIDDEN.pods:
            _check_log_collection(HIDDEN, pod)
            _check_log_rotation(pod)

        assert _sts_claims(MONGOD)[LOG_CLAIM] == self.INITIAL_SIZE
        assert _pvc_uids(MONGOD.log_pvcs) == mongod_log_uids, "replset log PVCs were recreated"
        assert _pod_uids(MONGOD) == mongod_pods, "replset pods must not be restarted"

        self.HIDDEN_LOG_UIDS.update(_pvc_uids(HIDDEN.log_pvcs))

    @pytest.mark.dependency(depends=["TestLogCollectorPVC::test_07_enable_hidden_log_pvc"])
    def test_08_resize_log_pvc(
        self, config: LogCollectorPVCConfig, psmdb_client: MongoManager
    ) -> None:
        """A larger replset request resizes its log PVCs only."""
        _skip_without_volume_expansion()

        _patch_log_storage(MONGOD, self.GROWN_SIZE)

        for pvc in MONGOD.log_pvcs:
            _wait_pvc_capacity(pvc, self.GROWN_SIZE)
            assert _pvc_request(pvc) == self.GROWN_SIZE

        _wait_claim_size(MONGOD, LOG_CLAIM, self.GROWN_SIZE)
        wait_for_running(f"{CLUSTER}-{RS}", 3)
        wait_cluster_consistency(CLUSTER)

        assert _sts_claims(MONGOD)[DATA_CLAIM] == self.DATA_SIZE
        for pvc, uid in self.DATA_UIDS.items():
            assert _pvc_capacity(pvc) == self.DATA_SIZE, f"data PVC {pvc} was resized"
            assert _pvc_field(pvc, "{.metadata.uid}") == uid, f"data PVC {pvc} was recreated"

        assert _sts_claims(HIDDEN)[LOG_CLAIM] == self.HIDDEN_SIZE
        for pvc in HIDDEN.log_pvcs:
            assert _pvc_request(pvc) == self.HIDDEN_SIZE, f"hidden log PVC {pvc} was resized"

        _generate_mongod_logs(psmdb_client, config.namespace)
        for pod in MONGOD.pods:
            _check_log_collection(MONGOD, pod)

    @pytest.mark.dependency(depends=["TestLogCollectorPVC::test_08_resize_log_pvc"])
    def test_09_shrink_is_rejected(self) -> None:
        """A smaller request is reverted instead of deleting log PVCs."""
        current = _log_storage_request(MONGOD)
        _patch_log_storage(MONGOD, "1Gi")

        logger.info("Waiting for the operator to revert the log storage request")
        try:
            retry(
                lambda: _log_storage_request(MONGOD),
                max_attempts=30,
                delay=5,
                condition=lambda size: size == current,
            )
        except RuntimeError:
            pytest.fail(
                f"log storage request is {_log_storage_request(MONGOD)}, expected {current}"
            )

        for pvc in MONGOD.log_pvcs:
            assert _pvc_exists(pvc), f"PVC {pvc} must survive a rejected shrink"

    @pytest.mark.dependency(depends=["TestLogCollectorPVC::test_09_shrink_is_rejected"])
    def test_10_autoscaling_grows_only_the_full_component(self) -> None:
        """Filling the hidden log PVC grows the hidden log volume and nothing else."""
        _skip_without_volume_expansion()

        mongod_request = _log_storage_request(MONGOD)
        _patch_autoscaling(threshold=50, growth_step="1Gi", max_size=self.HIDDEN_AUTOSCALED_SIZE)

        try:
            for pod in HIDDEN.pods:
                _fill_log_volume(HIDDEN, pod, target_percent=70)

            for pvc in HIDDEN.log_pvcs:
                _wait_pvc_capacity(pvc, self.HIDDEN_AUTOSCALED_SIZE)

            _wait_claim_size(HIDDEN, LOG_CLAIM, self.HIDDEN_AUTOSCALED_SIZE)
            wait_for_running(f"{CLUSTER}-{RS}", 3)
            wait_cluster_consistency(CLUSTER)

            assert _log_storage_request(HIDDEN) == self.HIDDEN_AUTOSCALED_SIZE, (
                "autoscaling must raise the hidden log request in the CR"
            )
            assert _log_storage_request(MONGOD) == mongod_request, (
                "autoscaling the hidden log volume must not change the replset request"
            )
            assert _sts_claims(MONGOD)[LOG_CLAIM] == mongod_request
            for pvc in MONGOD.log_pvcs:
                assert _pvc_request(pvc) == mongod_request, f"replset log PVC {pvc} was resized"
            assert _sts_claims(HIDDEN)[DATA_CLAIM] == self.DATA_SIZE, (
                "the hidden data claim stays below the threshold and must not grow"
            )
        finally:
            for pod in HIDDEN.pods:
                _remove_log_filler(HIDDEN, pod)

    @pytest.mark.dependency(
        depends=["TestLogCollectorPVC::test_10_autoscaling_grows_only_the_full_component"]
    )
    def test_11_disable_log_pvc(
        self, config: LogCollectorPVCConfig, psmdb_client: MongoManager
    ) -> None:
        """Removing replset log storage moves its logs back; the hidden member keeps its PVC."""
        _remove_log_storage(MONGOD)
        _wait_sts_claims(MONGOD, [DATA_CLAIM])
        wait_for_running(f"{CLUSTER}-{RS}", 3)
        wait_cluster_consistency(CLUSTER)

        _check_logs_on_data_pvc(MONGOD)
        for pvc, uid in self.DATA_UIDS.items():
            assert _pvc_field(pvc, "{.metadata.uid}") == uid, f"data PVC {pvc} was recreated"

        assert sorted(_sts_claims(HIDDEN)) == [DATA_CLAIM, LOG_CLAIM]
        assert _pvc_uids(HIDDEN.log_pvcs) == self.HIDDEN_LOG_UIDS, "hidden log PVCs were recreated"
        _check_logs_on_log_pvc(HIDDEN)

        _generate_mongod_logs(psmdb_client, config.namespace)
        for pod in MONGOD.pods:
            _check_log_collection(MONGOD, pod)

        count = _seed_count(psmdb_client, config.namespace)
        assert count == "100", f"Expected 100 documents after disabling log storage, got {count}"
