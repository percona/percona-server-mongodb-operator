#!/usr/bin/env bash
set -euo pipefail
shopt -s inherit_errexit 2>/dev/null || true

DISTRIBUTION="${1:?Distribution argument required (community|redhat)}"

cd "${BASH_SOURCE[0]%/*}"

sed=$(command -v gsed || command -v sed)
date=$(command -v gdate || command -v date)
repo_root="$(cd ../.. && pwd)"
release_versions_file="${repo_root}/e2e-tests/release_versions"
bundle_name="${BUNDLE_NAME:-${DISTRIBUTION}}"
bundle_directory="bundles/${bundle_name}"
project_directory="projects/${bundle_name}"
go_api_directory="$(cd ../../pkg/apis && pwd)"

# Used both as the operator-sdk project name and as the CSV file stem.
component_name="percona-server-mongodb-operator"

NS_RESOURCE_RBAC="../rbac/namespace"
NS_RESOURCE_OPERATOR="../manager/namespace"
KUSTOMIZATION_FILE="../../config/bundle/kustomization.yaml"

relatedImages="[]"
skips="[]"
containerImage=""
csv_stem=""
distribution_data="{}"

log() {
	echo >&2 "[olm] $*"
}

abort() {
	echo >&2 "[olm] ERROR: $*"
	exit 1
}

debug() {
	if [[ "${OLM_VERBOSE:-0}" == "1" || "${OLM_VERBOSE:-false}" == "true" ]]; then
		log "$@"
	fi
}

run_quiet() {
	local description="$1"
	shift

	local output_path=""
	if [[ "$1" == "-o" ]]; then
		output_path="$2"
		shift 2
	fi

	if [[ "${OLM_VERBOSE:-0}" == "1" || "${OLM_VERBOSE:-false}" == "true" ]]; then
		if [[ -n "${output_path}" ]]; then
			"$@" >"${output_path}"
		else
			"$@"
		fi
		return
	fi

	local capture_file
	capture_file="$(mktemp)"

	if [[ -n "${output_path}" ]]; then
		"$@" >"${output_path}" 2>"${capture_file}" && { rm -f "${capture_file}"; return; }
	else
		"$@" >"${capture_file}" 2>&1 && { rm -f "${capture_file}"; return; }
	fi

	cat "${capture_file}" >&2
	rm -f "${capture_file}"
	abort "${description} failed"
}

