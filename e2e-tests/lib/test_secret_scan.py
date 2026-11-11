"""Unit tests of the credential scan; run with `make py-unit` (no cluster, no conftest)."""

import base64
import json
import subprocess
import time
from typing import Any

import pytest

from lib import secret_scan
from lib.secret_scan import (
    ALLOWED_VALUES,
    MIN_VALUE_LENGTH,
    Finding,
    LogRecorder,
    data_values,
    mask,
    scan_text,
)

VALUES = {"s3-secret/AWS_SECRET_ACCESS_KEY": "some-secret-key"}


def test_exact_value_is_reported_masked() -> None:
    findings = scan_text("pod/op/manager", "line one\nkey=some-secret-key here\n", VALUES)
    assert findings == [
        Finding("pod/op/manager", 2, "secret value s3-secret/AWS_SECRET_ACCESS_KEY", "some***")
    ]
    assert "some-secret-key" not in str(findings[0])


def test_base64_value_is_reported() -> None:
    encoded = base64.b64encode(b"some-secret-key").decode()
    values = {"s3-secret/AWS_SECRET_ACCESS_KEY (base64)": encoded}
    findings = scan_text("secret-dump", f"  secret-key: {encoded}", values)
    assert [f.rule for f in findings] == ["secret value s3-secret/AWS_SECRET_ACCESS_KEY (base64)"]


def test_clean_text_has_no_findings() -> None:
    text = "mongodb://user:***@host/admin\nmongodb+srv://user:<password>@host\nbucket=operator-testing"
    assert scan_text("pod/db/mongod", text, VALUES) == []


def test_patterns() -> None:
    text = (
        "AKIAABCDEFGHIJKLMNOP\n"
        "aws_secret_access_key = x\n"
        "DefaultEndpointsProtocol=https;AccountKey=abc\n"
        '{"private_key": "-----BEGIN PRIVATE KEY-----"}\n'
        "uri mongodb://admin:hunter22@db-rs0-0:27017/admin"
    )
    rules = [f.rule for f in scan_text("src", text, {})]
    assert rules == [
        "aws-access-key-id",
        "aws-secret-access-key",
        "azure-account-key",
        "gcp-private-key",
        "private-key-block",
        "mongodb-uri-password",
    ]
    uri = next(f for f in scan_text("src", text, {}) if f.rule == "mongodb-uri-password")
    assert uri.masked == "mong***"
    assert "hunter22" not in uri.masked


def test_mask_keeps_four_characters() -> None:
    assert mask("abcdefgh") == "abcd***"
    assert mask("ab") == "ab***"


def test_limits() -> None:
    assert MIN_VALUE_LENGTH == 8
    assert "S3-BACKUP-BUCKET-NAME-HERE" in ALLOWED_VALUES


def test_every_line_of_a_multiline_value_is_searched() -> None:
    key = '{\n  "client_email": "e2e@project.iam",\n  "private_key_id": "0123456789abcdef"\n}'
    values = data_values("gcp-cs-sa-key-secret/key.json", _b64(key))
    text = 'email "client_email": "e2e@project.iam",\nid "private_key_id": "0123456789abcdef"'
    rules = [f.rule for f in scan_text("pod/op/manager", text, values)]
    assert rules == [
        "secret value gcp-cs-sa-key-secret/key.json#2",
        "secret value gcp-cs-sa-key-secret/key.json#3",
    ]


def test_original_base64_of_a_value_with_a_trailing_newline_is_searched() -> None:
    encoded = _b64("some-secret-key\n")
    values = data_values("s3-secret/AWS_SECRET_ACCESS_KEY", encoded)
    assert values["s3-secret/AWS_SECRET_ACCESS_KEY (base64)"] == encoded
    findings = scan_text("events", f"  secret-key: {encoded}", values)
    assert [f.rule for f in findings] == ["secret value s3-secret/AWS_SECRET_ACCESS_KEY (base64)"]


def test_lines_are_numbered_by_their_position_in_the_value() -> None:
    values = data_values("s3-secret/key", _b64("\nfirst-value\n\nfirst-value\nsecond-value\n"))
    assert {name: value for name, value in values.items() if "#" in name} == {
        "s3-secret/key#2": "first-value",
        "s3-secret/key#5": "second-value",
    }


