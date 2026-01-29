#!/usr/bin/env python3

import pytest
import logging

from lib import tools
from typing import Callable, Dict, Union

logger = logging.getLogger(__name__)


@pytest.fixture(scope="class", autouse=True)
def config(create_infra: Callable[[str], str]) -> Dict[str, Union[int, str]]:
    """Configuration for tests"""
    return {
        "namespace": create_infra("init-deploy"),
        "cluster": "some-name-rs0",
        "cluster2": "another-name-rs0",
        "max_conn": 17,
    }


@pytest.fixture(scope="class", autouse=True)
def setup_tests(test_paths):
    """Setup test environment"""
    tools.kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/secrets_with_tls.yml")
    tools.apply_runtime_class(test_paths["test_dir"])


class TestInitDeploy:
    """Test MongoDB cluster deployment and operations"""

    @pytest.mark.dependency()
    def test_create_first_cluster(self, config, test_paths):
        """Create first PSMDB cluster"""
        tools.apply_cluster(f"{test_paths['test_dir']}/../conf/{config['cluster']}.yml")
        tools.wait_for_running(config["cluster"], 3)

        tools.compare_kubectl(
            test_paths["test_dir"], f"statefulset/{config['cluster']}", config["namespace"]
        )
        tools.compare_kubectl(
            test_paths["test_dir"], f"service/{config['cluster']}", config["namespace"]
        )

    @pytest.mark.dependency(depends=["TestInitDeploy::test_create_first_cluster"])
    def test_verify_users_created(self, config, test_paths, psmdb_client):
        """Check if users created with correct permissions"""
        secret_name = "some-users"

        # Test userAdmin user
        user = tools.get_user_data(secret_name, "MONGODB_USER_ADMIN_USER")
        password = tools.get_user_data(secret_name, "MONGODB_USER_ADMIN_PASSWORD")
        psmdb_client.compare_mongo_user(
            f"{user}:{password}@{config['cluster']}.{config['namespace']}",
            "userAdmin",
            test_paths["test_dir"],
        )

        # Test backup user
        user = tools.get_user_data(secret_name, "MONGODB_BACKUP_USER")
        password = tools.get_user_data(secret_name, "MONGODB_BACKUP_PASSWORD")
        psmdb_client.compare_mongo_user(
            f"{user}:{password}@{config['cluster']}.{config['namespace']}",
            "backup",
            test_paths["test_dir"],
        )

        # Test clusterAdmin user
        user = tools.get_user_data(secret_name, "MONGODB_CLUSTER_ADMIN_USER")
        password = tools.get_user_data(secret_name, "MONGODB_CLUSTER_ADMIN_PASSWORD")
        psmdb_client.compare_mongo_user(
            f"{user}:{password}@{config['cluster']}.{config['namespace']}",
            "clusterAdmin",
            test_paths["test_dir"],
        )

        # Test clusterMonitor user
        user = tools.get_user_data(secret_name, "MONGODB_CLUSTER_MONITOR_USER")
        password = tools.get_user_data(secret_name, "MONGODB_CLUSTER_MONITOR_PASSWORD")
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
    def test_write_and_read_data(self, config, test_paths, psmdb_client):
        """Write data and read from all nodes"""

        psmdb_client.run_mongosh(
            'db.createUser({user:"myApp",pwd:"myPass",roles:[{db:"myApp",role:"readWrite"}]})',
            f"userAdmin:userAdmin123456@{config['cluster']}.{config['namespace']}",
        )

        tools.retry(
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
    def test_connection_count(self, config, psmdb_client):
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
    def test_primary_failover(self, config, test_paths, psmdb_client):
        """Kill Primary Pod, check reelection, check data"""
        initial_primary = psmdb_client.get_mongo_primary(
            f"clusterAdmin:clusterAdmin123456@{config['cluster']}.{config['namespace']}",
            config["cluster"],
        )
        assert initial_primary, "Failed to get initial primary"

        tools.kubectl_bin(
            "delete",
            "pods",
            "--grace-period=0",
            "--force",
            initial_primary,
            "-n",
            config["namespace"],
        )
        tools.wait_for_running(config["cluster"], 3)

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
    def test_create_second_cluster(self, config, test_paths):
        """Check if possible to create second cluster"""
        tools.apply_cluster(f"{test_paths['test_dir']}/conf/{config['cluster2']}.yml")
        tools.wait_for_running(config["cluster2"], 3)
        tools.compare_kubectl(
            test_paths["test_dir"], f"statefulset/{config['cluster2']}", config["namespace"]
        )
        tools.compare_kubectl(
            test_paths["test_dir"], f"service/{config['cluster2']}", config["namespace"]
        )

    @pytest.mark.dependency(depends=["TestInitDeploy::test_create_second_cluster"])
    def test_second_cluster_data_operations(self, config, test_paths, psmdb_client):
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
    def test_log_files_exist(self, config):
        """Check if mongod log files exist in pod"""
        result = tools.kubectl_bin(
            "exec", f"{config['cluster2']}-0", "-c", "mongod", "--", "ls", "/data/db/logs"
        )

        assert "mongod.log" in result, "mongod.log not found"
        assert "mongod.full.log" in result, "mongod.full.log not found"


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
