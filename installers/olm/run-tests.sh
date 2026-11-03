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

log() {
	echo "[olm] $(date +%H:%M:%S) $*"
}

die() {
	log "ERROR: $*" >&2
	exit 1
}

set_package() {
	case "$BUNDLE_TYPE" in
		community) PACKAGE="$NAME" ;;
		certified) PACKAGE="${NAME}-certified" ;;
		*)
			usage
			die "unsupported bundle type: ${BUNDLE_TYPE:-empty}"
			;;
	esac
	SOURCE="${BUNDLE_TYPE}-${CATALOG_ENV}"
	log "Package ${PACKAGE}, CatalogSource ${CATALOG_NAMESPACE}/${SOURCE}"
}

check_tools() {
	local command
	for command in kubectl jq make uv; do
		command -v "$command" >/dev/null 2>&1 || die "$command is required"
	done
	kubectl api-resources --api-group=packages.operators.coreos.com -o name \
		| grep -q '^packagemanifests' \
		|| die "OLM is not available on the current kubectl context"
	log "Using kubectl context $(kubectl config current-context)"
}

# Waits for the CatalogSource and its package, so a run right after deploy/<type> works.
wait_for_catalog() {
	log "Waiting for CatalogSource ${CATALOG_NAMESPACE}/${SOURCE} and package ${PACKAGE}"
	local deadline=$((SECONDS + ${OLM_CATALOG_TIMEOUT:-300})) state=""
	while ((SECONDS < deadline)); do
		state="$(kubectl get catalogsources.operators.coreos.com "$SOURCE" -n "$CATALOG_NAMESPACE" \
			-o jsonpath='{.status.connectionState.lastObservedState}' 2>/dev/null || true)"
		if [[ $state == "READY" ]] \
			&& kubectl get packagemanifests -n "$CATALOG_NAMESPACE" -l "catalog=${SOURCE}" -o name 2>/dev/null \
			| grep -q "/${PACKAGE}$"; then
			log "CatalogSource is READY"
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

# Sets TARGET_CSV and FROM_CSVS (OLM_FROM_CSVS, or every older CSV in the catalog).
pick_csvs() {
	TARGET_CSV="$(list_csvs | head -n 1)"
	[[ -n $TARGET_CSV ]] || die "no channel ${OLM_TARGET_CHANNEL:-(default)} for ${PACKAGE} in ${SOURCE}"
	log "Target CSV: ${TARGET_CSV}"

	local csv
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
	log "Starting CSVs: ${FROM_CSVS[*]}"
}

minor() {
	sed -nE 's/.*v([0-9]+\.[0-9]+)\.[0-9]+$/\1/p'
}

# Fails when the catalog covers fewer than OLM_MIN_MINORS previous minor versions.
check_minor_coverage() {
	local minors newest previous required="${OLM_MIN_MINORS:-2}"
	minors="$(printf '%s\n' "${FROM_CSVS[@]}" | minor | sort -uV | xargs)"
	log "Minor versions covered: ${minors}"
	[[ -z ${OLM_FROM_CSVS:-} ]] || return 0

	# Patches of the newest minor line (e.g. 1.24.0 for 1.24.1) are tested but not counted.
	newest="$(printf '%s\n' "${FROM_CSVS[@]}" | minor | sort -V | tail -n 1)"
	previous=$(($(wc -w <<<"$minors")))
	[[ "$(minor <<<"$TARGET_CSV")" == "$newest" ]] && previous=$((previous - 1))
	[[ $previous -ge $required ]] \
		|| die "the catalog covers ${previous} previous minor versions, ${required} required: build it with CATALOG_PREVIOUS_MINORS=${required} (or set OLM_MIN_MINORS / OLM_FROM_CSVS)"
}

# Runs the test once from the given CSV; returns non-zero when it fails.
run_upgrade() {
	local csv="$1" skip_delete="$2" report="${SRC_DIR}/e2e-tests/reports/${OLM_TEST}-$1" start=$SECONDS
	log "Testing upgrade ${csv} -> ${TARGET_CSV}"
	# The shared pytest harness (conftest.py, lib/) lives in e2e-tests; load it as a plugin.
	if (cd "$SRC_DIR" \
		&& OLM_BUNDLE_TYPE="$BUNDLE_TYPE" \
			OLM_PACKAGE="$PACKAGE" \
			OLM_CATALOG_SOURCE="$SOURCE" \
			OLM_CATALOG_NAMESPACE="$CATALOG_NAMESPACE" \
			OLM_FROM_CSV="$csv" \
			SKIP_DELETE="$skip_delete" \
			PYTHONPATH="e2e-tests:installers/olm/e2e-tests" \
			uv run pytest -p conftest "installers/olm/e2e-tests/${OLM_TEST}/" \
			--html="${report}.html" --junitxml="${report}.xml"); then
		log "PASSED ${csv} -> ${TARGET_CSV} in $((SECONDS - start))s, report ${report}.html"
	else
		log "FAILED ${csv} -> ${TARGET_CSV} in $((SECONDS - start))s, report ${report}.html"
		return 1
	fi
}

main() {
	set_package
	check_tools
	# The operator lives in the test namespace; a cluster-wide OPERATOR_NS would mislead the harness.
	unset OPERATOR_NS

	wait_for_catalog
	pick_csvs
	check_minor_coverage

	mkdir -p "${SRC_DIR}/e2e-tests/reports" "${SRC_DIR}/e2e-tests/logs"
	# Only the last run may keep its resources: the next run deletes the shared CRDs anyway.
	local csv skip_delete last="${FROM_CSVS[${#FROM_CSVS[@]} - 1]}" failed=()
	for csv in "${FROM_CSVS[@]}"; do
		skip_delete=0
		[[ $csv == "$last" ]] && skip_delete="${SKIP_DELETE:-0}"
		run_upgrade "$csv" "$skip_delete" || failed+=("$csv")
	done

	log "Summary: $((${#FROM_CSVS[@]} - ${#failed[@]}))/${#FROM_CSVS[@]} upgrades to ${TARGET_CSV} passed"
	if [[ ${#failed[@]} -gt 0 ]]; then
		die "upgrade failed from: ${failed[*]}"
	fi
	log "All upgrades passed: ${FROM_CSVS[*]}"
}

main
