import html
import os
import re
from collections.abc import MutableSequence
from typing import Any

from pytest_html import extras

from lib.k8s_collector import K8sCollector
from lib.kubectl import kubectl_bin

MIN_READY_NODES = 3

ENV_VARS_TO_REPORT = [
    "KUBE_VERSION",
    "PLATFORM",
    "EKS",
    "GKE",
    "AKS",
    "OPENSHIFT",
    "KIND",
    "MINIKUBE",
    "RANCHER",
    "GIT_COMMIT",
    "GIT_BRANCH",
    "OPERATOR_VERSION",
    "OPERATOR_NS",
    "IMAGE",
    "IMAGE_MONGOD",
    "IMAGE_SEARCH",
    "IMAGE_BACKUP",
    "IMAGE_PMM_CLIENT",
    "IMAGE_PMM_SERVER",
    "IMAGE_PMM3_CLIENT",
    "IMAGE_PMM3_SERVER",
    "IMAGE_LOGCOLLECTOR",
    "IMAGE_CLUSTERSYNC",
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
        escaped_value = html.escape(value)
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


def check_nodes_ready(min_ready: int = MIN_READY_NODES) -> dict[str, Any]:
    """Return node readiness counts at the current moment."""
    output = kubectl_bin(
        "get",
        "nodes",
        "-o",
        "jsonpath={.items[*].status.conditions[?(@.type=='Ready')].status}",
        check=False,
        return_stderr=True,
    )
    # Only count valid Ready condition values; anything else (e.g. an error
    # message on command failure) is ignored so it can't inflate the totals.
    statuses = [s for s in output.split() if s in ("True", "False")]
    ready = sum(1 for s in statuses if s == "True")
    total = len(statuses)
    return {"ready": ready, "total": total, "ok": total > 0 and ready >= min_ready}


def _nodes_cell(nodes: dict[str, Any] | None) -> str:
    """Render the Nodes column cell for a test row."""
    if not nodes or nodes["total"] == 0:
        return "<td>-</td>"
    if nodes["ok"]:
        return f'<td style="color: #00CC66; font-weight: bold;">{nodes["ready"]}/{nodes["total"]} ready</td>'
    return f'<td style="color: #ff6b6b; font-weight: bold;">{nodes["ready"]}/{nodes["total"]} not ready</td>'


def pytest_html_results_table_header(cells: MutableSequence[str]) -> None:
    """Add Class column and replace the Links column with a Nodes readiness column."""
    cells.insert(1, '<th class="sortable alpha" data-column-type="alpha">Class</th>')
    cells[-1] = '<th class="sortable alpha" data-column-type="alpha">K8s Nodes</th>'


def pytest_html_results_table_row(report: Any, cells: MutableSequence[str]) -> None:
    """Populate Class column and replace Links cell with node readiness status."""
    nodeid = report.nodeid
    if "test_bash_wrapper[" in nodeid:
        # test_pytest_wrapper.py::test_bash_wrapper[test-name]
        class_name = nodeid.split("[")[1].rstrip("]")
    else:
        # e2e-tests/<test-name>/test_*.py::Class::method -> <test-name>
        path_parts = nodeid.split("::")[0].split("/")
        class_name = path_parts[-2] if len(path_parts) >= 2 else "-"
    cells.insert(1, f"<td>{class_name}</td>")
    cells[-1] = _nodes_cell(getattr(report, "nodes_status", None))


LOG_LEVEL_COLORS = {
    "ERROR": "#ff6b6b",
    "WARN": "#ffa500",
    "INFO": "#00CC66",
    "DEBUG": "#3B8EFF",
    "Normal": "#00CC66",
    "Warning": "#ffa500",
}
LOG_LEVEL_PATTERN = re.compile(
    r"\b(" + "|".join(sorted(LOG_LEVEL_COLORS.keys(), key=len, reverse=True)) + r")\b"
)


def highlight_log_levels(logs: str) -> str:
    """HTML-escape logs and add basic color highlighting for common log levels"""
    return LOG_LEVEL_PATTERN.sub(
        lambda m: f'<span style="color: {LOG_LEVEL_COLORS[m.group(1)]};">{m.group(1)}</span>',
        html.escape(logs),
    )


def generate_report(namespace: str) -> list[Any]:
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
        extras.html(
            create_collapsible_section("Kubernetes Resources", html.escape(summary["resources"]))
        ),
        extras.html(
            create_collapsible_section(
                "Kubernetes Events", highlight_log_levels(summary["events"])
            )
        ),
    ]