require() {
	if [ $# -eq 1 ]; then
		command -v "$1" >/dev/null 2>&1 \
			|| abort "$1 not found in PATH"
	else
		"$@" >/dev/null 2>&1 \
			|| abort "Failed running: $*"
	fi
}

sed_in_place() {
	local expression="$1"
	local file="$2"
	local tmp_file

	tmp_file="$(mktemp)"
	"$sed" "$expression" "$file" >"$tmp_file"
	mv "$tmp_file" "$file"
}

check_tools() {
	local command

	for command in gawk gcsplit yq jq kubectl operator-sdk yamllint envsubst; do
		require "$command"
	done
}

release_version_value() {
	local key="$1"

	local version
	version="$(awk -F= -v key="${key}" '$1 == key { print $2 }' "${release_versions_file}" \
		| tr -d '"' \
		| tail -1)"

	if [[ -z "${version}" ]]; then
		abort "Missing ${key} in ${release_versions_file}"
	fi

	echo -n "${version}"
}

resolve_openshift_versions() {
	local openshift_min
	local openshift_max

	if [[ -n "${OPENSHIFT_VERSIONS:-}" ]]; then
		echo -n "${OPENSHIFT_VERSIONS}"
		return
	fi

	[[ -f "${release_versions_file}" ]] \
		|| abort "OPENSHIFT_VERSIONS is not set and ${release_versions_file} does not exist"

	openshift_min="$(release_version_value "OPENSHIFT_MIN" | awk -F. '{ print "v" $1 "." $2 }')"
	openshift_max="$(release_version_value "OPENSHIFT_MAX" | awk -F. '{ print "v" $1 "." $2 }')"

	[[ -n "${openshift_min}" && -n "${openshift_max}" ]] \
		|| abort "OPENSHIFT_MIN and OPENSHIFT_MAX must be set in ${release_versions_file}"

	echo -n "${openshift_min}-${openshift_max}"
}

load_distribution_hooks() {
	local hook_file="distributions/${DISTRIBUTION}.sh"

	[[ "${DISTRIBUTION}" == "community" || "${DISTRIBUTION}" == "redhat" ]] \
		|| abort "Unknown distribution: ${DISTRIBUTION}"
	[[ -f "${hook_file}" ]] || abort "Distribution hooks not found: ${hook_file}"

	log "Loading distribution hooks from ${hook_file}"
	# shellcheck source=/dev/null
	source "${hook_file}"

	for hook in build_distribution_data distribution_package_name customize_csv; do
		declare -F "${hook}" >/dev/null || abort "Missing distribution hook: ${hook}"
	done
}

configure_namespace_manifests() {
	sed_in_place "s|../rbac/cluster|$NS_RESOURCE_RBAC|g" "$KUSTOMIZATION_FILE"
	sed_in_place "s|../manager/cluster|$NS_RESOURCE_OPERATOR|g" "$KUSTOMIZATION_FILE"
}

prepare_operator_sources() {
	log "Preparing namespace-scoped operator manifests"

	cp ../../deploy/operator.yaml ../../config/manager/namespace

	gcsplit --elide-empty-files -f output- ../../deploy/rbac.yaml "/^---$/" "{*}" >/dev/null
	mv output-00 ../../config/rbac/namespace/role.yaml
	mv output-01 ../../config/rbac/namespace/service_account.yaml
	mv output-02 ../../config/rbac/namespace/role_binding.yaml
}

render_operator_manifests() {
	log "Rendering operator manifests for ${DISTRIBUTION}"

	run_quiet "Rendering operator manifests" -o operator_yamls.yaml \
		kubectl kustomize "../../config/${DISTRIBUTION}"

	yq eval '. | select(.kind == "CustomResourceDefinition")' operator_yamls.yaml >operator_crds.yaml
	yq eval '. | select(.kind == "Deployment")' operator_yamls.yaml >operator_deployments.yaml
	yq eval '. | select(.kind == "ServiceAccount")' operator_yamls.yaml >operator_accounts.yaml
	yq eval '. | select(.kind == "Role")' operator_yamls.yaml >operator_roles.yaml
}

create_sdk_workspace() {
	log "Creating Operator SDK workspace"

	rm -rf "${project_directory}"
	install -d "${project_directory}"

	(
		cd "${project_directory}"
		run_quiet "Creating Operator SDK workspace" \
			operator-sdk init --fetch-deps="false" --project-name="${component_name}"

		yq eval '[. | {"group": .spec.group, "kind": .spec.names.kind, "version": .spec.versions[].name}]' \
			../../../../deploy/crd.yaml >crd_gvks.yaml

		yq eval --inplace '.multigroup = true | .resources = load("crd_gvks.yaml" | fromyaml) | .' ./PROJECT

		ln -s "${go_api_directory}" .
		run_quiet "Generating Operator SDK kustomize manifests" \
			operator-sdk generate kustomize manifests --interactive="false"
	)
}

create_bundle_directory() {
	log "Creating bundle directory ${bundle_directory}"

	rm -rf "${bundle_directory}"
	install -d \
		"${bundle_directory}/manifests" \
		"${bundle_directory}/metadata"
}

render_bundle_metadata() {
	log "Rendering bundle metadata"

	export package="${PACKAGE_NAME_OVERRIDE:-$(distribution_package_name)}"
	export package_channel="${PACKAGE_CHANNEL:-stable}"
	export openshift_supported_versions
	openshift_supported_versions="$(resolve_openshift_versions)"

	yq eval '
		.annotations["operators.operatorframework.io.bundle.channels.v1"] = env(package_channel) |
		.annotations["operators.operatorframework.io.bundle.channel.default.v1"] = env(package_channel) |
		.annotations["operators.operatorframework.io.bundle.package.v1"] = env(package) |
		.annotations["com.redhat.openshift.versions"] = env(openshift_supported_versions) |
		.annotations["org.opencontainers.image.authors"] = "info@percona.com" |
		.annotations["org.opencontainers.image.url"] = "https://percona.com" |
		.annotations["org.opencontainers.image.vendor"] = "Percona"
	' bundle.annotations.yaml >"${bundle_directory}/metadata/annotations.yaml"
}

render_bundle_dockerfile() {
	local labels

	labels="$(yq eval -r '.annotations | to_entries | map("LABEL " + .key + "=" + (.value | tojson)) | join("\n")' \
		"${bundle_directory}/metadata/annotations.yaml")"

	labels="${labels}
LABEL com.redhat.delivery.backport=true
LABEL com.redhat.delivery.operator.bundle=true"

	LABELS="${labels}" envsubst <bundle.Dockerfile >"${bundle_directory}/Dockerfile"
	awk '{gsub(/^[ \t]+/, "    "); print}' "${bundle_directory}/Dockerfile" >"${bundle_directory}/Dockerfile.new"
	mv "${bundle_directory}/Dockerfile.new" "${bundle_directory}/Dockerfile"
}

write_crd_manifests() {
	local crd_names

	log "Writing CRD manifests"

	crd_names="$(yq eval -o=tsv '.metadata.name' ../../deploy/crd.yaml)"

	gawk -v names="${crd_names}" -v bundle_directory="${bundle_directory}" '
BEGIN {
    split(names, name_array, " ");
    idx=1;
}
/apiVersion: apiextensions.k8s.io\/v1/ {
    if (idx in name_array) {
        current_file = bundle_directory "/manifests/" name_array[idx] ".crd.yaml";
        idx++;
    } else {
        current_file = bundle_directory "/unnamed_" idx ".yaml";
        idx++;
    }
}
{
    if (current_file != "") {
        print > current_file;
    }
}
' ../../deploy/crd.yaml

	find "${bundle_directory}/manifests" -type f -name "*.crd.yaml" -print0 | while IFS= read -r -d '' file; do
		# shellcheck disable=SC2016
		sed_in_place '1s/^/---\
/; ${/^---$/d;}' "$file"
	done
}

validate_manifest_inputs() {
	yq eval -i '[.]' operator_deployments.yaml
	yq eval 'length == 1' operator_deployments.yaml --exit-status >/dev/null \
		|| abort "too many deployments accounts: $(yq eval . operator_deployments.yaml)"

	yq eval -i '[.]' operator_accounts.yaml
	yq eval 'length == 1' operator_accounts.yaml --exit-status >/dev/null \
		|| abort "too many service accounts: $(yq eval . operator_accounts.yaml)"

	yq eval -i '[.]' operator_roles.yaml
	yq eval 'length == 1' operator_roles.yaml --exit-status >/dev/null \
		|| abort "too many roles: $(yq eval . operator_roles.yaml)"
}

build_examples() {
	local cr_example
	local backup_example
	local clustersync_example
	local images
	local restore_example

	images="$(jq -c '.images' <<<"${distribution_data}")"

	cr_example="$(
		yq eval -o=json ../../deploy/cr.yaml |
			jq \
				--argjson images "${images}" \
				'
				def insert_after($k; $new):
					to_entries as $e
					| reduce $e[] as $item ({};
							. + {($item.key): $item.value}
							| if $item.key == $k then . + $new else . end
						);

				.spec |= (
					if has("initImage") then del(.initImage) else . end
					| .image = $images["mongod8.0"]
					| insert_after("image"; {"initImage": $images.operator})
					| if has("initImage") then . else . + {"initImage": $images.operator} end
					| .pmm.image = $images.pmm3
					| .backup.image = $images.backup
					| .logcollector.image = $images.logcollector
				)
			'
	)"

	clustersync_example="null"
	if jq -e '.clustersync? | strings | length > 0' >/dev/null <<<"${images}"; then
		clustersync_example="$(
			yq eval -o=json ../../deploy/clustersync.yaml |
				jq -s \
					--argjson images "${images}" \
					'
					map(
						select(.kind == "PerconaServerMongoDBClusterSync")
						| .spec.image = $images.clustersync
					)
					| first
					'
		)"
	fi

	backup_example="$(yq eval -o=json ../../deploy/backup/backup.yaml)"
	restore_example="$(yq eval -o=json ../../deploy/backup/restore.yaml)"

	jq -n "[${cr_example}, ${backup_example}, ${restore_example}, ${clustersync_example}] | map(select(. != null))"
}

