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

confirm_build_push() {
	local image="$1"
	local answer

	if ! is_true "${CONFIRM_BUILD_PUSH:-1}"; then
		return 0
	fi

	if [[ -r /dev/tty ]]; then
		read -r -p "Build and push bundle image ${image}? [y/N] " answer </dev/tty
	else
		read -r -p "Build and push bundle image ${image}? [y/N] " answer
	fi
	[[ "$answer" =~ ^(y|Y|yes|YES)$ ]]
}

build_image() {
	local container="$1"
	local directory="$2"
	local distro="$3"
	local version="$4"
	local bundle_repo="$5"
	local bundle_tag_suffix="${6:-}"

	directory=$(cd "${directory}" && pwd)

	local bundle_name="${distro}"
	if [[ "${distro}" == "redhat" ]]; then
		bundle_name="certified"
	fi

	local tag="${version}-${bundle_name}-bundle"
	if [[ -n "${bundle_tag_suffix}" ]]; then
		tag="${tag}-${bundle_tag_suffix}"
	fi
	local image="${bundle_repo}:${tag}"
	local platforms="${BUNDLE_PLATFORMS:-linux/amd64,linux/arm64}"
	local build_action="--load"

	if is_true "${BUNDLE_BUILD_PUSH:-1}"; then
		confirm_build_push "${image}" || {
			echo "Bundle image push skipped: ${image}"
			exit 1
		}
		build_action="--push"
	else
		echo "Push skipped. Building image locally."
	fi

	pushd "${directory}" >/dev/null

	"${container}" buildx build \
		--platform "${platforms}" \
		-t "${image}" \
		"${build_action}" \
		.

	popd >/dev/null
}

build_image "$@"
