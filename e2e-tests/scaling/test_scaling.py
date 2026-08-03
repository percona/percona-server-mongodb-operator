#!/usr/bin/env python3

import logging
from collections.abc import Callable
from dataclasses import dataclass

import pytest
import yaml
from lib.config import apply_cluster, compare_kubectl, render_cluster_config
from lib.kubectl import kubectl_bin, wait_for_delete, wait_for_running
from lib.mongo import MongoManager
from lib.utils import Paths, retry

logger = logging.getLogger(__name__)


@dataclass(frozen=True)
class ScalingConfig:
    namespace: str
    cluster: str
    psmdb: str


@pytest.fixture(scope="class", autouse=True)
def config(create_infra: Callable[[str], str]) -> ScalingConfig:
    """Configuration for tests"""
    return ScalingConfig(
        namespace=create_infra("scaling"),
        cluster="some-name-rs0",
        psmdb="some-name",
    )


@pytest.fixture(scope="class", autouse=True)
def setup_tests(test_paths: Paths) -> None:
    """Setup test environment"""
    kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/secrets.yml")


def _assert_no_mongod_pvcs() -> None:
    pvcs = kubectl_bin("get", "pvc", "-o", "name", check=False)
    assert "mongod-data" not in pvcs, "mongod-data PVCs still present"


def _scale(psmdb: str, size: int) -> None:
    kubectl_bin(
        "patch",
        "psmdb",
        psmdb,
        "--type=json",
        "-p",
        f'[{{"op": "replace", "path": "/spec/replsets/0/size", "value": {size}}}]',
    )


def _write_and_read_all(
    psmdb_client: MongoManager, config: ScalingConfig, test_paths: Paths, pods: range
) -> None:
    psmdb_client.run_mongosh(
        'db.createUser({user:"myApp",pwd:"myPass",roles:[{db:"myApp",role:"readWrite"}]})',
        f"userAdmin:userAdmin123456@{config.cluster}.{config.namespace}",
    )
    psmdb_client.run_mongosh(
        "db.getSiblingDB('myApp').test.insertOne({ x: 100500 })",
        f"myApp:myPass@{config.cluster}.{config.namespace}",
    )
    for i in pods:
        psmdb_client.compare_mongo_cmd(
            "find({}, { _id: 0 }).toArray()",
            f"myApp:myPass@{config.cluster}-{i}.{config.cluster}.{config.namespace}",
            test_file=f"{test_paths['test_dir']}/compare/find-1.json",
        )


class TestScaling:
    """Test scaling a PSMDB replica set up and down"""

    @pytest.mark.dependency()
    def test_create_cluster(
        self, config: ScalingConfig, test_paths: Paths, psmdb_client: MongoManager
    ) -> None:
        """Create cluster, write data and read it from all pods"""
        apply_cluster(f"{test_paths['conf_dir']}/{config.cluster}.yml")
        wait_for_running(config.cluster, 3)
        _write_and_read_all(psmdb_client, config, test_paths, range(3))

    @pytest.mark.dependency(depends=["TestScaling::test_create_cluster"])
    def test_scale_up(
        self, config: ScalingConfig, test_paths: Paths, psmdb_client: MongoManager
    ) -> None:
        """Scale up from 3 to 5 and check PVCs and data on new pods"""
        _scale(config.psmdb, 5)
        wait_for_running(config.cluster, 5)

        compare_kubectl(
            test_paths["test_dir"], f"pvc/mongod-data-{config.cluster}-3", config.namespace
        )
        compare_kubectl(
            test_paths["test_dir"], f"pvc/mongod-data-{config.cluster}-4", config.namespace
        )

        for i in (3, 4):
            psmdb_client.compare_mongo_cmd(
                "find({}, { _id: 0 }).toArray()",
                f"myApp:myPass@{config.cluster}-{i}.{config.cluster}.{config.namespace}",
                test_file=f"{test_paths['test_dir']}/compare/find-1.json",
            )

    @pytest.mark.dependency(depends=["TestScaling::test_scale_up"])
    def test_scale_down(self, config: ScalingConfig) -> None:
        """Scale down from 5 to 3 and check the extra pods are removed"""
        _scale(config.psmdb, 3)
        wait_for_delete(f"pod/{config.cluster}-3")
        wait_for_delete(f"pod/{config.cluster}-4")
        wait_for_running(config.cluster, 3)

    @pytest.mark.dependency(depends=["TestScaling::test_scale_down"])
    def test_scaling_on_exposed_cluster(
        self, config: ScalingConfig, test_paths: Paths, psmdb_client: MongoManager
    ) -> None:
        """Recreate the cluster with exposed pods and scale it down then up"""
        kubectl_bin("delete", "psmdb", "--all")
        kubectl_bin("delete", "pvc", "--all")
        retry(_assert_no_mongod_pvcs, max_attempts=15, delay=2)

        data = yaml.safe_load(
            render_cluster_config(f"{test_paths['conf_dir']}/{config.cluster}.yml")
        )
        data["spec"].setdefault("unsafeFlags", {})["replsetSize"] = True
        expose = data["spec"]["replsets"][0].setdefault("expose", {})
        expose["enabled"] = True
        expose["type"] = "ClusterIP"
        kubectl_bin("apply", "-f", "-", input_data=yaml.dump(data))
        wait_for_running(config.cluster, 3)

        _write_and_read_all(psmdb_client, config, test_paths, range(3))

        _scale(config.psmdb, 1)
        wait_for_delete(f"pod/{config.cluster}-1")
        wait_for_delete(f"pod/{config.cluster}-2")
        wait_for_running(config.cluster, 1)

        _scale(config.psmdb, 3)
        wait_for_running(config.cluster, 3)
