"""Helpers of the OLM tests: install through OLM (CatalogSource -> Subscription -> CSV),
build objects from a bundle's alm-examples, read data back, check the operator pod."""

import json
import logging
import os
import re
import time
from collections.abc import Callable
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path
from typing import Any

from lib.kubectl import kubectl_bin
from lib.mongo import MongoManager
from lib.operator import check_crd_for_deletion, delete_crd_rbac
from lib.secrets import get_user_data
from lib.utils import Paths, retry

logger = logging.getLogger(__name__)

# Fully qualified: "subscription" alone can resolve to Knative's Subscription on OpenShift.
CATALOGSOURCE = "catalogsources.operators.coreos.com"
SUBSCRIPTION = "subscriptions.operators.coreos.com"
INSTALLPLAN = "installplans.operators.coreos.com"
CSV = "clusterserviceversions.operators.coreos.com"
OPERATORGROUP = "operatorgroups.operators.coreos.com"


def olm_available() -> bool:
    """True when the cluster serves the OLM PackageManifest API (OpenShift or OLM installed)."""
    out = kubectl_bin(
        "api-resources", "--api-group=packages.operators.coreos.com", "-o", "name", check=False
    )
    return "packagemanifests" in out


def get_json(*args: str) -> Any:
    return json.loads(kubectl_bin("get", *args, "-o", "json"))


def wait_until(
    what: str,
    fetch: Callable[[], Any],
    done: Callable[[Any], bool],
    timeout: int,
    interval: int = 5,
) -> Any:
    """Poll fetch() until done(value), else raise TimeoutError with the last value seen."""
    logger.info(f"Waiting for {what}")
    deadline = time.monotonic() + timeout
    value = None
    while time.monotonic() < deadline:
        value = fetch()
        if done(value):
            return value
        time.sleep(interval)
    raise TimeoutError(f"Timeout after {timeout}s waiting for {what}. Last value: {value}")


def wait_catalog_ready(source: str, namespace: str, timeout: int = 300) -> None:
    wait_until(
        f"CatalogSource {namespace}/{source} to be READY",
        lambda: kubectl_bin(
            "get",
            CATALOGSOURCE,
            source,
            "-n",
            namespace,
            "-o",
            "jsonpath={.status.connectionState.lastObservedState}",
            check=False,
        ).strip(),
        lambda state: state == "READY",
        timeout,
    )


def get_package_manifest(package: str, source: str, namespace: str) -> dict[str, Any]:
    """PackageManifest of `package` served by `source`.

    Selected by the catalog label: the same package name is usually served by
    several catalogs (community-operators, certified-operators, the dev ones).
    Retried because PackageManifests can lag behind a freshly READY catalog.
    """

    def _fetch() -> dict[str, Any]:
        items = get_json("packagemanifests", "-n", namespace, "-l", f"catalog={source}")["items"]
        for item in items:
            if item["metadata"]["name"] == package:
                return dict(item)
        raise LookupError(f"Package {package} not found in CatalogSource {namespace}/{source}")

    return dict(retry(_fetch, max_attempts=12, delay=5))


def channel_of_csv(manifest: dict[str, Any], csv: str) -> str:
    for channel in manifest["status"]["channels"]:
        if any(entry["name"] == csv for entry in channel.get("entries", [])):
            return str(channel["name"])
    raise LookupError(f"CSV {csv} is not in any channel of {manifest['metadata']['name']}")


def create_operator_group(namespace: str) -> None:
    """OwnNamespace OperatorGroup: the operator watches only its own namespace."""
    kubectl_bin(
        "apply",
        "-n",
        namespace,
        "-f",
        "-",
        input_data=json.dumps(
            {
                "apiVersion": "operators.coreos.com/v1",
                "kind": "OperatorGroup",
                "metadata": {"name": namespace},
                "spec": {"targetNamespaces": [namespace]},
            }
        ),
    )


