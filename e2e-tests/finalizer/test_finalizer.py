import pytest
import logging

import tools
from typing import Dict, Union

logger = logging.getLogger(__name__)


@pytest.fixture(scope="class", autouse=True)
def config(create_infra) -> Dict[str, Union[int, str]]:
    """Configuration for tests"""
    return {
        "namespace": create_infra("finalizer"),
        "cluster": "some-name",
    }


@pytest.fixture(scope="class", autouse=True)
def setup_tests(test_paths):
    """Setup test environment"""
    tools.kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/secrets_with_tls.yml")


class TestFinalizer:
    """Test MongoDB cluster finalizers"""

    @pytest.mark.dependency()
    def test_create_cluster(self, config, test_paths):
        tools.apply_cluster(f"{test_paths['test_dir']}/conf/{config['cluster']}.yml")
        tools.wait_for_running(f"{config['cluster']}-rs0", 3, False)
        tools.wait_for_running(f"{config['cluster']}-cfg", 3)

    @pytest.mark.dependency(depends=["TestFinalizer::test_create_cluster"])
    def test_kill_primary_should_elect_new_one(self, config, psmdb_client):
        primary = psmdb_client.get_mongo_primary(
            f"clusterAdmin:clusterAdmin123456@{config['cluster']}-rs0.{config['namespace']}",
            config["cluster"],
        )
        if primary == f"{config['cluster']}-rs0-0":
            tools.kubectl_bin("delete", "pod", "--grace-period=0", "--force", primary)
            tools.wait_for_running(f"{config['cluster']}-rs0", 3)
        new_primary = psmdb_client.get_mongo_primary(
            f"clusterAdmin:clusterAdmin123456@{config['cluster']}-rs0.{config['namespace']}",
            config["cluster"],
        )
        assert new_primary != primary, "Primary did not change after killing the pod"

    @pytest.mark.dependency(depends=["TestFinalizer::test_kill_primary_should_elect_new_one"])
    def test_delete_cluster(self, config):
        tools.kubectl_bin("delete", "psmdb", config["cluster"], "--wait=false")
        tools.wait_for_delete(f"psmdb/{config['cluster']}")

        tools.wait_for_delete(f"pvc/mongod-data-{config['cluster']}-cfg-0")
        tools.wait_for_delete(f"pvc/mongod-data-{config['cluster']}-cfg-1")
        tools.wait_for_delete(f"pvc/mongod-data-{config['cluster']}-cfg-2")
        tools.wait_for_delete(f"pvc/mongod-data-{config['cluster']}-rs0-0")
        tools.wait_for_delete(f"pvc/mongod-data-{config['cluster']}-rs0-1")
        tools.wait_for_delete(f"pvc/mongod-data-{config['cluster']}-rs0-2")
