#!/usr/bin/env bash

set -euo pipefail

is_true() {
	case "${1:-}" in
		1|true|TRUE|yes|YES|y|Y)
			return 0
			;;
		*)
			return 1
			;;
	esac
}

confirm_push() {
	local image="$1"
	local answer

	if ! is_true "${CONFIRM_PUSH:-1}"; then
		return 0
	fi

	if [[ -r /dev/tty ]]; then
		read -r -p "Push bundle image ${image}? [y/N] " answer </dev/tty
	else
		read -r -p "Push bundle image ${image}? [y/N] " answer
	fi
	[[ "$answer" =~ ^(y|Y|yes|YES)$ ]]
}

bundle_image() {
	local distro="$1"
	local version="$2"
	local bundle_repo="$3"

	local bundle_name="${distro}"
	if [[ "${distro}" == "redhat" ]]; then
		bundle_name="certified"
	fi

	local tag="${version}-${bundle_name}-bundle"

	printf "%s:%s" "${bundle_repo}" "${tag}"
}

build_image() {
	local container="$1"
	local directory="$2"
	local image="$3"
	local platforms

	platforms="${BUNDLE_PLATFORM:-linux/amd64}"
	echo "Building bundle image locally: ${image}"

	directory=$(cd "${directory}" && pwd)
	pushd "${directory}" >/dev/null

	"${container}" buildx build \
		--platform "${platforms}" \
		-t "${image}" \
		--load \
		.

	popd >/dev/null
}

push_image() {
	local container="$1"
	local image="$2"

	confirm_push "${image}" || {
		echo "Bundle image push skipped: ${image}"
		exit 1
	}
	"${container}" push "${image}"
}

main() {
	local action="${1:-}"

	if [[ "${action}" != "build" && "${action}" != "push" ]]; then
		echo "Usage: $0 build|push CONTAINER BUNDLE_DIR DISTRO VERSION BUNDLE_REPO" >&2
		exit 1
	fi

	shift
	if [[ "$#" -ne 5 ]]; then
		echo "Usage: $0 build|push CONTAINER BUNDLE_DIR DISTRO VERSION BUNDLE_REPO" >&2
		exit 1
	fi

	local container="$1"
	local directory="$2"
	local distro="$3"
	local version="$4"
	local bundle_repo="$5"
	local image

	image=$(bundle_image "${distro}" "${version}" "${bundle_repo}")

	case "${action}" in
		build)
			build_image "${container}" "${directory}" "${image}"
			;;
		push)
			push_image "${container}" "${image}"
			;;
	esac
}

main "$@"