def create_subscription(
    namespace: str,
    package: str,
    source: str,
    source_namespace: str,
    channel: str,
    starting_csv: str,
) -> None:
    """Subscribe with Manual approval so every install/upgrade step is test-driven."""
    kubectl_bin(
        "apply",
        "-n",
        namespace,
        "-f",
        "-",
        input_data=json.dumps(
            {
                "apiVersion": "operators.coreos.com/v1alpha1",
                "kind": "Subscription",
                "metadata": {"name": package},
                "spec": {
                    "name": package,
                    "source": source,
                    "sourceNamespace": source_namespace,
                    "channel": channel,
                    "startingCSV": starting_csv,
                    "installPlanApproval": "Manual",
                    "config": {"env": [{"name": "DISABLE_TELEMETRY", "value": "true"}]},
                },
            }
        ),
    )


def get_csv(namespace: str, name: str) -> dict[str, Any]:
    return dict(get_json(CSV, name, "-n", namespace))


def csv_names(namespace: str) -> list[str]:
    return kubectl_bin(
        "get", CSV, "-n", namespace, "-o", "jsonpath={.items[*].metadata.name}"
    ).split()


def csv_operator_image(csv: dict[str, Any]) -> str:
    deployment = csv["spec"]["install"]["spec"]["deployments"][0]
    return str(deployment["spec"]["template"]["spec"]["containers"][0]["image"])


def csv_example(csv: dict[str, Any], kind: str) -> dict[str, Any]:
    """The `kind` example from the CSV alm-examples (matches that release's crVersion/images)."""
    for example in json.loads(csv["metadata"]["annotations"]["alm-examples"]):
        if example.get("kind") == kind:
            return dict(example)
    raise LookupError(f"No {kind} in alm-examples of {csv['metadata']['name']}")


def subscription_status(namespace: str, subscription: str) -> dict[str, Any]:
    return dict(get_json(SUBSCRIPTION, subscription, "-n", namespace).get("status", {}))


def installplan_phase(namespace: str, plan: str) -> str:
    return kubectl_bin(
        "get", INSTALLPLAN, plan, "-n", namespace, "-o", "jsonpath={.status.phase}"
    ).strip()


def approve_until_installed(
    namespace: str,
    subscription: str,
    target_csv: str,
    allowed_csvs: Callable[[list[str]], bool],
    timeout: int = 900,
    on_poll: Callable[[], None] | None = None,
    delete_rejected: bool = False,
) -> list[str]:
    """Approve the Subscription's pending InstallPlans until target_csv is Succeeded.

    allowed_csvs(csv_names) decides whether a pending plan may be approved, so an
    install never jumps past the starting CSV. With delete_rejected, a pending plan
    it rejects is deleted so OLM resolves again instead of waiting on it. on_poll
    runs on every iteration (e.g. to sample DB health while the operator is
    replaced). Fails fast when an approved InstallPlan or the installed CSV reports
    Failed.

    Returns the approved InstallPlan names, in order.
    """
    approved: list[str] = []
    skipped: dict[str, list[str]] = {}
    deadline = time.monotonic() + timeout
    status: dict[str, Any] = {}
    while time.monotonic() < deadline:
        if on_poll:
            on_poll()

        status = subscription_status(namespace, subscription)
        installed = status.get("installedCSV")
        if installed:
            # The Subscription can still name a CSV that OLM already deleted after replacing it.
            raw = kubectl_bin("get", CSV, installed, "-n", namespace, "-o", "json", check=False)
            csv_status = json.loads(raw).get("status", {}) if raw.strip() else {}
            if installed == target_csv and csv_status.get("phase") == "Succeeded":
                return approved
            if csv_status.get("phase") == "Failed":
                raise RuntimeError(f"CSV {installed} failed: {csv_status.get('message')}")

        for plan in approved:
            plan_status = _installplan(namespace, plan).get("status", {})
            if plan_status.get("phase") == "Failed":
                raise RuntimeError(
                    f"InstallPlan {plan} failed: {plan_status.get('conditions', [])}"
                )

        # The reference can name a plan that was just deleted, until OLM resolves again.
        plan = status.get("installPlanRef", {}).get("name")
        spec = _installplan(namespace, plan).get("spec") if plan and plan not in approved else None
        if spec and not spec.get("approved"):
            csvs = spec.get("clusterServiceVersionNames", [])
            if not allowed_csvs(csvs):
                if plan not in skipped:
                    logger.warning(f"Not approving InstallPlan {plan}: {csvs}")
                    skipped[plan] = csvs
                if delete_rejected:
                    delete_pending_plans(namespace, keep=allowed_csvs)
            else:
                logger.info(f"Approving InstallPlan {plan}: {csvs}")
                kubectl_bin(
                    "patch",
                    INSTALLPLAN,
                    plan,
                    "-n",
                    namespace,
                    "--type=merge",
                    "-p",
                    '{"spec":{"approved":true}}',
                )
                approved.append(plan)
        time.sleep(5)

    raise TimeoutError(
        f"Timeout after {timeout}s waiting for {target_csv} to be installed. "
        f"Subscription status: state={status.get('state')} "
        f"installedCSV={status.get('installedCSV')} currentCSV={status.get('currentCSV')} "
        f"conditions={status.get('conditions', [])} "
        f"not approved InstallPlans={skipped}"
    )


