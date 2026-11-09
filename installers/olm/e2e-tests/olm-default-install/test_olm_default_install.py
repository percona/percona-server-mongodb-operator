#!/usr/bin/env python3

import json
import logging
import os
import time
import uuid
from collections.abc import Callable, Generator
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

import pytest
from lib.kubectl import kubectl_bin
from lib.mongo import MongoManager
from lib.secrets import get_user_data
from lib.utils import Paths, env_bool, retry
from olm import (
    ALLOWED_OPERATOR_ERRORS,
    APP_PASSWORD,
    APP_USER,
    CatalogTarget,
    assert_operator_healthy,
    csv_operator_image,
    delete_crds,
    event_count,
    get_csv,
    get_json,
    install,
    olm_available,
    olm_test_paths,
    operator_pod,
    resolve_target,
    teardown,
    wait_until,
    warning_events,
)

logger = logging.getLogger(__name__)

# A sharded cluster (rs0, cfg and mongos, 3 pods each) is ready in about 5 minutes.
CLUSTER_READY_TIMEOUT = 900


@dataclass(frozen=True)
class OlmDefaultInstallConfig:
    namespace: str
    target: CatalogTarget


@dataclass
class State:
    """What the install and deploy steps saw, for the final checks."""

    operator_pod_uid: str = ""
    pods: dict[str, tuple[str, int]] = field(default_factory=dict)
    events: dict[str, int] = field(default_factory=dict)


@pytest.fixture(scope="class")
def test_paths(request: pytest.FixtureRequest) -> Paths:
    """Overrides the shared fixture: this test lives in installers/olm/e2e-tests/."""
    return olm_test_paths(request.path)


@pytest.fixture(scope="class")
def config(create_namespace: Callable[[str], str], test_paths: Paths) -> OlmDefaultInstallConfig:
    """Resolve the target CSV from the catalog, then create the test namespace."""
    if not olm_available():
        pytest.skip("OLM is not available on this cluster")

    target = resolve_target()
    logger.info(
        f"Target: {target.target_csv} ({target.target_channel}) from "
        f"{target.source_namespace}/{target.source}"
    )

    delete_crds(test_paths["src_dir"])
    namespace = create_namespace(f"olm-default-install-{uuid.uuid4().hex[:8]}")
    # Lets the failure report collect resources from this namespace.
    Path(os.environ["PYTEST_NAMESPACE_FILE"]).write_text(namespace)

    return OlmDefaultInstallConfig(namespace=namespace, target=target)


@pytest.fixture(scope="class", autouse=True)
def cleanup(config: OlmDefaultInstallConfig, test_paths: Paths) -> Generator[None]:
    yield

    Path(os.environ["PYTEST_NAMESPACE_FILE"]).unlink(missing_ok=True)
    if env_bool("SKIP_DELETE"):
        logger.info("SKIP_DELETE is set. Skipping test environment cleanup")
        return
    teardown(config.namespace, config.target.package, test_paths["src_dir"])


@pytest.fixture(scope="class")
def state() -> State:
    return State()


@pytest.fixture(scope="class")
def csv(config: OlmDefaultInstallConfig) -> dict[str, Any]:
    """The installed target CSV; request it only after test_install_operator."""
    return get_csv(config.namespace, config.target.target_csv)


@pytest.fixture(scope="class")
def example(csv: dict[str, Any]) -> dict[str, Any]:
    """The first alm-examples entry: the form the console prefills on Create."""
    first = json.loads(csv["metadata"]["annotations"]["alm-examples"])[0]
    assert first.get("kind") == "PerconaServerMongoDB", (
        f"First alm-examples entry of {csv['metadata']['name']} is {first.get('kind')}, "
        "expected PerconaServerMongoDB"
    )
    return dict(first)


def _components(example: dict[str, Any]) -> set[str]:
    """Status keys the example's cluster reports: its replsets, plus cfg and mongos if sharded."""
    spec = example["spec"]
    names = {rs["name"] for rs in spec["replsets"]}
    if spec.get("sharding", {}).get("enabled"):
        names |= {"cfg", "mongos"}
    return names


def _progress(namespace: str, cluster: str) -> dict[str, str]:
    """State, ready/size per component and message of the cluster; {} when unreadable."""
    raw = kubectl_bin("get", "psmdb", cluster, "-n", namespace, "-o", "json", check=False)
    if not raw.strip():
        return {}
    status = json.loads(raw).get("status", {})
    progress = {
        "state": str(status.get("state", "")),
        "ready": f"{status.get('ready', 0)}/{status.get('size', 0)}",
    }
    components = dict(status.get("replsets", {}))
    if status.get("mongos"):
        components["mongos"] = status["mongos"]
    for name, component in sorted(components.items()):
        progress[name] = f"{component.get('ready', 0)}/{component.get('size', 0)}"
    if status.get("message"):
        progress["message"] = str(status["message"])
    return progress


def _cluster_ready(progress: dict[str, str], components: set[str]) -> bool:
    if progress.get("state") != "ready" or not components <= progress.keys():
        return False
    counts = [progress["ready"], *(progress[name] for name in components)]
    return all(ready == size != "0" for ready, size in (c.split("/") for c in counts))


def _db_pods(namespace: str, cluster: str) -> dict[str, tuple[str, int]]:
    """pod name -> (uid, total container restarts); asserts every pod is Running and Ready."""
    pods = get_json("pods", "-n", namespace, "-l", f"app.kubernetes.io/instance={cluster}")
    not_ready = [
        f"{pod['metadata']['name']}: {pod['status'].get('phase')}"
        for pod in pods["items"]
        if pod["status"].get("phase") != "Running"
        or not any(
            c["type"] == "Ready" and c["status"] == "True"
            for c in pod["status"].get("conditions", [])
        )
    ]
    assert not not_ready, f"Cluster {cluster} is ready, but pods are not: {not_ready}"
    return {
        pod["metadata"]["name"]: (
            pod["metadata"]["uid"],
            sum(c.get("restartCount", 0) for c in pod["status"].get("containerStatuses", [])),
        )
        for pod in pods["items"]
    }


