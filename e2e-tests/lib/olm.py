"""Install and upgrade the operator through OLM (CatalogSource -> Subscription -> CSV)."""

import json
import logging
import time
from collections.abc import Callable
from typing import Any

from .kubectl import kubectl_bin
from .utils import retry

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
) -> list[str]:
    """Approve the Subscription's pending InstallPlans until target_csv is Succeeded.

    allowed_csvs(csv_names) decides whether a pending plan may be approved, so an
    install never jumps past the starting CSV. on_poll runs on every iteration
    (e.g. to sample DB health while the operator is replaced). Fails fast when an
    approved InstallPlan or the installed CSV reports Failed.

    Returns the approved InstallPlan names, in order.
    """
    approved: list[str] = []
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
            plan_status = get_json(INSTALLPLAN, plan, "-n", namespace).get("status", {})
            if plan_status.get("phase") == "Failed":
                raise RuntimeError(
                    f"InstallPlan {plan} failed: {plan_status.get('conditions', [])}"
                )

        plan = status.get("installPlanRef", {}).get("name")
        if plan and plan not in approved:
            spec = get_json(INSTALLPLAN, plan, "-n", namespace)["spec"]
            csvs = spec.get("clusterServiceVersionNames", [])
            if not spec.get("approved") and allowed_csvs(csvs):
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
        f"conditions={status.get('conditions', [])}"
    )


def delete_olm_install(namespace: str, subscription: str) -> None:
    """Best-effort removal of the Subscription, its CSVs and the OperatorGroup."""
    kubectl_bin(
        "delete", SUBSCRIPTION, subscription, "-n", namespace, "--ignore-not-found", check=False
    )
    kubectl_bin("delete", CSV, "--all", "-n", namespace, "--ignore-not-found", check=False)
    kubectl_bin(
        "delete", OPERATORGROUP, "--all", "-n", namespace, "--ignore-not-found", check=False
    )
