#!/usr/bin/env python3

import logging
import time
from typing import Callable, Dict, TypedDict

import pytest
from lib.config import apply_cluster, apply_runtime_class, compare_kubectl
from lib.kubectl import kubectl_bin, wait_for_running
from lib.mongo import MongoManager
from lib.secrets import apply_s3_storage_secrets, get_user_data
from lib.utils import retry

logger = logging.getLogger(__name__)


class InitDeployConfig(TypedDict):
    namespace: str
    cluster: str
    cluster2: str
    max_conn: int


@pytest.fixture(scope="class", autouse=True)
def config(create_infra: Callable[[str], str]) -> InitDeployConfig:
    """Configuration for tests"""
    return {
        "namespace": create_infra("init-deploy"),
        "cluster": "some-name-rs0",
        "cluster2": "another-name-rs0",
        "max_conn": 17,
    }


@pytest.fixture(scope="class", autouse=True)
def setup_tests(test_paths: Dict[str, str]) -> None:
    """Setup test environment"""
    kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/secrets_with_tls.yml")
    apply_runtime_class(test_paths["test_dir"])


class TestInitDeploy:
    """Test MongoDB cluster deployment and operations"""

    @pytest.mark.dependency()
    def test_create_first_cluster(
        self, config: InitDeployConfig, test_paths: Dict[str, str]
    ) -> None:
        """Create first PSMDB cluster"""
        apply_cluster(f"{test_paths['test_dir']}/../conf/{config['cluster']}.yml")
        wait_for_running(config["cluster"], 3)

        compare_kubectl(
            test_paths["test_dir"], f"statefulset/{config['cluster']}", config["namespace"]
        )
        compare_kubectl(
            test_paths["test_dir"], f"service/{config['cluster']}", config["namespace"]
        )

    @pytest.mark.dependency(depends=["TestInitDeploy::test_create_first_cluster"])
    def test_verify_users_created(
        self,
        config: InitDeployConfig,
        test_paths: Dict[str, str],
        psmdb_client: MongoManager,
    ) -> None:
        """Check if users created with correct permissions"""
        secret_name = "some-users"

        # Test userAdmin user
        user = get_user_data(secret_name, "MONGODB_USER_ADMIN_USER")
        password = get_user_data(secret_name, "MONGODB_USER_ADMIN_PASSWORD")
        psmdb_client.compare_mongo_user(
            f"{user}:{password}@{config['cluster']}.{config['namespace']}",
            "userAdmin",
            test_paths["test_dir"],
        )

        # Test backup user
        user = get_user_data(secret_name, "MONGODB_BACKUP_USER")
        password = get_user_data(secret_name, "MONGODB_BACKUP_PASSWORD")
        psmdb_client.compare_mongo_user(
            f"{user}:{password}@{config['cluster']}.{config['namespace']}",
            "backup",
            test_paths["test_dir"],
        )

        # Test clusterAdmin user
        user = get_user_data(secret_name, "MONGODB_CLUSTER_ADMIN_USER")
        password = get_user_data(secret_name, "MONGODB_CLUSTER_ADMIN_PASSWORD")
        psmdb_client.compare_mongo_user(
            f"{user}:{password}@{config['cluster']}.{config['namespace']}",
            "clusterAdmin",
            test_paths["test_dir"],
        )

        # Test clusterMonitor user
        user = get_user_data(secret_name, "MONGODB_CLUSTER_MONITOR_USER")
        password = get_user_data(secret_name, "MONGODB_CLUSTER_MONITOR_PASSWORD")
        psmdb_client.compare_mongo_user(
            f"{user}:{password}@{config['cluster']}.{config['namespace']}",
            "clusterMonitor",
            test_paths["test_dir"],
        )

        # Test that unauthorized user is rejected
        result = psmdb_client.run_mongosh(
            "db.runCommand({connectionStatus:1,showPrivileges:true})",
            f"test:test@{config['cluster']}.{config['namespace']}",
        )
        assert "Authentication failed" in result

    @pytest.mark.dependency(depends=["TestInitDeploy::test_verify_users_created"])
    def test_write_and_read_data(
        self,
        config: InitDeployConfig,
        test_paths: Dict[str, str],
        psmdb_client: MongoManager,
    ) -> None:
        """Write data and read from all nodes"""

        psmdb_client.run_mongosh(
            'db.createUser({user:"myApp",pwd:"myPass",roles:[{db:"myApp",role:"readWrite"}]})',
            f"userAdmin:userAdmin123456@{config['cluster']}.{config['namespace']}",
        )

        retry(
            lambda: psmdb_client.run_mongosh(
                "db.getSiblingDB('myApp').test.insertOne({ x: 100500 })",
                f"myApp:myPass@{config['cluster']}.{config['namespace']}",
            ),
            condition=lambda result: "acknowledged: true" in result,
        )

        for i in range(3):
            psmdb_client.compare_mongo_cmd(
                "find({}, { _id: 0 }).toArray()",
                f"myApp:myPass@{config['cluster']}-{i}.{config['cluster']}.{config['namespace']}",
                test_file=f"{test_paths['test_dir']}/compare/find-1.json",
            )

    @pytest.mark.dependency(depends=["TestInitDeploy::test_write_and_read_data"])
    def test_connection_count(self, config: InitDeployConfig, psmdb_client: MongoManager) -> None:
        """Check number of connections doesn't exceed maximum"""
        conn_count = int(
            psmdb_client.run_mongosh(
                "db.serverStatus().connections.current",
                f"clusterAdmin:clusterAdmin123456@{config['cluster']}.{config['namespace']}",
            ).strip()
        )
        assert conn_count <= config["max_conn"], (
            f"Connection count {conn_count} exceeds maximum {config['max_conn']}"
        )

    @pytest.mark.dependency(depends=["TestInitDeploy::test_connection_count"])
    def test_primary_failover(
        self,
        config: InitDeployConfig,
        test_paths: Dict[str, str],
        psmdb_client: MongoManager,
    ) -> None:
        """Kill Primary Pod, check reelection, check data"""
        initial_primary = psmdb_client.get_mongo_primary(
            f"clusterAdmin:clusterAdmin123456@{config['cluster']}.{config['namespace']}",
            config["cluster"],
        )
        assert initial_primary, "Failed to get initial primary"

        kubectl_bin(
            "delete",
            "pods",
            "--grace-period=0",
            "--force",
            initial_primary,
            "-n",
            config["namespace"],
        )
        wait_for_running(config["cluster"], 3)

        changed_primary = psmdb_client.get_mongo_primary(
            f"clusterAdmin:clusterAdmin123456@{config['cluster']}.{config['namespace']}",
            config["cluster"],
        )
        assert initial_primary != changed_primary, "Primary didn't change after pod deletion"

        psmdb_client.run_mongosh(
            "db.getSiblingDB('myApp').test.insertOne({ x: 100501 })",
            f"myApp:myPass@{config['cluster']}.{config['namespace']}",
        )

        for i in range(3):
            psmdb_client.compare_mongo_cmd(
                "find({}, { _id: 0 }).toArray()",
                f"myApp:myPass@{config['cluster']}-{i}.{config['cluster']}.{config['namespace']}",
                "-2nd",
                test_file=f"{test_paths['test_dir']}/compare/find-2.json",
            )

    @pytest.mark.dependency(depends=["TestInitDeploy::test_primary_failover"])
    def test_create_second_cluster(
        self, config: InitDeployConfig, test_paths: Dict[str, str]
    ) -> None:
        """Check if possible to create second cluster"""
        apply_s3_storage_secrets(test_paths["conf_dir"])
        apply_cluster(f"{test_paths['test_dir']}/conf/{config['cluster2']}.yml")
        wait_for_running(config["cluster2"], 3)

        compare_kubectl(
            test_paths["test_dir"], f"statefulset/{config['cluster2']}", config["namespace"]
        )
        compare_kubectl(
            test_paths["test_dir"], f"service/{config['cluster2']}", config["namespace"]
        )

    @pytest.mark.dependency(depends=["TestInitDeploy::test_create_second_cluster"])
    def test_second_cluster_data_operations(
        self,
        config: InitDeployConfig,
        test_paths: Dict[str, str],
        psmdb_client: MongoManager,
    ) -> None:
        """Write data and read from all nodes in second cluster"""
        # Create user
        psmdb_client.run_mongosh(
            'db.createUser({user:"myApp",pwd:"myPass",roles:[{db:"myApp",role:"readWrite"}]})',
            f"userAdmin:userAdmin123456@{config['cluster2']}.{config['namespace']}",
        )

        # Write data
        psmdb_client.run_mongosh(
            "db.getSiblingDB('myApp').test.insertOne({ x: 100502 })",
            f"myApp:myPass@{config['cluster2']}.{config['namespace']}",
        )

        # Read from all nodes
        for i in range(3):
            psmdb_client.compare_mongo_cmd(
                "find({}, { _id: 0 }).toArray()",
                f"myApp:myPass@{config['cluster2']}-{i}.{config['cluster2']}.{config['namespace']}",
                "-3rd",
                test_file=f"{test_paths['test_dir']}/compare/find-3.json",
            )

    @pytest.mark.dependency(depends=["TestInitDeploy::test_second_cluster_data_operations"])
    def test_connection_count_with_backup(
        self, config: InitDeployConfig, psmdb_client: MongoManager
    ) -> None:
        """Check number of connections doesn't exceed maximum with backup enabled"""
        max_conn = 50
        time.sleep(300)  # Wait for backup agent connections

        conn_count = int(
            psmdb_client.run_mongosh(
                "db.serverStatus().connections.current",
                f"clusterAdmin:clusterAdmin123456@{config['cluster2']}.{config['namespace']}",
            ).strip()
        )
        assert conn_count <= max_conn, (
            f"Connection count {conn_count} exceeds maximum {max_conn} with backup enabled"
        )

    @pytest.mark.dependency(depends=["TestInitDeploy::test_connection_count_with_backup"])
    def test_log_files_exist(self, config: InitDeployConfig) -> None:
        """Check if mongod log files exist in pod"""
        result = kubectl_bin(
            "exec", f"{config['cluster2']}-0", "-c", "mongod", "--", "ls", "/data/db/logs"
        )

        assert "mongod.log" in result, "mongod.log not found"
        assert "mongod.full.log" in result, "mongod.full.log not found"


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
