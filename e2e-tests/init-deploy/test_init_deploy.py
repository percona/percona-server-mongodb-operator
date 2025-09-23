#!/usr/bin/env python3

import pytest
import time
import logging

from types import SimpleNamespace

import tools

logger = logging.getLogger(__name__)


class TestInitDeploy:
    """Test MongoDB cluster deployment and operations"""

    @pytest.fixture(scope="class", autouse=True)
    def env(self, create_infra, destroy_infra, test_paths):
        """Setup test environment and cleanup after tests"""
        try:
            namespace = create_infra("init-deploy")
            tools.kubectl_bin(
                "apply",
                "-f",
                f"{test_paths['test_dir']}/conf/secrets_with_tls.yml",
                "-f",
                f"{test_paths['test_dir']}/../conf/client-70.yml",
            )
            tools.apply_runtime_class(test_paths["test_dir"])

            yield SimpleNamespace(
                test_dir=test_paths["test_dir"],
                conf_dir=test_paths["conf_dir"],
                src_dir=test_paths["src_dir"],
                namespace=namespace,
                cluster="some-name-rs0",
                cluster2="another-name-rs0",
                max_conn=17,
            )
        except Exception as e:
            pytest.fail(f"Environment setup failed: {e}")
        finally:
            destroy_infra(namespace)

    @pytest.mark.dependency()
    def test_create_first_cluster(self, env):
        """Create first PSMDB cluster"""
        tools.apply_cluster(f"{env.test_dir}/../conf/{env.cluster}.yml")
        tools.wait_for_running(env.cluster, 3)

        tools.compare_kubectl(env.test_dir, f"statefulset/{env.cluster}", env.namespace)
        tools.compare_kubectl(env.test_dir, f"service/{env.cluster}", env.namespace)

    @pytest.mark.dependency(depends=["TestInitDeploy::test_create_first_cluster"])
    def test_verify_users_created(self, env):
        """Check if users created with correct permissions"""
        secret_name = "some-users"

        # Test userAdmin user
        user = tools.get_user_data(secret_name, "MONGODB_USER_ADMIN_USER")
        password = tools.get_user_data(secret_name, "MONGODB_USER_ADMIN_PASSWORD")
        tools.compare_mongo_user(
            f"{user}:{password}@{env.cluster}.{env.namespace}", "userAdmin", env.test_dir
        )

        # Test backup user
        user = tools.get_user_data(secret_name, "MONGODB_BACKUP_USER")
        password = tools.get_user_data(secret_name, "MONGODB_BACKUP_PASSWORD")
        tools.compare_mongo_user(
            f"{user}:{password}@{env.cluster}.{env.namespace}", "backup", env.test_dir
        )

        # Test clusterAdmin user
        user = tools.get_user_data(secret_name, "MONGODB_CLUSTER_ADMIN_USER")
        password = tools.get_user_data(secret_name, "MONGODB_CLUSTER_ADMIN_PASSWORD")
        tools.compare_mongo_user(
            f"{user}:{password}@{env.cluster}.{env.namespace}", "clusterAdmin", env.test_dir
        )

        # Test clusterMonitor user
        user = tools.get_user_data(secret_name, "MONGODB_CLUSTER_MONITOR_USER")
        password = tools.get_user_data(secret_name, "MONGODB_CLUSTER_MONITOR_PASSWORD")
        tools.compare_mongo_user(
            f"{user}:{password}@{env.cluster}.{env.namespace}", "clusterMonitor", env.test_dir
        )

        # Test that unauthorized user is rejected
        result = tools.run_mongosh(
            "db.runCommand({connectionStatus:1,showPrivileges:true})",
            f"test:test@{env.cluster}.{env.namespace}",
        )
        assert "Authentication failed" in result

    @pytest.mark.dependency(depends=["TestInitDeploy::test_verify_users_created"])
    def test_write_and_read_data(self, env):
        """Write data and read from all nodes"""

        tools.run_mongosh(
            'db.createUser({user:"myApp",pwd:"myPass",roles:[{db:"myApp",role:"readWrite"}]})',
            f"userAdmin:userAdmin123456@{env.cluster}.{env.namespace}",
        )

        # Wait for user to be fully created
        time.sleep(2)

        tools.run_mongosh(
            "db.getSiblingDB('myApp').test.insertOne({ x: 100500 })",
            f"myApp:myPass@{env.cluster}.{env.namespace}",
        )

        for i in range(3):
            tools.compare_mongo_cmd(
                "find({}, { _id: 0 }).toArray()",
                f"myApp:myPass@{env.cluster}-{i}.{env.cluster}.{env.namespace}",
                test_file=f"{env.test_dir}/compare/find-1.json",
            )

    @pytest.mark.dependency(depends=["TestInitDeploy::test_write_and_read_data"])
    def test_connection_count(self, env):
        """Check number of connections doesn't exceed maximum"""
        conn_count = int(
            tools.run_mongosh(
                "db.serverStatus().connections.current",
                f"clusterAdmin:clusterAdmin123456@{env.cluster}.{env.namespace}",
            ).strip()
        )
        assert conn_count <= env.max_conn, (
            f"Connection count {conn_count} exceeds maximum {env.max_conn}"
        )

    @pytest.mark.dependency(depends=["TestInitDeploy::test_connection_count"])
    def test_primary_failover(self, env):
        """Kill Primary Pod, check reelection, check data"""
        initial_primary = tools.get_mongo_primary(
            f"clusterAdmin:clusterAdmin123456@{env.cluster}.{env.namespace}", env.cluster
        )
        assert initial_primary, "Failed to get initial primary"

        tools.kubectl_bin(
            "delete", "pods", "--grace-period=0", "--force", initial_primary, "-n", env.namespace
        )
        tools.wait_for_running(env.cluster, 3)

        changed_primary = tools.get_mongo_primary(
            f"clusterAdmin:clusterAdmin123456@{env.cluster}.{env.namespace}", env.cluster
        )
        assert initial_primary != changed_primary, "Primary didn't change after pod deletion"

        tools.run_mongosh(
            "db.getSiblingDB('myApp').test.insertOne({ x: 100501 })",
            f"myApp:myPass@{env.cluster}.{env.namespace}",
        )

        for i in range(3):
            tools.compare_mongo_cmd(
                "find({}, { _id: 0 }).toArray()",
                f"myApp:myPass@{env.cluster}-{i}.{env.cluster}.{env.namespace}",
                "-2nd",
                test_file=f"{env.test_dir}/compare/find-2.json",
            )

    @pytest.mark.dependency(depends=["TestInitDeploy::test_primary_failover"])
    def test_create_second_cluster(self, env):
        """Check if possible to create second cluster"""
        tools.apply_cluster(f"{env.test_dir}/conf/{env.cluster2}.yml")
        tools.wait_for_running(env.cluster2, 3)
        tools.compare_kubectl(env.test_dir, f"statefulset/{env.cluster2}", env.namespace)
        tools.compare_kubectl(env.test_dir, f"service/{env.cluster2}", env.namespace)

    @pytest.mark.dependency(depends=["TestInitDeploy::test_create_second_cluster"])
    def test_second_cluster_data_operations(self, env):
        """Write data and read from all nodes in second cluster"""
        # Create user
        tools.run_mongosh(
            'db.createUser({user:"myApp",pwd:"myPass",roles:[{db:"myApp",role:"readWrite"}]})',
            f"userAdmin:userAdmin123456@{env.cluster2}.{env.namespace}",
        )

        # Write data
        tools.run_mongosh(
            "db.getSiblingDB('myApp').test.insertOne({ x: 100502 })",
            f"myApp:myPass@{env.cluster2}.{env.namespace}",
        )

        # Read from all nodes
        for i in range(3):
            tools.compare_mongo_cmd(
                "find({}, { _id: 0 }).toArray()",
                f"myApp:myPass@{env.cluster2}-{i}.{env.cluster2}.{env.namespace}",
                "-3rd",
                test_file=f"{env.test_dir}/compare/find-3.json",
            )

    @pytest.mark.dependency(depends=["TestInitDeploy::test_second_cluster_data_operations"])
    def test_log_files_exist(self, env):
        """Check if mongod log files exist in pod"""
        result = tools.kubectl_bin(
            "exec", f"{env.cluster2}-0", "-c", "mongod", "--", "ls", "/data/db/logs"
        )

        assert "mongod.log" in result, "mongod.log not found"
        assert "mongod.full.log" in result, "mongod.full.log not found"


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
