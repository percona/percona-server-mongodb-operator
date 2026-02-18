import json
import logging
import os
import re
import subprocess
from pathlib import Path

import yaml

from .kubectl import kubectl_bin, wait_pod

logger = logging.getLogger(__name__)


def deploy_operator(test_dir: str, src_dir: str) -> None:
    """Deploy the operator with simplified logic."""
    logger.info("Start PSMDB operator")
    operator_ns = os.environ.get("OPERATOR_NS")

    crd_file = f"{test_dir}/conf/crd.yaml"
    if not os.path.isfile(crd_file):
        crd_file = f"{src_dir}/deploy/crd.yaml"

    kubectl_bin("apply", "--server-side", "--force-conflicts", "-f", crd_file)

    rbac_type = "cw-rbac" if operator_ns else "rbac"
    operator_file = f"{src_dir}/deploy/{'cw-' if operator_ns else ''}operator.yaml"

    apply_rbac(src_dir, rbac_type)

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
        "jsonpath={.items[].metadata.name}",
    ]
    operator_ns = os.environ.get("OPERATOR_NS")
    if operator_ns:
        args.extend(["-n", operator_ns])
    try:
        out = kubectl_bin(*args)
        names = [n for n in out.strip().split() if n]
        if not names:
            raise RuntimeError(
                "No Running operator pod found. Ensure the operator deployment succeeded"
            )
        if len(names) > 1:
            raise RuntimeError(f"Multiple operator pods found: {names}")
        return names[0]
    except Exception as e:
        raise RuntimeError(f"Failed to get operator pod: {e}") from e


def apply_rbac(src_dir: str, rbac: str = "rbac") -> None:
    """Apply RBAC YAML with namespace substitution"""
    operator_ns = os.getenv("OPERATOR_NS", "psmdb-operator")
    path = Path(src_dir) / "deploy" / f"{rbac}.yaml"

    yaml_content = path.read_text()
    modified_yaml = re.sub(
        r"^(\s*)namespace:\s*.*$", rf"\1namespace: {operator_ns}", yaml_content, flags=re.MULTILINE
    )

    args = ["apply", "-f", "-"]
    if os.getenv("OPERATOR_NS"):
        args = ["apply", "-n", operator_ns, "-f", "-"]

    kubectl_bin(*args, input_data=modified_yaml)


def delete_crd_rbac(src_dir: Path) -> None:
    logger.info("Deleting old CRDs and RBACs")
    crd_path = (src_dir / "deploy" / "crd.yaml").resolve()

    docs = list(yaml.safe_load_all(crd_path.read_text()))
    crd_names = []
    resource_kinds = []
    for doc in docs:
        if doc and doc.get("kind") == "CustomResourceDefinition":
            crd_names.append(doc["metadata"]["name"])
            group = doc["spec"]["group"]
            plural = doc["spec"]["names"]["plural"]
            resource_kinds.append(f"{plural}.{group}")

    kubectl_bin("delete", "-f", str(crd_path), "--ignore-not-found", "--wait=false", check=False)

    for kind in resource_kinds:
        try:
            items_json = kubectl_bin("get", kind, "--all-namespaces", "-o", "json")
            data = json.loads(items_json)
            for item in data.get("items", []):
                ns = item["metadata"]["namespace"]
                name = item["metadata"]["name"]
                kubectl_bin(
                    "patch",
                    kind,
                    "-n",
                    ns,
                    name,
                    "--type=merge",
                    "-p",
                    '{"metadata":{"finalizers":[]}}',
                )
        except subprocess.CalledProcessError:
            pass

    for name in crd_names:
        kubectl_bin("wait", "--for=delete", "crd", name, check=False)


def check_crd_for_deletion(file_path: str) -> None:
    """Check and remove finalizers from CRDs to allow deletion"""
    with open(file_path, "r") as f:
        yaml_content = f.read()

    for doc in yaml_content.split("---"):
        if not doc.strip():
            continue
        try:
            parsed_doc = yaml.safe_load(doc)
            if not parsed_doc or "metadata" not in parsed_doc:
                continue

            crd_name = parsed_doc["metadata"]["name"]

            result = kubectl_bin(
                "get",
                f"crd/{crd_name}",
                "-o",
                "jsonpath={.status.conditions[-1].type}",
                "--ignore-not-found",
            )
            is_crd_terminating = result.strip() == "Terminating"

            if is_crd_terminating:
                logger.info(f"Removing finalizers from CRD {crd_name} to allow deletion")
                kubectl_bin(
                    "patch",
                    f"crd/{crd_name}",
                    "--type=merge",
                    "-p",
                    '{"metadata":{"finalizers":[]}}',
                )
                try:
                    kubectl_bin(
                        "patch",
                        crd_name,
                        "--all-namespaces",
                        "--type=merge",
                        "-p",
                        '{"metadata":{"finalizers":[]}}',
                    )
                except Exception as patch_error:
                    logger.warning(
                        f"Could not patch {crd_name} instances (may not exist): {patch_error}"
                    )

        except yaml.YAMLError as yaml_error:
            logger.error(f"Error parsing YAML document: {yaml_error}")
        except Exception as e:
            logger.error(f"Error removing finalizers from CRD: {e}")
