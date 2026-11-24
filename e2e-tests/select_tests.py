# /// script
# requires-python = ">=3.11"
# dependencies = ["pyyaml"]
# ///
"""List e2e tests for PR and release jobs.

Test metadata: e2e-tests/tests.yaml

Examples:
  uv run e2e-tests/select_tests.py list --suite pr
  uv run e2e-tests/select_tests.py list --suite release --platform eks \\
      --operator-mode cluster-wide --mongo-version 8.0
  uv run e2e-tests/select_tests.py list --suite pr --group backup
  uv run e2e-tests/select_tests.py list --suite pr -o csv
  uv run e2e-tests/select_tests.py placement
  uv run e2e-tests/select_tests.py check-placement
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import yaml

E2E_ROOT = Path(__file__).resolve().parent
TESTS_YAML = E2E_ROOT / "tests.yaml"

OWN_CLUSTER = "ownCluster"


@dataclass(frozen=True)
class PlacementRule:
    """Something a test does that every other test on the same cluster can see.

    `needs` is OWN_CLUSTER (the test runs alone on a cluster) or a lock name (tests with the
    same lock run one at a time on a cluster, next to tests without it).
    """

    name: str
    pattern: re.Pattern[str]
    needs: str
    why: str


def _rule(name: str, pattern: str, needs: str, why: str) -> PlacementRule:
    return PlacementRule(name, re.compile(pattern, re.MULTILINE), needs, why)


# Checked against every file of a test directory except compare/. A match without the tag in
# tests.yaml fails `check-placement`. ownCluster satisfies every rule.
PLACEMENT_RULES = [
    _rule(
        "chaos-mesh",
        r"deploy_chaos_mesh|chaos_mesh",
        OWN_CLUSTER,
        "chaos-mesh installs cluster-wide webhooks and daemons, and its teardown removes them",
    ),
    _rule(
        "crd",
        r"delete_crd|delete crd|/crd\.yaml|^kind: CustomResourceDefinition",
        OWN_CLUSTER,
        "replaces or deletes a CRD, which every test on the cluster uses",
    ),
    _rule(
        "released-operator",
        r"\bGIT_TAG\b",
        OWN_CLUSTER,
        "deploys a released operator, with that release's CRD",
    ),
    _rule(
        "default-storageclass",
        r"is-default-class=true|annotate storageclass",
        OWN_CLUSTER,
        "changes the default StorageClass for every new PVC",
    ),
    _rule(
        "admission-webhook",
        r"^kind: (Mutating|Validating)WebhookConfiguration",
        OWN_CLUSTER,
        "an admission webhook acts on objects in every namespace",
    ),
    _rule(
        "vpa",
        r"install_vpa_crd|vpa-v1-crd|goldilocks",
        OWN_CLUSTER,
        "installs the cluster-wide VerticalPodAutoscaler CRD (and for Goldilocks the VPA "
        "recommender), which the operator watches in every namespace",
    ),
    _rule(
        "cert-manager",
        r"deploy_cert_manager|cert_manager|^kind: ClusterIssuer",
        OWN_CLUSTER,
        "with cert-manager installed the operator issues every test's certificates through it; "
        "other tests run without it, as in serial runs",
    ),
    _rule(
        "vault",
        r"deploy_vault|destroy_vault",
        "vault",
        "the Vault chart creates cluster-scoped RBAC and a webhook named after the release",
    ),
    _rule(
        "cluster-rbac",
        r"(create|delete|patch|edit|replace) (clusterrole|clusterrolebinding)\b"
        r"|clusterrole/|^kind: (ClusterRole|ClusterRoleBinding)$",
        "cluster-rbac",
        "creates, changes or deletes ClusterRoles or ClusterRoleBindings",
    ),
    _rule(
        "nodes",
        r"(label|taint|cordon|uncordon|drain|annotate) nodes?\b",
        "nodes",
        "changes labels, taints or scheduling of shared nodes",
    ),
    _rule(
        "storageclass",
        r"^kind: StorageClass",
        "storageclass",
        "creates a cluster-scoped StorageClass",
    ),
]

EXAMPLES = """\
examples:
  # Full PR suite
  select_tests.py list --suite pr

  # Release suite for a platform + operator mode
  select_tests.py list --suite release --platform eks --operator-mode cluster-wide \\
      --mongo-version 8.0

  # Tests in a group
  select_tests.py list --suite pr --group backup --platform gke

  # Full release suite for a platform
  select_tests.py list --suite release --platform openshift
