import atexit
import base64
import logging
import os
import subprocess
import tempfile
import urllib.parse
from pathlib import Path
from typing import Optional

from .kubectl import kubectl_bin

logger = logging.getLogger(__name__)

_temp_files_to_cleanup: list[str] = []


def _cleanup_temp_files() -> None:
    for path in _temp_files_to_cleanup:
        try:
            if os.path.exists(path):
                os.unlink(path)
        except OSError:
            pass


atexit.register(_cleanup_temp_files)


def get_secret_data(secret_name: str, data_key: str) -> str:
    """Get and decode secret data from Kubernetes"""
    try:
        result = kubectl_bin(
            "get", f"secrets/{secret_name}", "-o", f"jsonpath={{.data.{data_key}}}"
        ).strip()
        decoded_data = base64.b64decode(result).decode("utf-8")
        return decoded_data
    except subprocess.CalledProcessError as e:
        logger.error(f"Error: {e.stderr}")
        return ""


def get_user_data(secret_name: str, data_key: str) -> str:
    """Get and URL-encode secret data"""
    secret_data = get_secret_data(secret_name, data_key)
    return urllib.parse.quote(secret_data, safe="")


def get_cloud_secret_default(conf_dir: Optional[Path] = None) -> str:
    """Return default for SKIP_BACKUPS_TO_AWS_GCP_AZURE based on cloud-secret.yml existence."""
    if conf_dir is None:
        conf_dir = Path(__file__).parent.parent / "conf"
    if (conf_dir / "cloud-secret.yml").exists():
        return ""
    return "1"


def apply_s3_storage_secrets(conf_dir: str) -> None:
    """Apply secrets for cloud storages."""
    if not os.environ.get("SKIP_BACKUPS_TO_AWS_GCP_AZURE"):
        logger.info("Creating secrets for cloud storages (minio + cloud)")
        kubectl_bin(
            "apply",
            "-f",
            f"{conf_dir}/minio-secret.yml",
            "-f",
            f"{conf_dir}/cloud-secret.yml",
        )
    else:
        logger.info("Creating secrets for cloud storages (minio only)")
        kubectl_bin("apply", "-f", f"{conf_dir}/minio-secret.yml")


def setup_gcs_credentials(secret_name: str = "gcp-cs-secret") -> bool:
    """Setup GCS credentials from K8s secret for gsutil."""
    result = subprocess.run(["gsutil", "ls"], capture_output=True, check=False)
    if result.returncode == 0:
        logger.info("GCS credentials already set in environment")
        return True

    logger.info(f"Setting up GCS credentials from K8s secret: {secret_name}")

    access_key = get_secret_data(secret_name, "AWS_ACCESS_KEY_ID")
    secret_key = get_secret_data(secret_name, "AWS_SECRET_ACCESS_KEY")

    if not access_key or not secret_key:
        logger.error("Failed to extract GCS credentials from secret")
        return False

    boto_fd, boto_path = tempfile.mkstemp(prefix="boto.", suffix=".cfg")
    try:
        with os.fdopen(boto_fd, "w") as f:
            f.write("[Credentials]\n")
            f.write(f"gs_access_key_id = {access_key}\n")
            f.write(f"gs_secret_access_key = {secret_key}\n")
        os.chmod(boto_path, 0o600)
        os.environ["BOTO_CONFIG"] = boto_path
        _temp_files_to_cleanup.append(boto_path)
        logger.info("GCS credentials configured successfully")
        return True
    except Exception as e:
        logger.error(f"Failed to create boto config: {e}")
        os.unlink(boto_path)
        return False


# TODO: implement this function
def check_passwords_leak(namespace: Optional[str] = None) -> None:
    """Check for password leaks in Kubernetes pod logs."""
    pass
