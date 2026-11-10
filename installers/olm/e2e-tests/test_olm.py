"""Unit tests of the OLM test helpers; run with `make py-unit` (no cluster, no conftest).

run-tests.sh runs only the test directories, so the OLM e2e runs do not collect this file.
"""

import re
from collections.abc import Iterator
from datetime import UTC, datetime, timedelta
from typing import Any

import olm
import pytest
from lib.backup import MAX_EMPTY_READS
from olm import ErrorWindow, _log_time, unexpected_errors, wait_reconciled

START = datetime(2026, 11, 10, 10, 0, tzinfo=UTC)
END = START + timedelta(minutes=5)
CRASH = re.compile(r"FULL CLUSTER CRASH")
WINDOW = ErrorWindow(CRASH, START, END, max_count=2)
OUTSIDE = f"  [allowed only between {START} and {END}]"
MOMENT = timedelta(milliseconds=1)
CONFLICT = "the object has been modified; please apply your changes to the latest version"


def line(at: datetime, level: str = "ERROR", message: str = "FULL CLUSTER CRASH") -> str:
    return f"{at.isoformat().replace('+00:00', 'Z')}\t{level}\t{message}"


@pytest.mark.parametrize(
    ("lines", "expected"),
    [
        pytest.param([line(START + timedelta(minutes=1))], [], id="inside"),
        pytest.param([line(START)], [], id="at-start"),
        pytest.param([line(END)], [], id="at-end"),
        pytest.param(
            [line(START - MOMENT)], [line(START - MOMENT) + OUTSIDE], id="just-before-start"
        ),
        pytest.param([line(END + MOMENT)], [line(END + MOMENT) + OUTSIDE], id="just-after-end"),
        pytest.param([line(START), line(END)], [], id="at-cap"),
        pytest.param(
            [line(START), line(START), line(END)],
            [f"{line(END)}  [allowed at most 2 times]"],
            id="one-over-cap",
        ),
        pytest.param(
            ["\tERROR\tFULL CLUSTER CRASH"],
            ["\tERROR\tFULL CLUSTER CRASH" + OUTSIDE],
            id="no-timestamp",
        ),
        pytest.param(
            [line(START, message="reconcile failed")],
            [line(START, message="reconcile failed")],
            id="unrelated-error",
        ),
        pytest.param([line(START, level="INFO", message="reconcile failed")], [], id="info"),
        pytest.param(
            [line(START, message=CONFLICT)],
            [],
            id="allowed-everywhere",
        ),
    ],
)
def test_unexpected_errors(lines: list[str], expected: list[str]) -> None:
    assert unexpected_errors(lines, (WINDOW,)) == expected


def test_error_lines_are_unexpected_without_windows() -> None:
    assert unexpected_errors([line(START)]) == [line(START)]


@pytest.mark.parametrize(
    ("text", "expected"),
    [
        pytest.param("2026-11-10T10:00:00.5Z\tINFO", START + timedelta(seconds=0.5), id="utc"),
        pytest.param("2026-11-10T12:00:00+02:00\tINFO", START, id="offset"),
        pytest.param("2026-11-10T10:00:00\tINFO", None, id="no-zone"),
        pytest.param("\tERROR\tmessage", None, id="no-timestamp"),
        pytest.param("", None, id="empty"),
    ],
)
def test_log_time(text: str, expected: datetime | None) -> None:
    assert _log_time(text) == expected


def fake_kubectl(monkeypatch: pytest.MonkeyPatch, observed: list[str]) -> None:
    """kubectl for wait_reconciled: generation 4 before the patch, 5 after, then `observed`
    for each status.observedGeneration read."""
    reads: Iterator[str] = iter(["4", "5", *observed])

    def kubectl(*args: str, **_: Any) -> str:
        return next(reads)

    monkeypatch.setattr(olm, "kubectl_bin", kubectl)
    monkeypatch.setattr("olm.time.sleep", lambda _: None)


def test_wait_reconciled_accepts_a_newer_observed_generation(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    fake_kubectl(monkeypatch, ["4", "6"])
    wait_reconciled("ns", "db")


def test_wait_reconciled_fails_after_empty_reads_in_a_row(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    fake_kubectl(monkeypatch, ["", "4", *[""] * MAX_EMPTY_READS])
    with pytest.raises(AssertionError, match=f"could not be read {MAX_EMPTY_READS} times"):
        wait_reconciled("ns", "db")
