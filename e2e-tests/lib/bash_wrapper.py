import os
import subprocess
import sys
import threading
from collections import deque
from pathlib import Path
from typing import IO

import pytest

from .allure_support import attach_text

# Lines of script output kept for the report when the script fails.
OUTPUT_TAIL_LINES = 1000
# Background jobs started by the script (for example `kubectl delete ns &`)
# can keep the output pipe open after bash exits; stop reading after this.
READER_GRACE_SECONDS = 5


def _pump(stream: IO[str], tail: deque[str]) -> None:
    for line in stream:
        sys.stdout.write(line)
        sys.stdout.flush()
        tail.append(line)


def run_bash_test(test_name: str) -> None:
    """Run a bash test script and stream its output to the console.

    The last OUTPUT_TAIL_LINES lines are attached to the Allure report when
    the script fails.
    """
    script_path = Path(__file__).parent.parent / test_name / "run"

    if not script_path.exists():
        pytest.fail(f"Script not found: {script_path}")

    # Keep the namespace alive so pytest can collect diagnostics on failure;
    # pytest handles teardown afterward (see bash_test_cleanup fixture).
    env = {**os.environ, "SKIP_DELETE": "1"}
    tail: deque[str] = deque(maxlen=OUTPUT_TAIL_LINES)

    proc = subprocess.Popen(
        ["bash", "run"],
        cwd=script_path.parent,
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        encoding="utf-8",
        errors="replace",
    )
    assert proc.stdout is not None
    reader = threading.Thread(target=_pump, args=(proc.stdout, tail), daemon=True)
    reader.start()
    try:
        returncode = proc.wait()
    except BaseException:
        proc.kill()
        proc.wait()
        raise
    reader.join(timeout=READER_GRACE_SECONDS)
    # A reader still blocked on the pipe would block close() too; leave it
    # streaming whatever the background jobs print.
    if not reader.is_alive():
        proc.stdout.close()

    if returncode != 0:
        attach_text("".join(tail), f"Bash test output (last {OUTPUT_TAIL_LINES} lines)")
        pytest.fail(f"Test {test_name} failed with exit code {returncode}")
