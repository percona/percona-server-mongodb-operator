#!/usr/bin/env python3

import os
import re
import sys
import threading
import subprocess
from datetime import datetime
from concurrent.futures import ThreadPoolExecutor, Future, as_completed
from typing import List, Optional, Dict, Any


class K8sCollector:
    def __init__(self, namespace: str, custom_resources: Optional[List[str]] = None):
        self.namespace = namespace
        self.custom_resources = custom_resources or []
        self.timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
        self.output_dir = f"{namespace}_{self.timestamp}"
        self.error_log_file = ""
        self.lock = threading.Lock()

    def run_kubectl(
        self, args: List[str], capture_output: bool = True, text: bool = True
    ) -> Optional[subprocess.CompletedProcess]:
        """Run kubectl command and return result"""
        try:
            result = subprocess.run(
                ["kubectl"] + args, capture_output=capture_output, text=text, check=False
            )
            return result
        except Exception as e:
            print(f"Error running kubectl command: {e}")
            return None

    def setup_directories(self) -> None:
        """Create output directory structure"""
        print(f"Creating output directory: {self.output_dir}")

        directories = [
            self.output_dir,
            f"{self.output_dir}/logs",
            f"{self.output_dir}/describe",
            f"{self.output_dir}/events",
            f"{self.output_dir}/get",
        ]

        for directory in directories:
            os.makedirs(directory, exist_ok=True)

        self.error_log_file = f"{self.output_dir}/error_summary.log"
        with open(self.error_log_file, "w") as f:
            f.write(
                f"Error Log Summary for Namespace: {self.namespace} (Extracted on {self.timestamp})\n"
            )
            f.write("=" * 73 + "\n\n")

    def get_resource_names(self, resource_type: str) -> List[str]:
        """Get list of resource names for a given type"""
        result = self.run_kubectl(["get", resource_type, "-n", self.namespace, "-o", "name"])
        if result and result.returncode == 0 and result.stdout.strip():
            return [line.split("/")[-1] for line in result.stdout.strip().split("\n")]
        return []

    def process_resource_type(self, resource_type: str, singular: str, plural: str) -> List[str]:
        """Process a specific resource type"""
        print(f"Extracting {resource_type} information...")

        get_dir = f"{self.output_dir}/get/{plural}"
        os.makedirs(get_dir, exist_ok=True)

        result = self.run_kubectl(["get", plural, "-n", self.namespace, "-o", "wide"])
        with open(f"{get_dir}/{plural}.txt", "w") as f:
            if result and result.returncode == 0:
                f.write(result.stdout)
            else:
                f.write(f"No {resource_type}s found\n")

        resources = self.get_resource_names(plural)
        with ThreadPoolExecutor(max_workers=5) as executor:
            futures = []
            for resource in resources:
                print(f"Processing {resource_type}: {resource}")
                futures.append(executor.submit(self.describe_resource, singular, resource))
                futures.append(
                    executor.submit(self.get_resource_yaml, singular, resource, get_dir)
                )

            # Wait for all tasks to complete
            for future in as_completed(futures):
                try:
                    future.result()
                except Exception as e:
                    print(f"Error processing resource: {e}")

        return resources

    def describe_resource(self, resource_type: str, resource_name: str) -> None:
        """Describe a specific resource"""
        result = self.run_kubectl(["describe", resource_type, resource_name, "-n", self.namespace])
        if result:
            with open(f"{self.output_dir}/describe/{resource_type}_{resource_name}.txt", "w") as f:
                f.write(result.stdout)

    def get_resource_yaml(self, resource_type: str, resource_name: str, output_dir: str) -> None:
        """Get resource YAML"""
        result = self.run_kubectl(
            ["get", resource_type, resource_name, "-n", self.namespace, "-o", "yaml"]
        )
        if result:
            with open(f"{output_dir}/{resource_name}.yaml", "w") as f:
                f.write(result.stdout)

    def process_custom_resource(self, resource: str) -> None:
        """Process custom resource"""
        print(f"Extracting custom resource: {resource}...")

        api_result = self.run_kubectl(["api-resources"])
        if not api_result or resource not in api_result.stdout:
            print(f"Warning: Custom resource '{resource}' not found in the cluster. Skipping.")
            return

        get_dir = f"{self.output_dir}/get/{resource}"
        os.makedirs(get_dir, exist_ok=True)

        check_result = self.run_kubectl(["get", resource, "-n", self.namespace])
        if not check_result or check_result.returncode != 0:
            print(f"No resources of type '{resource}' found in namespace {self.namespace}")
            return

        result = self.run_kubectl(["get", resource, "-n", self.namespace, "-o", "wide"])
        with open(f"{get_dir}/{resource}.txt", "w") as f:
            f.write(result.stdout if result else "")

        resources = self.get_resource_names(resource)

        with ThreadPoolExecutor(max_workers=5) as executor:
            futures = []

            for resource_name in resources:
                print(f"Processing custom resource {resource}: {resource_name}")

                futures.append(executor.submit(self.describe_resource, resource, resource_name))
                futures.append(
                    executor.submit(self.get_resource_yaml, resource, resource_name, get_dir)
                )

            for future in as_completed(futures):
                try:
                    future.result()
                except Exception as e:
                    print(f"Error processing custom resource: {e}")

    def extract_pod_logs(self, pod_name: str, container_name: str) -> None:
        """Extract logs for a specific container"""
        print(f"  Extracting logs for container: {container_name}")

        log_dir = f"{self.output_dir}/logs/{pod_name}"
        os.makedirs(log_dir, exist_ok=True)

        result = self.run_kubectl(["logs", pod_name, "-c", container_name, "-n", self.namespace])
        log_file = f"{log_dir}/{container_name}.log"

        with open(log_file, "w") as f:
            if result:
                f.write(result.stdout)

        self.extract_error_logs(pod_name, container_name, log_file)

    def extract_error_logs(self, pod_name: str, container_name: str, log_file: str) -> None:
        """Extract error logs from container log file"""
        try:
            with open(log_file, "r") as f:
                content = f.read()

            if content and "error" in content.lower():
                error_lines = [line for line in content.split("\n") if "error" in line.lower()]

                if error_lines:
                    with self.lock:
                        with open(self.error_log_file, "a") as f:
                            f.write(
                                f"=== Error logs from pod: {pod_name}, container: {container_name} ===\n\n"
                            )
                            for line in error_lines:
                                f.write(f"{line}\n")
                            f.write("\n" + "-" * 48 + "\n\n")

        except Exception as e:
            print(f"Error extracting error logs: {e}")

    def get_container_names(self, pod_name: str) -> List[str]:
        """Get container names for a pod"""
        result = self.run_kubectl(
            [
                "get",
                "pod",
                pod_name,
                "-n",
                self.namespace,
                "-o",
                "jsonpath={.spec.containers[*].name}",
            ]
        )

        if result and result.returncode == 0 and result.stdout.strip():
            return result.stdout.strip().split()
        return []

    def process_pods(self) -> None:
        """Process all pods in the namespace"""
        print("Extracting Pod information...")

        pods_dir = f"{self.output_dir}/get/pods"
        os.makedirs(pods_dir, exist_ok=True)

        result = self.run_kubectl(["get", "pods", "-n", self.namespace, "-o", "wide"])
        with open(f"{pods_dir}/pods.txt", "w") as f:
            if result and result.returncode == 0:
                f.write(result.stdout)
            else:
                f.write("No Pods found\n")

        pods = self.get_resource_names("pods")

        with ThreadPoolExecutor(max_workers=3) as executor:
            futures = []

            for pod in pods:
                print(f"Processing pod: {pod}")

                futures.append(executor.submit(self.describe_resource, "pod", pod))
                futures.append(executor.submit(self.get_resource_yaml, "pod", pod, pods_dir))

                containers = self.get_container_names(pod)
                for container in containers:
                    futures.append(executor.submit(self.extract_pod_logs, pod, container))

            # Wait for all tasks
            for future in as_completed(futures):
                try:
                    future.result()
                except Exception as e:
                    print(f"Error processing pod: {e}")

    def extract_namespace_events(self) -> None:
        """Extract namespace events"""
        print(f"Extracting events for namespace: {self.namespace}")

        events_dir = f"{self.output_dir}/events"

        print("Getting events...")
        result = self.run_kubectl(["get", "events", "-n", self.namespace, "-o", "wide"])
        with open(f"{events_dir}/events.txt", "w") as f:
            f.write(result.stdout if result else "")

        print("Getting events in JSON format...")
        result = self.run_kubectl(["get", "events", "-n", self.namespace, "-o", "json"])
        with open(f"{events_dir}/events.json", "w") as f:
            f.write(result.stdout if result else "")

    def collect_all(self) -> None:
        """Main collection method"""
        print(f"=== Starting extraction for namespace: {self.namespace} ===")

        self.setup_directories()
        self.process_pods()
        with ThreadPoolExecutor(max_workers=6) as executor:
            futures: List[Future] = [
                executor.submit(
                    self.process_resource_type, "StatefulSet", "statefulset", "statefulsets"
                ),
                executor.submit(
                    self.process_resource_type, "Deployment", "deployment", "deployments"
                ),
                executor.submit(self.process_resource_type, "Secret", "secret", "secrets"),
                executor.submit(self.process_resource_type, "Job", "job", "jobs"),
                executor.submit(
                    self.process_resource_type, "ConfigMap", "configmap", "configmaps"
                ),
                executor.submit(self.process_resource_type, "Service", "service", "services"),
                executor.submit(self.extract_namespace_events),
            ]

            for future in as_completed(futures):
                try:
                    future.result()
                except Exception as e:
                    print(f"Error in parallel processing: {e}")

        if self.custom_resources:
            print("Processing custom resources...")
            for resource in self.custom_resources:
                resource = resource.strip()
                if resource:
                    self.process_custom_resource(resource)

        print("=== Extraction completed successfully ===")
        print(f"Output is available in: {self.output_dir}")
        print(f"Error log summary: {self.error_log_file}")


def collect_k8s_resources(
    namespace: str, custom_resources: Optional[List[str]] = None, output_dir: Optional[str] = None
) -> Dict[str, Any]:
    """
    Collect Kubernetes resources for a given namespace.
    """
    collector = K8sCollector(namespace, custom_resources)

    if output_dir:
        collector.output_dir = f"{output_dir}_{collector.timestamp}"

    try:
        collector.collect_all()
        return {
            "success": True,
            "output_dir": collector.output_dir,
            "error_log": collector.error_log_file,
            "namespace": namespace,
            "timestamp": collector.timestamp,
        }
    except Exception as e:
        return {"success": False, "error": str(e), "namespace": namespace}


def get_namespace(test_log: str) -> str:
    """Extract namespace from test log"""
    match = re.search(r"create namespace (\S*?\d+)\b", test_log)
    if match:
        namespace = match.group(1)
        print(f"Extracted namespace: {namespace}", file=sys.stderr)
        return namespace

    raise ValueError("Namespace not found in logs")
