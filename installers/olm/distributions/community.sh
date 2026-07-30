#!/usr/bin/env bash
set -euo pipefail

set_release_image_ref() {
	local variable="$1"
	local key="$2"
	local image

	image="$(release_version_value "${key}")" || return

	case "${image}" in
		*.*/*|*:*/*|localhost/*) printf -v "${variable}" '%s' "${image}" ;;
		*) printf -v "${variable}" 'docker.io/%s' "${image}" ;;
	esac
}

build_distribution_data() {
	local operator backup logcollector mongod pmm clustersync
	local image_ref

	for image_ref in \
		"operator IMAGE_OPERATOR" \
		"backup IMAGE_BACKUP" \
		"logcollector IMAGE_LOGCOLLECTOR" \
		"mongod IMAGE_MONGOD80" \
		"pmm IMAGE_PMM3_CLIENT" \
		"clustersync IMAGE_CLUSTERSYNC"; do
		set_release_image_ref ${image_ref} || return
	done

	jq -nc \
		--arg operator "${operator}" \
		--arg backup "${backup}" \
		--arg logcollector "${logcollector}" \
		--arg mongod "${mongod}" \
		--arg pmm "${pmm}" \
		--arg clustersync "${clustersync}" \
		'{
			images:
				({
					operator: $operator,
					backup: $backup,
					logcollector: $logcollector,
					"mongod8.0": $mongod,
					pmm3: $pmm
				}
				+ (if $clustersync == "" then {} else {clustersync: $clustersync} end))
		}'
}

distribution_package_name() {
	echo -n "${component_name}"
}

customize_csv() {
	yq -P eval --inplace \
		'.metadata.annotations["olm.skipRange"] = env(skip_range)' \
		"$1"
}