def test_short_and_allowlisted_values_are_skipped() -> None:
    assert data_values("s3-secret/region", _b64("us-east-1\nshort\n")) == {
        "s3-secret/region (base64)": _b64("us-east-1\nshort\n"),
        "s3-secret/region#1": "us-east-1",
    }
    assert data_values("s3-secret/bucket", _b64("S3-BACKUP-BUCKET-NAME-HERE")) == {}


def _b64(value: str) -> str:
    return base64.b64encode(value.encode()).decode()


class FakeCluster:
    """Pods and logs served to secret_scan through a fake kubectl_bin."""

    def __init__(self) -> None:
        self.pods: dict[str, dict[str, Any]] = {}
        # Still listed by `get pods`, but gone when read.
        self.deleted: list[dict[str, Any]] = []
        # Pods recreated under the same name right before their first log read.
        self.replacements: dict[str, tuple[str, dict[str, str], tuple[str, ...]]] = {}
        self.logs: dict[tuple[str, str], str] = {}
        self.broken: set[tuple[str, str]] = set()
        self.calls: list[tuple[str, ...]] = []

    def add(
        self, name: str, uid: str, logs: dict[str, str], waiting: tuple[str, ...] = ()
    ) -> None:
        containers = [c for c in [*logs, *waiting] if c != "init"]
        self.pods[name] = {
            "metadata": {"name": name, "uid": uid},
            "spec": {
                "initContainers": [{"name": "init"}],
                "containers": [{"name": c} for c in containers],
            },
            "status": {
                "initContainerStatuses": [{"name": "init", "state": {"terminated": {}}}],
                "containerStatuses": [
                    {"name": c, "state": {"waiting": {}} if c in waiting else {"running": {}}}
                    for c in containers
                ],
            },
        }
        for container, log in {"init": "", **logs}.items():
            self.logs[(name, container)] = log

    def delete_after_listing(self, name: str) -> None:
        self.deleted.append(self.pods.pop(name))

    def recreate_before_reading(
        self, name: str, uid: str, logs: dict[str, str], waiting: tuple[str, ...] = ()
    ) -> None:
        """Replace pod `name` with a new pod `uid` between listing it and reading its log,
        as a StatefulSet does with a deleted pod."""
        self.replacements[name] = (uid, logs, waiting)

    def kubectl(self, *args: str, **_: Any) -> str:
        self.calls.append(args)
        if args[:2] == ("get", "pods"):
            return json.dumps({"items": [*self.pods.values(), *self.deleted]})
        if args[:2] == ("get", "pod"):
            return json.dumps(self.pods[args[2]]) if args[2] in self.pods else ""
        if args[0] == "logs":
            pod, container = args[1], args[args.index("-c") + 1]
            if pod in self.replacements:
                for old in [key for key in self.logs if key[0] == pod]:
                    del self.logs[old]
                self.add(pod, *self.replacements.pop(pod))
            # A deleted pod, or a container that has not started, has no log to read.
            if (
                pod not in self.pods
                or (pod, container) not in self.logs
                or (pod, container) in self.broken
            ):
                raise subprocess.CalledProcessError(1, ["kubectl", *args], stderr="NotFound")
            return self.logs[(pod, container)]
        raise AssertionError(f"Unexpected kubectl call: {args}")


@pytest.fixture
def cluster(monkeypatch: pytest.MonkeyPatch) -> FakeCluster:
    fake = FakeCluster()
    monkeypatch.setattr(secret_scan, "kubectl_bin", fake.kubectl)
    monkeypatch.setattr(secret_scan, "READ_DELAY", 0)
    return fake


def test_recorder_keeps_the_latest_logs_of_replaced_pods(cluster: FakeCluster) -> None:
    cluster.add("db-rs0-0", "old00000-uid", {"init": "init log", "mongod": "started"})
    recorder = LogRecorder("ns", "db")
    recorder.record()
    cluster.logs[("db-rs0-0", "mongod")] = "started\nrestored"
    recorder.record()
    cluster.add("db-rs0-0", "new00000-uid", {"init": "init again", "mongod": "rebuilt"})
    recorder.record()
    assert recorder.sources() == {
        "pod/db-rs0-0/init (uid old00000)": "init log",
        "pod/db-rs0-0/mongod (uid old00000)": "started\nrestored",
    }