def _installplan(namespace: str, name: str) -> dict[str, Any]:
    """The InstallPlan, or {} when it no longer exists."""
    raw = kubectl_bin("get", INSTALLPLAN, name, "-n", namespace, "-o", "json", check=False)
    return dict(json.loads(raw)) if raw.strip() else {}


def delete_pending_plans(namespace: str, keep: Callable[[list[str]], bool]) -> list[str]:
    """Delete unapproved InstallPlans that keep(csv_names) rejects, so OLM resolves again.

    A Manual Subscription keeps pointing at its pending plan; while that plan exists,
    OLM does not offer a different upgrade.
    """
    deleted = []
    for plan in get_json(INSTALLPLAN, "-n", namespace)["items"]:
        csvs = plan["spec"].get("clusterServiceVersionNames", [])
        if plan["spec"].get("approved") or keep(csvs):
            continue
        name = plan["metadata"]["name"]
        logger.info(f"Deleting pending InstallPlan {name}: {csvs}")
        kubectl_bin("delete", INSTALLPLAN, name, "-n", namespace, "--ignore-not-found")
        deleted.append(name)
    return deleted


def delete_olm_install(namespace: str, subscription: str) -> None:
    """Best-effort removal of the Subscription, its CSVs and the OperatorGroup."""
    kubectl_bin(
        "delete", SUBSCRIPTION, subscription, "-n", namespace, "--ignore-not-found", check=False
    )
    kubectl_bin("delete", CSV, "--all", "-n", namespace, "--ignore-not-found", check=False)
    kubectl_bin(
        "delete", OPERATORGROUP, "--all", "-n", namespace, "--ignore-not-found", check=False
    )


# Storage name and PBM-side settings of the SeaweedFS fixture (deploy_s3_storage in
# e2e-tests/conftest.py); the credentials are e2e-tests/conf/s3-secret.yml.
LOCAL_S3 = "local-s3"
LOCAL_S3_STORAGE = {
    "type": "s3",
    "s3": {
        "bucket": "operator-testing",
        "credentialsSecret": "s3-secret",
        "region": "us-east-1",
        "endpointUrl": "http://s3-storage-all-in-one:8333/",
        "insecureSkipTLSVerify": False,
    },
}

APP_USER = "myApp"
APP_PASSWORD = "myPass"
RS0_SIZE = 3

# Operator log lines matching these are expected while controllers settle after a restart.
# Calibrate on real runs; keep each entry specific.
ALLOWED_OPERATOR_ERRORS = [
    re.compile(r"the object has been modified; please apply your changes to the latest version"),
]
OPERATOR_ERROR = re.compile(r"\tERROR\t|\"level\":\"error\"")


