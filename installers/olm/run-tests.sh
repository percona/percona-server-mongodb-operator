#!/usr/bin/env bash

set -euo pipefail

BUNDLE_TYPE="${1:-}"

CATALOG_NAMESPACE="${CATALOG_NAMESPACE:-openshift-marketplace}"
CATALOG_ENV="${CATALOG_ENV:-dev}"
NAME="${NAME:-percona-server-mongodb-operator}"
OLM_TESTS="${OLM_TESTS:-olm-default-install olm-backup-restore olm-upgrade}"
SRC_DIR="$(cd "$(dirname "$0")/../.." && pwd)"

usage() {
	cat <<EOT
Usage:
  $0 <community|certified>

Runs the OLM tests in installers/olm/e2e-tests/ against the deployed
${CATALOG_ENV} catalog. Deploy it first (make deploy/<type>). OLM_TESTS
(default: "${OLM_TESTS}") selects the tests:

  olm-default-install once, against the target: the head of OLM_TARGET_CHANNEL,
                      or of the default channel;
  olm-backup-restore  once, against the target;
  olm-upgrade         once per starting CSV: every bundle of the catalog older
                      than the target is upgraded to it. Fails when fewer than
                      OLM_MIN_MINORS (2) previous minor versions are covered.
                      OLM_FROM_CSVS (space-separated CSV names) picks the
                      starting points instead.

Each run cleans up after itself because the next one needs the operator CRDs
gone; set SKIP_DELETE=1 to keep the last run's resources.
EOT
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
	# helm: the tests install the SeaweedFS backup storage from its chart.
	for command in kubectl oc helm jq make uv; do
		command -v "$command" >/dev/null 2>&1 || die "$command is required"
	done
}

# The runs take an hour or more, and another tool (a cluster deploy, another test run)
# can switch the current context of the shared kubeconfig meanwhile. Every run uses a
# copy of the context checked here; namespaces the tests switch to stay in the copy.
pin_context() {
	local context
	context="$(kubectl config current-context)" || die "no current kubectl context"
	PINNED_KUBECONFIG="$(mktemp)"
	trap 'rm -f "$PINNED_KUBECONFIG"' EXIT
	kubectl config view --minify --flatten --context "$context" >"$PINNED_KUBECONFIG"
	export KUBECONFIG="$PINNED_KUBECONFIG"
	kubectl api-resources --api-group=packages.operators.coreos.com -o name \
		| grep -q '^packagemanifests' \
		|| die "OLM is not available on kubectl context ${context}"
	log "Using kubectl context ${context}, pinned for every run"
}

# Tests that passed in a JUnit report: a run whose steps were all skipped passes pytest
# but checks nothing.
passed_tests() {
	uv run python -I - "$1" <<'EOT'
import sys
import xml.etree.ElementTree as ET

root = ET.parse(sys.argv[1]).getroot()
suites = [root] if root.tag == "testsuite" else list(root)
print(sum(
    int(s.get("tests", 0)) - int(s.get("skipped", 0)) - int(s.get("failures", 0))
    - int(s.get("errors", 0))
    for s in suites
))
EOT
}

check_tests() {
	local test
	[[ -n ${OLM_TESTS// /} ]] || die "OLM_TESTS is empty"
	for test in $OLM_TESTS; do
		[[ -d "${SRC_DIR}/installers/olm/e2e-tests/${test}" ]] \
			|| die "unknown test ${test} in OLM_TESTS (no installers/olm/e2e-tests/${test})"
	done
	log "Tests: ${OLM_TESTS}"
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
	[[ ${#FROM_CSVS[@]} -gt 0 ]] && log "Starting CSVs: ${FROM_CSVS[*]}"
	return 0
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

# Plans the runs as "<test> <csv>" lines: olm-upgrade once per starting CSV, every
# other test once with the target, in OLM_TESTS order.
plan_runs() {
	local test csv
	RUNS=()
	for test in $OLM_TESTS; do
		if [[ $test == "olm-upgrade" ]]; then
			[[ ${#FROM_CSVS[@]} -gt 0 ]] \
				|| die "no starting CSVs for ${PACKAGE} in CatalogSource ${CATALOG_NAMESPACE}/${SOURCE}"
			check_minor_coverage
			for csv in "${FROM_CSVS[@]}"; do
				RUNS+=("${test} ${csv}")
			done
		else
			RUNS+=("${test} ${TARGET_CSV}")
		fi
	done
}

# Runs one test; for olm-upgrade the CSV is the starting point, otherwise the target.
# Returns non-zero when the test fails.
run_test() {
	local test="$1" csv="$2" skip_delete="$3"
	local report="${SRC_DIR}/e2e-tests/reports/${test}-${csv}" start=$SECONDS from_csv="" passed
	[[ $test == "olm-upgrade" ]] && from_csv="$csv"
	log "Running ${test} with ${csv} (target ${TARGET_CSV})"
	# The shared pytest harness (conftest.py, lib/) lives in e2e-tests; load it as a plugin.
	if (cd "$SRC_DIR" \
		&& OLM_BUNDLE_TYPE="$BUNDLE_TYPE" \
			OLM_PACKAGE="$PACKAGE" \
			OLM_CATALOG_SOURCE="$SOURCE" \
			OLM_CATALOG_NAMESPACE="$CATALOG_NAMESPACE" \
			OLM_FROM_CSV="$from_csv" \
			SKIP_DELETE="$skip_delete" \
			PYTHONPATH="e2e-tests:installers/olm/e2e-tests" \
			uv run pytest -p conftest "installers/olm/e2e-tests/${test}/" \
			--html="${report}.html" --junitxml="${report}.xml"); then
		passed="$(passed_tests "${report}.xml" || true)"
		if [[ ! $passed =~ ^[0-9]+$ ]]; then
			log "FAILED ${test} ${csv}: cannot read ${report}.xml"
			return 1
		fi
		if [[ $passed -eq 0 ]]; then
			log "FAILED ${test} ${csv}: every step was skipped, report ${report}.html"
			return 1
		fi
		log "PASSED ${test} ${csv} in $((SECONDS - start))s, report ${report}.html"
	else
		log "FAILED ${test} ${csv} in $((SECONDS - start))s, report ${report}.html"
		return 1
	fi
}

main() {
	set_package
	check_tools
	pin_context
	check_tests
	# The operator lives in the test namespace; a cluster-wide OPERATOR_NS would mislead the harness.
	unset OPERATOR_NS

	wait_for_catalog
	pick_csvs
	plan_runs

	mkdir -p "${SRC_DIR}/e2e-tests/reports" "${SRC_DIR}/e2e-tests/logs"
	# Only the last run may keep its resources: the next run deletes the shared CRDs anyway.
	local run skip_delete last="${RUNS[${#RUNS[@]} - 1]}" failed=()
	for run in "${RUNS[@]}"; do
		skip_delete=0
		[[ $run == "$last" ]] && skip_delete="${SKIP_DELETE:-0}"
		# shellcheck disable=SC2086 # the run is "<test> <csv>", two words by construction.
		run_test $run "$skip_delete" || failed+=("$run")
	done

	log "Summary: $((${#RUNS[@]} - ${#failed[@]}))/${#RUNS[@]} runs passed (target ${TARGET_CSV})"
	if [[ ${#failed[@]} -gt 0 ]]; then
		die "failed: $(printf '%s, ' "${failed[@]}" | sed 's/, $//')"
	fi
	log "All runs passed"
}

main
