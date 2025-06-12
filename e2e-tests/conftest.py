import pytest

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
        "--collect-k8s-resources",
        action="store_true",
        default=False,
        help="Enable collection of K8s resources on test failure",
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