build_managed_resources() {
	yq eval -o=json '.' operator_roles.yaml |
		jq '
			def kind:
				{
					"certificaterequests": "CertificateRequest",
					"certificates": "Certificate",
					"configmaps": "ConfigMap",
					"cronjobs": "CronJob",
					"deployments": "Deployment",
					"issuers": "Issuer",
					"persistentvolumeclaims": "PersistentVolumeClaim",
					"poddisruptionbudgets": "PodDisruptionBudget",
					"pods": "Pod",
					"replicasets": "ReplicaSet",
					"secrets": "Secret",
					"serviceexports": "ServiceExport",
					"serviceimports": "ServiceImport",
					"services": "Service",
					"statefulsets": "StatefulSet",
					"volumesnapshots": "VolumeSnapshot"
				}[.] // .;

			def version($apiGroup):
				if $apiGroup == "" then "v1"
				else $apiGroup + "/v1"
				end;

			[
				(if type == "array" then . else [.] end)[].rules[]
				| select((.verbs // []) | any(. == "create" or . == "update" or . == "patch" or . == "delete" or . == "deletecollection"))
				| .apiGroups[] as $apiGroup
				| select($apiGroup != "psmdb.percona.com")
				| .resources[]
				| select((contains("/") | not) and . != "events" and . != "leases")
				| {
					"version": version($apiGroup),
					"kind": kind,
					"name": ""
				}
			] | unique_by(.version + "/" + .kind) | sort_by(.version, .kind)
		'
}

build_owned_crds() {
	local managed_resources

	managed_resources="$(build_managed_resources)"

	yq eval-all -o=json '[.]' ../../deploy/crd.yaml |
		jq --argjson managed_resources "${managed_resources}" '
		def crd_description:
			{
				"PerconaServerMongoDB": "Instance of a Percona Server for MongoDB replica set",
				"PerconaServerMongoDBBackup": "Instance of a Percona Server for MongoDB Backup",
				"PerconaServerMongoDBRestore": "Instance of a Percona Server for MongoDB Restore",
				"PerconaServerMongoDBClusterSync": "Instance of a Percona Server for MongoDB Cluster Sync"
			}[.spec.names.kind] // ("Instance of a " + .spec.names.kind);

		[
			.[]
			| select(.kind == "CustomResourceDefinition")
			| {
				"description": crd_description,
				"displayName": .spec.names.kind,
				"kind": .spec.names.kind,
				"name": .metadata.name,
				"version": (.spec.versions[] | select(.storage == true) | .name),
				"specDescriptors": [],
				"statusDescriptors": [],
				"resources": (if .spec.names.kind == "PerconaServerMongoDB" then $managed_resources else [] end)
			}
		]
	'
}

prepare_distribution() {
	distribution_data="$(build_distribution_data)" || exit $?

	jq -e '
		(.images | type == "object") and
		((["operator", "backup", "logcollector", "mongod8.0", "pmm3"] - (.images | keys)) | length == 0)
	' >/dev/null <<<"${distribution_data}" \
		|| abort "Distribution data is missing required images"

	containerImage="$(jq -er '.images.operator' <<<"${distribution_data}")"
	relatedImages="$(jq -c '.relatedImages // []' <<<"${distribution_data}")"
	skips="$(jq -c '.skips // []' <<<"${distribution_data}")"
}

render_csv() {
	local account
	local deployment
	local examples
	local owned_crds
	local rules
	local timestamp
	local version
	local csv_file
	local icon_base64

	log "Rendering CSV"

	csv_stem="$(yq -r '.projectName' "${project_directory}/PROJECT")"
	deployment="$(yq eval operator_deployments.yaml)"

	if [[ -z "${containerImage}" ]]; then
		containerImage="$(yq eval '.[0].spec.template.spec.containers[0].image' operator_deployments.yaml)"
	else
		deployment="$(
			IMAGE="${containerImage}" yq eval '.[0].spec.template.spec.containers[0].image = env(IMAGE)' \
				<<<"${deployment}"
		)"
	fi

	examples="$(build_examples)"
	owned_crds="$(build_owned_crds)"
	account="$(yq eval '.[] | .metadata.name' operator_accounts.yaml)"
	rules="$(yq eval '.[] | .rules' operator_roles.yaml)"
	version="${CSV_VERSION:-${VERSION}}"
	timestamp="$("$date" -u +"%Y-%m-%dT%H:%M:%SZ")"
	csv_file="${bundle_directory}/manifests/${component_name}.clusterserviceversion.yaml"
	icon_base64="$(base64 <"${repo_root}/kubernetes.svg" | tr -d '\n')"

	export examples
	export owned_crds
	export deployment
	export account
	export rules
	export version
	export stem="${csv_stem}"
	export timestamp
	export name="${CSV_NAME_OVERRIDE:-${csv_stem}.v${version}}"
	export name_certified="${CSV_NAME_OVERRIDE:-${csv_stem}-certified.v${version}}"
	export skip_range="<${version}"
	export containerImage
	export relatedImages
	export skips
	export icon_base64

	yq -P eval '
	  .metadata.annotations["alm-examples"] = strenv(examples) |
	  .metadata.annotations["containerImage"] = env(containerImage) |
	  .metadata.annotations["createdAt"] = strenv(timestamp) |
	  .metadata.name = env(name) |
	  .spec.version = env(version) |
	  .spec.icon = [{ "base64data": strenv(icon_base64), "mediatype": "image/svg+xml" }] |
	  .spec.customresourcedefinitions.owned = (strenv(owned_crds) | from_json) |
	  .spec.install.spec.permissions = [{ "serviceAccountName": env(account), "rules": env(rules) }] |
	  .spec.install.spec.deployments = (env(deployment) | [.[] | { "name": .metadata.name, "spec": .spec }])' \
		bundle.csv.yaml >"${csv_file}"

	customize_csv "${csv_file}"
}

validate_bundle() {
	if [[ "${OLM_VERBOSE:-0}" == "1" || "${OLM_VERBOSE:-false}" == "true" ]] && command -v tree >/dev/null 2>&1; then
		tree -C "${bundle_directory}"
	fi

	run_quiet "YAML validation" \
		yamllint -d '{extends: default, rules: {line-length: disable, indentation: disable}}' "${bundle_directory}"
}

normalize_bundle_permissions() {
	chmod -R a+rX "${bundle_directory}"
}

main() {
	check_tools
	load_distribution_hooks
	configure_namespace_manifests
	prepare_operator_sources
	render_operator_manifests
	create_sdk_workspace
	create_bundle_directory
	render_bundle_metadata
	render_bundle_dockerfile
	write_crd_manifests
	validate_manifest_inputs
	prepare_distribution
	render_csv
	normalize_bundle_permissions
	validate_bundle
}

main "$@"
