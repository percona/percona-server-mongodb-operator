# ==============================================================================
# Percona Server MongoDB Operator - OLM Bundle Generation
# ==============================================================================

.DEFAULT_GOAL := help
.SUFFIXES:
SHELL := /bin/bash

NAME ?= percona-server-mongodb-operator
BUNDLE_TYPES := community certified

SED := $(shell which gsed || which sed)
VERSION ?= $(shell git rev-parse --abbrev-ref HEAD | $(SED) -e 's^/^-^g; s^[.]^-^g;' | tr '[:upper:]' '[:lower:]')
CSV_VERSION ?= $(shell printf '%s\n' '$(VERSION)' | $(SED) -nE 's/.*([0-9]+)[.-]([0-9]+)[.-]([0-9]+).*/\1.\2.\3/p')
RELEASE_VERSIONS ?= ../../e2e-tests/release_versions

PACKAGE_CHANNEL ?= stable
MIN_KUBE_VERSION ?= ""
OPENSHIFT_VERSIONS ?= $(shell awk -F= '$$1 == "OPENSHIFT_MIN" { split($$2, v, "."); min = "v" v[1] "." v[2] } $$1 == "OPENSHIFT_MAX" { split($$2, v, "."); max = "v" v[1] "." v[2] } END { if (min && max) print min "-" max }' '$(RELEASE_VERSIONS)')

COMMUNITY_REGISTRY ?= docker.io
IMAGE_TAG_OWNER ?= percona
IMAGE_TAG_BASE ?= $(IMAGE_TAG_OWNER)/$(NAME)
IMAGE ?= $(COMMUNITY_REGISTRY)/$(IMAGE_TAG_BASE):$(VERSION)

REDHAT_OPERATOR_IMAGE ?= registry.connect.redhat.com/percona/percona-server-mongodb-operator:$(VERSION)

CONTAINER ?= docker
DOCKER_DEFAULT_PLATFORM ?= linux/amd64

BUNDLE_TYPE ?= community
BUNDLE_REPO ?= $(IMAGE_TAG_BASE)
DEV_BUNDLE_REPO ?= docker.io/perconalab/$(NAME)
DEV_DISPLAY_NAME_SUFFIX ?= (Dev)
DEPLOY_BUNDLE_REPO ?= $(DEV_BUNDLE_REPO)
BUNDLE_IMAGE_VERSION ?= $(CSV_VERSION)
BUNDLE_PLATFORMS ?= linux/amd64,linux/arm64
BUNDLE_TAG_TYPE = $(if $(filter redhat certified,$(BUNDLE_TYPE)),certified,$(BUNDLE_TYPE))
BUNDLE_IMG ?= $(BUNDLE_REPO):$(BUNDLE_IMAGE_VERSION)-$(BUNDLE_TAG_TYPE)-bundle
CATALOG_REPO ?= $(DEV_BUNDLE_REPO)
CATALOG_NAMESPACE ?= openshift-marketplace
CATALOG_PLATFORMS ?= linux/amd64,linux/arm64
CATALOG_SOURCE_SUFFIX ?=

CONFIRM_PUSH ?= 1
SKIP_DIGEST_FAILURE ?= 0
RUN_BUNDLE_TIMEOUT ?= 10m

REPO_ROOT := $(shell git rev-parse --show-toplevel)
KUSTOMIZE := $(REPO_ROOT)/bin/kustomize

OS_KERNEL ?= $(shell uname -s | tr '[:upper:]' '[:lower:]')
OS_MACHINE ?= $(shell uname -m | sed -e 's/^x86_64$$/amd64/' -e 's/^x86_/amd/' -e 's/^aarch64$$/arm64/')
SYSTEM := $(OS_KERNEL)-$(OS_MACHINE)
TOOLS_DIR := tools/$(SYSTEM)

JQ_VERSION := 1.7.1
JQ_PLATFORM := $(if $(filter darwin,$(OS_KERNEL)),macos,$(OS_KERNEL))
OPERATOR_SDK_VERSION := v1.41.1
OPM_VERSION := v1.66.0

export PATH := $(CURDIR)/$(TOOLS_DIR):$(PATH)

