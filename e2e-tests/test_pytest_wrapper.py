import os
import subprocess
import pytest
from pathlib import Path


def test_e2e(test_name: str, script_path: Path) -> None:
    """Run bash script with live output and capture for error reporting"""
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
            error_msg = f"""
Test {test_name} failed with exit code {process.returncode}

OUTPUT:
{"".join(output)}
"""
            pytest.fail(error_msg)

    finally:
        os.chdir(original_cwd)
