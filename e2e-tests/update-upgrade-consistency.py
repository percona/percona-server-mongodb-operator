#!/usr/bin/env python3

import argparse
import re
import shutil
from pathlib import Path


VERSION_RE = re.compile(r"^\d+\.\d+\.\d+$")


def version_suffix(version: str) -> str:
    return version.replace(".", "")


def read_text(path: Path) -> str:
    return path.read_text()


def write_text(path: Path, text: str) -> None:
    path.write_text(text)


def replace_single(pattern: str, repl: str, text: str, *, flags: int = 0) -> str:
    result, count = re.subn(pattern, repl, text, count=1, flags=flags)
    if count != 1:
        raise ValueError(f"Expected 1 match for pattern: {pattern}")
    return result


def replace_ordered(pattern: str, replacements: list[str], text: str, *, flags: int = 0) -> str:
    compiled = re.compile(pattern, flags)
    idx = 0

    def _repl(_match: re.Match[str]) -> str:
        nonlocal idx
        if idx >= len(replacements):
            raise ValueError(f"Too many matches for pattern: {pattern}")
        value = replacements[idx]
        idx += 1
        return value

    result = compiled.sub(_repl, text)
    if idx != len(replacements):
        raise ValueError(
            f"Expected {len(replacements)} matches for pattern: {pattern}, got {idx}"
        )
    return result


def parse_suffixes(path: Path, resource_pattern: str, expected_count: int) -> list[str]:
    text = read_text(path)
    matches = re.findall(resource_pattern, text)
    if len(matches) != expected_count:
        raise ValueError(
            f"{path}: expected {expected_count} suffix matches, got {len(matches)}"
        )
    return matches


def render_compare_path(compare_dir: Path, template: str, suffix: str) -> Path:
    return compare_dir / template.format(suffix=suffix)


def read_generation(compare_dir: Path, template: str, suffix: str) -> int:
    path = render_compare_path(compare_dir, template, suffix)
    if not path.exists():
        raise FileNotFoundError(f"Missing compare file: {path}")
    text = read_text(path)
    match = re.search(r"^  generation:\s*(\d+)\s*$", text, flags=re.MULTILINE)
    if not match:
        raise ValueError(f"Missing metadata.generation in: {path}")
    return int(match.group(1))


def set_generation(path: Path, generation: int) -> None:
    text = read_text(path)
    updated, count = re.subn(
        r"^  generation:\s*\d+\s*$",
        f"  generation: {generation}",
        text,
        count=1,
        flags=re.MULTILINE,
    )
    if count != 1:
        raise ValueError(f"Failed updating metadata.generation in: {path}")
    write_text(path, updated)


def ensure_file(target: Path, source: Path) -> None:
    if target.exists():
        return
    if not source.exists():
        raise FileNotFoundError(f"Cannot create {target}, source missing: {source}")
    shutil.copyfile(source, target)


def ensure_and_set_generations(
    compare_dir: Path,
    templates: list[str],
    old_suffixes: list[str],
    new_suffixes: list[str],
    stage_generations: list[int],
) -> None:
    for template in templates:
        for idx, new_suffix in enumerate(new_suffixes):
            target = render_compare_path(compare_dir, template, new_suffix)
            source = render_compare_path(compare_dir, template, old_suffixes[idx])
            ensure_file(target, source)
            if template.startswith("statefulset_"):
                set_generation(target, stage_generations[idx])


def update_conf(conf_path: Path, start_version: str) -> None:
    text = read_text(conf_path)
    text = replace_single(r"(crVersion:\s*)\d+\.\d+\.\d+", rf"\g<1>{start_version}", text)
    write_text(conf_path, text)