"""


def load_yaml(path: Path) -> dict[str, Any]:
    with path.open() as f:
        return yaml.safe_load(f) or {}


def skip_names(entries: list[Any]) -> set[str]:
    """Skip entries may be plain names or {name, reason} objects."""
    return {e["name"] if isinstance(e, dict) else e for e in (entries or [])}


def parse_mongo_version(value: str) -> tuple[int, int]:
    parts = value.split(".")
    if len(parts) != 2 or not all(part.isdigit() for part in parts):
        raise argparse.ArgumentTypeError("expected MAJOR.MINOR, for example 8.0")
    return int(parts[0]), int(parts[1])


def mongo_skip_names(
    version_groups: list[dict[str, Any]], version: tuple[int, int] | None
) -> set[str]:
    if version is None:
        return set()
    for group in version_groups:
        if group.get("version") == list(version):
            return skip_names(group.get("tests", []))
    return set()


def collect_meta(tests: dict[str, Any], platform_skips: dict[str, Any]) -> dict[str, list[str]]:
    suites: set[str] = set()
    modes: set[str] = set()
    groups: set[str] = set()
    for meta in tests.values():
        suites.update(meta.get("suites") or [])
        modes.update(meta.get("operatorModes") or [])
        groups.update(meta.get("groups") or [])
    return {
        "suites": sorted(suites),
        "modes": sorted(modes),
        "groups": sorted(groups),
        "platforms": sorted(platform_skips),
    }


def universe(
    tests: dict[str, Any],
    platform_skips: dict[str, Any],
    mongo_version_skips: list[dict[str, Any]],
    suite: str,
    platform: str | None,
    mongo_version: tuple[int, int] | None,
    operator_mode: str | None,
    groups: list[str] | None = None,
) -> dict[str, Any]:
    """Restrict selectable tests by suite, platform, MongoDB version, mode, groups."""
    skipped = skip_names(platform_skips.get(platform, [])) if platform else set()
    skipped.update(mongo_skip_names(mongo_version_skips, mongo_version))
    want_groups = set(groups or ())
    out = {}
    for name, meta in tests.items():
        if suite not in (meta.get("suites") or []):
            continue
        if name in skipped:
            continue
        if operator_mode and operator_mode not in (meta.get("operatorModes") or []):
            continue
        if want_groups and not (set(meta.get("groups") or []) & want_groups):
            continue
        out[name] = meta
    return out


def emit(names: list[str], fmt: str) -> None:
    if fmt == "count":
        print(len(names))
        return
    if fmt == "csv":
        print(",".join(names))
        return
    if fmt == "json":
        json.dump(names, sys.stdout)
        print()
        return
    for name in names:
        print(name)


def add_filter_args(p: argparse.ArgumentParser, meta: dict[str, list[str]]) -> None:
    p.add_argument(
        "-s",
        "--suite",
        default="pr",
        metavar="SUITE",
        choices=meta["suites"] or None,
        help=f"Suite from tests.yaml (default: pr). Known: {', '.join(meta['suites']) or '?'}",
    )
    p.add_argument(
        "-p",
        "--platform",
        metavar="PLATFORM",
        help=f"Apply platformSkips. Known: {', '.join(meta['platforms']) or '?'}",
    )
    p.add_argument(
        "--mongo-version",
        type=parse_mongo_version,
        metavar="MAJOR.MINOR",
        help="Apply mongoVersionSkips for an exact MongoDB major.minor version.",
    )
    p.add_argument(
        "-m",
        "--operator-mode",
        metavar="MODE",
        choices=meta["modes"] or None,
        help="Filter by operatorModes (cluster-wide|namespaced).",
    )
    p.add_argument(
        "-g",
        "--group",
        action="append",
        dest="groups",
        metavar="GROUP",
        help=f"Restrict to group(s). Repeatable. Known: {', '.join(meta['groups']) or '?'}",
    )


def add_output_args(p: argparse.ArgumentParser) -> None:
    p.add_argument(
        "-o",
        "--format",
        choices=["lines", "csv", "json", "count"],
        default="lines",
        help="Output format (default: lines).",
    )
    p.add_argument(
        "-v",
        "--verbose",
        action="store_true",
        help="Print selection details to stderr.",
    )


def load_context() -> tuple[dict[str, Any], dict[str, Any], list[dict[str, Any]]]:
    manifest = load_yaml(TESTS_YAML)
    return (
        manifest.get("tests", {}),
        manifest.get("platformSkips", {}),
        manifest.get("mongoVersionSkips", []),
    )


def filtered_universe(
    args: argparse.Namespace,
    all_tests: dict[str, Any],
    platform_skips: dict[str, Any],
    mongo_version_skips: list[dict[str, Any]],
) -> dict[str, Any]:
    if args.platform and args.platform not in platform_skips:
        print(
            f"warning: unknown platform '{args.platform}', no skips applied",
            file=sys.stderr,
        )
    return universe(
        all_tests,
        platform_skips,
        mongo_version_skips,
        args.suite,
        args.platform,
        args.mongo_version,
        args.operator_mode,
        getattr(args, "groups", None),
    )


def cmd_list(args: argparse.Namespace) -> int:
    all_tests, platform_skips, mongo_version_skips = load_context()
    tests = filtered_universe(args, all_tests, platform_skips, mongo_version_skips)
    names = sorted(tests)
    if args.verbose:
        mongo_version = (
            ".".join(str(part) for part in args.mongo_version) if args.mongo_version else None
        )
        print(
            f"suite={args.suite} platform={args.platform} "
            f"mongo_version={mongo_version} operator_mode={args.operator_mode} "
            f"count={len(names)}",
            file=sys.stderr,
        )
    emit(names, args.format)
    return 0


def placement_of(meta: dict[str, Any]) -> tuple[bool, list[str]]:
    return bool(meta.get(OWN_CLUSTER, False)), list(meta.get("locks") or [])


def cmd_placement(args: argparse.Namespace) -> int:
    """Print the tests that cannot share a cluster freely, for the CI runner."""
    all_tests, _, _ = load_context()
    out: dict[str, dict[str, Any]] = {}
    for name in sorted(all_tests):
        own, locks = placement_of(all_tests[name] or {})
        if own or locks:
            out[name] = {OWN_CLUSTER: own, "locks": locks}
    json.dump(out, sys.stdout, indent=2 if args.pretty else None)
    print()
    return 0


def test_files(test_dir: Path) -> list[Path]:
    """Files a test runs or applies: everything except expected objects and caches."""
    skip = {"compare", "__pycache__"}
    return sorted(
        path
        for path in test_dir.rglob("*")
        if path.is_file() and not skip.intersection(path.relative_to(test_dir).parts)
    )


def placement_findings(test_dir: Path) -> dict[str, str]:
    """Map each placement rule a test triggers to the first matching file:line."""
    findings: dict[str, str] = {}
    for path in test_files(test_dir):
        try:
            text = path.read_text()
        except (OSError, UnicodeDecodeError):
            continue
        for rule in PLACEMENT_RULES:
            if rule.name in findings:
                continue
            match = rule.pattern.search(text)
            if match:
                line = text.count("\n", 0, match.start()) + 1
                findings[rule.name] = f"{path.relative_to(E2E_ROOT)}:{line}"
    return findings


def placement_errors(all_tests: dict[str, Any]) -> list[str]:
    rules = {rule.name: rule for rule in PLACEMENT_RULES}
    errors: list[str] = []
    for name in sorted(all_tests):
        meta = all_tests[name] or {}
        own = meta.get(OWN_CLUSTER, False)
        locks = meta.get("locks") or []
        if not isinstance(own, bool):
            errors.append(f"{name}: {OWN_CLUSTER} must be true or false")
            continue
        if not isinstance(locks, list) or not all(isinstance(lock, str) for lock in locks):
            errors.append(f"{name}: locks must be a list of names")
            continue
        if own and locks:
            errors.append(f"{name}: {OWN_CLUSTER} already runs the test alone, drop locks")
        test_dir = E2E_ROOT / name
        if own or not test_dir.is_dir():
            continue
        missing = [
            (rules[rule_name], where)
            for rule_name, where in placement_findings(test_dir).items()
            if rules[rule_name].needs not in locks
        ]
        if not missing:
            continue
        if any(rule.needs == OWN_CLUSTER for rule, _ in missing):
            tag = f"{OWN_CLUSTER}: true"
        else:
            wanted = sorted(set(locks) | {rule.needs for rule, _ in missing})
            tag = f"locks: [{', '.join(wanted)}]"
        reasons = "; ".join(f"{rule.why} ({where})" for rule, where in missing)
        errors.append(f"{name}: needs `{tag}`: {reasons}")
    return errors


def cmd_check_placement(args: argparse.Namespace) -> int:
    """Fail when a test does something cluster-wide without the matching tag in tests.yaml."""
    all_tests, _, _ = load_context()
    errors = placement_errors(all_tests)
    for error in errors:
        print(error, file=sys.stderr)
    if errors:
        print(
            f"\n{len(errors)} placement problem(s) in {TESTS_YAML.name}. "
            "See the placement notes at the top of tests.yaml.",
            file=sys.stderr,
        )
        return 1
    print(f"placement OK for {len(all_tests)} tests")
    return 0


def build_parser() -> argparse.ArgumentParser:
    # Load meta early so --help shows known suites/platforms/groups.
    try:
        manifest = load_yaml(TESTS_YAML)
        meta = collect_meta(manifest.get("tests", {}), manifest.get("platformSkips", {}))
    except (OSError, yaml.YAMLError):
        meta = {"suites": [], "modes": [], "groups": [], "platforms": []}

    parser = argparse.ArgumentParser(
        prog="select_tests.py",
        description="List e2e tests for PR / release jobs.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=EXAMPLES,
    )
    sub = parser.add_subparsers(dest="command", required=True, metavar="COMMAND")

    p_list = sub.add_parser(
        "list",
        help="List tests matching filters.",
        description="List every test in the filtered universe.",
    )
    add_filter_args(p_list, meta)
    add_output_args(p_list)
    p_list.set_defaults(func=cmd_list)

    p_placement = sub.add_parser(
        "placement",
        help="Print tests that need their own cluster or a lock, as JSON.",
        description=(
            "Print {test: {ownCluster, locks}} for every test that cannot share a cluster "
            "freely. The CI runner reads it when it runs several tests per cluster."
        ),
    )
    p_placement.add_argument("--pretty", action="store_true", help="Indent the JSON.")
    p_placement.set_defaults(func=cmd_placement)

    p_check = sub.add_parser(
        "check-placement",
        help="Check that tests making cluster-wide changes are tagged in tests.yaml.",
        description="Exit 1 when a test matches a placement rule without the matching tag.",
    )
    p_check.set_defaults(func=cmd_check_placement)

    return parser


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    return int(args.func(args))


if __name__ == "__main__":
    raise SystemExit(main())