def version(csv: str) -> tuple[int, ...]:
    """1.22.0 from percona-server-mongodb-operator.v1.22.0 (also covers -certified names)."""
    match = re.search(r"v?(\d+)\.(\d+)\.(\d+)$", csv)
    assert match, f"Cannot parse a version from CSV name {csv}"
    return tuple(int(part) for part in match.groups())


@dataclass(frozen=True)
class CatalogTarget:
    """The dev catalog under test and the CSV at the head of its target channel."""

    package: str
    source: str
    source_namespace: str
    target_channel: str
    target_csv: str
    manifest: dict[str, Any]


def resolve_target() -> CatalogTarget:
    """Resolve the catalog and the target CSV from the OLM_* variables.

    OLM_BUNDLE_TYPE selects the dev catalog (community|certified, see installers/olm);
    OLM_PACKAGE, OLM_CATALOG_SOURCE and OLM_CATALOG_NAMESPACE override the derived
    names. The target is the head of OLM_TARGET_CHANNEL (default: the catalog's default
    channel). It must be OLM_TARGET_VERSION, which for a dev catalog defaults to
    OPERATOR_VERSION (crVersion of deploy/cr.yaml), so a stale catalog fails fast.
    """
    bundle_type = os.environ.get("OLM_BUNDLE_TYPE", "community")
    package = os.environ.get("OLM_PACKAGE") or (
        "percona-server-mongodb-operator" + ("-certified" if bundle_type == "certified" else "")
    )
    source = os.environ.get("OLM_CATALOG_SOURCE") or f"{bundle_type}-dev"
    source_namespace = os.environ.get("OLM_CATALOG_NAMESPACE") or "openshift-marketplace"

    wait_catalog_ready(source, source_namespace)
    manifest = get_package_manifest(package, source, source_namespace)
    target_channel = os.environ.get("OLM_TARGET_CHANNEL") or manifest["status"]["defaultChannel"]
    target_csv = next(
        channel["currentCSV"]
        for channel in manifest["status"]["channels"]
        if channel["name"] == target_channel
    )
    # Only dev catalogs (installers/olm, CATALOG_ENV=dev) are built from this checkout,
    # and only their default channel must hold the version under test.
    expected = os.environ.get("OLM_TARGET_VERSION") or (
        os.environ.get("OPERATOR_VERSION", "")
        if source.endswith("-dev") and not os.environ.get("OLM_TARGET_CHANNEL")
        else ""
    )
    if expected:
        assert version(target_csv) == version(expected), (
            f"Catalog {source} serves {target_csv}, but the version under test is "
            f"{expected}. Rebuild the catalog (make deploy/<type>) or set OLM_TARGET_VERSION."
        )
    return CatalogTarget(package, source, source_namespace, target_channel, target_csv, manifest)


def olm_test_paths(test_file: Path) -> Paths:
    """test_paths for a test under installers/olm/e2e-tests/<name>/ (conf from e2e-tests/)."""
    src_dir = test_file.parents[4]
    return {
        "test_file": str(test_file),
        "test_dir": str(test_file.parent),
        "conf_dir": str(src_dir / "e2e-tests" / "conf"),
        "src_dir": str(src_dir),
    }


def delete_crds(src_dir: str) -> None:
    """OLM leaves CRDs behind when a CSV is deleted; a stale newer CRD blocks older installs."""
    delete_crd_rbac(Path(src_dir))
    check_crd_for_deletion(Path(src_dir) / "deploy" / "crd.yaml")


def teardown(namespace: str, package: str, src_dir: str) -> None:
    """Remove the OLM install, the CRDs and the namespace, in that order.

    Operator first, so OLM cannot reinstall anything; CRD deletion then strips the
    psmdb finalizers the operator is no longer there to handle.
    """
    delete_olm_install(namespace, package)
    delete_crds(src_dir)
    kubectl_bin(
        "delete", "namespace", namespace, "--ignore-not-found", "--wait=false", check=False
    )


