#!/usr/bin/env bash
set -euo pipefail

push_trap_exit() {
	local -a array

	eval "array=($(trap -p EXIT))"

	# shellcheck disable=SC2064
	trap "$1;${array[2]-}" EXIT
}

TMPDIR="$(mktemp -d)"
push_trap_exit "rm -rf '${TMPDIR}'"
export TMPDIR

validate_bundle_image() {
	local container="$1"
	local directory="$2"
	local image
	local port
	local registry

	directory="$(cd "${directory}" && pwd)"

	export DOCKER_DEFAULT_PLATFORM="${DOCKER_DEFAULT_PLATFORM:-linux/amd64}"

	registry="$(
		"${container}" run \
			--detach \
			--publish-all \
			docker.io/library/registry:latest
	)"

	push_trap_exit "echo -n 'Removing '; '${container}' rm '${registry}'"
	push_trap_exit "echo -n 'Stopping '; '${container}' stop '${registry}'"

	port="$(
		"${container}" inspect "${registry}" \
			--format='{{ (index .NetworkSettings.Ports "5000/tcp" 0).HostPort }}'
	)"

	image="localhost:${port}/psmdb-operator-bundle:latest"

	"${container}" build \
		--platform="${DOCKER_DEFAULT_PLATFORM}" \
		--tag "${image}" \
		"${directory}"

	"${container}" push "${image}"

	opm alpha bundle validate \
		--use-http \
		--image-builder="${container}" \
		--optional-validators="operatorhub,bundle-objects" \
		--tag="${image}"
}

validate_bundle_image "$@"
