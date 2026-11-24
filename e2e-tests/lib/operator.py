import json
import logging
import os
import re
from pathlib import Path
from typing import Any

import yaml

from .allure_support import step
from .kubectl import kubectl_bin, wait_pod
from .utils import retry

logger = logging.getLogger(__name__)


def _resource_kind(crd_doc: dict[str, Any]) -> str:
    """Build the `plural.group` resource identifier from a CRD document."""
    return f"{crd_doc['spec']['names']['plural']}.{crd_doc['spec']['group']}"


def _remove_instance_finalizers(resource_kind: str) -> None:
    """Clear finalizers on all instances of a CR type across namespaces."""
    raw = kubectl_bin("get", resource_kind, "--all-namespaces", "-o", "json", check=False)
    if not raw.strip():
        return
    try:
        items = json.loads(raw)
    except json.JSONDecodeError:
        return
    for item in items.get("items", []):
        meta = item["metadata"]
        kubectl_bin(
            "patch",
            resource_kind,
            "-n",
            meta["namespace"],
            meta["name"],
            "--type=merge",
            "-p",
            '{"metadata":{"finalizers":[]}}',
            check=False,
        )


@step("Deploy the operator")
def deploy_operator(test_dir: str, src_dir: str, watch_namespace: str = "") -> None:
    """Deploy the operator. watch_namespace is the test namespace, used with SHARED_CLUSTER=1."""
    logger.info("Start PSMDB operator")
    prefix = "cw-" if os.environ.get("OPERATOR_NS") else ""

    crd_file = f"{test_dir}/conf/crd.yaml"
    if not os.path.isfile(crd_file):
        crd_file = f"{src_dir}/deploy/crd.yaml"

    kubectl_bin("apply", "--server-side", "--force-conflicts", "-f", crd_file)

    operator_file = f"{src_dir}/deploy/{prefix}operator.yaml"

    apply_rbac(src_dir, f"{prefix}rbac")

    with open(operator_file, "r") as f:
        data = yaml.safe_load(f)

    for container in data["spec"]["template"]["spec"]["containers"]:
        container["image"] = os.environ.get("IMAGE")
        if "env" in container:
            env_vars = {env["name"]: env for env in container["env"]}
            if "DISABLE_TELEMETRY" in env_vars:
                env_vars["DISABLE_TELEMETRY"]["value"] = "true"
            if "LOG_LEVEL" in env_vars:
                env_vars["LOG_LEVEL"]["value"] = "DEBUG"
            if "WATCH_NAMESPACE" in env_vars and prefix and _shared_cluster_operator():
                env_vars["WATCH_NAMESPACE"]["value"] = watch_namespace

    yaml_content = yaml.dump(data, default_flow_style=False)
    kubectl_bin("apply", "-f", "-", input_data=yaml_content)
    operator_pod = get_operator_pod()
    wait_pod(operator_pod)

    logs = kubectl_bin("logs", operator_pod)
    startup_logs = [line for line in logs.splitlines() if "Manager starting up" in line]
    if startup_logs:
        logger.info(f"Operator startup: {startup_logs[0]}")
    else:
        logger.warning("No 'Manager starting up' message found in logs")


def get_operator_pod() -> str:
    """Get the operator pod name"""
    args = [
        "get",
        "pods",
        "--selector=name=percona-server-mongodb-operator",
        "-o",
        "jsonpath={.items[*].metadata.name}",
    ]
    operator_ns = os.environ.get("OPERATOR_NS")
    if operator_ns:
        args.extend(["-n", operator_ns])

    def _fetch() -> str:
        out = kubectl_bin(*args, check=False)
        names = [n for n in out.strip().split() if n]

        if not names:
            raise RuntimeError("Operator pod not created yet")
        if len(names) > 1:
            raise RuntimeError(f"Multiple operator pods found: {names}")

        return names[0]

    return str(retry(_fetch, max_attempts=30, delay=2))


def apply_rbac(src_dir: str, rbac: str = "rbac") -> None:
    """Apply RBAC YAML with namespace substitution"""
    operator_ns = os.getenv("OPERATOR_NS", "psmdb-operator")
    path = Path(src_dir) / "deploy" / f"{rbac}.yaml"

    yaml_content = path.read_text()
    modified_yaml = re.sub(
        r"^(\s*)namespace:\s*.*$", rf"\1namespace: {operator_ns}", yaml_content, flags=re.MULTILINE
    )

    if _shared_cluster_operator():
        docs = [doc for doc in yaml.safe_load_all(modified_yaml) if doc]
        for doc in docs:
            if doc["kind"] in ("ClusterRole", "ClusterRoleBinding"):
                doc["metadata"]["name"] += f"-{operator_ns}"
            if doc["kind"] == "ClusterRoleBinding":
                doc["roleRef"]["name"] += f"-{operator_ns}"
        modified_yaml = yaml.safe_dump_all(docs)

    ns_flag = ["-n", operator_ns] if os.getenv("OPERATOR_NS") else []
    kubectl_bin("apply", *ns_flag, "-f", "-", input_data=modified_yaml)


def _shared_cluster_operator() -> bool:
    """SHARED_CLUSTER=1: other tests run on the same cluster at the same time. A cluster-wide
    operator then gets a ClusterRole and ClusterRoleBinding named after OPERATOR_NS and watches
    only the test namespace, so each test has its own operator."""
    return os.getenv("SHARED_CLUSTER") == "1" and bool(os.getenv("OPERATOR_NS"))


def delete_crd_rbac(src_dir: Path) -> None:
    logger.info("Deleting old CRDs and RBACs")
    crd_path = (src_dir / "deploy" / "crd.yaml").resolve()

    crds = [
        doc
        for doc in yaml.safe_load_all(crd_path.read_text())
        if doc and doc.get("kind") == "CustomResourceDefinition"
    ]

    kubectl_bin("delete", "-f", str(crd_path), "--ignore-not-found", "--wait=false", check=False)

    for crd in crds:
        _remove_instance_finalizers(_resource_kind(crd))

    for crd in crds:
        kubectl_bin("wait", "--for=delete", "crd", crd["metadata"]["name"], check=False)


def check_crd_for_deletion(file_path: Path) -> None:
    """Check and remove finalizers from CRDs to allow deletion"""
    for doc in yaml.safe_load_all(Path(file_path).read_text()):
        if not doc or doc.get("kind") != "CustomResourceDefinition":
            continue

        crd_name = doc["metadata"]["name"]
        try:
            result = kubectl_bin(
                "get",
                f"crd/{crd_name}",
                "-o",
                "jsonpath={.status.conditions[-1].type}",
                "--ignore-not-found",
            )
            if result.strip() != "Terminating":
                continue

            logger.info(f"Removing finalizers from CRD {crd_name} to allow deletion")
            kubectl_bin(
                "patch",
                f"crd/{crd_name}",
                "--type=merge",
                "-p",
                '{"metadata":{"finalizers":[]}}',
            )
            _remove_instance_finalizers(_resource_kind(doc))
        except Exception as e:
            logger.error(f"Error removing finalizers from CRD {crd_name}: {e}")
