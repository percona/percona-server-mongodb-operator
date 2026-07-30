#!/usr/bin/env bash

set -euo pipefail

ACTION="${1:-}"
BUNDLE_TYPE="${2:-}"
CATALOG_REPO="${3:-}"
BUNDLE_REPO="${4:-}"

CONTAINER="${CONTAINER:-docker}"
CATALOG_PLATFORM="${CATALOG_PLATFORM:-linux/amd64,linux/arm64}"
CATALOG_BUNDLE_LIMIT="${CATALOG_BUNDLE_LIMIT:-2}"
CATALOG_NAMESPACE="${CATALOG_NAMESPACE:-openshift-marketplace}"
CONFIRM_PUSH="${CONFIRM_PUSH:-1}"
NAME="${NAME:-percona-server-mongodb-operator}"
CATALOG_ENV="${CATALOG_ENV:-dev}"

usage() {
	cat <<EOF
Usage:
  BUNDLE_IMAGE_VERSION=<version> $0 build \
    <community|certified> <catalog-repo> <bundle-repo>

  $0 apply \
    <community|certified> <catalog-repo>

Examples:
  BUNDLE_IMAGE_VERSION=1.23.0 $0 build \
    community \
    docker.io/perconalab/percona-server-mongodb-operator \
    docker.io/perconalab/percona-server-mongodb-operator

  $0 apply \
    community \
    docker.io/perconalab/percona-server-mongodb-operator

  $0 apply \
    certified \
    docker.io/percona/percona-server-mongodb-operator
  # -> CatalogSource name: certified-prod
EOF
}

die() {
	echo "[olm] ERROR: $*" >&2
	exit 1
}

require() {
	command -v "$1" >/dev/null 2>&1 || die "$1 is required"
}

is_true() {
	case "${1:-}" in
		1|true|TRUE|yes|YES|y|Y) return 0 ;;
		*) return 1 ;;
	esac
}

configure_bundle_type() {
	case "$BUNDLE_TYPE" in
		community)
			GITHUB_REPO="k8s-operatorhub/community-operators"
			OPERATOR_PATH="operators/percona-server-mongodb-operator"
			;;
		certified)
			GITHUB_REPO="redhat-openshift-ecosystem/certified-operators"
			OPERATOR_PATH="operators/percona-server-mongodb-operator-certified"
			;;
		*)
			usage
			die "unsupported bundle type: ${BUNDLE_TYPE:-empty}"
			;;
	esac
}

catalog_image() {
	if [[ -n "$CATALOG_ENV" ]]; then
		echo -n "${CATALOG_REPO}:${BUNDLE_TYPE}-${CATALOG_ENV}-catalog"
		return
	fi
	echo -n "${CATALOG_REPO}:${BUNDLE_TYPE}-catalog"
}

current_bundle_image() {
	echo -n "${BUNDLE_REPO}:${BUNDLE_IMAGE_VERSION}-${BUNDLE_TYPE}-bundle"
}

previous_bundle_image() {
	if [[ -n "$CATALOG_ENV" ]]; then
		echo -n "${CATALOG_REPO}:$1-${BUNDLE_TYPE}-bundle-${CATALOG_ENV}-catalog"
		return
	fi
	echo -n "${CATALOG_REPO}:$1-${BUNDLE_TYPE}-bundle-catalog"
}

catalog_source_name() {
	if [[ -n "$CATALOG_ENV" ]]; then
		echo -n "${BUNDLE_TYPE}-${CATALOG_ENV}"
		return
	fi
	echo -n "${BUNDLE_TYPE}"
}

list_bundle_images() {
	local prefix="$1"
	local version

	for version in "${OLD_VERSIONS[@]}"; do
		echo "${prefix}$(previous_bundle_image "$version")"
	done

	echo "${prefix}$(current_bundle_image)"
}

download_operatorhub() {
	local archive="${TMP_DIR}/operatorhub.tar.gz"

	echo "[olm] Downloading ${GITHUB_REPO}"

	mkdir -p "$OPERATORHUB_DIR"

	curl -fsSL \
		"https://github.com/${GITHUB_REPO}/archive/refs/heads/main.tar.gz" \
		-o "$archive"

	tar -xzf "$archive" \
		-C "$OPERATORHUB_DIR" \
		--strip-components=1
}

find_previous_versions() {
	local operator_dir="${OPERATORHUB_DIR}/${OPERATOR_PATH}"

	[[ -d "$operator_dir" ]] ||
		die "operator directory not found: ${operator_dir}"

	find "$operator_dir" \
		-mindepth 1 \
		-maxdepth 1 \
		-type d \
		-exec basename {} \; |
		grep -E '^[0-9]+\.[0-9]+\.[0-9]+$' |
		awk -v current="$BUNDLE_IMAGE_VERSION" '
			$0 != current && !seen[$0]++ { print }
		' |
		sort -t. -k1,1n -k2,2n -k3,3n |
		tail -n "$CATALOG_BUNDLE_LIMIT"
}

confirm_build() {
	local answer
	local tty_source=/dev/stdin

	is_true "$CONFIRM_PUSH" || return 0

	[[ -r /dev/tty ]] && tty_source=/dev/tty

	echo
	echo "[olm] Images to build and push:"
	list_bundle_images "  - "
	echo

	read -r -p "Build and push these images? [y/N] " answer <"$tty_source"

	case "$answer" in
		y|Y|yes|YES) ;;
		*)
			echo "[olm] Build and push skipped"
			exit 0
			;;
	esac
}

