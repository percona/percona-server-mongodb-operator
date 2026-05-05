#!/usr/bin/env python3

import logging
import os
import tarfile
from concurrent.futures import ThreadPoolExecutor, as_completed
from datetime import datetime
from typing import Dict, List, Optional

from lib.kubectl import kubectl_bin

logger = logging.getLogger(__name__)

REPORTS_DIR = os.path.join(os.path.dirname(__file__), "..", "reports")


class K8sCollector:
    def __init__(self, namespace: str, custom_resources: Optional[List[str]] = None):
        self.namespace = namespace
        self.custom_resources = custom_resources or []
        self.timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
        self.output_dir = os.path.join(REPORTS_DIR, f"{namespace}_{self.timestamp}")

    def kubectl(self, *args: str) -> str:
        """Run kubectl command and return stdout"""
        return kubectl_bin(*args, check=False)

    def kubectl_ns(self, *args: str) -> str:
        """Run kubectl command with namespace flag"""
        return kubectl_bin(*args, "-n", self.namespace, check=False)

    def get_names(self, resource_type: str) -> List[str]:
        """Get list of resource names"""
        output = self.kubectl_ns("get", resource_type, "-o", "name")
        return [line.split("/")[-1] for line in output.strip().split("\n") if line]

    def save(self, path: str, content: str) -> None:
        """Save content to file, creating directories as needed"""
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w") as f:
            f.write(content)

    def process_resource(self, resource_type: str) -> None:
        """Process a resource type: get list, describe each, get yaml"""
        logger.debug(f"Processing {resource_type}...")
        base = f"{self.output_dir}/get/{resource_type}"

        self.save(
            f"{base}/{resource_type}.txt", self.kubectl_ns("get", resource_type, "-o", "wide")
        )

        for name in self.get_names(resource_type):
            self.save(
                f"{self.output_dir}/describe/{resource_type}_{name}.txt",
                self.kubectl_ns("describe", resource_type, name),
            )
            self.save(
                f"{base}/{name}.yaml", self.kubectl_ns("get", resource_type, name, "-o", "yaml")
            )

    def extract_pod_logs(self, pod: str) -> None:
        """Extract logs for all containers in a pod"""
        containers = self.kubectl_ns(
            "get", "pod", pod, "-o", "jsonpath={.spec.containers[*].name}"
        ).split()

        for container in containers:
            logger.debug(f"Extracting logs: {pod}/{container}")
            logs = self.kubectl_ns("logs", pod, "-c", container)
            self.save(f"{self.output_dir}/logs/{pod}/{container}.log", logs)

    def process_pods(self) -> None:
        """Process pods with parallel log extraction"""
        logger.debug("Processing pods...")
        base = f"{self.output_dir}/get/pods"

        self.save(f"{base}/pods.txt", self.kubectl_ns("get", "pods", "-o", "wide"))

        pods = self.get_names("pods")
        for pod in pods:
            self.save(
                f"{self.output_dir}/describe/pod_{pod}.txt",
                self.kubectl_ns("describe", "pod", pod),
            )
            self.save(f"{base}/{pod}.yaml", self.kubectl_ns("get", "pod", pod, "-o", "yaml"))

        with ThreadPoolExecutor(max_workers=5) as executor:
            list(executor.map(self.extract_pod_logs, pods))

    def extract_events(self) -> None:
        """Extract namespace events"""
        logger.debug("Extracting events...")
        self.save(
            f"{self.output_dir}/events/events.txt", self.kubectl_ns("get", "events", "-o", "wide")
        )
        self.save(
            f"{self.output_dir}/events/events.json", self.kubectl_ns("get", "events", "-o", "json")
        )

    def extract_errors(self) -> None:
        """Extract error lines from all logs into summary"""
        logs_dir = f"{self.output_dir}/logs"
        if not os.path.exists(logs_dir):
            return

        errors = []
        for root, _, files in os.walk(logs_dir):
            for file in files:
                if not file.endswith(".log"):
                    continue
                path = os.path.join(root, file)
                with open(path) as f:
                    error_lines = [line for line in f if "error" in line.lower()]
                if error_lines:
                    rel_path = os.path.relpath(path, logs_dir)
                    errors.append(f"=== {rel_path} ===\n" + "".join(error_lines))

        if errors:
            self.save(
                f"{self.output_dir}/error_summary.log",
                f"Errors for {self.namespace} ({self.timestamp})\n{'=' * 50}\n\n"
                + "\n\n".join(errors),
            )

    def capture_summary(self) -> Dict[str, str]:
        """Capture simplified resources for HTML report. Returns dict with resources, logs, events."""
        sections = []

        sections.append("=== Nodes ===")
        sections.append(self.kubectl("get", "nodes") or "(no output)")
        sections.append("")

        sections.append(f"=== All from namespace {self.namespace} ===")
        sections.append(self.kubectl_ns("get", "all") or "(no output)")
        sections.append("")

        sections.append("=== Secrets ===")
        sections.append(self.kubectl_ns("get", "secrets") or "(no output)")
        sections.append("")

        sections.append("=== PSMDB Cluster ===")
        sections.append(
            self.kubectl_ns(
                "get", "psmdb", "-o", "custom-columns=NAME:.metadata.name,STATE:.status.state"
            )
            or "(no output)"
        )
        sections.append("")

        sections.append("=== PSMDB Backup ===")
        sections.append(self.kubectl_ns("get", "psmdb-backup") or "(no output)")
        sections.append("")

        sections.append("=== PSMDB Restore ===")
        sections.append(self.kubectl_ns("get", "psmdb-restore") or "(no output)")

        logs = self.kubectl_ns(
            "logs", "-l", "app.kubernetes.io/name=percona-server-mongodb-operator", "--tail=50"
        )

        events = self.kubectl_ns("get", "events", "--sort-by=.lastTimestamp")

        return {
            "resources": "\n".join(sections),
            "logs": f"=== PSMDB Operator Logs ===\n{logs or '(no output)'}",
            "events": f"=== Kubernetes Events ===\n{events or '(no output)'}",
        }

    def collect_all(self) -> None:
        """Main collection method"""
        logger.info(f"Collecting from namespace: {self.namespace}")

        self.process_pods()

        resources = ["statefulsets", "deployments", "secrets", "jobs", "configmaps", "services"]
        resources.extend(r for r in self.custom_resources if r)

        with ThreadPoolExecutor(max_workers=6) as executor:
            futures = [executor.submit(self.process_resource, r) for r in resources]
            futures.append(executor.submit(self.extract_events))
            for f in as_completed(futures):
                try:
                    f.result()
                except Exception as e:
                    logger.error(f"Error: {e}")

        self.extract_errors()
        logger.info(f"Done. Output: {self.output_dir}")


def collect_resources(
    namespace: str, custom_resources: Optional[List[str]] = None, output_dir: Optional[str] = None
) -> None:
    """Collect Kubernetes resources for a given namespace."""
    collector = K8sCollector(namespace, custom_resources)
    if output_dir:
        collector.output_dir = os.path.join(REPORTS_DIR, f"{output_dir}_{collector.timestamp}")

    os.makedirs(REPORTS_DIR, exist_ok=True)

    try:
        collector.collect_all()
        with tarfile.open(f"{collector.output_dir}.tar.gz", "w:gz") as tar:
            tar.add(collector.output_dir, arcname=os.path.basename(collector.output_dir))
    except Exception as e:
        logger.error(f"Error collecting from {namespace}: {e}")