def test_recorder_keeps_the_log_of_a_container_run_before_a_restart(
    cluster: FakeCluster,
) -> None:
    cluster.add("db-rs0-0", "pod00000-uid", {"mongod": "first run"})
    recorder = LogRecorder("ns", "db")
    recorder.record()
    cluster.logs[("db-rs0-0", "mongod")] = "second run"
    cluster.pods["db-rs0-0"]["status"]["containerStatuses"][0]["restartCount"] = 1
    recorder.record()
    # The pod still runs: only the run before the restart is returned.
    assert recorder.sources() == {"pod/db-rs0-0/mongod (uid pod00000)": "first run"}
    cluster.delete_after_listing("db-rs0-0")
    recorder.record()
    assert recorder.sources() == {
        "pod/db-rs0-0/init (uid pod00000)": "",
        "pod/db-rs0-0/mongod (uid pod00000)": "first run",
        "pod/db-rs0-0/mongod (uid pod00000, restart 1)": "second run",
    }


def test_recorder_keeps_a_pod_deleted_while_it_reads(cluster: FakeCluster) -> None:
    cluster.add("db-rs0-0", "pod00000-uid", {"mongod": "kept"})
    recorder = LogRecorder("ns", "db")
    recorder.record()
    cluster.delete_after_listing("db-rs0-0")
    recorder.record()
    assert recorder.sources() == {
        "pod/db-rs0-0/init (uid pod00000)": "",
        "pod/db-rs0-0/mongod (uid pod00000)": "kept",
    }


def test_recorder_drops_a_read_of_a_pod_recreated_under_the_same_name(
    cluster: FakeCluster,
) -> None:
    cluster.add("db-rs0-0", "old00000-uid", {"mongod": "started"})
    recorder = LogRecorder("ns", "db")
    recorder.record()
    cluster.logs[("db-rs0-0", "mongod")] = "started\nrestored"
    recorder.record()
    cluster.recreate_before_reading("db-rs0-0", "new00000-uid", {"mongod": "rebuilt"})
    recorder.record()
    assert recorder.sources() == {
        "pod/db-rs0-0/init (uid old00000)": "",
        "pod/db-rs0-0/mongod (uid old00000)": "started\nrestored",
    }
    recorder.record()
    assert "rebuilt" not in recorder.sources().values()


def test_recorder_skips_a_recreated_pod_that_has_not_started(cluster: FakeCluster) -> None:
    cluster.add("db-rs0-0", "old00000-uid", {"mongod": "restored"})
    recorder = LogRecorder("ns", "db")
    recorder.record()
    cluster.recreate_before_reading("db-rs0-0", "new00000-uid", {}, waiting=("mongod",))
    cluster.calls.clear()
    recorder.record()
    assert recorder.sources() == {
        "pod/db-rs0-0/init (uid old00000)": "",
        "pod/db-rs0-0/mongod (uid old00000)": "restored",
    }
    # No retries: the read of the new pod's mongod failed once and the pod was skipped.
    assert sum(1 for args in cluster.calls if args[0] == "logs" and "mongod" in args) == 1


def test_recorder_reads_only_started_containers(cluster: FakeCluster) -> None:
    cluster.add("db-rs0-0", "pod00000-uid", {"mongod": "log"}, waiting=("backup-agent",))
    LogRecorder("ns", "db").record()
    read = {args[args.index("-c") + 1] for args in cluster.calls if args[0] == "logs"}
    assert read == {"init", "mongod"}


def test_recorder_fails_on_a_log_it_cannot_read(cluster: FakeCluster) -> None:
    cluster.add("db-rs0-0", "pod00000-uid", {"mongod": "log"})
    cluster.broken.add(("db-rs0-0", "mongod"))
    with pytest.raises(AssertionError, match="container mongod of pod db-rs0-0 after 3 attempts"):
        LogRecorder("ns", "db").record()