def install(namespace: str, target: CatalogTarget, channel: str, csv: str) -> dict[str, Any]:
    """Install `csv` from `channel` and return the installed CSV, once it is Succeeded."""
    create_operator_group(namespace)
    create_subscription(
        namespace, target.package, target.source, target.source_namespace, channel, csv
    )
    approve_until_installed(namespace, target.package, csv, allowed_csvs=lambda csvs: csv in csvs)
    return get_csv(namespace, csv)


def example_cluster(csv: dict[str, Any], name: str, storage: str | None) -> dict[str, Any]:
    """A 3-member rs0 from the CSV's example CR, with backups to `storage` or disabled.

    The example matches the release's crVersion and images. Sharding, PMM, PITR and the
    extra member types are off; automatic upgrades are off so images stay pinned.
    """
    cr = csv_example(csv, "PerconaServerMongoDB")
    cr["metadata"] = {"name": name, "finalizers": cr["metadata"].get("finalizers", [])}
    spec = cr["spec"]
    spec["upgradeOptions"] = {"apply": "disabled"}
    spec.setdefault("secrets", {})["users"] = f"{name}-secrets"
    for section in ("sharding", "pmm"):
        spec.setdefault(section, {})["enabled"] = False
    backup = spec.setdefault("backup", {})
    backup["enabled"] = storage is not None
    backup["pitr"] = {"enabled": False}
    backup.pop("tasks", None)
    backup["storages"] = {storage: LOCAL_S3_STORAGE} if storage else {}
    rs0 = spec["replsets"][0]
    rs0["size"] = RS0_SIZE
    rs0["affinity"] = {"antiAffinityTopologyKey": "none"}
    for member in ("arbiter", "nonvoting", "hidden"):
        if member in rs0:
            rs0[member]["enabled"] = False
    spec["replsets"] = [rs0]
    return cr


def example_backup(
    csv: dict[str, Any], name: str, cluster: str, storage: str, backup_type: str
) -> dict[str, Any]:
    """A PerconaServerMongoDBBackup from the CSV's example."""
    backup = csv_example(csv, "PerconaServerMongoDBBackup")
    backup["metadata"] = {"name": name, "finalizers": backup["metadata"].get("finalizers", [])}
    backup["spec"] = {"clusterName": cluster, "storageName": storage, "type": backup_type}
    return backup


def example_restore(csv: dict[str, Any], name: str, cluster: str, backup: str) -> dict[str, Any]:
    """A PerconaServerMongoDBRestore from the CSV's example."""
    restore = csv_example(csv, "PerconaServerMongoDBRestore")
    restore["metadata"] = {"name": name}
    restore["spec"] = {"clusterName": cluster, "backupName": backup}
    return restore


def apply(namespace: str, obj: dict[str, Any]) -> None:
    logger.info(f"Applying {obj['kind']}/{obj['metadata']['name']}")
    kubectl_bin("apply", "-n", namespace, "-f", "-", input_data=json.dumps(obj))


def cluster_uri(
    cluster: str,
    namespace: str,
    user: str,
    password: str,
    pod: int | None = None,
    component: str = "rs0",
) -> str:
    """user:password@host of the `component` Service (rs0, cfg or mongos), or of one pod."""
    service = f"{cluster}-{component}"
    host = service + (f"-{pod}.{service}" if pod is not None else "")
    return f"{user}:{password}@{host}.{namespace}"


def _mongosh(client: MongoManager, command: str, uri: str, component: str) -> str:
    if component == "mongos":
        # mongos is a ClusterIP Service: no SRV record and no replica set name.
        return client.run_mongosh(command, uri, driver="mongodb", replica_set="")
    return client.run_mongosh(command, uri)


