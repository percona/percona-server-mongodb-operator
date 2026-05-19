import os
import subprocess
from pathlib import Path

import pytest


def pytest_addoption(parser: pytest.Parser) -> None:
    parser.addoption("--test-name", action="store", help="Name of the bash test to run")


def run_bash_test(test_name: str) -> None:
    """Run bash script with live output and capture for error reporting"""
    script_path = Path(__file__).parent.parent / test_name / "run"

    if not script_path.exists():
        pytest.fail(f"Script not found: {script_path}")

    original_cwd = os.getcwd()
    script_dir = script_path.parent

    try:
        os.chdir(script_dir)
        process = subprocess.Popen(
            ["bash", "run"], stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True
        )

        output = []
        if process.stdout is not None:
            for line in iter(process.stdout.readline, ""):
                print(line, end="")
                output.append(line)

        process.wait()

        if process.returncode != 0:
            error_msg = f"Test {test_name} failed with exit code {process.returncode}\n\nOUTPUT:\n{''.join(output)}"
            pytest.fail(error_msg)

    finally:
        os.chdir(original_cwd)
