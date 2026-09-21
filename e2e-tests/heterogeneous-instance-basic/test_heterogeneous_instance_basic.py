#!/usr/bin/env python3

import logging
from collections.abc import Callable
from dataclasses import dataclass
from typing import Any

import pytest
from lib import instances
from lib.config import apply_cluster
from lib.instances import InstanceGroup
from lib.kubectl import kubectl_bin
from lib.mongo import MongoManager
from lib.utils import Paths

logger = logging.getLogger(__name__)

# The mongod configuration inst1 is given part-way through, to prove that a
# per-instance change rolls that group and only that group. slowOpThresholdMs
# differs from the replica-set wide value every other group still inherits.
INST1_CONFIGURATION = """operationProfiling:
  mode: slowOp
  slowOpThresholdMs: 200
"""


@dataclass(frozen=True)
class HeterogeneousConfig:
    namespace: str
    psmdb: str
    cluster: str


@pytest.fixture(scope="class", autouse=True)
def config(create_infra: Callable[[str], str]) -> HeterogeneousConfig:
    """Configuration for tests"""
    return HeterogeneousConfig(
        namespace=create_infra("heterogeneous-instance-basic"),
        psmdb="heterogeneous",
        cluster="heterogeneous-rs0",
    )


@pytest.fixture(scope="class", autouse=True)
def setup_tests(test_paths: Paths) -> None:
    """Setup test environment"""
    kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/secrets.yml")


def admin_uri(config: HeterogeneousConfig) -> str:
    return f"clusterAdmin:clusterAdmin123456@{config.cluster}.{config.namespace}"


def app_uri(config: HeterogeneousConfig, host: str | None = None) -> str:
    return f"myApp:myPass@{host or config.cluster}.{config.namespace}"


def read_members(
    config: HeterogeneousConfig, psmdb_client: MongoManager
) -> dict[str, dict[str, Any]]:
    """rs.conf().members, keyed by pod name."""
    return instances.member_config(
        psmdb_client.run_mongosh("EJSON.stringify(rs.conf().members)", admin_uri(config))
    )


def groups(config: HeterogeneousConfig) -> dict[str, InstanceGroup]:
    return {g.name: g for g in instances.read_instance_groups(config.psmdb)}


