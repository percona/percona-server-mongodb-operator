import json
import logging
from pathlib import Path

import yaml

from .cli import oc_bin
from .config import apply_cluster
from .kubectl import (
    kubectl_bin,
    wait_certificate,
    wait_cluster_consistency,
    wait_for_delete,
    wait_for_running,
)
from .mongo import MongoManager
from .utils import env_bool, render_yaml_diff, retry

logger = logging.getLogger(__name__)

# Users from conf/secrets.yml and the LDAP group from openldap.yaml.
_USER_ADMIN = "userAdmin:userAdmin123456"
_LDAP_USER = "percona:password"
_LDAP_GROUP_ROLE = "cn=admin,ou=perconadba,dc=ldap,dc=local"
_LDAP_GROUP_ROLES = (
    "readAnyDatabase",
    "dbAdminAnyDatabase",
    "clusterMonitor",
    "readWriteAnyDatabase",
    "restore",
    "backup",
)


def deploy_openldap(test_dir: str, namespace: str, tls: bool, timeout: int) -> None:
    """Deploy openldap; with tls, issue its certificate through cert-manager first."""
    kubectl_bin("create", "serviceaccount", "openldap")
    if env_bool("OPENSHIFT"):
        oc_bin("adm", "policy", "add-scc-to-user", "anyuid", "-z", "openldap", "-n", namespace)

    docs = [d for d in yaml.safe_load_all(Path(f"{test_dir}/conf/openldap.yaml").read_text()) if d]
    if tls:
        cert_docs = [d for d in docs if d["kind"] in ("Issuer", "Certificate")]
        for doc in cert_docs:
            if doc["metadata"]["name"] == "ldap-ca":
                doc["spec"]["dnsNames"] = [f"openldap.{namespace}.svc.cluster.local"]
        kubectl_bin("apply", "-f", "-", input_data=yaml.safe_dump_all(cert_docs))
        # openldap copies the certs once on start, so it must not start before ldap-ca is issued
        wait_certificate("ldap-ca")
        docs = [d for d in docs if d not in cert_docs]

    kubectl_bin("apply", "-f", "-", input_data=yaml.safe_dump_all(docs))
    kubectl_bin("rollout", "status", "deployment/openldap", f"--timeout={timeout}s")


def _create_conf_secret(name: str, key: str, conf_file: str, ldap_server: str) -> None:
    conf = yaml.safe_load(Path(conf_file).read_text())
    conf["security"]["ldap"]["servers"] = ldap_server
    kubectl_bin(
        "create", "secret", "generic", name, f"--from-literal={key}={yaml.safe_dump(conf)}"
    )


def _check_ldap_auth(
    client: MongoManager, host: str, compare_file: str, driver: str, replica_set: str | None
) -> None:
    """Map the LDAP group to a role, then check an LDAP user gets that role on login."""
    roles = [{"role": role, "db": "admin"} for role in _LDAP_GROUP_ROLES]
    client.run_mongosh(
        f'db.getSiblingDB("admin").createRole({{role: "{_LDAP_GROUP_ROLE}", '
        f"privileges: [], roles: {json.dumps(roles)}}})",
        f"{_USER_ADMIN}@{host}",
        driver=driver,
        replica_set=replica_set,
    )

    out = retry(
        lambda: client.run_mongosh(
            "JSON.stringify(db.runCommand({connectionStatus:1}))",
            f"{_LDAP_USER}@{host}",
            driver=driver,
            mongo_flag="--authenticationMechanism PLAIN --authenticationDatabase $external",
            replica_set=replica_set,
        ),
        max_attempts=5,
        delay=10,
    )
    actual = json.loads(out)["authInfo"]
    actual["authenticatedUserRoles"].sort(key=lambda r: r["role"])
    expected = json.loads(Path(compare_file).read_text())
    message = render_yaml_diff(expected, actual, compare_file, "connectionStatus.authInfo")
    assert not message, message


def _delete_cluster(cluster: str) -> None:
    kubectl_bin("delete", "psmdb", cluster)
    wait_for_delete(f"psmdb/{cluster}")
    kubectl_bin("delete", "pvc", "-l", f"app.kubernetes.io/instance={cluster}")


def check_replset_ldap(
    client: MongoManager, test_dir: str, namespace: str, ldap_server: str
) -> None:
    cluster = "some-name"
    _create_conf_secret(
        f"{cluster}-rs0-mongod", "mongod.conf", f"{test_dir}/conf/mongod.conf", ldap_server
    )
    apply_cluster(f"{test_dir}/conf/{cluster}.yml")
    wait_for_running(f"{cluster}-rs0", 3)

    _check_ldap_auth(
        client,
        f"{cluster}-rs0.{namespace}",
        f"{test_dir}/compare/authInfo.json",
        driver="mongodb+srv",
        replica_set=None,
    )
    _delete_cluster(cluster)


def check_sharded_ldap(
    client: MongoManager, test_dir: str, namespace: str, ldap_server: str
) -> None:
    cluster = "some-name-sharded"
    _create_conf_secret(
        f"{cluster}-mongos", "mongos.conf", f"{test_dir}/conf/mongos.conf", ldap_server
    )
    _create_conf_secret(
        f"{cluster}-cfg-mongod", "mongod.conf", f"{test_dir}/conf/mongod.conf", ldap_server
    )
    apply_cluster(f"{test_dir}/conf/{cluster}.yml")
    wait_for_running(f"{cluster}-rs0", 3)
    wait_for_running(f"{cluster}-cfg", 3, False)
    wait_for_running(f"{cluster}-mongos", 3)
    wait_cluster_consistency(cluster)

    _check_ldap_auth(
        client,
        f"{cluster}-mongos.{namespace}",
        f"{test_dir}/compare/authInfo.json",
        driver="mongodb",
        replica_set="",
    )
    _delete_cluster(cluster)
