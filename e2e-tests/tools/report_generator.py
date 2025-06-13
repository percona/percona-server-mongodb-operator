import subprocess

from pytest_html import extras


def run_kubectl_commands(commands: list) -> str:
    """Execute kubectl commands and return formatted output"""
    output = []

    for title, cmd in commands:
        output.append(f"\n<strong>=== {title} ===</strong>")
        try:
            result = subprocess.run(cmd.split(), capture_output=True, text=True, timeout=15)
            if result.returncode == 0:
                output.append(result.stdout.strip())
            else:
                output.append(f"Error (exit code {result.returncode}): {result.stderr.strip()}")
        except subprocess.TimeoutExpired:
            output.append("Command timed out after 15 seconds")
        except Exception as e:
            output.append(f"Failed to execute: {str(e)}")

    return "\n".join(output)


def capture_k8s_resources(namespace: str) -> str:
    """Capture Kubernetes resources information"""
    commands = [
        ("Nodes", "kubectl get nodes"),
        (f"All from namespace {namespace}", f"kubectl get all -n {namespace}"),
        ("Secrets", f"kubectl get secrets -n {namespace}"),
        (
            "PSMDB Cluster",
            f"kubectl get psmdb -n {namespace} -o custom-columns=NAME:.metadata.name,STATE:.status.state",
        ),
        ("PSMDB Backup", f"kubectl get psmdb-backup -n {namespace}"),
        ("PSMDB Restore", f"kubectl get psmdb-restore -n {namespace}"),
    ]
    return run_kubectl_commands(commands)


def capture_k8s_logs(namespace: str) -> str:
    """Capture Kubernetes logs"""
    commands = [
        (
            "PSMDB Operator Logs",
            f"kubectl logs -l app.kubernetes.io/name=percona-server-mongodb-operator --tail=50 -n {namespace}",
        ),
    ]
    return run_kubectl_commands(commands)


def capture_k8s_events(namespace: str) -> str:
    """Capture Kubernetes events"""
    commands = [
        ("PSMDB Operator Events", f"kubectl get events -n {namespace}"),
    ]
    return run_kubectl_commands(commands)


def highlight_log_levels(logs: str) -> str:
    """Add basic color highlighting for common log levels"""
    logs = logs.replace("ERROR", '<span style="color: #ff6b6b;">ERROR</span>')
    logs = logs.replace("WARN", '<span style="color: #ffa500;">WARN</span>')
    logs = logs.replace("INFO", '<span style="color: #00CC66;">INFO</span>')
    logs = logs.replace("DEBUG", '<span style="color: #3B8EFF;">DEBUG</span>')
    logs = logs.replace("Normal", '<span style="color: #00CC66;">Normal</span>')
    logs = logs.replace("Warning", '<span style="color: #ffa500;">Warning</span>')
    return logs


def generate_report(namespace: str) -> list:
    def create_collapsible_section(title: str, icon: str, content: str) -> str:
        return f"""
        <div style="margin: 10px 0;">
            <details style="border: 1px solid #ccc; border-radius: 4px; padding: 5px;">
                <summary style="cursor: pointer; font-weight: bold; padding: 10px; background-color: #f5f5f5; margin: -5px; border-radius: 3px;">
                    {icon} {title}
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

    final_report = []

    pod_logs = capture_k8s_logs(namespace)
    highlighted_logs = highlight_log_levels(pod_logs)
    final_report.append(
        extras.html(create_collapsible_section("Operator Pod Logs", "📋", highlighted_logs))
    )

    k8s_info = capture_k8s_resources(namespace)
    final_report.append(
        extras.html(create_collapsible_section("Kubernetes Resources", "🔍", k8s_info))
    )

    k8s_events = capture_k8s_events(namespace)
    highlighted_events = highlight_log_levels(k8s_events)
    final_report.append(
        extras.html(create_collapsible_section("Kubernetes Events", "📣", highlighted_events))
    )

    return final_report
