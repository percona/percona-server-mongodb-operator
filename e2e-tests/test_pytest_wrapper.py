import os
import subprocess
import pytest
from pathlib import Path
from typing import List, Tuple


def get_bash_tests(test_suite: str = "") -> List[Tuple[str, Path]]:
    """Get bash test scripts from file or all directories"""
    current_dir = Path(__file__).parent
    bash_tests: List[Tuple[str, Path]] = []

    if test_suite:
        file_path = current_dir / f"run-{test_suite}.csv"
        if not file_path.exists():
            raise FileNotFoundError(f"Test suite file not found: {file_path}")

        with open(file_path, "r", encoding="utf-8") as f:
            test_names = [line.strip() for line in f if line.strip()]
    else:
        test_names = [d.name for d in current_dir.iterdir() if d.is_dir()]

    for test_name in test_names:
        test_dir = current_dir / test_name
        run_script = test_dir / "run"
        if run_script.exists():
            bash_tests.append((test_name, run_script))

    return bash_tests


def pytest_generate_tests(metafunc):
    """Generate tests dynamically"""
    if "test_name" in metafunc.fixturenames and "script_path" in metafunc.fixturenames:
        test_suite = metafunc.config.getoption("--test-suite")
        bash_tests = get_bash_tests(test_suite)
        metafunc.parametrize(
            "test_name,script_path", bash_tests, ids=[name for name, _ in bash_tests]
        )


def test_e2e(test_name: str, script_path: Path) -> None:
    """Run bash script and check exit code"""
    original_cwd = os.getcwd()
    script_dir = script_path.parent

    try:
        os.chdir(script_dir)
        result = subprocess.run(["bash", "run"], capture_output=True, text=True)

        if result.returncode != 0:
            error_msg = f"""
Test {test_name} failed with exit code {result.returncode}

STDOUT:
{result.stdout}

STDERR:
{result.stderr}
"""
            pytest.fail(error_msg)

        assert result.returncode == 0

    finally:
        os.chdir(original_cwd)
