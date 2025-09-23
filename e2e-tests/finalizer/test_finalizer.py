import pytest
import logging

from types import SimpleNamespace

import tools

logger = logging.getLogger(__name__)


class TestFinalizer:
    """Test MongoDB cluster finalizers"""

    @pytest.fixture(scope="class", autouse=True)
    def env(self, create_infra, destroy_infra, test_paths):
        """Setup test environment and cleanup after tests"""
        try:
            namespace = create_infra("finalizer")
            tools.kubectl_bin(
                "apply",
                "-f",
                f"{test_paths['conf_dir']}/secrets_with_tls.yml",
                "-f",
                f"{test_paths['conf_dir']}/client-70.yml",
            )

            yield SimpleNamespace(
                test_dir=test_paths["test_dir"],
                namespace=namespace,
                cluster="some-name",
            )
        except Exception as e:
            pytest.fail(f"Environment setup failed: {e}")
        finally:
            destroy_infra(namespace)

    @pytest.mark.dependency()
    def test_create_cluster(self, env):
        tools.apply_cluster(f"{env.test_dir}/conf/{env.cluster}.yml")
        tools.wait_for_running(f"{env.cluster}-rs0", 3)
        tools.wait_for_running(f"{env.cluster}-cfg", 3)

    @pytest.mark.dependency(depends=["TestFinalizer::test_create_cluster"])
    def test_kill_primary_should_elect_new_one(self, env):
        primary = tools.get_mongo_primary(
            f"clusterAdmin:clusterAdmin123456@{env.cluster}-rs0.{env.namespace}", env.cluster
        )
        if primary == f"{env.cluster}-rs0-0":
            tools.kubectl_bin("delete", "pod", "--grace-period=0", "--force", primary)
            tools.wait_for_running(f"{env.cluster}-rs0", 3)
        new_primary = tools.get_mongo_primary(
            f"clusterAdmin:clusterAdmin123456@{env.cluster}-rs0.{env.namespace}", env.cluster
        )
        assert new_primary != primary, "Primary did not change after killing the pod"

    @pytest.mark.dependency(depends=["TestFinalizer::test_kill_primary_should_elect_new_one"])
    def test_delete_cluster(self, env):
        tools.kubectl_bin("delete", "psmdb", env.cluster, "--wait=false")
        tools.wait_for_delete(f"psmdb/{env.cluster}")

        tools.wait_for_delete(f"pvc/mongod-data-{env.cluster}-cfg-0")
        tools.wait_for_delete(f"pvc/mongod-data-{env.cluster}-cfg-1")
        tools.wait_for_delete(f"pvc/mongod-data-{env.cluster}-cfg-2")
        tools.wait_for_delete(f"pvc/mongod-data-{env.cluster}-rs0-0")
        tools.wait_for_delete(f"pvc/mongod-data-{env.cluster}-rs0-1")
        tools.wait_for_delete(f"pvc/mongod-data-{env.cluster}-rs0-2")
