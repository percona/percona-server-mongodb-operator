import os
import subprocess
from pathlib import Path

import pytest


def run_bash_test(test_name: str) -> None:
    """Run bash script with stdout/stderr inherited from the pytest process."""
    script_path = Path(__file__).parent.parent / test_name / "run"

    if not script_path.exists():
        pytest.fail(f"Script not found: {script_path}")

    original_cwd = os.getcwd()
    script_dir = script_path.parent

    try:
        os.chdir(script_dir)
        # Keep the namespace alive so pytest can collect diagnostics on failure;
        # pytest handles teardown afterward (see bash_test_cleanup fixture).
        env = {**os.environ, "SKIP_DELETE": "1"}
        result = subprocess.run(["bash", "run"], check=False, env=env)

        if result.returncode != 0:
            pytest.fail(f"Test {test_name} failed with exit code {result.returncode}")

    finally:
        os.chdir(original_cwd)