# ==============================================================================
# Helpers
# ==============================================================================
GREEN := $(shell tput setaf 2)
RESET := $(shell tput sgr0)

define quiet_run
if [[ "$${OLM_VERBOSE:-0}" == "1" || "$${OLM_VERBOSE:-false}" == "true" ]]; then \
	$(2); \
else \
	output_file="$$(mktemp)"; \
	if $(2) >"$${output_file}" 2>&1; then \
		rm -f "$${output_file}"; \
	else \
		cat "$${output_file}" >&2; \
		rm -f "$${output_file}"; \
		echo "[olm] ERROR: $(1) failed" >&2; \
		exit 1; \
	fi; \
fi
endef

export VERSION CSV_VERSION BUNDLE_IMAGE_VERSION IMAGE REDHAT_OPERATOR_IMAGE OPENSHIFT_VERSIONS PACKAGE_CHANNEL MIN_KUBE_VERSION
export PACKAGE_NAME_OVERRIDE CSV_NAME_OVERRIDE DISPLAY_NAME_OVERRIDE
export DOCKER_DEFAULT_PLATFORM BUNDLE_REPO BUNDLE_PLATFORMS SKIP_DIGEST_FAILURE

# ==============================================================================
# Validation and Tool Helpers
# ==============================================================================
.PHONY: check-prereqs
check-prereqs: check-version check-csv-version check-tools tools $(KUSTOMIZE)

.PHONY: check-version
check-version:
ifndef VERSION
	$(error VERSION is not set)
endif

.PHONY: check-csv-version
check-csv-version:
ifndef CSV_VERSION
	$(error CSV_VERSION is not set and could not be parsed from VERSION=$(VERSION))
endif
	@if ! [[ "$(CSV_VERSION)" =~ ^[0-9]+\.[0-9]+\.[0-9]+$$ ]]; then \
		echo "CSV_VERSION must be a semver without prerelease/build metadata, got: $(CSV_VERSION)"; \
		exit 1; \
	fi

.PHONY: check-tools
check-tools:
	@for cmd in gawk gcsplit yq yamllint envsubst; do \
		if ! command -v $$cmd >/dev/null 2>&1; then \
			echo "Error: $$cmd is required but not installed"; \
			exit 1; \
		fi; \
	done

$(KUSTOMIZE):
	$(MAKE) -C ../.. bin/kustomize

.PHONY: tools
tools: $(TOOLS_DIR) $(TOOLS_DIR)/jq $(TOOLS_DIR)/operator-sdk $(TOOLS_DIR)/opm

$(TOOLS_DIR):
	mkdir -p $(TOOLS_DIR)

$(TOOLS_DIR)/jq:
	curl -fL "https://github.com/jqlang/jq/releases/download/jq-$(JQ_VERSION)/jq-$(JQ_PLATFORM)-$(OS_MACHINE)" -o $@
	chmod +x $@

$(TOOLS_DIR)/operator-sdk: | $(TOOLS_DIR)
	curl -sL "https://github.com/operator-framework/operator-sdk/releases/download/$(OPERATOR_SDK_VERSION)/operator-sdk_$(OS_KERNEL)_$(OS_MACHINE)" -o $@
	chmod +x $@

$(TOOLS_DIR)/opm: | $(TOOLS_DIR)
	curl -sL "https://github.com/operator-framework/operator-registry/releases/download/$(OPM_VERSION)/$(OS_KERNEL)-$(OS_MACHINE)-opm" -o $@
	chmod +x $@

# ==============================================================================
# Development Rules
# ==============================================================================
.PHONY: bundle
bundle: ## Build all development OLM bundles and push bundle images to perconalab
bundle: $(BUNDLE_TYPES:%=bundle/%)

.PHONY: bundle/community
bundle/community: ## Build and push community bundle image to perconalab

.PHONY: bundle/certified
bundle/certified: ## Build and push certified bundle image to perconalab

