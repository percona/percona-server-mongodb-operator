#!/usr/bin/env bash

set -euo pipefail

confirm_push() {
	local image="$1"
	local answer

	case "${CONFIRM_PUSH:-0}" in
		1|true|TRUE|yes|YES)
			read -r -p "Push image ${image}? [y/N] " answer

			case "${answer}" in
				y|Y|yes|YES)
					return 0
					;;
				*)
					return 1
					;;
			esac
			;;
		*)
			return 1
			;;
	esac
}

build_image() {
	local container="$1"
	local directory="$2"
	local distro="$3"
	local version="$4"

	directory=$(cd "${directory}" && pwd)

	local bundle_name="${distro}"
	if [[ "${distro}" == "redhat" ]]; then
		bundle_name="certified"
	fi

	local tag="${version}-${bundle_name}-bundle"
	local image="${BUNDLE_REPO}:${tag}"
	local platforms="${BUNDLE_PLATFORMS:-linux/amd64,linux/arm64}"
	local build_action="--load"

	if confirm_push "${image}"; then
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