def _mongos_uri(cluster: str, namespace: str, user: str, password: str) -> str:
    return f"{user}:{password}@{cluster}-mongos.{namespace}"


class TestOlmDefaultInstall:
    """Install the target release from the catalog and create the console's default cluster."""

    @pytest.mark.dependency()
    def test_install_operator(self, config: OlmDefaultInstallConfig, state: State) -> None:
        """Install the target CSV from its channel"""
        csv = install(
            config.namespace, config.target, config.target.target_channel, config.target.target_csv
        )
        pod = operator_pod(config.namespace, csv)
        image = pod["spec"]["containers"][0]["image"]
        assert image == csv_operator_image(csv), f"Operator runs {image}"
        # A replacement pod would start with a fresh restart count and log.
        state.operator_pod_uid = pod["metadata"]["uid"]

    @pytest.mark.dependency(depends=["TestOlmDefaultInstall::test_install_operator"])
    def test_deploy_default_cluster(
        self,
        config: OlmDefaultInstallConfig,
        csv: dict[str, Any],
        example: dict[str, Any],
        state: State,
    ) -> None:
        """The first alm-examples entry, applied unchanged, reaches ready"""
        cluster = example["metadata"]["name"]
        logger.info(
            f"Applying the first alm-examples entry unchanged: PerconaServerMongoDB/{cluster}"
        )
        kubectl_bin("apply", "-n", config.namespace, "-f", "-", input_data=json.dumps(example))

        components = _components(example)

        def fetch() -> dict[str, str]:
            progress = _progress(config.namespace, cluster)
            message = progress.get("message", "")
            # A conflict on a concurrent update is retried by the operator, not an error.
            if progress.get("state") == "error" and not any(
                pattern.search(message) for pattern in ALLOWED_OPERATOR_ERRORS
            ):
                raise RuntimeError(f"Cluster {cluster} reported an error: {progress}")
            return progress

        start = time.monotonic()
        progress = wait_until(
            f"cluster {cluster} to be ready with {sorted(components)}",
            fetch,
            lambda progress: _cluster_ready(progress, components),
            timeout=CLUSTER_READY_TIMEOUT,
        )
        state.pods = _db_pods(config.namespace, cluster)
        # Warning events from the start-up are expected; new ones from here on are not.
        state.events = {
            uid: event_count(ev) for uid, ev in warning_events(config.namespace).items()
        }
        logger.info(
            f"Cluster {cluster} ready in {time.monotonic() - start:.0f}s: "
            + ", ".join(f"{name} {progress[name]}" for name in sorted(components))
        )

    @pytest.mark.dependency(depends=["TestOlmDefaultInstall::test_deploy_default_cluster"])
    def test_write_data(
        self,
        config: OlmDefaultInstallConfig,
        example: dict[str, Any],
        test_paths: Paths,
        psmdb_client: MongoManager,
    ) -> None:
        """Write a document through mongos and read it back"""
        cluster = example["metadata"]["name"]
        secret = example["spec"]["secrets"]["users"]
        psmdb_client.run_mongosh(
            f'db.createUser({{user:"{APP_USER}",pwd:"{APP_PASSWORD}",'
            f'roles:[{{db:"{APP_USER}",role:"readWrite"}}]}})',
            _mongos_uri(
                cluster,
                config.namespace,
                get_user_data(secret, "MONGODB_USER_ADMIN_USER"),
                get_user_data(secret, "MONGODB_USER_ADMIN_PASSWORD"),
            ),
            driver="mongodb",
            replica_set="",
        )
        app_uri = _mongos_uri(cluster, config.namespace, APP_USER, APP_PASSWORD)
        retry(
            lambda: psmdb_client.run_mongosh(
                f"db.getSiblingDB('{APP_USER}').test.insertOne({{ x: 100500 }})",
                app_uri,
                driver="mongodb",
                replica_set="",
            ),
            condition=lambda result: "acknowledged: true" in result,
        )
        psmdb_client.compare_mongo_cmd(
            "find({}, { _id: 0 }).toArray()",
            app_uri,
            database=APP_USER,
            test_file=f"{test_paths['test_dir']}/compare/find-1.json",
            replica_set="",
        )

    @pytest.mark.dependency(depends=["TestOlmDefaultInstall::test_write_data"])
    def test_cluster_stable(
        self, config: OlmDefaultInstallConfig, example: dict[str, Any], state: State
    ) -> None:
        """No DB pod was replaced or restarted and no Warning event came up once ready"""
        pods = _db_pods(config.namespace, example["metadata"]["name"])
        assert pods == state.pods, f"DB pods changed. When ready: {state.pods}, now: {pods}"
        new_warnings = [
            f"{ev['involvedObject']['kind']}/{ev['involvedObject']['name']}: "
            f"{ev.get('reason')}: {ev.get('message')}"
            for uid, ev in warning_events(config.namespace).items()
            if event_count(ev) > state.events.get(uid, 0)
        ]
        assert not new_warnings, "Warning events after the cluster was ready:\n" + "\n".join(
            new_warnings
        )

    @pytest.mark.dependency(depends=["TestOlmDefaultInstall::test_write_data"])
    def test_operator_healthy(
        self, config: OlmDefaultInstallConfig, csv: dict[str, Any], state: State
    ) -> None:
        """The operator was not replaced or restarted and logged no errors"""
        assert_operator_healthy(config.namespace, csv, state.operator_pod_uid)
