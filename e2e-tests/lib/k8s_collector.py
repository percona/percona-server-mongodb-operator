#!/usr/bin/env python3

import os
import re
import sys
import tarfile
import subprocess
from datetime import datetime
from concurrent.futures import ThreadPoolExecutor, as_completed
from typing import List, Optional


REPORTS_DIR = os.path.join(os.path.dirname(__file__), "..", "reports")


class K8sCollector:
    def __init__(self, namespace: str, custom_resources: Optional[List[str]] = None):
        self.namespace = namespace
        self.custom_resources = custom_resources or []
        self.timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
        self.output_dir = os.path.join(REPORTS_DIR, f"{namespace}_{self.timestamp}")

    def kubectl(self, *args: str) -> str:
        """Run kubectl command and return stdout"""
        result = subprocess.run(
            ["kubectl", *args], capture_output=True, text=True, check=False
        )
        return result.stdout if result.returncode == 0 else ""

    def kubectl_ns(self, *args: str) -> str:
        """Run kubectl command with namespace flag"""
        return self.kubectl(*args, "-n", self.namespace)

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
        print(f"Processing {resource_type}...")
        base = f"{self.output_dir}/get/{resource_type}"

        self.save(f"{base}/{resource_type}.txt", self.kubectl_ns("get", resource_type, "-o", "wide"))

        for name in self.get_names(resource_type):
            self.save(f"{self.output_dir}/describe/{resource_type}_{name}.txt",
                      self.kubectl_ns("describe", resource_type, name))
            self.save(f"{base}/{name}.yaml", self.kubectl_ns("get", resource_type, name, "-o", "yaml"))

    def extract_pod_logs(self, pod: str) -> None:
        """Extract logs for all containers in a pod"""
        containers = self.kubectl_ns(
            "get", "pod", pod, "-o", "jsonpath={.spec.containers[*].name}"
        ).split()

        for container in containers:
            print(f"  Extracting logs: {pod}/{container}")
            logs = self.kubectl_ns("logs", pod, "-c", container)
            self.save(f"{self.output_dir}/logs/{pod}/{container}.log", logs)

    def process_pods(self) -> None:
        """Process pods with parallel log extraction"""
        print("Processing pods...")
        base = f"{self.output_dir}/get/pods"

        self.save(f"{base}/pods.txt", self.kubectl_ns("get", "pods", "-o", "wide"))

        pods = self.get_names("pods")
        for pod in pods:
            self.save(f"{self.output_dir}/describe/pod_{pod}.txt",
                      self.kubectl_ns("describe", "pod", pod))
            self.save(f"{base}/{pod}.yaml", self.kubectl_ns("get", "pod", pod, "-o", "yaml"))

        with ThreadPoolExecutor(max_workers=5) as executor:
            list(executor.map(self.extract_pod_logs, pods))

    def extract_events(self) -> None:
        """Extract namespace events"""
        print("Extracting events...")
        self.save(f"{self.output_dir}/events/events.txt",
                  self.kubectl_ns("get", "events", "-o", "wide"))
        self.save(f"{self.output_dir}/events/events.json",
                  self.kubectl_ns("get", "events", "-o", "json"))

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
                    error_lines = [l for l in f if "error" in l.lower()]
                if error_lines:
                    rel_path = os.path.relpath(path, logs_dir)
                    errors.append(f"=== {rel_path} ===\n" + "".join(error_lines))

        if errors:
            self.save(f"{self.output_dir}/error_summary.log",
                      f"Errors for {self.namespace} ({self.timestamp})\n{'='*50}\n\n" +
                      "\n\n".join(errors))

    def collect_all(self) -> None:
        """Main collection method"""
        print(f"=== Collecting from namespace: {self.namespace} ===")

        self.process_pods()

        resources = ["statefulsets", "deployments", "secrets", "jobs", "configmaps", "services"]
        resources.extend(r.strip() for r in self.custom_resources if r.strip())

        with ThreadPoolExecutor(max_workers=6) as executor:
            futures = [executor.submit(self.process_resource, r) for r in resources]
            futures.append(executor.submit(self.extract_events))
            for f in as_completed(futures):
                try:
                    f.result()
                except Exception as e:
                    print(f"Error: {e}")

        self.extract_errors()
        print(f"=== Done. Output: {self.output_dir} ===")


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
        print(f"Error collecting from {namespace}: {e}")


def get_namespace(test_log: str) -> str:
    """Extract namespace from test log"""
    match = re.search(r"create namespace (\S*?\d+)\b", test_log)
    if match:
        print(f"Extracted namespace: {match.group(1)}", file=sys.stderr)
        return match.group(1)
    raise ValueError("Namespace not found in logs")

