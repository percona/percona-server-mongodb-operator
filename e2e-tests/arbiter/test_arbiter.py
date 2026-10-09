#!/usr/bin/env python3

import logging
import time
from collections.abc import Callable
from dataclasses import dataclass

import pytest
from lib.config import apply_cluster, compare_kubectl
from lib.kubectl import kubectl_bin, wait_for_running, wait_pod
from lib.mongo import MongoManager
from lib.utils import Paths

logger = logging.getLogger(__name__)


@dataclass(frozen=True)
class ArbiterConfig:
    namespace: str
    cluster: str


@pytest.fixture(scope="class", autouse=True)
def config(create_infra: Callable[[str], str]) -> ArbiterConfig:
    """Configuration for tests"""
    return ArbiterConfig(
        namespace=create_infra("arbiter"),
        cluster="arbiter-rs0",
    )


@pytest.fixture(scope="class", autouse=True)
def setup_tests(deploy_cert_manager: Callable[..., None], test_paths: Paths) -> None:
    """Setup test environment"""
    deploy_cert_manager("--enable-certificate-owner-ref")
    kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/secrets.yml")


class TestArbiter:
    """Test PSMDB cluster with an arbiter node"""

    @pytest.mark.dependency()
    def test_create_cluster(self, config: ArbiterConfig, test_paths: Paths) -> None:
        """Create cluster and check arbiter started with expected config"""
        apply_cluster(f"{test_paths['test_dir']}/conf/{config.cluster}.yml")

        wait_pod(f"{config.cluster}-arbiter-0")
        wait_for_running(config.cluster, 2, False)

        compare_kubectl(
            test_paths["test_dir"],
            f"statefulset/{config.cluster}-arbiter",
            config.namespace,
        )

    @pytest.mark.dependency(depends=["TestArbiter::test_create_cluster"])
    def test_arbiter_healthy_in_replset(
        self, config: ArbiterConfig, psmdb_client: MongoManager
    ) -> None:
        """Wait for convergence, check arbiter liveness and membership"""
        logger.info("Waiting 240s for the arbiter to converge and checking it doesn't restart")
        time.sleep(240)

        restart_count = kubectl_bin(
            "get",
            "pod",
            f"--selector=statefulset.kubernetes.io/pod-name={config.cluster}-arbiter-0",
            "-o",
            'jsonpath={.items[*].status.containerStatuses[?(@.name == "mongod-arbiter")].restartCount}',
        ).strip()
        assert restart_count in ("", "0"), f"Arbiter restarted (restartCount={restart_count})"

        members = psmdb_client.run_mongosh(
            "rs.conf().members.map(m => m.host).join('\\n')",
            f"clusterAdmin:clusterAdmin123456@{config.cluster}.{config.namespace}",
        )
        assert f"{config.cluster}-arbiter-0" in members, (
            f"Arbiter not found in replset config: {members}"
        )

    @pytest.mark.dependency(depends=["TestArbiter::test_arbiter_healthy_in_replset"])
    def test_write_and_read_data(
        self, config: ArbiterConfig, test_paths: Paths, psmdb_client: MongoManager
    ) -> None:
        """Create user, write data and read it from data nodes"""
        psmdb_client.run_mongosh(
            'db.createUser({user:"myApp",pwd:"myPass",roles:[{db:"myApp",role:"readWrite"}]})',
            f"userAdmin:userAdmin123456@{config.cluster}.{config.namespace}",
        )
        psmdb_client.run_mongosh(
            "db.getSiblingDB('myApp').test.insertOne({ x: 100500 })",
            f"myApp:myPass@{config.cluster}.{config.namespace}",
        )

        for i in range(2):
            psmdb_client.compare_mongo_cmd(
                "find({}, { _id: 0 }).toArray()",
                f"myApp:myPass@{config.cluster}-{i}.{config.cluster}.{config.namespace}",
                test_file=f"{test_paths['test_dir']}/compare/find-1.json",
            )

    @pytest.mark.dependency(depends=["TestArbiter::test_write_and_read_data"])
    def test_primary_failover(
        self, config: ArbiterConfig, test_paths: Paths, psmdb_client: MongoManager
    ) -> None:
        """Kill Primary Pod, check reelection, check data"""
        initial_primary = psmdb_client.get_mongo_primary(
            f"clusterAdmin:clusterAdmin123456@{config.cluster}.{config.namespace}",
            config.cluster,
        )
        assert initial_primary, "Failed to get initial primary"

        kubectl_bin(
            "delete",
            "pods",
            "--grace-period=0",
            "--force",
            initial_primary,
            "-n",
            config.namespace,
        )
        wait_for_running(config.cluster, 2, False)

        changed_primary = psmdb_client.get_mongo_primary(
            f"clusterAdmin:clusterAdmin123456@{config.cluster}.{config.namespace}",
            config.cluster,
        )
        assert initial_primary != changed_primary, "Primary didn't change after pod deletion"

        for i in range(2):
            psmdb_client.compare_mongo_cmd(
                "find({}, { _id: 0 }).toArray()",
                f"myApp:myPass@{config.cluster}-{i}.{config.cluster}.{config.namespace}",
                test_file=f"{test_paths['test_dir']}/compare/find-1.json",
            )