def create_app_user(
    client: MongoManager,
    cluster: str,
    namespace: str,
    component: str = "rs0",
    users_secret: str = "",
) -> None:
    """Create the readWrite APP_USER through `component` as the user admin of `users_secret`
    (default: <cluster>-secrets)."""
    secret = users_secret or f"{cluster}-secrets"
    _mongosh(
        client,
        f'db.createUser({{user:"{APP_USER}",pwd:"{APP_PASSWORD}",'
        f'roles:[{{db:"{APP_USER}",role:"readWrite"}}]}})',
        cluster_uri(
            cluster,
            namespace,
            get_user_data(secret, "MONGODB_USER_ADMIN_USER"),
            get_user_data(secret, "MONGODB_USER_ADMIN_PASSWORD"),
            component=component,
        ),
        component,
    )


def insert(
    client: MongoManager, cluster: str, namespace: str, value: int, component: str = "rs0"
) -> None:
    """Insert {x: value} into myApp.test through `component`, retried until acknowledged."""
    uri = cluster_uri(cluster, namespace, APP_USER, APP_PASSWORD, component=component)
    retry(
        lambda: _mongosh(
            client,
            f"db.getSiblingDB('{APP_USER}').test.insertOne({{ x: {value} }})",
            uri,
            component,
        ),
        condition=lambda result: "acknowledged: true" in result,
    )


def check_members(client: MongoManager, cluster: str, namespace: str, expected_file: str) -> None:
    """Every rs0 member returns the documents of `expected_file`, over a direct connection.

    A replicaSet URI (run_mongosh, compare_mongo_cmd) sends every read to the primary.
    """
    expected = json.loads(Path(expected_file).read_text())
    query = (
        f"EJSON.stringify(db.getSiblingDB('{APP_USER}').test.find({{}}, {{ _id: 0 }}).toArray())"
    )
    for i in range(RS0_SIZE):
        uri = (
            f"mongodb://{cluster_uri(cluster, namespace, APP_USER, APP_PASSWORD, i)}"
            ".svc.cluster.local/admin?ssl=false&directConnection=true"
            "&readPreference=secondaryPreferred"
        )

        def read(uri: str = uri) -> Any:
            out = kubectl_bin(
                "exec",
                client.client,
                "--",
                "env",
                "HOME=/tmp",
                "mongosh",
                uri,
                "--quiet",
                "--eval",
                query,
                check=False,
            )
            try:
                return json.loads(out)
            except json.JSONDecodeError:
                return out

        wait_until(
            f"member {i} to return {expected}",
            read,
            lambda result: result == expected,
            timeout=60,
            interval=5,
        )


def operator_deployment(csv: dict[str, Any]) -> str:
    return str(csv["spec"]["install"]["spec"]["deployments"][0]["name"])


def operator_pod(namespace: str, csv: dict[str, Any]) -> dict[str, Any]:
    """The single pod of the CSV's operator Deployment, found by its own selector."""
    deployment = csv["spec"]["install"]["spec"]["deployments"][0]
    labels = deployment["spec"]["selector"]["matchLabels"]
    selector = ",".join(f"{key}={value}" for key, value in labels.items())
    pods = wait_until(
        f"a single {deployment['name']} pod",
        lambda: get_json("pods", "-n", namespace, "-l", selector)["items"],
        lambda items: len(items) == 1,
        timeout=300,
    )
    return dict(pods[0])


def operator_restarts(pod: dict[str, Any]) -> int:
    return sum(c.get("restartCount", 0) for c in pod["status"].get("containerStatuses", []))


@dataclass(frozen=True)
class ErrorWindow:
    """An operator error line expected only between `start` and `end`, at most `max_count`
    times. The time is the RFC3339 timestamp that starts each operator log line."""

    pattern: re.Pattern[str]
    start: datetime
    end: datetime
    max_count: int


def _log_time(line: str) -> datetime | None:
    try:
        logged = datetime.fromisoformat(line.split(maxsplit=1)[0])
    except (IndexError, ValueError):
        return None
    return logged if logged.tzinfo else None


