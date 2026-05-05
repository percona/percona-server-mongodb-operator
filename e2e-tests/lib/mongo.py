import json
import logging
import os
import re
from pathlib import Path
from typing import Any, Dict

from deepdiff import DeepDiff

from .kubectl import kubectl_bin
from .utils import retry

logger = logging.getLogger(__name__)


class MongoManager:
    def __init__(self, client: str):
        self.client = client

    def run_mongosh(
        self,
        command: str,
        uri: str,
        driver: str = "mongodb+srv",
        suffix: str = ".svc.cluster.local",
        mongo_flag: str = "",
        timeout: int = 30,
    ) -> str:
        """Execute mongosh command in PSMDB client container."""
        replica_set = "cfg" if "cfg" in uri else "rs0"
        connection_string = f"{driver}://{uri}{suffix}/admin?ssl=false&replicaSet={replica_set}"
        if mongo_flag:
            connection_string += f" {mongo_flag}"

        result = kubectl_bin(
            "exec",
            self.client,
            "--",
            "timeout",
            str(timeout),
            "mongosh",
            f"{connection_string}",
            "--eval",
            command,
            "--quiet",
            check=False,
        )
        return result

    def compare_mongo_user(self, uri: str, expected_role: str, test_dir: str) -> None:
        """Compare MongoDB user permissions"""

        def get_expected_file(test_dir: str, user: str) -> Any:
            """Get the appropriate expected file based on MongoDB version"""
            base_path = Path(test_dir) / "compare"
            base_file = base_path / f"{user}.json"

            image_mongod = os.environ.get("IMAGE_MONGOD", "")
            version_mappings = [("8.0", "-80"), ("7.0", "-70"), ("6.0", "-60")]

            for version, suffix in version_mappings:
                if version in image_mongod:
                    version_file = base_path / f"{user}{suffix}.json"
                    if version_file.exists():
                        logger.info(f"Using version-specific file: {version_file}")
                        with open(version_file) as f:
                            return json.load(f)

            if base_file.exists():
                logger.info(f"Using base file: {base_file}")
                with open(base_file) as f:
                    return json.load(f)
            else:
                raise FileNotFoundError(f"Expected file not found: {base_file}")

        def clean_mongo_json(data: Dict[str, Any]) -> Dict[str, Any]:
            """Remove timestamps and metadata from MongoDB response"""

            def remove_timestamps(obj: Any) -> Any:
                if isinstance(obj, dict):
                    return {
                        k: remove_timestamps(v)
                        for k, v in obj.items()
                        if k not in {"ok", "$clusterTime", "operationTime"}
                    }
                elif isinstance(obj, list):
                    return [remove_timestamps(v) for v in obj]
                elif isinstance(obj, str):
                    return re.sub(
                        r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}[\+\-]\d{4}", "", obj
                    )
                else:
                    return obj

            cleaned = remove_timestamps(data)
            if not isinstance(cleaned, dict):
                raise TypeError("Expected cleaned MongoDB response to be a dict")
            return cleaned

        result = retry(
            lambda: self.run_mongosh(
                "EJSON.stringify(db.runCommand({connectionStatus:1,showPrivileges:true}))",
                uri,
            )
        )
        actual_data = clean_mongo_json(json.loads(result))
        expected_data = get_expected_file(test_dir, expected_role)

        diff = DeepDiff(expected_data, actual_data, ignore_order=True)
        assert not diff, f"MongoDB user permissions differ: {diff.pretty()}"

    def compare_mongo_cmd(
        self,
        command: str,
        uri: str,
        postfix: str = "",
        suffix: str = "",
        database: str = "myApp",
        collection: str = "test",
        sort: str = "",
        test_file: str = "",
    ) -> None:
        """Compare MongoDB command output"""
        full_cmd = f"{collection}.{command}"
        if sort:
            full_cmd = f"{collection}.{command}.{sort}"

        logger.info(f"Running: {full_cmd} on db {database}")

        mongo_expr = f"EJSON.stringify(db.getSiblingDB('{database}').{full_cmd})"
        result = json.loads(self.run_mongosh(mongo_expr, uri, "mongodb"))

        logger.info(f"MongoDB output: {result}")

        with open(test_file) as file:
            expected = json.load(file)

        diff = DeepDiff(expected, result)
        assert not diff, f"MongoDB command output differs: {diff.pretty()}"

    def get_mongo_primary(self, uri: str, cluster_name: str) -> str:
        """Get current MongoDB primary node"""
        primary_endpoint = self.run_mongosh("EJSON.stringify(db.hello().me)", uri)

        if cluster_name in primary_endpoint:
            return primary_endpoint.split(".")[0].replace('"', "")
        else:
            endpoint_host = primary_endpoint.split(":")[0]
            result = kubectl_bin("get", "service", "-o", "wide")

            for line in result.splitlines():
                if endpoint_host in line:
                    return line.split()[0].replace('"', "")
            raise ValueError("Primary node not found in service list")