write_bundle_dockerfile() {
	local bundle_dir="$1"
	local annotations="${bundle_dir}/metadata/annotations.yaml"

	[[ -f "$annotations" ]] ||
		die "bundle annotations not found: ${annotations}"

	[[ -d "${bundle_dir}/manifests" ]] ||
		die "bundle manifests not found: ${bundle_dir}/manifests"

	{
		echo "FROM scratch"

		yq -o=json '.annotations' "$annotations" |
			jq -r '
				to_entries[] |
				"LABEL \(.key)=\(.value | tostring | @json)"
			'

		echo
		echo "COPY manifests/ /manifests/"
		echo "COPY metadata/ /metadata/"
	} >"${bundle_dir}/Dockerfile"
}

build_previous_bundle() {
	local version="$1"
	local bundle_dir="${OPERATORHUB_DIR}/${OPERATOR_PATH}/${version}"
	local image

	image="$(previous_bundle_image "$version")"

	echo "[olm] Building and pushing bundle ${image}"

	write_bundle_dockerfile "$bundle_dir"

	"$CONTAINER" buildx build \
		--platform "$CATALOG_PLATFORM" \
		--tag "$image" \
		--push \
		"$bundle_dir"
}

write_catalog_template() {
	{
		echo "Schema: olm.semver"
		echo "GenerateMajorChannels: false"
		echo "GenerateMinorChannels: false"
		echo "Stable:"
		echo "  Bundles:"
		list_bundle_images "    - Image: "
	} >"$CATALOG_TEMPLATE"
}

build_catalog() {
	local image

	image="$(catalog_image)"

	mkdir -p "$CATALOG_DIR"
	write_catalog_template

	echo "[olm] Catalog bundles:"
	list_bundle_images "  - "
	echo "[olm] Rendering ${BUNDLE_TYPE} catalog"

	opm alpha render-template semver \
		-o yaml \
		<"$CATALOG_TEMPLATE" \
		>"${CATALOG_DIR}/catalog.yaml"

	echo "[olm] Validating ${BUNDLE_TYPE} catalog"
	opm validate "$CATALOG_DIR"

	echo "[olm] Generating catalog Dockerfile"
	opm generate dockerfile "$CATALOG_DIR"

	echo "[olm] Building and pushing catalog ${image}"

	"$CONTAINER" buildx build \
		--platform "$CATALOG_PLATFORM" \
		--file "${CATALOG_DIR}.Dockerfile" \
		--tag "$image" \
		--push \
		"$CATALOG_ROOT"

	echo "[olm] Catalog image pushed: ${image}"
}

run_build() {
	local version

	BUNDLE_IMAGE_VERSION="${BUNDLE_IMAGE_VERSION:-}"
	[[ -n "$CATALOG_REPO" ]] || die "catalog repository is required"
	[[ -n "$BUNDLE_REPO" ]] || die "bundle repository is required"
	[[ -n "$BUNDLE_IMAGE_VERSION" ]] || die "BUNDLE_IMAGE_VERSION is required"
	[[ "$CATALOG_BUNDLE_LIMIT" =~ ^[0-9]+$ ]] ||
		die "CATALOG_BUNDLE_LIMIT must be a non-negative integer"

	for command in curl tar jq yq opm "$CONTAINER"; do
		require "$command"
	done

	TMP_DIR="$(mktemp -d)"
	OPERATORHUB_DIR="${TMP_DIR}/operatorhub"
	CATALOG_ROOT="${TMP_DIR}/catalog-build"
	CATALOG_DIR="${CATALOG_ROOT}/catalog"
	CATALOG_TEMPLATE="${TMP_DIR}/catalog-template.yaml"

	trap 'rm -rf "$TMP_DIR"' EXIT

	download_operatorhub

	OLD_VERSIONS=()

	while IFS= read -r version; do
		[[ -n "$version" ]] && OLD_VERSIONS+=("$version")
	done < <(find_previous_versions)

	[[ "${#OLD_VERSIONS[@]}" -eq "$CATALOG_BUNDLE_LIMIT" ]] ||
		die "expected ${CATALOG_BUNDLE_LIMIT} previous versions, found ${#OLD_VERSIONS[@]}"

	confirm_build

	for version in "${OLD_VERSIONS[@]}"; do
		build_previous_bundle "$version"
	done

	build_catalog
}

run_apply() {
	local source_name
	local image

	require kubectl
	[[ -n "$CATALOG_REPO" ]] || die "catalog repository is required"

	source_name="$(catalog_source_name)"
	image="$(catalog_image)"

	echo "[olm] Applying CatalogSource ${source_name}"
	echo "[olm] Namespace: ${CATALOG_NAMESPACE}"
	echo "[olm] Image: ${image}"

	kubectl apply -f - <<EOF
apiVersion: operators.coreos.com/v1alpha1
kind: CatalogSource
metadata:
  name: ${source_name}
  namespace: ${CATALOG_NAMESPACE}
spec:
  displayName: ${source_name}
  sourceType: grpc
  image: ${image}
  updateStrategy:
    registryPoll:
      interval: 10m
EOF
}

configure_bundle_type

case "$ACTION" in
	build) run_build ;;
	apply) run_apply ;;
	*)
		usage
		die "unsupported action: ${ACTION:-empty}"
		;;
esac
