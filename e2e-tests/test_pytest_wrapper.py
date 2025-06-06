import os
import subprocess
from pathlib import Path
from typing import List, Tuple

import pytest


def get_bash_tests() -> List[Tuple[str, Path]]:
    """Find all bash test scripts in the same directory as this test file"""
    current_dir = Path(__file__).parent
    bash_tests: List[Tuple[str, Path]] = []
    
    for test_dir in current_dir.iterdir():
        if test_dir.is_dir():
            run_script = test_dir / "run"
            if run_script.exists():
                bash_tests.append((test_dir.name, run_script))

    return bash_tests


bash_tests = get_bash_tests()


@pytest.mark.parametrize(
    "test_name,script_path", bash_tests, ids=[name for name, _ in bash_tests]
)
def test_e2e(test_name: str, script_path: Path) -> None:
    """Run bash script and check exit code"""

    original_cwd: str = os.getcwd()
    script_dir: Path = script_path.parent

    try:
        os.chdir(script_dir)
        result: subprocess.CompletedProcess[str] = subprocess.run(
            ["bash", "run"], capture_output=True, text=True
        )

        if result.returncode != 0:
            print(f"\nSTDOUT:\n{result.stdout}")
            print(f"\nSTDERR:\n{result.stderr}")

            k8s_result: subprocess.CompletedProcess[str] = subprocess.run(
                ["kubectl", "get", "nodes"], capture_output=True, text=True
            )
            print(f"\nK8s LOGS:\n{k8s_result.stdout}")

        assert result.returncode == 0, (
            f"Test {test_name} failed with exit code {result.returncode}"
        )

    finally:
        os.chdir(original_cwd)
