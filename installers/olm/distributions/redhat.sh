#!/usr/bin/env bash
set -euo pipefail

# shellcheck disable=SC2016
redhat_release="${VERSION}"
redhat_skips_min_version="${REDHAT_SKIPS_MIN_VERSION:-1.17.0}"
redhat_registry="${REDHAT_REGISTRY:-registry.connect.redhat.com}"
redhat_catalog_api="${REDHAT_CATALOG_API:-https://catalog.redhat.com/api/containers/v1}"
redhat_catalog_curl_timeout="${REDHAT_CATALOG_CURL_TIMEOUT:-20}"
redhat_operator_repository="${REDHAT_OPERATOR_REPOSITORY:-percona/percona-server-mongodb-operator}"
redhat_containers_repository="${REDHAT_CONTAINERS_REPOSITORY:-percona/percona-server-mongodb-operator-containers}"
redhat_operator_tag="${REDHAT_OPERATOR_TAG:-${redhat_release}}"
redhat_related_images="[]"
redhat_missing_digests=()
release_versions_file="${repo_root}/e2e-tests/release_versions"

image_tag() {
	local image="$1"

	echo -n "${image##*:}"
}

digest_key() {
	echo -n "$1" \
		| "$sed" -E 's/[^[:alnum:]]+/_/g' \
		| tr '[:lower:]' '[:upper:]'
}

catalog_digest() {
	local repository="$1"
	local tag="$2"
	local digest

	debug "Resolving Red Hat digest for ${redhat_registry}/${repository}:${tag}"

	digest="$(
		curl -fsSL \
			--connect-timeout 5 \
			--max-time "${redhat_catalog_curl_timeout}" \
			"${redhat_catalog_api}/repositories/registry/${redhat_registry}/repository/${repository}/tag/${tag}" \
			2>/dev/null \
			| jq -er '.docker_image_digest // .data.docker_image_digest // .data[0].docker_image_digest' 2>/dev/null
	)" || digest="$(
		curl -fsSL \
			--connect-timeout 5 \
			--max-time "${redhat_catalog_curl_timeout}" \
			"${redhat_catalog_api}/repositories/registry/${redhat_registry}/repository/${repository}/images?page_size=500" \
			2>/dev/null \
			| jq -er \
				--arg tag "${tag}" \
				'first(.data[] | select(any(.repositories[]?.tags[]?; .name == $tag)) | .docker_image_digest)' \
				2>/dev/null
	)" || digest=""

	if [[ -n "${digest}" && "${digest}" != "null" ]]; then
		echo -n "sha256:${digest#sha256:}"
		return
	fi

	return 1
}

set_image_ref() {
	local key="$1"
	local name="$2"
	local repository="$3"
	local tag="$4"
	local digest_var
	local digest

	digest_var="REDHAT_IMAGE_DIGEST_$(digest_key "${key}")"
	digest="${!digest_var:-}"

	if [[ -z "${digest}" ]]; then
		digest="$(catalog_digest "${repository}" "${tag}")" || digest=""
	fi

	if [[ -z "${digest}" ]]; then
		if [[ "${key}" == "IMAGE_CLUSTERSYNC" ]]; then
			abort "unable to resolve digest for required clustersync tag ${redhat_registry}/${repository}:${tag}"
		fi

		digest="<DIGEST>"
		redhat_missing_digests+=("${name}:${redhat_registry}/${repository}:${tag}")
	fi

	if [[ "${digest}" != "<DIGEST>" ]]; then
		digest="sha256:${digest#sha256:}"
	fi

	echo -n "${redhat_registry}/${repository}@${digest}"
}

validate_certified_tag() {
	local key="$1"
	local tag="$2"
	local source_tag="${3:-}"
	local expected=""

	case "${key}" in
		IMAGE_OPERATOR)
			expected="${redhat_release}"
			;;
		IMAGE_MONGOD60|IMAGE_MONGOD70|IMAGE_MONGOD80)
			expected="${redhat_release}-psmdb-${source_tag}"
			;;
		IMAGE_BACKUP)
			expected="${redhat_release}-backup"
			;;
		IMAGE_PMM_CLIENT)
			expected="${redhat_release}-pmm"
			;;
		IMAGE_PMM3_CLIENT)
			expected="${redhat_release}-pmm3"
			;;
		IMAGE_LOGCOLLECTOR)
			expected="${redhat_release}-logcollector-${source_tag}"
			;;
		IMAGE_CLUSTERSYNC)
			expected="${redhat_release}-clustersync"
			;;
		*)
			abort "unsupported certified image key: ${key}"
			;;
	esac

	[[ "${tag}" == "${expected}" ]] \
		|| abort "invalid Red Hat tag for ${key}: got '${tag}', expected '${expected}'"
}

add_related_image() {
	local key="$1"
	local name="$2"
	local repository="$3"
	local tag="$4"
	local source_tag="${5:-}"
	local image

	validate_certified_tag "${key}" "${tag}" "${source_tag}"

	image="$(set_image_ref "${key}" "${name}" "${repository}" "${tag}")"

	log "Related image ${name}: ${image}"

	redhat_related_images="$(
		jq -c \
			--arg name "${name}" \
			--arg image "${image}" \
			'. + [{ name: $name, image: $image }]' \
			<<<"${redhat_related_images}"
	)"
}

related_image_by_name() {
	local name="$1"

	jq --raw-output \
		--arg name "${name}" \
		'map(select(.name == $name)) | last.image // ""' \
		<<<"${redhat_related_images}"
}