def unexpected_errors(lines: list[str], windows: tuple[ErrorWindow, ...] = ()) -> list[str]:
    """Error lines not covered by ALLOWED_OPERATOR_ERRORS or by one of `windows`.

    A line that matches a window's pattern outside its time range, or after the window
    has allowed `max_count` lines, is returned with a note saying so.
    """
    used = [0] * len(windows)
    errors = []
    for line in lines:
        if not OPERATOR_ERROR.search(line) or any(
            pattern.search(line) for pattern in ALLOWED_OPERATOR_ERRORS
        ):
            continue
        note = ""
        for i, window in enumerate(windows):
            if not window.pattern.search(line):
                continue
            logged = _log_time(line)
            if logged is None or not window.start <= logged <= window.end:
                note = f"  [allowed only between {window.start} and {window.end}]"
            elif used[i] >= window.max_count:
                note = f"  [allowed at most {window.max_count} times]"
            else:
                used[i] += 1
                break
        else:
            errors.append(line + note)
    return errors


def operator_errors(
    namespace: str, pod_name: str, windows: tuple[ErrorWindow, ...] = ()
) -> list[str]:
    """Error lines of the operator log, see unexpected_errors."""
    return unexpected_errors(kubectl_bin("logs", pod_name, "-n", namespace).splitlines(), windows)


def assert_operator_healthy(
    namespace: str,
    csv: dict[str, Any],
    expected_uid: str = "",
    windows: tuple[ErrorWindow, ...] = (),
) -> dict[str, Any]:
    """The operator pod is Ready, was not replaced or restarted, and logged no errors.

    `windows` allows error lines that are expected only during a step of the caller.
    Returns the pod.
    """
    pod = operator_pod(namespace, csv)
    name = pod["metadata"]["name"]
    if expected_uid:
        assert pod["metadata"]["uid"] == expected_uid, f"Operator pod was replaced by {name}"
    ready = [c["status"] for c in pod["status"].get("conditions", []) if c["type"] == "Ready"]
    assert ready == ["True"], f"Operator pod {name} is not Ready: {pod['status']}"
    if restarts := operator_restarts(pod):
        previous = kubectl_bin("logs", name, "-n", namespace, "--previous", check=False)
        raise AssertionError(f"Operator restarted {restarts} times. Previous logs:\n{previous}")
    errors = operator_errors(namespace, name, windows)
    assert not errors, "Operator logged errors:\n" + "\n".join(errors)
    return pod


def warning_events(namespace: str) -> dict[str, dict[str, Any]]:
    events = get_json("events", "-n", namespace, "--field-selector=type=Warning")
    return {ev["metadata"]["uid"]: ev for ev in events["items"]}


def event_count(event: dict[str, Any]) -> int:
    return int(event.get("count") or event.get("series", {}).get("count") or 1)


# Only read by the version-service cron, which upgradeOptions.apply=disabled and
# DISABLE_TELEMETRY leave idle: changing it bumps the CR generation without touching pods.
RECONCILE_PROBE = '{"spec":{"upgradeOptions":{"schedule":"0 3 * * *"}}}'


def wait_reconciled(namespace: str, cluster: str, timeout: int = 300) -> None:
    """Bump the generation of psmdb/`cluster` and wait for status.observedGeneration.

    Proves that the operator reconciles the CR: an idle operator also leaves the
    cluster unchanged.
    """
    before = kubectl_bin(
        "get", "psmdb", cluster, "-n", namespace, "-o", "jsonpath={.metadata.generation}"
    )
    generation = kubectl_bin(
        "patch",
        "psmdb",
        cluster,
        "-n",
        namespace,
        "--type=merge",
        "-p",
        RECONCILE_PROBE,
        "-o",
        "jsonpath={.metadata.generation}",
    )
    assert int(generation) > int(before), (
        f"{RECONCILE_PROBE} did not change psmdb/{cluster} (generation {before})"
    )
    wait_until(
        f"the operator to reconcile generation {generation} of {cluster}",
        lambda: kubectl_bin(
            "get",
            "psmdb",
            cluster,
            "-n",
            namespace,
            "-o",
            "jsonpath={.status.observedGeneration}",
            check=False,
        ).strip(),
        lambda observed: observed == generation,
        timeout=timeout,
    )
