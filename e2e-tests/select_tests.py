# /// script
# requires-python = ">=3.11"
# dependencies = ["pyyaml"]
# ///
"""List e2e tests for PR and release jobs.

Test metadata: e2e-tests/tests.yaml

Examples:
  uv run e2e-tests/select_tests.py list --suite pr
  uv run e2e-tests/select_tests.py list --suite release --platform eks \\
      --operator-mode cluster-wide
  uv run e2e-tests/select_tests.py list --suite pr --group backup
  uv run e2e-tests/select_tests.py list --suite pr -o csv
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Any

import yaml

E2E_ROOT = Path(__file__).resolve().parent
TESTS_YAML = E2E_ROOT / "tests.yaml"

EXAMPLES = """\
examples:
  # Full PR suite
  select_tests.py list --suite pr

  # Release suite for a platform + operator mode
  select_tests.py list --suite release --platform eks --operator-mode cluster-wide

  # Tests in a group
  select_tests.py list --suite pr --group backup --platform gke

  # Full release suite for a platform
  select_tests.py list --suite release --platform openshift
"""


def load_yaml(path: Path) -> dict[str, Any]:
    with path.open() as f:
        return yaml.safe_load(f) or {}


def skip_names(entries: list[Any]) -> set[str]:
    """platformSkips entries may be plain names or {name, reason} objects."""
    return {e["name"] if isinstance(e, dict) else e for e in (entries or [])}


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
    suite: str,
    platform: str | None,
    operator_mode: str | None,
    groups: list[str] | None = None,
) -> dict[str, Any]:
    """Restrict selectable tests by suite, platform, operator mode, groups."""
    skipped = skip_names(platform_skips.get(platform, [])) if platform else set()
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
        help=f"Suite from tests.yaml (default: pr). Known: {', '.join(meta['suites']) or '?'}",
    )
    p.add_argument(
        "-p",
        "--platform",
        metavar="PLATFORM",
        help=f"Apply platformSkips. Known: {', '.join(meta['platforms']) or '?'}",
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


def load_context() -> tuple[dict[str, Any], dict[str, Any]]:
    manifest = load_yaml(TESTS_YAML)
    return manifest.get("tests", {}), manifest.get("platformSkips", {})


def filtered_universe(
    args: argparse.Namespace, all_tests: dict[str, Any], platform_skips: dict[str, Any]
) -> dict[str, Any]:
    if args.platform and args.platform not in platform_skips:
        print(
            f"warning: unknown platform '{args.platform}', no skips applied",
            file=sys.stderr,
        )
    return universe(
        all_tests,
        platform_skips,
        args.suite,
        args.platform,
        args.operator_mode,
        getattr(args, "groups", None),
    )


def cmd_list(args: argparse.Namespace) -> int:
    all_tests, platform_skips = load_context()
    tests = filtered_universe(args, all_tests, platform_skips)
    names = sorted(tests)
    if args.verbose:
        print(
            f"suite={args.suite} platform={args.platform} "
            f"operator_mode={args.operator_mode} count={len(names)}",
            file=sys.stderr,
        )
    emit(names, args.format)
    return 0


def build_parser() -> argparse.ArgumentParser:
    # Load meta early so --help shows known suites/platforms/groups.
    try:
        manifest = load_yaml(TESTS_YAML)
        meta = collect_meta(manifest.get("tests", {}), manifest.get("platformSkips", {}))
    except OSError:
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

    return parser


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    return int(args.func(args))


if __name__ == "__main__":
    raise SystemExit(main())
