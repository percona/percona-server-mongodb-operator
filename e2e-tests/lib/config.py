import logging
import os
import subprocess

import yaml
from deepdiff import DeepDiff

from .kubectl import kubectl_bin

logger = logging.getLogger(__name__)


def cat_config(config_file: str) -> str:
    """Process config file with yq transformations"""
    with open(config_file, "r") as f:
        config = yaml.safe_load(f)

    if "spec" in config:
        spec = config["spec"]

        if "image" not in spec or spec["image"] is None:
            spec["image"] = os.environ.get("IMAGE_MONGOD")

        if "pmm" in spec:
            spec["pmm"]["image"] = os.environ.get("IMAGE_PMM_CLIENT")

        if "initImage" in spec:
            spec["initImage"] = os.environ.get("IMAGE")

        if "backup" in spec:
            spec["backup"]["image"] = os.environ.get("IMAGE_BACKUP")

        if "upgradeOptions" not in spec:
            spec["upgradeOptions"] = {}
        spec["upgradeOptions"]["apply"] = "Never"

    return yaml.dump(config)


def apply_cluster(config_file: str) -> None:
    """Apply cluster configuration"""
    logger.info("Creating PSMDB cluster")
    config_yaml = cat_config(config_file)

    if not os.environ.get("SKIP_BACKUPS_TO_AWS_GCP_AZURE"):
        kubectl_bin("apply", "-f", "-", input_data=config_yaml)
    else:
        config = yaml.safe_load(config_yaml)
        if "spec" in config and "backup" in config["spec"] and "tasks" in config["spec"]["backup"]:
            config["spec"]["backup"]["tasks"] = config["spec"]["backup"]["tasks"][:1]
        kubectl_bin("apply", "-f", "-", input_data=yaml.dump(config))


def filter_yaml(
    yaml_content: str, namespace: str, resource: str = "", skip_generation_check: bool = False
) -> str:
    """Filter YAML content using yq command"""

    # TODO: consider using Python for filtering instead of yq
    yq_filter = f"""
        del(.metadata.ownerReferences[].apiVersion) |
        del(.metadata.managedFields) |
        del(.. | select(has("creationTimestamp")).creationTimestamp) |
        del(.. | select(has("namespace")).namespace) |
        del(.. | select(has("uid")).uid) |
        del(.metadata.resourceVersion) |
        del(.spec.template.spec.containers[].env[] | select(.name == "NAMESPACE")) |
        del(.metadata.selfLink) |
        del(.metadata.annotations."cloud.google.com/neg") |
        del(.metadata.annotations."kubectl.kubernetes.io/last-applied-configuration") |
        del(.. | select(has("image")).image) |
        del(.. | select(has("clusterIP")).clusterIP) |
        del(.. | select(has("clusterIPs")).clusterIPs) |
        del(.. | select(has("dataSource")).dataSource) |
        del(.. | select(has("procMount")).procMount) |
        del(.. | select(has("storageClassName")).storageClassName) |
        del(.. | select(has("finalizers")).finalizers) |
        del(.. | select(has("kubernetes.io/pvc-protection"))."kubernetes.io/pvc-protection") |
        del(.. | select(has("volumeName")).volumeName) |
        del(.. | select(has("volume.beta.kubernetes.io/storage-provisioner"))."volume.beta.kubernetes.io/storage-provisioner") |
        del(.. | select(has("volume.kubernetes.io/storage-provisioner"))."volume.kubernetes.io/storage-provisioner") |
        del(.spec.volumeMode) |
        del(.. | select(has("volume.kubernetes.io/selected-node"))."volume.kubernetes.io/selected-node") |
        del(.. | select(has("percona.com/last-config-hash"))."percona.com/last-config-hash") |
        del(.. | select(has("percona.com/configuration-hash"))."percona.com/configuration-hash") |
        del(.. | select(has("percona.com/ssl-hash"))."percona.com/ssl-hash") |
        del(.. | select(has("percona.com/ssl-internal-hash"))."percona.com/ssl-internal-hash") |
        del(.spec.volumeClaimTemplates[].spec.volumeMode | select(. == "Filesystem")) |
        del(.. | select(has("healthCheckNodePort")).healthCheckNodePort) |
        del(.. | select(has("nodePort")).nodePort) |
        del(.status) |
        (.. | select(tag == "!!str")) |= sub("{namespace}"; "NAME_SPACE") |
        del(.spec.volumeClaimTemplates[].apiVersion) |
        del(.spec.volumeClaimTemplates[].kind) |
        del(.spec.ipFamilies) |
        del(.spec.ipFamilyPolicy) |
        del(.spec.persistentVolumeClaimRetentionPolicy) |
        del(.spec.internalTrafficPolicy) |
        del(.spec.allocateLoadBalancerNodePorts) |
        (.. | select(. == "extensions/v1beta1")) = "apps/v1" |
        (.. | select(. == "batch/v1beta1")) = "batch/v1"
    """

    cmd = ["yq", "eval", yq_filter.strip(), "-"]
    result = subprocess.run(cmd, input=yaml_content, text=True, capture_output=True, check=True)
    filtered_yaml = result.stdout

    if "cronjob" in resource.lower() or skip_generation_check:
        cmd = ["yq", "eval", "del(.metadata.generation)", "-"]
        result = subprocess.run(
            cmd, input=filtered_yaml, text=True, capture_output=True, check=True
        )
        filtered_yaml = result.stdout

    return filtered_yaml


def compare_kubectl(test_dir: str, resource: str, namespace: str, postfix: str = "") -> None:
    """Compare kubectl resource with expected output using yq filtering"""
    expected_result = f"{test_dir}/compare/{resource.replace('/', '_')}{postfix}.yml"

    try:
        actual_yaml = kubectl_bin("get", resource, "-o", "yaml")
        with open(expected_result, "r") as f:
            expected_yaml = f.read()

        filtered_actual = filter_yaml(actual_yaml, namespace)
        filtered_expected = filter_yaml(expected_yaml, namespace)

        actual_data = yaml.safe_load(filtered_actual)
        expected_data = yaml.safe_load(filtered_expected)

        diff = DeepDiff(expected_data, actual_data)
        assert not diff, f"YAML files differ: {diff.pretty()}"

    except subprocess.CalledProcessError as e:
        raise ValueError(f"Failed to process resource {resource}: {e}")


def apply_runtime_class(test_dir: str) -> None:
    """Apply runtime class configuration"""
    logger.info("Applying runc runtime class")
    with open(f"{test_dir}/../conf/container-rc.yaml", "r") as f:
        content = f.read()
    if os.environ.get("EKS"):
        content = content.replace("docker", "runc")
    kubectl_bin("apply", "-f", "-", input_data=content)
