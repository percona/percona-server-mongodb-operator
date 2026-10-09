#!/usr/bin/env bash
set -euo pipefail

validate_bundle_directory() {
	local directory="$1"

	operator-sdk bundle validate "${directory}" --select-optional='suite=operatorframework'
}

validate_bundle_directory "$@"
