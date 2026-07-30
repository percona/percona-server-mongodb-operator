#!/usr/bin/env bash
set -euo pipefail

push_trap_exit() {
	local -a array

	eval "array=($(trap -p EXIT))"

	# shellcheck disable=SC2064
	trap "$1;${array[2]-}" EXIT
}

wait_registry() {
	local port="$1"
	local attempt

	for attempt in $(seq 1 30); do
		if curl -fsSL "http://127.0.0.1:${port}/v2/" >/dev/null 2>&1; then
			return 0
		fi
		sleep 1
	done

	echo "registry container did not become ready" >&2
	return 1
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
	command -v curl >/dev/null 2>&1 || {
		echo "curl is required" >&2
		exit 1
	}

	export DOCKER_DEFAULT_PLATFORM="${DOCKER_DEFAULT_PLATFORM:-linux/amd64}"

	registry="$(
		"${container}" run \
			--detach \
			--publish-all \
			docker.io/library/registry:2
	)"

	push_trap_exit "echo -n 'Removing '; '${container}' rm '${registry}'"
	push_trap_exit "echo -n 'Stopping '; '${container}' stop '${registry}'"

	port="$(
		"${container}" inspect "${registry}" \
			--format='{{ (index .NetworkSettings.Ports "5000/tcp" 0).HostPort }}'
	)"
	wait_registry "${port}"

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
