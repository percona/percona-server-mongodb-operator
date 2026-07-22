#!/usr/bin/env bash

# shellcheck disable=SC2016

redhat_release="${VERSION}"
redhat_registry="${REDHAT_REGISTRY:-registry.connect.redhat.com}"
redhat_catalog_api="${REDHAT_CATALOG_API:-https://catalog.redhat.com/api/containers/v1}"
redhat_catalog_curl_timeout="${REDHAT_CATALOG_CURL_TIMEOUT:-20}"
redhat_operator_repository="${REDHAT_OPERATOR_REPOSITORY:-percona/percona-server-mongodb-operator}"
redhat_containers_repository="${REDHAT_CONTAINERS_REPOSITORY:-percona/percona-server-mongodb-operator-containers}"
redhat_operator_tag="${REDHAT_OPERATOR_TAG:-${redhat_release}}"
redhat_related_images="[]"
redhat_missing_digests=()

image_tag() {
	local image="$1"

	printf '%s\n' "${image##*:}"
}

digest_key() {
	printf '%s' "$1" \
		| sed -E 's/[^[:alnum:]]+/_/g' \
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
		printf 'sha256:%s\n' "${digest#sha256:}"
		return
	fi

	return 1
}

image_ref() {
	local key="$1"
	local name="$2"
	local repository="$3"
	local tag="$4"
	local digest_var="REDHAT_IMAGE_DIGEST_$(digest_key "${key}")"
	local digest="${!digest_var:-}"

	if [[ -z "${digest}" ]]; then
		digest="$(catalog_digest "${repository}" "${tag}")" || digest=""
	fi

	if [[ -z "${digest}" ]]; then
		if [[ "${SKIP_DIGEST_FAILURE:-0}" == "1" || "${SKIP_DIGEST_FAILURE:-false}" == "true" ]]; then
			digest="<DIGEST>"
			redhat_missing_digests+=("${name}:${redhat_registry}/${repository}:${tag}")
		else
			abort "unable to resolve digest for ${redhat_registry}/${repository}:${tag}; set SKIP_DIGEST_FAILURE=1 to continue with <DIGEST>"
		fi
	fi

	if [[ "${digest}" != "<DIGEST>" ]]; then
		digest="sha256:${digest#sha256:}"
	fi

	printf '%s/%s@%s\n' "${redhat_registry}" "${repository}" "${digest}"
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

	image="$(image_ref "${key}" "${name}" "${repository}" "${tag}")"

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
		abort "${key} is required in e2e-tests/release_versions"
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
	local release_versions="${repo_root}/e2e-tests/release_versions"
	local mongod60_tag
	local mongod70_tag
	local mongod80_tag
	local logcollector_tag

	log "Building Red Hat related images from ${release_versions}"

	[[ -f "${release_versions}" ]] \
		|| abort "release versions file not found: ${release_versions}"

	# shellcheck source=/dev/null
	source "${release_versions}"

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
