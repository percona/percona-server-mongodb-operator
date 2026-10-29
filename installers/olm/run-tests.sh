#!/usr/bin/env bash

set -euo pipefail

BUNDLE_TYPE="${1:-}"

CATALOG_NAMESPACE="${CATALOG_NAMESPACE:-openshift-marketplace}"
CATALOG_ENV="${CATALOG_ENV:-dev}"
NAME="${NAME:-percona-server-mongodb-operator}"
OLM_TEST="${OLM_TEST:-olm-upgrade}"
SRC_DIR="$(cd "$(dirname "$0")/../.." && pwd)"

usage() {
	cat <<EOF
Usage:
  $0 <community|certified>

Runs the installers/olm/e2e-tests/${OLM_TEST} test once per starting CSV:
every bundle of the deployed ${CATALOG_ENV} catalog except the default channel
head is upgraded to that head. It fails when fewer than OLM_MIN_MINORS (2)
previous minor versions are covered. Deploy the catalog first (make deploy/<type>).

Set OLM_FROM_CSVS to a space-separated list of CSV names to pick the
starting points instead. Each run cleans up after itself (SKIP_DELETE=0) because
the next one needs the operator CRDs gone; set SKIP_DELETE=1 to keep them.
EOF
}

die() {
	echo "[olm] ERROR: $*" >&2
	exit 1
}

case "$BUNDLE_TYPE" in
	community) PACKAGE="$NAME" ;;
	certified) PACKAGE="${NAME}-certified" ;;
	*)
		usage
		die "unsupported bundle type: ${BUNDLE_TYPE:-empty}"
		;;
esac

for command in kubectl jq make uv; do
	command -v "$command" >/dev/null 2>&1 || die "$command is required"
done

SOURCE="${BUNDLE_TYPE}-${CATALOG_ENV}"

kubectl api-resources --api-group=packages.operators.coreos.com -o name |
	grep -q '^packagemanifests' ||
	die "OLM is not available on the current kubectl context"

# The operator lives in the test namespace; a cluster-wide OPERATOR_NS would mislead the harness.
unset OPERATOR_NS
export SKIP_DELETE="${SKIP_DELETE:-0}"

list_starting_csvs() {
	kubectl get packagemanifests -n "$CATALOG_NAMESPACE" -l "catalog=${SOURCE}" -o json |
		jq -r --arg package "$PACKAGE" '
			.items[]
			| select(.metadata.name == $package)
			| .status as $status
			| ($status.channels[] | select(.name == $status.defaultChannel) | .currentCSV) as $target
			| $status.channels[].entries[].name
			| select(. != $target)
		' |
		awk '!seen[$0]++'
}

# Minor line of the default channel head, the version every run upgrades to.
get_target_minor() {
	kubectl get packagemanifests -n "$CATALOG_NAMESPACE" -l "catalog=${SOURCE}" -o json |
		jq -r --arg package "$PACKAGE" '
			.items[]
			| select(.metadata.name == $package)
			| .status as $status
			| $status.channels[] | select(.name == $status.defaultChannel) | .currentCSV
		' |
		sed -nE 's/.*v([0-9]+\.[0-9]+)\.[0-9]+$/\1/p'
}

if [[ -n "${OLM_FROM_CSVS:-}" ]]; then
	read -r -a FROM_CSVS <<<"$OLM_FROM_CSVS"
else
	FROM_CSVS=()
	while IFS= read -r csv; do
		[[ -n "$csv" ]] && FROM_CSVS+=("$csv")
	done < <(list_starting_csvs)
fi

[[ "${#FROM_CSVS[@]}" -gt 0 ]] ||
	die "no starting CSVs for ${PACKAGE} in CatalogSource ${CATALOG_NAMESPACE}/${SOURCE}"

echo "[olm] Starting CSVs: ${FROM_CSVS[*]}"
# Patches of the newest minor line (e.g. 1.24.0 for 1.24.1) are tested but not counted.
NEWEST_MINOR="$(printf '%s\n' "${FROM_CSVS[@]}" | sed -nE 's/.*v([0-9]+\.[0-9]+)\.[0-9]+$/\1/p' | sort -V | tail -n 1)"
MINORS="$(printf '%s\n' "${FROM_CSVS[@]}" | sed -nE 's/.*v([0-9]+\.[0-9]+)\.[0-9]+$/\1/p' | sort -uV | xargs)"
echo "[olm] Minor versions covered: ${MINORS}"
if [[ -z "${OLM_FROM_CSVS:-}" ]]; then
	MIN_MINORS="${OLM_MIN_MINORS:-2}"
	PREVIOUS_MINORS=$(($(wc -w <<<"$MINORS")))
	[[ "$(get_target_minor)" == "$NEWEST_MINOR" ]] && PREVIOUS_MINORS=$((PREVIOUS_MINORS - 1))
	[[ "$PREVIOUS_MINORS" -ge "$MIN_MINORS" ]] ||
		die "the catalog covers ${PREVIOUS_MINORS} previous minor versions, ${MIN_MINORS} required: build it with CATALOG_PREVIOUS_MINORS=${MIN_MINORS} (or set OLM_MIN_MINORS / OLM_FROM_CSVS)"
fi

mkdir -p "${SRC_DIR}/e2e-tests/reports" "${SRC_DIR}/e2e-tests/logs"
FAILED=()
for csv in "${FROM_CSVS[@]}"; do
	echo "[olm] Testing upgrade ${csv} -> ${SOURCE} default channel"
	report="${SRC_DIR}/e2e-tests/reports/${OLM_TEST}-${csv}"
	# The shared pytest harness (conftest.py, lib/) lives in e2e-tests; load it as a plugin.
	if ! (cd "$SRC_DIR" &&
		OLM_BUNDLE_TYPE="$BUNDLE_TYPE" \
			OLM_PACKAGE="$PACKAGE" \
			OLM_CATALOG_SOURCE="$SOURCE" \
			OLM_CATALOG_NAMESPACE="$CATALOG_NAMESPACE" \
			OLM_FROM_CSV="$csv" \
			PYTHONPATH="e2e-tests:installers/olm/e2e-tests" \
			uv run pytest -p conftest "installers/olm/e2e-tests/${OLM_TEST}/" \
			--html="${report}.html" --junitxml="${report}.xml"); then
		FAILED+=("$csv")
	fi
done

if [[ "${#FAILED[@]}" -gt 0 ]]; then
	die "upgrade failed from: ${FAILED[*]}"
fi
echo "[olm] All upgrades passed: ${FROM_CSVS[*]}"