.PHONY: $(BUNDLE_TYPES:%=bundle/%)
$(BUNDLE_TYPES:%=bundle/%): bundle/%: check-version check-csv-version
	@package='$(NAME)'; \
	if [[ "$*" == "certified" ]]; then \
		package='$(NAME)-certified'; \
	fi; \
	csv_name="$${package}.v$(CSV_VERSION)"; \
	$(MAKE) bundle-prod/$* PACKAGE_NAME_OVERRIDE="$${package}" CSV_NAME_OVERRIDE="$${csv_name}" DISPLAY_NAME_OVERRIDE="Percona Distribution for MongoDB Operator $(DEV_DISPLAY_NAME_SUFFIX)"
	BUNDLE_REPO='$(DEV_BUNDLE_REPO)' ./build-image.sh '$(CONTAINER)' 'bundles/$*' '$*' '$(BUNDLE_IMAGE_VERSION)'

.PHONY: catalog
catalog: ## Build and push all development catalog images
catalog: $(BUNDLE_TYPES:%=catalog/%)

.PHONY: catalog/community
catalog/community: ## Build and push community development catalog image

.PHONY: catalog/certified
catalog/certified: ## Build and push certified development catalog image

.PHONY: $(BUNDLE_TYPES:%=catalog/%)
$(BUNDLE_TYPES:%=catalog/%): catalog/%: bundle/%
	$(MAKE) catalog-prod/$* BUNDLE_REPO='$(DEV_BUNDLE_REPO)'

.PHONY: deploy
deploy: ## Build, push, and deploy all development catalog sources
deploy: $(BUNDLE_TYPES:%=deploy/%)

.PHONY: deploy/community
deploy/community: ## Build, push, and deploy community development catalog source

.PHONY: deploy/certified
deploy/certified: ## Build, push, and deploy certified development catalog source

.PHONY: $(BUNDLE_TYPES:%=deploy/%)
$(BUNDLE_TYPES:%=deploy/%): deploy/%: catalog/%
	$(MAKE) apply-catalog/$* CATALOG_SOURCE_NAME='$*-dev'

# ==============================================================================
# Production Rules
# ==============================================================================
.PHONY: bundles-prod
bundles-prod: ## Build all production OLM bundles (community, certified)
bundles-prod: check-prereqs $(BUNDLE_TYPES:%=bundle-prod/%)

.PHONY: bundle-prod/community
bundle-prod/community: ## Build community OLM bundle

.PHONY: bundle-prod/certified
bundle-prod/certified: ## Build certified OLM bundle

.PHONY: $(BUNDLE_TYPES:%=bundle-prod/%)
$(BUNDLE_TYPES:%=bundle-prod/%): bundle-prod/%: tools $(KUSTOMIZE) check-version check-csv-version check-tools
	@echo "$(GREEN)Building $* bundle...$(RESET)"
	@distribution="$*"; \
	operator_image="$(IMAGE)"; \
	if [[ "$*" == "certified" ]]; then \
		distribution="redhat"; \
		operator_image="$(REDHAT_OPERATOR_IMAGE)"; \
	fi; \
	manager_kustomization="../../config/manager/namespace/kustomization.yaml"; \
	original_manager_kustomization="$$(mktemp)"; \
	cp "$${manager_kustomization}" "$${original_manager_kustomization}"; \
	trap 'cp "$${original_manager_kustomization}" "$${manager_kustomization}"; rm -f "$${original_manager_kustomization}"' EXIT; \
	(cd ../../config/manager/namespace/ && $(KUSTOMIZE) edit set image psmdb-operator="$${operator_image}"); \
	BUNDLE_NAME="$*" ./generate.sh "$${distribution}"; \
	$(call quiet_run,Validating $* bundle,operator-sdk bundle validate "bundles/$*" --select-optional='suite=operatorframework'); \
	if [[ "$*" == "community" ]]; then \
		$(call quiet_run,Validating $* community bundle,operator-sdk bundle validate "bundles/$*" --select-optional='name=community' --optional-values='index-path=bundles/$*/Dockerfile'); \
	fi
	@echo "$(GREEN)Bundle stored in installers/olm/bundles/$*$(RESET)"

.PHONY: catalog-prod
catalog-prod: ## Build and push all production catalog images
catalog-prod: $(BUNDLE_TYPES:%=catalog-prod/%)

.PHONY: catalog-prod/community
catalog-prod/community: ## Build and push community catalog image to perconalab

