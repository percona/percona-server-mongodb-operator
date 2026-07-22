#!/bin/bash

set -euo pipefail

confirm_push() {
	local image="$1"
	local answer

	if [[ "${CONFIRM_PUSH:-1}" == "0" || "${CONFIRM_PUSH:-1}" == "false" ]]; then
		return
	fi

	read -r -p "Push image ${image}? [y/N] " answer
	[[ "${answer}" == "y" || "${answer}" == "Y" || "${answer}" == "yes" || "${answer}" == "YES" ]]
}

build_image() {
	local container="$1" directory="$2" distro="$3" version="$4"
	directory=$(cd "${directory}" && pwd)

	local bundle_name="${distro}"
	if [[ "${distro}" == "redhat" ]]; then
		bundle_name="certified"
	fi

	local tag="${version}-${bundle_name}-bundle"
	local image="${BUNDLE_REPO}:${tag}"
	local platforms="${BUNDLE_PLATFORMS:-linux/amd64,linux/arm64}"

	pushd "${directory}"

	confirm_push "${image}" || exit 1
	"${container}" buildx build \
		--platform "${platforms}" \
		-t "${image}" \
		--push \
		.

	popd
}

build_image "$@"
