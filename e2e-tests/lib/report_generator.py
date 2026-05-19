import os
import re
from typing import Any, List, MutableSequence

from pytest_html import extras

from lib.k8s_collector import K8sCollector

ENV_VARS_TO_REPORT = [
    "KUBE_VERSION",
    "EKS",
    "GKE",
    "OPENSHIFT",
    "MINIKUBE",
    "GIT_COMMIT",
    "GIT_BRANCH",
    "OPERATOR_VERSION",
    "OPERATOR_NS",
    "IMAGE",
    "IMAGE_MONGOD",
    "IMAGE_BACKUP",
    "IMAGE_PMM_CLIENT",
    "IMAGE_PMM_SERVER",
    "IMAGE_PMM3_CLIENT",
    "IMAGE_PMM3_SERVER",
    "CERT_MANAGER_VER",
    "CHAOS_MESH_VER",
    "MINIO_VER",
    "CLEAN_NAMESPACE",
    "DELETE_CRD_ON_START",
    "SKIP_DELETE",
    "SKIP_BACKUPS_TO_AWS_GCP_AZURE",
]


def pytest_html_results_summary(
    prefix: MutableSequence[str], summary: MutableSequence[str], postfix: MutableSequence[str]
) -> None:
    """Add environment variables table to HTML report summary."""
    rows = ""
    for i, var in enumerate(ENV_VARS_TO_REPORT):
        value = os.environ.get(var, "")
        escaped_value = value.replace("<", "&lt;").replace(">", "&gt;")
        bg = "#f9f9f9" if i % 2 == 0 else "#ffffff"
        rows += f'<tr style="background-color: {bg};"><td style="border: 1px solid #ddd; padding: 6px;"><code>{var}</code></td><td style="border: 1px solid #ddd; padding: 6px;"><code>{escaped_value}</code></td></tr>\n'

    if rows:
        table = f"""
        <h3>Environment Variables</h3>
        <table style="border-collapse: collapse; margin: 10px 0; font-size: 13px; font-family: monospace;">
            <thead>
                <tr style="background-color: #e0e0e0;">
                    <th style="border: 1px solid #ddd; padding: 8px; text-align: left;">Variable</th>
                    <th style="border: 1px solid #ddd; padding: 8px; text-align: left;">Value</th>
                </tr>
            </thead>
            <tbody>
                {rows}
            </tbody>
        </table>
        """
        prefix.append(table)


def pytest_html_results_table_header(cells: MutableSequence[str]) -> None:
    """Add Class column to HTML report."""
    cells.insert(1, '<th class="sortable alpha" data-column-type="alpha">Class</th>')


def pytest_html_results_table_row(report: Any, cells: MutableSequence[str]) -> None:
    """Populate Class column in HTML report."""
    nodeid = report.nodeid
    if "test_bash_wrapper[" in nodeid:
        # Extract test name from: test_pytest_wrapper.py::test_bash_wrapper[test-name]
        class_name = nodeid.split("[")[1].rstrip("]")
    else:
        parts = nodeid.split("::")
        class_name = parts[1].replace("Test", "") if len(parts) > 2 else "-"
    cells.insert(1, f"<td>{class_name}</td>")


LOG_LEVEL_COLORS = {
    "ERROR": "#ff6b6b",
    "WARN": "#ffa500",
    "INFO": "#00CC66",
    "DEBUG": "#3B8EFF",
    "Normal": "#00CC66",
    "Warning": "#ffa500",
}
LOG_LEVEL_PATTERN = re.compile(r"\b(" + "|".join(LOG_LEVEL_COLORS.keys()) + r")\b")


def highlight_log_levels(logs: str) -> str:
    """Add basic color highlighting for common log levels"""
    return LOG_LEVEL_PATTERN.sub(
        lambda m: f'<span style="color: {LOG_LEVEL_COLORS[m.group(1)]};">{m.group(1)}</span>',
        logs,
    )


def generate_report(namespace: str) -> List[Any]:
    def create_collapsible_section(title: str, content: str) -> str:
        return f"""
        <div style="margin: 10px 0;">
            <details style="border: 1px solid #ccc; border-radius: 4px; padding: 5px;">
                <summary style="cursor: pointer; font-weight: bold; padding: 10px; background-color: #f5f5f5; margin: -5px; border-radius: 3px;">
                    {title}
                </summary>
                <div style="padding: 10px; background-color: #1e1e1e;">
                    <pre style="
                        white-space: pre-wrap; 
                        word-wrap: break-word; 
                        background-color: #1e1e1e; 
                        color: #d4d4d4;
                        padding: 15px; 
                        border-radius: 5px; 
                        font-family: 'Courier New', 'Consolas', 'Monaco', monospace;
                        font-size: 13px;
                        line-height: 1.4;
                        overflow-x: auto;
                        border: 1px solid #333;
                        max-height: 600px;
                        overflow-y: auto;
                        scrollbar-width: thin;
                        scrollbar-color: #666 #333;
                    ">{content}</pre>
                </div>
            </details>
        </div>
        """

    summary = K8sCollector(namespace).capture_summary()

    return [
        extras.html(
            create_collapsible_section("Operator Pod Logs", highlight_log_levels(summary["logs"]))
        ),
        extras.html(create_collapsible_section("Kubernetes Resources", summary["resources"])),
        extras.html(
            create_collapsible_section(
                "Kubernetes Events", highlight_log_levels(summary["events"])
            )
        ),
    ]
