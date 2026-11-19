"""Unit tests for the wait helpers in lib.kubectl. They need no cluster: kubectl is faked."""

import json
from typing import Any

import pytest

from lib import kubectl


class FakeClock:
    def __init__(self) -> None:
        self.now = 0.0

    def monotonic(self) -> float:
        return self.now

    def sleep(self, seconds: float) -> None:
        self.now += seconds


def _pod(phase: str, ready: str, waiting: dict[str, str] | None = None) -> dict[str, Any]:
    state = {"waiting": waiting} if waiting else {"running": {}}
    return {
        "status": {
            "phase": phase,
            "conditions": [{"type": "Ready", "status": ready}],
            "containerStatuses": [{"name": "mongod", "state": state}],
        }
    }


@pytest.fixture
def clock(monkeypatch: pytest.MonkeyPatch) -> FakeClock:
    fake = FakeClock()
    monkeypatch.setattr("lib.kubectl.time.monotonic", fake.monotonic)
    monkeypatch.setattr("lib.kubectl.time.sleep", fake.sleep)
    return fake


def _fake_kubectl(monkeypatch: pytest.MonkeyPatch, clock: FakeClock, pod_at: Any) -> None:
    """Answer `kubectl get pod` with pod_at(clock.now), as jsonpath status or full JSON."""

    def fake(*args: str, **_: Any) -> str:
        pod = pod_at(clock.now)
        if "json" in args:
            return json.dumps(pod)
        ready = pod["status"]["conditions"][0]["status"]
        return f"{pod['status']['phase']} {ready}"

    monkeypatch.setattr(kubectl, "kubectl_bin", fake)


def test_wait_pod_returns_when_ready(monkeypatch: pytest.MonkeyPatch, clock: FakeClock) -> None:
    _fake_kubectl(
        monkeypatch,
        clock,
        lambda t: _pod("Running", "True") if t >= 30 else _pod("Pending", "False"),
    )
    kubectl.wait_pod("rs0-0")
    assert clock.now == 30


def test_wait_pod_stops_early_on_image_pull_error(
    monkeypatch: pytest.MonkeyPatch, clock: FakeClock
) -> None:
    waiting = {"reason": "ImagePullBackOff", "message": "Back-off pulling image bad:tag"}
    _fake_kubectl(monkeypatch, clock, lambda _: _pod("Pending", "False", waiting))

    with pytest.raises(RuntimeError, match="ImagePullBackOff.*bad:tag"):
        kubectl.wait_pod("rs0-0", timeout=600)
    assert clock.now < kubectl.FATAL_GRACE + 2 * kubectl.PROBLEM_CHECK_INTERVAL


def test_wait_pod_waits_out_a_short_image_pull_error(
    monkeypatch: pytest.MonkeyPatch, clock: FakeClock
) -> None:
    def pod_at(t: float) -> dict[str, Any]:
        if t < 60:
            return _pod("Pending", "False", {"reason": "ErrImagePull"})
        if t < 90:
            return _pod("Pending", "False")
        return _pod("Running", "True")

    _fake_kubectl(monkeypatch, clock, pod_at)
    kubectl.wait_pod("rs0-0")
    assert clock.now == 90


def test_wait_pod_does_not_stop_early_on_crash_loop(
    monkeypatch: pytest.MonkeyPatch, clock: FakeClock
) -> None:
    waiting = {"reason": "CrashLoopBackOff", "message": "back-off 5m0s"}
    _fake_kubectl(monkeypatch, clock, lambda _: _pod("Running", "False", waiting))

    with pytest.raises(TimeoutError, match="after 300s.*Last problem: mongod: CrashLoopBackOff"):
        kubectl.wait_pod("rs0-0", timeout=300)
    assert clock.now >= 300


def test_wait_pod_reports_last_status_on_timeout(
    monkeypatch: pytest.MonkeyPatch, clock: FakeClock
) -> None:
    _fake_kubectl(monkeypatch, clock, lambda _: _pod("Pending", "False"))

    with pytest.raises(TimeoutError, match="Last status: Pending False$"):
        kubectl.wait_pod("rs0-0", timeout=30)