def update_run_upgrade_consistency(run_path: Path, versions: list[str], suffixes: list[str]) -> None:
    start, mid, final = versions
    start_suffix, mid_suffix, final_suffix = suffixes

    text = read_text(run_path)
    text = replace_single(
        r'desc "create first PSMDB cluster \d+\.\d+\.\d+ \$CLUSTER"',
        f'desc "create first PSMDB cluster {start} $CLUSTER"',
        text,
    )
    text = replace_ordered(r"desc 'test \d+\.\d+\.\d+'", [f"desc 'test {mid}'", f"desc 'test {final}'"], text)
    text = replace_ordered(
        r'"spec": \{"crVersion":"\d+\.\d+\.\d+"\}',
        [f'"spec": {{"crVersion":"{mid}"}}', f'"spec": {{"crVersion":"{final}"}}'],
        text,
    )
    text = replace_ordered(
        r'compare_kubectl service/\$\{CLUSTER\}-rs0 "-\d+"',
        [
            f'compare_kubectl service/${{CLUSTER}}-rs0 "-{start_suffix}"',
            f'compare_kubectl service/${{CLUSTER}}-rs0 "-{mid_suffix}"',
            f'compare_kubectl service/${{CLUSTER}}-rs0 "-{final_suffix}"',
        ],
        text,
    )
    text = replace_ordered(
        r'compare_kubectl statefulset/\$\{CLUSTER\}-rs0 "-\d+"',
        [
            f'compare_kubectl statefulset/${{CLUSTER}}-rs0 "-{start_suffix}"',
            f'compare_kubectl statefulset/${{CLUSTER}}-rs0 "-{mid_suffix}"',
            f'compare_kubectl statefulset/${{CLUSTER}}-rs0 "-{final_suffix}"',
        ],
        text,
    )
    write_text(run_path, text)


