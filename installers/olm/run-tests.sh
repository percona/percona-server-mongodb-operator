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
every bundle of the deployed ${CATALOG_ENV} catalog older than the target (the
head of OLM_TARGET_CHANNEL, or of the default channel) is upgraded to it. It fails when fewer than OLM_MIN_MINORS (2)
previous minor versions are covered. Deploy the catalog first (make deploy/<type>).

Set OLM_FROM_CSVS to a space-separated list of CSV names to pick the
starting points instead. Each run cleans up after itself because the next one
needs the operator CRDs gone; set SKIP_DELETE=1 to keep the last run's resources.
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

kubectl api-resources --api-group=packages.operators.coreos.com -o name \
	| grep -q '^packagemanifests' \
	|| die "OLM is not available on the current kubectl context"

# The operator lives in the test namespace; a cluster-wide OPERATOR_NS would mislead the harness.
unset OPERATOR_NS
# Only the last run may keep its resources: the next run deletes the shared CRDs anyway.
KEEP_LAST="${SKIP_DELETE:-0}"

# Waits for the CatalogSource and its package, so a run right after deploy/<type> works.
wait_for_catalog() {
	local deadline=$((SECONDS + ${OLM_CATALOG_TIMEOUT:-300})) state=""
	while ((SECONDS < deadline)); do
		state="$(kubectl get catalogsources.operators.coreos.com "$SOURCE" -n "$CATALOG_NAMESPACE" \
			-o jsonpath='{.status.connectionState.lastObservedState}' 2>/dev/null || true)"
		if [[ $state == "READY" ]] \
			&& kubectl get packagemanifests -n "$CATALOG_NAMESPACE" -l "catalog=${SOURCE}" -o name 2>/dev/null \
			| grep -q "/${PACKAGE}$"; then
			return 0
		fi
		sleep 5
	done
	die "CatalogSource ${CATALOG_NAMESPACE}/${SOURCE} is not READY with package ${PACKAGE} (state: ${state:-none})"
}

# Prints the package's channel entries: the target CSV (head of OLM_TARGET_CHANNEL or the
# default channel) first, then every older CSV.
list_csvs() {
	kubectl get packagemanifests -n "$CATALOG_NAMESPACE" -l "catalog=${SOURCE}" -o json \
		| jq -r --arg package "$PACKAGE" --arg channel "${OLM_TARGET_CHANNEL:-}" '
			def ver: capture("v(?<a>[0-9]+)\\.(?<b>[0-9]+)\\.(?<c>[0-9]+)$") | [.a, .b, .c] | map(tonumber);
			.items[]
			| select(.metadata.name == $package)
			| .status as $status
			| (if $channel == "" then $status.defaultChannel else $channel end) as $name
			| ($status.channels[] | select(.name == $name) | .currentCSV) as $target
			| $target, ($status.channels[].entries[].name | select(ver < ($target | ver)))
		' \
		| awk '!seen[$0]++'
}

wait_for_catalog
TARGET_CSV="$(list_csvs | head -n 1)"
[[ -n $TARGET_CSV ]] || die "no channel ${OLM_TARGET_CHANNEL:-(default)} for ${PACKAGE} in ${SOURCE}"
echo "[olm] Target CSV: ${TARGET_CSV}"

if [[ -n ${OLM_FROM_CSVS:-} ]]; then
	read -r -a FROM_CSVS <<<"$OLM_FROM_CSVS"
else
	FROM_CSVS=()
	while IFS= read -r csv; do
		[[ -n $csv ]] && FROM_CSVS+=("$csv")
	done < <(list_csvs | tail -n +2)
fi

[[ ${#FROM_CSVS[@]} -gt 0 ]] \
	|| die "no starting CSVs for ${PACKAGE} in CatalogSource ${CATALOG_NAMESPACE}/${SOURCE}"

echo "[olm] Starting CSVs: ${FROM_CSVS[*]}"
# Patches of the newest minor line (e.g. 1.24.0 for 1.24.1) are tested but not counted.
NEWEST_MINOR="$(printf '%s\n' "${FROM_CSVS[@]}" | sed -nE 's/.*v([0-9]+\.[0-9]+)\.[0-9]+$/\1/p' | sort -V | tail -n 1)"
MINORS="$(printf '%s\n' "${FROM_CSVS[@]}" | sed -nE 's/.*v([0-9]+\.[0-9]+)\.[0-9]+$/\1/p' | sort -uV | xargs)"
echo "[olm] Minor versions covered: ${MINORS}"
if [[ -z ${OLM_FROM_CSVS:-} ]]; then
	MIN_MINORS="${OLM_MIN_MINORS:-2}"
	PREVIOUS_MINORS=$(($(wc -w <<<"$MINORS")))
	[[ "$(sed -nE 's/.*v([0-9]+\.[0-9]+)\.[0-9]+$/\1/p' <<<"$TARGET_CSV")" == "$NEWEST_MINOR" ]] \
		&& PREVIOUS_MINORS=$((PREVIOUS_MINORS - 1))
	[[ $PREVIOUS_MINORS -ge $MIN_MINORS ]] \
		|| die "the catalog covers ${PREVIOUS_MINORS} previous minor versions, ${MIN_MINORS} required: build it with CATALOG_PREVIOUS_MINORS=${MIN_MINORS} (or set OLM_MIN_MINORS / OLM_FROM_CSVS)"
fi

mkdir -p "${SRC_DIR}/e2e-tests/reports" "${SRC_DIR}/e2e-tests/logs"
FAILED=()
LAST_CSV="${FROM_CSVS[${#FROM_CSVS[@]} - 1]}"
for csv in "${FROM_CSVS[@]}"; do
	echo "[olm] Testing upgrade ${csv} -> ${TARGET_CSV}"
	skip_delete=0
	[[ $csv == "$LAST_CSV" ]] && skip_delete="$KEEP_LAST"
	report="${SRC_DIR}/e2e-tests/reports/${OLM_TEST}-${csv}"
	# The shared pytest harness (conftest.py, lib/) lives in e2e-tests; load it as a plugin.
	if ! (cd "$SRC_DIR" \
		&& OLM_BUNDLE_TYPE="$BUNDLE_TYPE" \
			OLM_PACKAGE="$PACKAGE" \
			OLM_CATALOG_SOURCE="$SOURCE" \
			OLM_CATALOG_NAMESPACE="$CATALOG_NAMESPACE" \
			OLM_FROM_CSV="$csv" \
			SKIP_DELETE="$skip_delete" \
			PYTHONPATH="e2e-tests:installers/olm/e2e-tests" \
			uv run pytest -p conftest "installers/olm/e2e-tests/${OLM_TEST}/" \
			--html="${report}.html" --junitxml="${report}.xml"); then
		FAILED+=("$csv")
	fi
done

if [[ ${#FAILED[@]} -gt 0 ]]; then
	die "upgrade failed from: ${FAILED[*]}"
fi
echo "[olm] All upgrades passed: ${FROM_CSVS[*]}"
