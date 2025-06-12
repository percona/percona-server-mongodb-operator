import re
import pytest

from pathlib import Path
from typing import Tuple, List

from tools.report_generator import generate_report
from tools.k8s_resources_collector import collect_k8s_resources, get_namespace


def pytest_addoption(parser):
    """Add custom command line option for test suite file"""
    parser.addoption(
        "--test-suite",
        action="store",
        default=None,
        help="Name of the test suite file (will look for run-{name}.csv)",
    )
    parser.addoption(
        "--test-regex",
        action="store",
        default=None,
        help="Run tests matching the given regex pattern",
    )
    parser.addoption(
        "--collect-k8s-resources",
        action="store_true",
        default=False,
        help="Enable collection of K8s resources on test failure",
    )


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
    """Generate tests dynamically with regex filtering"""
    if "test_name" in metafunc.fixturenames and "script_path" in metafunc.fixturenames:
        test_suite = metafunc.config.getoption("--test-suite")
        test_regex = metafunc.config.getoption("--test-regex")

        bash_tests = get_bash_tests(test_suite)
        if test_regex:
            try:
                pattern = re.compile(test_regex)
                filtered_tests = [
                    (name, path) for name, path in bash_tests if pattern.search(name)
                ]
                bash_tests = filtered_tests

                print(f"\nFiltered to {len(bash_tests)} test(s) matching regex '{test_regex}':")
                for name, _ in bash_tests:
                    print(f"  - {name}")

            except re.error as e:
                pytest.exit(f"Invalid regex pattern '{test_regex}': {e}")

        metafunc.parametrize(
            "test_name,script_path", bash_tests, ids=[name for name, _ in bash_tests]
        )


def pytest_html_report_title(report):
    report.title = "PSMDB E2E Test Report"


@pytest.hookimpl(hookwrapper=True)
def pytest_runtest_makereport(item, call):
    outcome = yield
    report = outcome.get_result()

    if report.when == "call" and report.failed:
        try:
            namespace = get_namespace(str(report.longrepr))
            html_report = generate_report(namespace)

            if not hasattr(report, "extras"):
                report.extras = []
            report.extras.extend(html_report)

            collect_resources = item.config.getoption("--collect-k8s-resources")
            if collect_resources:
                collect_k8s_resources(
                    namespace=namespace,
                    custom_resources=["psmdb", "psmdb-backup", "psmdb-restore"],
                    output_dir=f"e2e-tests/reports/{namespace}",
                )
        except Exception as e:
            print(f"Error adding K8s info: {e}")