require_release_image() {
	local key="$1"

	if [[ -z "${!key:-}" ]]; then
		abort "${key} is required in ${release_versions_file}"
	fi
}

report_missing_digests() {
	local item

	[[ "${#redhat_missing_digests[@]}" -eq 0 ]] && return

	log "Digest resolution failed for the following image(s); <DIGEST> was used because SKIP_DIGEST_FAILURE is enabled:"
	for item in "${redhat_missing_digests[@]}"; do
		log "  - ${item}"
	done
}

build_redhat_related_images() {
	local mongod60_tag
	local mongod70_tag
	local mongod80_tag
	local logcollector_tag

	log "Building Red Hat related images from ${release_versions_file}"

	[[ -f "${release_versions_file}" ]] \
		|| abort "release versions file not found: ${release_versions_file}"

	# shellcheck source=/dev/null
	source "${release_versions_file}"

	for key in \
		IMAGE_MONGOD60 \
		IMAGE_MONGOD70 \
		IMAGE_MONGOD80 \
		IMAGE_BACKUP \
		IMAGE_PMM_CLIENT \
		IMAGE_PMM3_CLIENT \
		IMAGE_LOGCOLLECTOR; do
		require_release_image "${key}"
	done

	mongod60_tag="$(image_tag "${IMAGE_MONGOD60}")"
	mongod70_tag="$(image_tag "${IMAGE_MONGOD70}")"
	mongod80_tag="$(image_tag "${IMAGE_MONGOD80}")"
	logcollector_tag="$(image_tag "${IMAGE_LOGCOLLECTOR}")"

	add_related_image "IMAGE_MONGOD80" "mongod8.0" "${redhat_containers_repository}" "${redhat_release}-psmdb-${mongod80_tag}" "${mongod80_tag}"
	add_related_image "IMAGE_MONGOD70" "mongod7.0" "${redhat_containers_repository}" "${redhat_release}-psmdb-${mongod70_tag}" "${mongod70_tag}"
	add_related_image "IMAGE_MONGOD60" "mongod6.0" "${redhat_containers_repository}" "${redhat_release}-psmdb-${mongod60_tag}" "${mongod60_tag}"
	add_related_image "IMAGE_BACKUP" "backup" "${redhat_containers_repository}" "${redhat_release}-backup"
	add_related_image "IMAGE_PMM_CLIENT" "pmm" "${redhat_containers_repository}" "${redhat_release}-pmm"
	add_related_image "IMAGE_PMM3_CLIENT" "pmm3" "${redhat_containers_repository}" "${redhat_release}-pmm3"
	add_related_image "IMAGE_CLUSTERSYNC" "clustersync" "${redhat_containers_repository}" "${redhat_release}-clustersync"
	add_related_image "IMAGE_LOGCOLLECTOR" "logcollector" "${redhat_containers_repository}" "${redhat_release}-logcollector-${logcollector_tag}" "${logcollector_tag}"
	add_related_image "IMAGE_OPERATOR" "operator" "${redhat_operator_repository}" "${redhat_operator_tag}"

	report_missing_digests

	jq -nc \
		--arg operator_image "$(related_image_by_name operator)" \
		--argjson related_images "${redhat_related_images}" \
		'{
			operatorImage: $operator_image,
			relatedImages: $related_images
		}'
}

build_redhat_skips() {
	local min_version="${redhat_skips_min_version}"
	local current_version="v${redhat_release#v}"

	min_version="v${min_version#v}"

	log "Building Red Hat skips from ${min_version} up to ${current_version}"

	git -C "${repo_root}" tag --list 'v*' \
		| jq -Rsc \
			--arg min_version "${min_version}" \
			--arg current_version "${current_version}" \
			--arg package_name "${component_name}-certified" \
			'
			def version_parts:
				ltrimstr("v")
				| split(".")
				| map(tonumber);

			($min_version | version_parts) as $min
			| ($current_version | version_parts) as $current
			| split("\n")
			| map(select(length > 0))
			| map(select(test("^v[0-9]+\\.[0-9]+\\.[0-9]+$")))
			| map({
				tag: .,
				version: version_parts
			})
			| map(
				select(
					.version >= $min
					and .version < $current
				)
			)
			| sort_by(.version)
			| map($package_name + "." + .tag)
			'
}

distribution_package_name() {
	echo -n "${component_name}-certified"
}

customize_csv() {
	yq -P eval --inplace '
		.spec.relatedImages = (strenv(relatedImages) | from_json) |
		.spec.skips = (strenv(skips) | from_json) |
		.metadata.annotations.certified = "true" |
		.metadata.annotations["features.operators.openshift.io/disconnected"] = "true" |
		.metadata.name = strenv(name_certified)
	' "$1"
}

build_distribution_data() {
	local redhat_data
	local images
	local related_images
	local skips

	redhat_data="$(build_redhat_related_images)" || return

	jq -e \
		'.operatorImage and (.relatedImages | type == "array")' \
		>/dev/null \
		<<<"${redhat_data}" \
		|| abort "Invalid Red Hat image data"

	images="$(
		jq -c '
			.operatorImage as $operator
			| reduce .relatedImages[] as $item
				({}; .[$item.name] = $item.image)
			| .operator = $operator
		' <<<"${redhat_data}"
	)"

	related_images="$(jq -c '.relatedImages' <<<"${redhat_data}")"
	skips="$(build_redhat_skips)"

	jq -nc \
		--argjson images "${images}" \
		--argjson related_images "${related_images}" \
		--argjson skips "${skips}" \
		'{
			images: $images,
			relatedImages: $related_images,
			skips: $skips
		}'
}