.PHONY: catalog-prod/certified
catalog-prod/certified: ## Build and push certified catalog image to perconalab

.PHONY: $(BUNDLE_TYPES:%=catalog-prod/%)
$(BUNDLE_TYPES:%=catalog-prod/%): catalog-prod/%: tools check-version check-csv-version
	@catalog_root='catalogs'; \
	catalog_dir="$${catalog_root}/$*"; \
	catalog_image='$(CATALOG_REPO):$*-catalog'; \
	bundle_image='$(BUNDLE_REPO):$(BUNDLE_IMAGE_VERSION)-$*-bundle'; \
	rm -rf "$${catalog_dir}" "$${catalog_dir}.Dockerfile"; \
	mkdir -p "$${catalog_dir}"; \
	printf '%s\n' \
		'Schema: olm.semver' \
		'GenerateMajorChannels: false' \
		'GenerateMinorChannels: false' \
		'Stable:' \
		'  Bundles:' \
		"    - Image: $${bundle_image}" \
		> "$${catalog_dir}/template.yaml"; \
	opm alpha render-template semver -o yaml < "$${catalog_dir}/template.yaml" > "$${catalog_dir}/catalog.yaml"; \
	opm validate "$${catalog_dir}"; \
	opm generate dockerfile "$${catalog_dir}"; \
	if [[ "$(CONFIRM_PUSH)" != "0" && "$(CONFIRM_PUSH)" != "false" ]]; then \
		read -r -p "Push image $${catalog_image}? [y/N] " answer; \
		[[ "$${answer}" == "y" || "$${answer}" == "Y" || "$${answer}" == "yes" || "$${answer}" == "YES" ]] || exit 1; \
	fi; \
	$(CONTAINER) buildx build \
		--platform '$(CATALOG_PLATFORMS)' \
		-f "$${catalog_dir}.Dockerfile" \
		-t "$${catalog_image}" \
		--push \
		"$${catalog_root}"; \
	echo "$(GREEN)Catalog image pushed: $${catalog_image}$(RESET)"

.PHONY: deploy-prod
deploy-prod: ## Build, push, and deploy all production catalog sources
deploy-prod: $(BUNDLE_TYPES:%=deploy-prod/%)

.PHONY: deploy-prod/community
deploy-prod/community: ## Build, push, and deploy community bundle

.PHONY: deploy-prod/certified
deploy-prod/certified: ## Build, push, and deploy certified bundle

.PHONY: $(BUNDLE_TYPES:%=deploy-prod/%)
$(BUNDLE_TYPES:%=deploy-prod/%): deploy-prod/%: tools check-version check-csv-version
	$(MAKE) bundle-prod/$*
	BUNDLE_REPO='$(DEPLOY_BUNDLE_REPO)' ./build-image.sh '$(CONTAINER)' 'bundles/$*' '$*' '$(BUNDLE_IMAGE_VERSION)'
	$(MAKE) catalog-prod/$* BUNDLE_REPO='$(DEPLOY_BUNDLE_REPO)'
	$(MAKE) apply-catalog/$*