def update_run_upgrade_consistency_sharded_tls(
    run_path: Path, versions: list[str], suffixes: list[str]
) -> None:
    start, mid, final = versions
    start_suffix, mid_suffix, final_suffix = suffixes

    text = read_text(run_path)
    text = replace_single(
        r'desc "create first PSMDB cluster \d+\.\d+\.\d+ \$CLUSTER"',
        f'desc "create first PSMDB cluster {start} $CLUSTER"',
        text,
    )
    text = replace_ordered(r"desc 'test \d+\.\d+\.\d+'", [f"desc 'test {mid}'", f"desc 'test {final}'"], text)
    text = replace_ordered(
        r'"spec": \{"crVersion":"\d+\.\d+\.\d+"\}',
        [f'"spec": {{"crVersion":"{mid}"}}', f'"spec": {{"crVersion":"{final}"}}'],
        text,
    )

    text = replace_ordered(
        r'compare_kubectl service/\$\{CLUSTER\}-rs0 "-\d+"',
        [
            f'compare_kubectl service/${{CLUSTER}}-rs0 "-{start_suffix}"',
            f'compare_kubectl service/${{CLUSTER}}-rs0 "-{mid_suffix}"',
            f'compare_kubectl service/${{CLUSTER}}-rs0 "-{final_suffix}"',
        ],
        text,
    )
    text = replace_ordered(
        r'compare_kubectl service/\$\{CLUSTER\}-cfg "-\d+"',
        [
            f'compare_kubectl service/${{CLUSTER}}-cfg "-{start_suffix}"',
            f'compare_kubectl service/${{CLUSTER}}-cfg "-{mid_suffix}"',
            f'compare_kubectl service/${{CLUSTER}}-cfg "-{final_suffix}"',
        ],
        text,
    )
    text = replace_ordered(
        r'compare_kubectl statefulset/\$\{CLUSTER\}-rs0 "-\d+"',
        [
            f'compare_kubectl statefulset/${{CLUSTER}}-rs0 "-{start_suffix}"',
            f'compare_kubectl statefulset/${{CLUSTER}}-rs0 "-{mid_suffix}"',
            f'compare_kubectl statefulset/${{CLUSTER}}-rs0 "-{final_suffix}"',
        ],
        text,
    )
    text = replace_ordered(
        r'compare_kubectl statefulset/\$\{CLUSTER\}-cfg "-\d+"',
        [
            f'compare_kubectl statefulset/${{CLUSTER}}-cfg "-{start_suffix}"',
            f'compare_kubectl statefulset/${{CLUSTER}}-cfg "-{mid_suffix}"',
            f'compare_kubectl statefulset/${{CLUSTER}}-cfg "-{final_suffix}"',
        ],
        text,
    )

    write_text(run_path, text)


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Update upgrade-consistency e2e tests for a new release."
    )
    parser.add_argument("--start", required=True, help="Start CR version, e.g. 1.21.2")
    parser.add_argument("--mid", required=True, help="Middle CR version, e.g. 1.22.0")
    parser.add_argument("--final", required=True, help="Final CR version, e.g. 1.23.0")
    args = parser.parse_args()

    versions = [args.start, args.mid, args.final]
    for value in versions:
        if not VERSION_RE.match(value):
            raise ValueError(f"Invalid version format: {value}")

    suffixes = [version_suffix(v) for v in versions]

    repo_root = Path(__file__).resolve().parent.parent
    e2e_dir = repo_root / "e2e-tests"

    uc_run = e2e_dir / "upgrade-consistency" / "run"
    uc_conf = e2e_dir / "upgrade-consistency" / "conf" / "some-name-rs0.yml"
    uc_compare = e2e_dir / "upgrade-consistency" / "compare"

    sh_run = e2e_dir / "upgrade-consistency-sharded-tls" / "run"
    sh_conf = e2e_dir / "upgrade-consistency-sharded-tls" / "conf" / "some-name.yml"
    sh_compare = e2e_dir / "upgrade-consistency-sharded-tls" / "compare"

    old_uc_suffixes = parse_suffixes(
        uc_run, r'compare_kubectl statefulset/\$\{CLUSTER\}-rs0 "-(\d+)"', 3
    )
    old_sh_suffixes = parse_suffixes(
        sh_run, r'compare_kubectl statefulset/\$\{CLUSTER\}-rs0 "-(\d+)"', 3
    )

    uc_stage_gens = [
        read_generation(uc_compare, "statefulset_some-name-rs0-{suffix}.yml", old_uc_suffixes[0]),
        read_generation(uc_compare, "statefulset_some-name-rs0-{suffix}.yml", old_uc_suffixes[1]),
        read_generation(uc_compare, "statefulset_some-name-rs0-{suffix}.yml", old_uc_suffixes[2]),
    ]
    sh_stage_gens = [
        read_generation(sh_compare, "statefulset_some-name-rs0-{suffix}.yml", old_sh_suffixes[0]),
        read_generation(sh_compare, "statefulset_some-name-rs0-{suffix}.yml", old_sh_suffixes[1]),
        read_generation(sh_compare, "statefulset_some-name-rs0-{suffix}.yml", old_sh_suffixes[2]),
    ]

    update_conf(uc_conf, args.start)
    update_conf(sh_conf, args.start)

    update_run_upgrade_consistency(uc_run, versions, suffixes)
    update_run_upgrade_consistency_sharded_tls(sh_run, versions, suffixes)

    ensure_and_set_generations(
        uc_compare,
        [
            "service_some-name-rs0-{suffix}.yml",
            "statefulset_some-name-rs0-{suffix}.yml",
            "statefulset_some-name-rs0-{suffix}-oc.yml",
        ],
        old_uc_suffixes,
        suffixes,
        uc_stage_gens,
    )

    ensure_and_set_generations(
        sh_compare,
        [
            "service_some-name-rs0-{suffix}.yml",
            "service_some-name-cfg-{suffix}.yml",
            "statefulset_some-name-rs0-{suffix}.yml",
            "statefulset_some-name-rs0-{suffix}-oc.yml",
            "statefulset_some-name-cfg-{suffix}.yml",
            "statefulset_some-name-cfg-{suffix}-oc.yml",
        ],
        old_sh_suffixes,
        suffixes,
        sh_stage_gens,
    )

    print(
        "Updated upgrade-consistency suites:",
        f"start={args.start}, mid={args.mid}, final={args.final}",
    )


if __name__ == "__main__":
    main()
