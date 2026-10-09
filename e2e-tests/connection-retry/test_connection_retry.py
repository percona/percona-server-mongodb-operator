#!/usr/bin/env python3

import json
import logging
import os
import time
from collections.abc import Callable
from dataclasses import dataclass
from typing import Any

import pytest
from lib.config import apply_cluster
from lib.kubectl import kubectl_bin, wait_cluster_consistency, wait_for_running
from lib.mongo import MongoManager
from lib.operator import get_operator_pod
from lib.utils import Paths, retry

logger = logging.getLogger(__name__)

# Must match psmdb.AppName: failCommand only fails commands sent by the
# operator, so replication, healthchecks and mongosh keep working.
OPERATOR_APP_NAME = "percona-server-mongodb-operator"
RETRY_LOG = "retrying mongo connection"


@dataclass(frozen=True)
class ConnectionRetryConfig:
    namespace: str
    cluster: str

    @property
    def uri(self) -> str:
        return f"clusterAdmin:clusterAdmin123456@{self.cluster}-rs0.{self.namespace}"


@pytest.fixture(scope="class", autouse=True)
def config(create_infra: Callable[[str], str]) -> ConnectionRetryConfig:
    """Configuration for tests"""
    return ConnectionRetryConfig(
        namespace=create_infra("connection-retry"),
        cluster="connection-retry",
    )


@pytest.fixture(scope="class", autouse=True)
def setup_tests(test_paths: Paths) -> None:
    """Setup test environment"""
    kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/secrets.yml")


def operator_logs() -> list[str]:
    args = ["logs", get_operator_pod()]
    if operator_ns := os.environ.get("OPERATOR_NS"):
        args.extend(["-n", operator_ns])
    return kubectl_bin(*args).splitlines()


def retry_lines(logs: list[str]) -> list[str]:
    return [line for line in logs if RETRY_LOG in line]


def admin_command(client: MongoManager, config: ConnectionRetryConfig, cmd: dict[str, Any]) -> Any:
    out = client.run_mongosh(
        f"EJSON.stringify(db.adminCommand({json.dumps(cmd)}))", config.uri, timeout=120
    )
    return json.loads(out)


def enable_fail_command(
    client: MongoManager, config: ConnectionRetryConfig, mode: Any, data: dict[str, Any]
) -> int:
    """Turn on failCommand for the operator's connections to the primary and
    return how many times the failpoint was entered before."""
    res = admin_command(
        client,
        config,
        {
            "configureFailPoint": "failCommand",
            "mode": mode,
            "data": {**data, "appName": OPERATOR_APP_NAME},
        },
    )
    return int(res["count"])


def disable_fail_command(client: MongoManager, config: ConnectionRetryConfig) -> int:
    """Turn off failCommand and return how many times it was entered in total."""
    res = admin_command(client, config, {"configureFailPoint": "failCommand", "mode": "off"})
    return int(res["count"])


def wait_for_fail_command(
    client: MongoManager, config: ConnectionRetryConfig, times_entered: int
) -> None:
    admin_command(
        client,
        config,
        {"waitForFailPoint": "failCommand", "timesEntered": times_entered, "maxTimeMS": 90000},
    )


class TestConnectionRetry:
    @pytest.mark.dependency()
    def test_create_cluster(self, config: ConnectionRetryConfig, test_paths: Paths) -> None:
        apply_cluster(f"{test_paths['test_dir']}/conf/{config.cluster}-rs0.yml")
        wait_for_running(f"{config.cluster}-rs0", 3)

    @pytest.mark.dependency(depends=["TestConnectionRetry::test_create_cluster"])
    def test_transient_error_is_retried(
        self, config: ConnectionRetryConfig, psmdb_client: MongoManager
    ) -> None:
        """A few dropped connections are absorbed by the retry and the cluster stays ready."""
        since = len(operator_logs())

        count = enable_fail_command(
            psmdb_client,
            config,
            {"times": 3},
            {"failCommands": ["ping"], "closeConnection": True},
        )
        try:
            wait_for_fail_command(psmdb_client, config, count + 3)
        finally:
            disable_fail_command(psmdb_client, config)

        lines = retry(lambda: retry_lines(operator_logs()[since:]), max_attempts=10, delay=3, condition=bool)
        logger.info(f"Operator retried {len(lines)} times")

        wait_cluster_consistency(config.cluster)

    @pytest.mark.dependency(depends=["TestConnectionRetry::test_transient_error_is_retried"])
    def test_outage_longer_than_retry_budget(
        self, config: ConnectionRetryConfig, psmdb_client: MongoManager
    ) -> None:
        """Dial gives up once the backoff is exhausted instead of pinning the
        reconcile, and the cluster recovers when the outage is over."""
        since = len(operator_logs())

        enable_fail_command(
            psmdb_client,
            config,
            "alwaysOn",
            {"failCommands": ["ping"], "closeConnection": True},
        )
        try:
            # DefaultBackoff sleeps ~30s in total before giving up.
            time.sleep(60)
        finally:
            disable_fail_command(psmdb_client, config)

        logs = operator_logs()[since:]
        retries = retry_lines(logs)
        assert len(retries) >= 4, f"Expected Dial to exhaust its retries, got {len(retries)}"
        assert any("ping mongo" in line for line in logs), "Expected Dial to give up"

        wait_cluster_consistency(config.cluster)

    @pytest.mark.dependency(depends=["TestConnectionRetry::test_outage_longer_than_retry_budget"])
    def test_primary_handshake_failure_is_not_a_full_cluster_crash(
        self, config: ConnectionRetryConfig, psmdb_client: MongoManager
    ) -> None:
        """The primary failing the handshake looks like ReplicaSetNoPrimary to
        the operator. A short blip must be retried, not handled as a full
        cluster crash, which force-reconfigures the replset."""
        since = len(operator_logs())

        enable_fail_command(
            psmdb_client,
            config,
            "alwaysOn",
            {"failCommands": ["hello", "isMaster"], "closeConnection": True},
        )
        try:
            time.sleep(20)
        finally:
            disable_fail_command(psmdb_client, config)

        logs = operator_logs()[since:]
        assert retry_lines(logs), "Expected the operator to retry"

        crashes = [line for line in logs if "FULL CLUSTER CRASH" in line]
        assert not crashes, "Handshake blip handled as a crash:\n" + "\n".join(crashes)

        wait_cluster_consistency(config.cluster)

    @pytest.mark.dependency(
        depends=["TestConnectionRetry::test_primary_handshake_failure_is_not_a_full_cluster_crash"]
    )
    def test_auth_error_is_not_retried(
        self, config: ConnectionRetryConfig, psmdb_client: MongoManager
    ) -> None:
        since = len(operator_logs())

        # The driver authenticates speculatively in the handshake, so
        # saslContinue is the first auth command on the wire.
        count = enable_fail_command(
            psmdb_client,
            config,
            "alwaysOn",
            {"failCommands": ["saslContinue"], "errorCode": 18},
        )
        try:
            time.sleep(30)
        finally:
            entered = disable_fail_command(psmdb_client, config)

        assert entered > count, "The operator never hit the auth failpoint"

        lines = retry_lines(operator_logs()[since:])
        assert not lines, "Auth failures must not be retried:\n" + "\n".join(lines)

        wait_cluster_consistency(config.cluster)