.PHONY: $(BUNDLE_TYPES:%=apply-catalog/%)
$(BUNDLE_TYPES:%=apply-catalog/%): apply-catalog/%:
	@set -e; \
	catalog_source='$(CATALOG_SOURCE_NAME)'; \
	if [[ -z "$${catalog_source}" ]]; then \
		catalog_source='$(NAME)'; \
		if [[ "$*" == "certified" ]]; then catalog_source='$(NAME)-certified'; fi; \
	fi; \
	catalog_image='$(CATALOG_REPO):$*-catalog'; \
	catalog_dir='catalogs/$*'; \
	catalog_namespace='$(CATALOG_NAMESPACE)'; \
	legacy_catalog_source=''; \
	if [[ -n '$(CATALOG_SOURCE_NAME)' ]]; then \
		legacy_catalog_source='$(NAME)'; \
		if [[ "$*" == "certified" ]]; then legacy_catalog_source='$(NAME)-certified'; fi; \
	fi; \
	if ! kubectl get namespace "$${catalog_namespace}" >/dev/null 2>&1; then \
		kubectl create namespace "$${catalog_namespace}"; \
	fi; \
	printf '%s\n' \
		'apiVersion: operators.coreos.com/v1alpha1' \
		'kind: CatalogSource' \
		'metadata:' \
		"  name: $${catalog_source}" \
		"  namespace: $${catalog_namespace}" \
		'spec:' \
		"  displayName: $${catalog_source}" \
		'  sourceType: grpc' \
		"  image: $${catalog_image}" \
		'  imagePullPolicy: Always' \
		> "$${catalog_dir}/catalog-source.yaml"; \
	kubectl apply --validate=false -f "$${catalog_dir}/catalog-source.yaml"; \
	kubectl delete pod -n "$${catalog_namespace}" -l "olm.catalogSource=$${catalog_source}" --ignore-not-found; \
	if [[ -n "$${legacy_catalog_source}" && "$${legacy_catalog_source}" != "$${catalog_source}" ]]; then \
		kubectl delete catalogsource "$${legacy_catalog_source}" -n "$${catalog_namespace}" --ignore-not-found; \
	fi; \
	echo "$(GREEN)CatalogSource deployed: $${catalog_source} in $${catalog_namespace} -> $${catalog_image}$(RESET)"

# ==============================================================================
# Image Rules
# ==============================================================================
.PHONY: build
build: ## Build bundle image (set BUNDLE_TYPE=community|certified)
build: check-version check-csv-version
	@echo "$(GREEN)Building bundle image...$(RESET)"
	$(CONTAINER) build -t $(BUNDLE_IMG) --platform=$(DOCKER_DEFAULT_PLATFORM) bundles/$(BUNDLE_TYPE)
	@echo "$(GREEN)Bundle image built: $(BUNDLE_IMG)$(RESET)"

.PHONY: push
push: ## Push bundle image to registry
push: check-version check-csv-version
	@echo "$(GREEN)Pushing bundle image to registry...$(RESET)"
	@if [[ "$(CONFIRM_PUSH)" != "0" && "$(CONFIRM_PUSH)" != "false" ]]; then \
		read -r -p "Push image $(BUNDLE_IMG)? [y/N] " answer; \
		[[ "$${answer}" == "y" || "$${answer}" == "Y" || "$${answer}" == "yes" || "$${answer}" == "YES" ]] || exit 1; \
	fi
	$(CONTAINER) push $(BUNDLE_IMG)
	@echo "$(GREEN)Bundle image pushed: $(BUNDLE_IMG)$(RESET)"

.PHONY: build-bundle-images
build-bundle-images: check-version check-csv-version $(BUNDLE_TYPES:%=build-%-image)

build-%-image:
	./build-image.sh '$(CONTAINER)' 'bundles/$*' '$*' '$(BUNDLE_IMAGE_VERSION)'

# ==============================================================================
# Validation Rules
# ==============================================================================
.PHONY: validate
validate: ## Run bundle image and directory validation
validate: tools
validate: $(BUNDLE_TYPES:%=validate-%-image)
validate: $(BUNDLE_TYPES:%=validate-%-directory)

.PHONY: validate/community validate/certified
validate/community validate/certified: validate/%: tools
	./validate-image.sh '$(CONTAINER)' 'bundles/$*'
	./validate-directory.sh 'bundles/$*'

validate-%-directory:
	./validate-directory.sh 'bundles/$*'

validate-%-image:
	./validate-image.sh '$(CONTAINER)' 'bundles/$*'

# ==============================================================================
# Utility Rules
# ==============================================================================
.PHONY: install-olm
install-olm: ## Install OLM in Kubernetes cluster
install-olm: tools
	operator-sdk olm install

.PHONY: clean
clean: ## Remove generated files and downloaded tools
	rm -rf ./bundles ./catalogs ./projects ./tools

.PHONY: help
help: ## Show this help message
	@awk 'BEGIN {FS = ": ## "; printf "\n$(GREEN)Usage:$(RESET)\n  make [target]\n\n$(GREEN)Targets:$(RESET)\n"} /^[a-zA-Z0-9_\/%-]+: ## / {printf "  %-28s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