class TestHeterogeneousInstanceBasic:
    """A replica set built entirely from instances[]: two custom groups plus an
    arbiter, a non-voting group and a hidden one, with no group named mongod."""

    @pytest.mark.dependency()
    def test_create_cluster(self, config: HeterogeneousConfig, test_paths: Paths) -> None:
        """Create the cluster and check every group produced the objects it should"""
        apply_cluster(f"{test_paths['test_dir']}/conf/{config.cluster}.yml")
        instances.wait_for_instances_running(config.psmdb)

        declared = groups(config)
        assert sorted(declared) == ["arbiter", "hidden", "inst1", "inst2", "nonVoting"]

        # No group is named mongod, so the bare replset StatefulSet must not
        # exist: every workload carries its group's suffix.
        expected_sts = {g.statefulset for g in declared.values()}
        assert expected_sts == {
            f"{config.cluster}-inst1",
            f"{config.cluster}-inst2",
            f"{config.cluster}-arbiter",
            f"{config.cluster}-nv",
            f"{config.cluster}-hidden",
        }
        live_sts = instances.statefulset_names(config.psmdb)
        assert config.cluster not in live_sts, (
            f"a bare {config.cluster} StatefulSet exists although no group is named mongod"
        )
        assert live_sts == expected_sts

        # One headless service fronts every group, which is what makes each
        # pod's DNS name resolvable regardless of which workload it belongs to.
        assert (
            kubectl_bin(
                "get", "service", config.cluster, "-o", "jsonpath={.spec.clusterIP}"
            ).strip()
            == "None"
        )

        for group in declared.values():
            replicas = kubectl_bin(
                "get", "sts", group.statefulset, "-o", "jsonpath={.spec.replicas}"
            ).strip()
            assert replicas == str(group.replicas), (
                f"{group.statefulset} has {replicas} replicas, declared {group.replicas}"
            )

            containers = kubectl_bin(
                "get",
                "sts",
                group.statefulset,
                "-o",
                "jsonpath={.spec.template.spec.containers[*].name}",
            ).split()
            assert group.container in containers, (
                f"{group.statefulset} runs {containers}, expected {group.container}"
            )

            service = kubectl_bin(
                "get", "sts", group.statefulset, "-o", "jsonpath={.spec.serviceName}"
            ).strip()
            assert service == config.cluster

            # The component label is how the operator finds a group's pods, and
            # for a reserved name it is not the group name.
            component = kubectl_bin(
                "get",
                "sts",
                group.statefulset,
                "-o",
                "jsonpath={.metadata.labels.app\\.kubernetes\\.io/component}",
            ).strip()
            assert component == group.component, (
                f"{group.statefulset} is labelled component={component}, "
                f"expected {group.component}"
            )

        # A group gets a mongod ConfigMap only when its resolved configuration
        # is non-empty, and where that configuration comes from is whatever the
        # group's legacy counterpart did (InstanceSpec.resolveConfiguration):
        #
        #   inst1, inst2  custom names, so they inherit replsets[].configuration
        #   arbiter       has no mongod group to borrow from, so it inherits too
        #                 -- under its own -arbiter suffix, not the shared
        #                 -mongod one it would share if a mongod group existed
        #   nonVoting     read only their own configuration and never inherit,
        #   hidden        exactly as the legacy nonVoting/hidden blocks did, so
        #                 with none set here they get no ConfigMap at all
        configmaps = kubectl_bin(
            "get", "cm", "-o", "jsonpath={range .items[*]}{.metadata.name}{'\\n'}{end}"
        ).split()
        for suffix in ("inst1", "inst2", "arbiter"):
            assert f"{config.cluster}-{suffix}" in configmaps, (
                f"no ConfigMap {config.cluster}-{suffix} in {configmaps}"
            )
        for suffix in ("nv", "hidden"):
            assert f"{config.cluster}-{suffix}" not in configmaps, (
                f"{config.cluster}-{suffix} inherited replsets[].configuration, which "
                f"the legacy nonVoting/hidden blocks never did: {configmaps}"
            )
        assert f"{config.cluster}-mongod" not in configmaps, (
            f"the arbiter fell back to the legacy shared ConfigMap: {configmaps}"
        )

        # Data-bearing groups get a claim per member; the arbiter's dbPath is an
        # emptyDir, so it gets none.
        pvcs = kubectl_bin(
            "get", "pvc", "-o", "jsonpath={range .items[*]}{.metadata.name}{'\\n'}{end}"
        ).split()
        for group in declared.values():
            for pvc in group.pvcs:
                assert pvc in pvcs, f"no PVC {pvc} for group {group.name}"
        assert not any(pvc.startswith(f"mongod-data-{config.cluster}-arbiter") for pvc in pvcs), (
            f"the arbiter must not claim storage, found {pvcs}"
        )

    @pytest.mark.dependency(depends=["TestHeterogeneousInstanceBasic::test_create_cluster"])
    def test_write_and_read_data(
        self, config: HeterogeneousConfig, test_paths: Paths, psmdb_client: MongoManager
    ) -> None:
        """Create a user, write through the replica set and read back from every data pod"""
        psmdb_client.run_mongosh(
            'db.createUser({user:"myApp",pwd:"myPass",roles:[{db:"myApp",role:"readWrite"}]})',
            f"userAdmin:userAdmin123456@{config.cluster}.{config.namespace}",
        )
        psmdb_client.run_mongosh(
            "db.getSiblingDB('myApp').test.insertOne({ x: 100500 })", app_uri(config)
        )

        # Every data-bearing member has to converge, including the hidden one
        # that normal secondary routing would never reach. The reads are direct
        # so each one is answered by that member and not by the primary.
        for group in groups(config).values():
            if not group.data_bearing:
                continue
            for pod in group.pods:
                psmdb_client.compare_mongo_cmd(
                    "find({}, { _id: 0 }).toArray()",
                    app_uri(config, f"{pod}.{config.cluster}"),
                    test_file=f"{test_paths['test_dir']}/compare/find-1.json",
                    direct=True,
                )

    @pytest.mark.dependency(depends=["TestHeterogeneousInstanceBasic::test_write_and_read_data"])
    def test_pause_and_unpause(
        self, config: HeterogeneousConfig, test_paths: Paths, psmdb_client: MongoManager
    ) -> None:
        """Pause until every member pod is gone, then bring the whole topology back"""
        instances.pause(config.psmdb, True)
        instances.wait_for_paused(config.psmdb)

        # Pausing scales the workloads to zero but keeps them, and it must not
        # touch the claims: the data has to be there when the cluster comes back.
        declared = groups(config)
        assert instances.statefulset_names(config.psmdb) == {
            g.statefulset for g in declared.values()
        }
        pvcs = kubectl_bin(
            "get", "pvc", "-o", "jsonpath={range .items[*]}{.metadata.name}{'\\n'}{end}"
        ).split()
        for group in declared.values():
            for pvc in group.pvcs:
                assert pvc in pvcs, f"pausing removed {pvc}"

        instances.pause(config.psmdb, False)
        instances.wait_for_instances_running(config.psmdb)

        psmdb_client.compare_mongo_cmd(
            "find({}, { _id: 0 }).toArray()",
            app_uri(config),
            test_file=f"{test_paths['test_dir']}/compare/find-1.json",
        )

    @pytest.mark.dependency(depends=["TestHeterogeneousInstanceBasic::test_pause_and_unpause"])
    def test_instance_change_rolls_only_that_group(self, config: HeterogeneousConfig) -> None:
        """Give inst1 its own configuration: inst1 restarts, nothing else does"""
        declared = groups(config)
        inst1 = declared["inst1"]
        before = instances.pod_identities(config.psmdb)
        assert set(inst1.pods) <= set(before), f"inst1 pods missing from {sorted(before)}"

        instances.set_instance_configuration(config.psmdb, "inst1", INST1_CONFIGURATION)

        # A pod is rolled when it comes back with both a new uid and a new
        # controller-revision-hash. Poll rather than wait on the cluster state:
        # the operator may not have left "ready" yet when this first runs.
        def rolled() -> bool:
            current = instances.pod_identities(config.psmdb)
            return all(pod in current and current[pod] != before[pod] for pod in inst1.pods)

        instances.wait_until("inst1 pods to be replaced", rolled, timeout=900)
        instances.wait_for_instances_running(config.psmdb)

        after = instances.pod_identities(config.psmdb)
        untouched = {
            pod: identity
            for name, group in declared.items()
            if name != "inst1"
            for pod in group.pods
            if (identity := before.get(pod)) is not None
        }
        assert {pod: after.get(pod) for pod in untouched} == untouched, (
            "a group other than inst1 was rolled by a change to inst1's configuration"
        )

        # The change reached the group's own ConfigMap, not the shared one.
        rendered = kubectl_bin(
            "get", "cm", f"{config.cluster}-inst1", "-o", "jsonpath={.data.mongod\\.conf}"
        )
        assert "slowOpThresholdMs: 200" in rendered, rendered
        sibling = kubectl_bin(
            "get", "cm", f"{config.cluster}-inst2", "-o", "jsonpath={.data.mongod\\.conf}"
        )
        assert "slowOpThresholdMs: 100" in sibling, sibling

    @pytest.mark.dependency(
        depends=["TestHeterogeneousInstanceBasic::test_instance_change_rolls_only_that_group"]
    )
    def test_scale_up_inst1(
        self, config: HeterogeneousConfig, test_paths: Paths, psmdb_client: MongoManager
    ) -> None:
        """Scale inst1 from 3 to 5 and check the new members join and carry data

        Two at a time, not one: inst1's members vote, and the operator rejects a
        spec whose voting member count is even. That lands on 5 + 1 (arbiter) +
        1 (hidden) = 7 voters, which is exactly MongoDB's ceiling and therefore
        accepted -- the check rejects more than 7, not 7.
        """
        instances.scale_instance(config.psmdb, "inst1", 5)
        instances.wait_for_instances_running(config.psmdb)

        inst1 = groups(config)["inst1"]
        assert inst1.replicas == 5

        members = read_members(config, psmdb_client)
        for pod in inst1.pods:
            assert pod in members, f"{pod} did not join the replica set: {sorted(members)}"

        # Directly, so this proves the new members were seeded rather than that
        # the replica set is still reachable through their DNS names.
        for pod in (f"{config.cluster}-inst1-3", f"{config.cluster}-inst1-4"):
            psmdb_client.compare_mongo_cmd(
                "find({}, { _id: 0 }).toArray()",
                app_uri(config, f"{pod}.{config.cluster}"),
                test_file=f"{test_paths['test_dir']}/compare/find-1.json",
                direct=True,
            )

    @pytest.mark.dependency(depends=["TestHeterogeneousInstanceBasic::test_scale_up_inst1"])
    def test_remove_inst2(self, config: HeterogeneousConfig, psmdb_client: MongoManager) -> None:
        """Drop inst2 from instances[] and check its workload and members go"""
        retired = groups(config)["inst2"]

        instances.remove_instance(config.psmdb, "inst2")

        # The operator drops one member per reconciliation, so the workload
        # empties over several passes before it is deleted.
        instances.wait_for_gone("sts", [retired.statefulset], timeout=900)
        instances.wait_for_gone("pods", retired.pods, timeout=900)

        instances.wait_for_instances_running(config.psmdb)

        assert "inst2" not in groups(config)
        assert retired.statefulset not in instances.statefulset_names(config.psmdb)

        members = read_members(config, psmdb_client)
        assert not [pod for pod in members if pod.startswith(f"{config.cluster}-inst2")], (
            f"retired inst2 members are still in rs.conf(): {sorted(members)}"
        )

    @pytest.mark.dependency(depends=["TestHeterogeneousInstanceBasic::test_remove_inst2"])
    def test_member_configuration(
        self, config: HeterogeneousConfig, psmdb_client: MongoManager
    ) -> None:
        """Every surviving member carries the rsConfig its group declared"""
        declared = groups(config)
        members = read_members(config, psmdb_client)

        expected_pods = {pod for group in declared.values() for pod in group.pods}
        assert set(members) == expected_pods, (
            f"rs.conf() members {sorted(members)} do not match the declared "
            f"topology {sorted(expected_pods)}"
        )

        # votes, priority, hidden, arbiterOnly per group, as declared in the CR
        # and adjusted after the scale-up and the removal.
        expected = {
            "inst1": {"votes": 1, "priority": 2, "hidden": False, "arbiterOnly": False},
            "arbiter": {"votes": 1, "priority": 0, "hidden": False, "arbiterOnly": True},
            "nonVoting": {"votes": 0, "priority": 0, "hidden": False, "arbiterOnly": False},
            "hidden": {"votes": 1, "priority": 0, "hidden": True, "arbiterOnly": False},
        }
        assert set(expected) == set(declared), (
            f"expectations cover {sorted(expected)}, cluster declares {sorted(declared)}"
        )

        for name, want in expected.items():
            for pod in declared[name].pods:
                member = members[pod]
                got = {field: member.get(field) for field in want}
                assert got == want, f"member {pod} of group {name}: {got} != {want}"

        # Voting members after the scale-up and the removal: 5 from inst1, plus
        # the arbiter and the hidden member. Odd, and within MongoDB's limit.
        voters = sum(1 for member in members.values() if member.get("votes"))
        assert voters == 7, f"expected 7 voting members, got {voters}"

        # Group tags survive alongside the identity tags the operator adds, and
        # an arbiter carries no tags at all.
        for pod in declared["nonVoting"].pods:
            tags = members[pod].get("tags") or {}
            assert tags.get("nonVoting") == "true", tags
            assert tags.get("podName") == pod, tags
        for pod in declared["hidden"].pods:
            tags = members[pod].get("tags") or {}
            assert tags.get("hidden") == "true", tags
        for pod in declared["arbiter"].pods:
            assert not (members[pod].get("tags") or {}), members[pod]