def test_recorder_raises_the_error_of_its_thread(cluster: FakeCluster) -> None:
    cluster.add("db-rs0-0", "pod00000-uid", {"mongod": "log"})
    cluster.broken.add(("db-rs0-0", "mongod"))
    with (
        pytest.raises(AssertionError, match="Recording the logs of the db pods failed"),
        LogRecorder("ns", "db", interval=0.01),
    ):
        pass


def test_recorder_does_not_hide_the_error_of_the_step(cluster: FakeCluster) -> None:
    cluster.add("db-rs0-0", "pod00000-uid", {"mongod": "log"})
    cluster.broken.add(("db-rs0-0", "mongod"))
    with (
        pytest.raises(RuntimeError, match="restore failed"),
        LogRecorder("ns", "db", interval=0.01),
    ):
        raise RuntimeError("restore failed")


def test_recorder_reads_once_more_when_it_stops(cluster: FakeCluster) -> None:
    cluster.add("db-rs0-0", "old00000-uid", {"mongod": "old"})
    with LogRecorder("ns", "db", interval=60) as recorder:
        # Wait for the first read of the thread, which then sleeps for the interval.
        deadline = time.monotonic() + 5
        while len(recorder._logs) < 2 and time.monotonic() < deadline:
            time.sleep(0.01)
        cluster.add("db-rs0-0", "new00000-uid", {"mongod": "new"})
    assert recorder.sources() == {
        "pod/db-rs0-0/init (uid old00000)": "",
        "pod/db-rs0-0/mongod (uid old00000)": "old",
    }


def test_pod_logs_of_a_deleted_pod_are_skipped(cluster: FakeCluster) -> None:
    assert secret_scan._pod_logs("ns", "gone") is None


def test_pod_logs_read_the_pod_recreated_under_the_same_name(cluster: FakeCluster) -> None:
    cluster.add("db-rs0-0", "old00000-uid", {"mongod": "old"})
    cluster.recreate_before_reading("db-rs0-0", "new00000-uid", {"mongod": "new"})
    logs = secret_scan._pod_logs("ns", "db-rs0-0")
    assert logs and logs["pod/db-rs0-0/mongod"] == "new"
    assert "old" not in logs.values()


def test_pod_logs_of_a_pod_deleted_while_read_are_skipped(
    cluster: FakeCluster, monkeypatch: pytest.MonkeyPatch
) -> None:
    cluster.add("db-rs0-0", "pod00000-uid", {"mongod": "log"})
    real = cluster.kubectl

    def delete_on_log_read(*args: str, **kwargs: Any) -> str:
        if args[0] == "logs":
            cluster.pods.pop("db-rs0-0", None)
        return real(*args, **kwargs)

    monkeypatch.setattr(secret_scan, "kubectl_bin", delete_on_log_read)
    assert secret_scan._pod_logs("ns", "db-rs0-0") is None


def test_pod_logs_fail_when_the_pod_changes_on_every_read(
    cluster: FakeCluster, monkeypatch: pytest.MonkeyPatch
) -> None:
    cluster.add("db-rs0-0", "uid00000-0", {"mongod": "log"})
    real = cluster.kubectl
    replaced = 0

    def replace_on_log_read(*args: str, **kwargs: Any) -> str:
        nonlocal replaced
        if args[0] == "logs" and "--previous" not in args:
            replaced += 1
            cluster.recreate_before_reading("db-rs0-0", f"uid00000-{replaced}", {"mongod": "log"})
        return real(*args, **kwargs)

    monkeypatch.setattr(secret_scan, "kubectl_bin", replace_on_log_read)
    with pytest.raises(AssertionError, match="could not be read consistently"):
        secret_scan._pod_logs("ns", "db-rs0-0")


def test_pod_logs_fail_when_a_container_does_not_start(cluster: FakeCluster) -> None:
    cluster.add("db-rs0-0", "pod00000-uid", {"mongod": "log"}, waiting=("backup-agent",))
    with pytest.raises(AssertionError, match=r"\['backup-agent'\] of pod db-rs0-0 did not start"):
        secret_scan._pod_logs("ns", "db-rs0-0")
