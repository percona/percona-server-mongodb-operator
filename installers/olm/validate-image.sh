#!/usr/bin/env bash
set -eu

push_trap_exit() {
	local -a array
	eval "array=($(trap -p EXIT))"
	# shellcheck disable=SC2064
	trap "$1;${array[2]-}" EXIT
}

TMPDIR=$(mktemp -d)
push_trap_exit "rm -rf '${TMPDIR}'"
export TMPDIR

validate_bundle_image() {
	local container="$1" directory="$2"
	directory=$(cd "${directory}" && pwd)

	local image port registry
	registry=$(${container} run --detach --publish-all docker.io/library/registry:latest)
	push_trap_exit "echo -n 'Removing '; ${container} rm '${registry}'"
	push_trap_exit "echo -n 'Stopping '; ${container} stop '${registry}'"

	port=$(${container} inspect "${registry}" \
		--format='{{ (index .NetworkSettings.Ports "5000/tcp" 0).HostPort }}')
	image="localhost:${port}/psmdb-operator-bundle:latest"

	${container} build --platform="${DOCKER_DEFAULT_PLATFORM:-linux/amd64}" -t "${image}" "${directory}"
	${container} push "${image}"

	opm alpha bundle validate --use-http --image-builder="${container}" \
		--optional-validators='operatorhub,bundle-objects' \
		--tag="${image}"
}

validate_bundle_image "$@"
