# ==============================================================================
# Percona Server MongoDB Operator - OLM Bundle Generation
# ==============================================================================

.DEFAULT_GOAL := help
.SUFFIXES:
SHELL := /bin/bash

REGISTRY ?= docker.io
PROD_REPOSITORY ?= percona
DEV_REPOSITORY ?= perconalab
NAME ?= percona-server-mongodb-operator
BUNDLE_TYPES := community certified

SED := $(shell which gsed || which sed)
VERSION ?= $(shell git rev-parse --abbrev-ref HEAD | $(SED) -e 's^/^-^g; s^[.]^-^g;' | tr '[:upper:]' '[:lower:]')
CSV_VERSION ?= $(shell printf '%s\n' '$(VERSION)' | $(SED) -nE 's/.*([0-9]+)[.-]([0-9]+)[.-]([0-9]+).*/\1.\2.\3/p')
RELEASE_VERSIONS ?= ../../e2e-tests/release_versions

CONFIRM_PUSH ?= 1

BUNDLE_TYPE ?= community
BUNDLE_IMAGE_VERSION ?= $(CSV_VERSION)
BUNDLE_PLATFORM ?= linux/amd64
BUNDLE_PACKAGE_CHANNEL ?= stable
BUNDLE_SKIP_DIGEST_FAILURE ?= 0
BUNDLE_DEV_REPO ?= $(REGISTRY)/$(DEV_REPOSITORY)/$(NAME)
BUNDLE_PROD_REPO ?= $(REGISTRY)/$(PROD_REPOSITORY)/$(NAME)

CATALOG_BUNDLE_LIMIT ?= 2
CATALOG_PLATFORM ?= linux/amd64
CATALOG_NAMESPACE ?= openshift-marketplace
CATALOG_BUILD_PUSH ?= 1

REPO_ROOT := $(shell git rev-parse --show-toplevel)
KUSTOMIZE := $(REPO_ROOT)/bin/kustomize

OS_KERNEL ?= $(shell uname -s | tr '[:upper:]' '[:lower:]')
OS_MACHINE ?= $(shell uname -m | sed -e 's/^x86_64$$/amd64/' -e 's/^x86_/amd/' -e 's/^aarch64$$/arm64/')
SYSTEM := $(OS_KERNEL)-$(OS_MACHINE)
TOOLS_DIR := tools/$(SYSTEM)

CONTAINER ?= docker
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

bundle_distribution = $(if $(filter certified,$(1)),redhat,$(1))

export VERSION CSV_VERSION BUNDLE_IMAGE_VERSION BUNDLE_PACKAGE_CHANNEL
export PACKAGE_NAME_OVERRIDE CSV_NAME_OVERRIDE
export BUNDLE_PLATFORM CONFIRM_PUSH BUNDLE_SKIP_DIGEST_FAILURE

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
	@for cmd in curl gawk gcsplit yq yamllint envsubst '$(CONTAINER)'; do \
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
	curl -fsSL "https://github.com/jqlang/jq/releases/download/jq-$(JQ_VERSION)/jq-$(JQ_PLATFORM)-$(OS_MACHINE)" -o $@.tmp
	mv $@.tmp $@
	chmod +x $@

$(TOOLS_DIR)/operator-sdk: | $(TOOLS_DIR)
	curl -fsSL "https://github.com/operator-framework/operator-sdk/releases/download/$(OPERATOR_SDK_VERSION)/operator-sdk_$(OS_KERNEL)_$(OS_MACHINE)" -o $@.tmp
	mv $@.tmp $@
	chmod +x $@

$(TOOLS_DIR)/opm: | $(TOOLS_DIR)
	curl -fsSL "https://github.com/operator-framework/operator-registry/releases/download/$(OPM_VERSION)/$(OS_KERNEL)-$(OS_MACHINE)-opm" -o $@.tmp
	mv $@.tmp $@
	chmod +x $@

# ==============================================================================
# Generic Bundle Rules
# ==============================================================================
.PHONY: generate-bundle check-bundle-type

check-bundle-type:
	@if [[ "$(BUNDLE_TYPE)" != "community" && "$(BUNDLE_TYPE)" != "certified" ]]; then \
		echo "BUNDLE_TYPE must be community or certified"; \
		exit 1; \
	fi

generate-bundle: check-bundle-type tools $(KUSTOMIZE) check-version check-csv-version check-tools
	@echo "$(GREEN)Building $(BUNDLE_TYPE) bundle...$(RESET)"
	@set -euo pipefail; \
	distribution='$(BUNDLE_TYPE)'; \
	if [[ '$(BUNDLE_TYPE)' == 'certified' ]]; then \
		distribution='redhat'; \
	fi; \
	manager_kustomization='../../config/manager/namespace/kustomization.yaml'; \
	original_manager_kustomization="$$(mktemp)"; \
	cp "$${manager_kustomization}" "$${original_manager_kustomization}"; \
	trap 'cp "$${original_manager_kustomization}" "$${manager_kustomization}"; rm -f "$${original_manager_kustomization}"' EXIT; \
	./generate.sh "$${distribution}" &&	echo "$(GREEN)Bundle stored in installers/olm/bundles/$${distribution}$(RESET)"

# ==============================================================================
# Development Rules
# ==============================================================================

## Generate all development OLM bundles
.PHONY: bundles bundle $(BUNDLE_TYPES:%=bundle/%)
bundles: ## Generate all development OLM bundles
bundles: bundle
bundle: $(BUNDLE_TYPES:%=bundle/%)

bundle/community:
bundle/certified:

$(BUNDLE_TYPES:%=bundle/%): bundle/%: check-version check-csv-version
	@package='$(NAME)'; \
	if [[ "$*" == "certified" ]]; then \
		package='$(NAME)-certified'; \
	fi; \
	csv_name="$${package}.v$(CSV_VERSION)"; \
	$(MAKE) generate-bundle \
		BUNDLE_TYPE='$*' \
		PACKAGE_NAME_OVERRIDE="$${package}" \
		CSV_NAME_OVERRIDE="$${csv_name}"

## Build and push all development catalog images to DEV_REPOSITORY
.PHONY: catalog-build-push $(BUNDLE_TYPES:%=catalog-build-push/%)
catalog-build-push: ## Build and push all development catalog images
catalog-build-push: $(BUNDLE_TYPES:%=catalog-build-push/%)

catalog-build-push/community:
catalog-build-push/certified:

$(BUNDLE_TYPES:%=catalog-build-push/%): catalog-build-push/%: tools check-version check-csv-version
	@if [[ "$(CATALOG_BUILD_PUSH)" == "true" || "$(CATALOG_BUILD_PUSH)" == "1" ]]; then \
		BUNDLE_IMAGE_VERSION='$(BUNDLE_IMAGE_VERSION)' \
		CATALOG_BUNDLE_LIMIT='$(CATALOG_BUNDLE_LIMIT)' \
		CATALOG_PLATFORM='$(CATALOG_PLATFORM)' \
		CONTAINER='$(CONTAINER)' \
		CONFIRM_PUSH='$(CONFIRM_PUSH)' \
		./build-catalog.sh build \
			'$*' \
			'$(BUNDLE_DEV_REPO)' \
			'$(BUNDLE_DEV_REPO)'; \
	else \
		echo "[olm] Skipping catalog build and push"; \
	fi

## Build, push, and deploy all development catalog sources
.PHONY: deploy $(BUNDLE_TYPES:%=deploy/%)
deploy: ## Build, push, and deploy all development catalog sources
deploy: $(BUNDLE_TYPES:%=deploy/%)

deploy/community:
deploy/certified:

$(BUNDLE_TYPES:%=deploy/%): deploy/%: tools check-version check-csv-version
	$(MAKE) bundle/$*
	$(MAKE) build/$*
	$(MAKE) push/$*
	$(MAKE) catalog-build-push/$*
	CATALOG_NAMESPACE='$(CATALOG_NAMESPACE)' \
	NAME='$(NAME)' \
	bash ./build-catalog.sh apply '$*' '$(BUNDLE_DEV_REPO)'

# ==============================================================================
# Production Rules
# ==============================================================================

## Generate all production OLM bundles.
.PHONY: bundles-prod $(BUNDLE_TYPES:%=bundle-prod/%)
bundles-prod: ## Generate all production OLM bundles
bundles-prod: $(BUNDLE_TYPES:%=bundle-prod/%)

bundle-prod/community:
bundle-prod/certified:

$(BUNDLE_TYPES:%=bundle-prod/%): bundle-prod/%:
	$(MAKE) generate-bundle BUNDLE_TYPE='$*'

## Build production bundles, optionally build/push the catalog, and deploy it
.PHONY: deploy-prod $(BUNDLE_TYPES:%=deploy-prod/%)
deploy-prod: ## Build production bundles, catalog images, and deploy catalog sources
deploy-prod: $(BUNDLE_TYPES:%=deploy-prod/%)

deploy-prod/community:
deploy-prod/certified:

$(BUNDLE_TYPES:%=deploy-prod/%): deploy-prod/%: tools check-version check-csv-version
	$(MAKE) bundle-prod/$*
	$(MAKE) build-prod BUNDLE_TYPE='$*'
	$(MAKE) push-prod BUNDLE_TYPE='$*'
	@if [[ "$(CATALOG_BUILD_PUSH)" == "true" || "$(CATALOG_BUILD_PUSH)" == "1" ]]; then \
		BUNDLE_IMAGE_VERSION='$(BUNDLE_IMAGE_VERSION)' \
		CATALOG_BUNDLE_LIMIT='$(CATALOG_BUNDLE_LIMIT)' \
		CATALOG_PLATFORM='$(CATALOG_PLATFORM)' \
		CONTAINER='$(CONTAINER)' \
		CONFIRM_PUSH='$(CONFIRM_PUSH)' \
		bash ./build-catalog.sh build \
			'$*' \
			'$(BUNDLE_DEV_REPO)' \
			'$(BUNDLE_PROD_REPO)'; \
	else \
		echo "[olm] Skipping catalog build and push"; \
	fi
	CATALOG_NAMESPACE='$(CATALOG_NAMESPACE)' \
	NAME='$(NAME)' \
	bash ./build-catalog.sh apply '$*' '$(BUNDLE_DEV_REPO)'

# ==============================================================================
# Image Rules
# ==============================================================================

.PHONY: build build-prod $(BUNDLE_TYPES:%=build/%) $(BUNDLE_TYPES:%=build-prod/%)
build: ## Build bundle image locally (set BUNDLE_TYPE=community|certified)
build-prod: ## Build production bundle image locally (set BUNDLE_TYPE=community|certified)

$(BUNDLE_TYPES:%=build/%): build/%:
	$(MAKE) build BUNDLE_TYPE='$*'

$(BUNDLE_TYPES:%=build-prod/%): build-prod/%:
	$(MAKE) build-prod BUNDLE_TYPE='$*'

build: BUNDLE_REPO = $(BUNDLE_DEV_REPO)
build-prod: BUNDLE_REPO = $(BUNDLE_PROD_REPO)
build build-prod: check-bundle-type check-version check-csv-version
	BUNDLE_PLATFORM='$(BUNDLE_PLATFORM)' \
	./build-bundle.sh \
		build \
		'$(CONTAINER)' \
		'bundles/$(call bundle_distribution,$(BUNDLE_TYPE))' \
		'$(call bundle_distribution,$(BUNDLE_TYPE))' \
		'$(BUNDLE_IMAGE_VERSION)' \
		'$(BUNDLE_REPO)'


.PHONY: push push-prod $(BUNDLE_TYPES:%=push/%) $(BUNDLE_TYPES:%=push-prod/%)
push: ## Push existing bundle image (set BUNDLE_TYPE=community|certified)
push-prod: ## Push existing production bundle image (set BUNDLE_TYPE=community|certified)

$(BUNDLE_TYPES:%=push/%): push/%:
	$(MAKE) push BUNDLE_TYPE='$*'

$(BUNDLE_TYPES:%=push-prod/%): push-prod/%:
	$(MAKE) push-prod BUNDLE_TYPE='$*'

push: BUNDLE_REPO = $(BUNDLE_DEV_REPO)
push-prod: BUNDLE_REPO = $(BUNDLE_PROD_REPO)
push push-prod: check-bundle-type check-version check-csv-version
	./build-bundle.sh \
		push \
		'$(CONTAINER)' \
		'bundles/$(call bundle_distribution,$(BUNDLE_TYPE))' \
		'$(call bundle_distribution,$(BUNDLE_TYPE))' \
		'$(BUNDLE_IMAGE_VERSION)' \
		'$(BUNDLE_REPO)'

.PHONY: build-bundle-images
build-bundle-images: check-version check-csv-version
	$(MAKE) build BUNDLE_TYPE=community
	$(MAKE) build BUNDLE_TYPE=certified

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
	./validate-image.sh '$(CONTAINER)' 'bundles/$(call bundle_distribution,$*)'
	./validate-directory.sh 'bundles/$(call bundle_distribution,$*)'

validate-%-directory:
	./validate-directory.sh 'bundles/$(call bundle_distribution,$*)'

validate-%-image:
	./validate-image.sh '$(CONTAINER)' 'bundles/$(call bundle_distribution,$*)'

# ==============================================================================
# Utility Rules
# ==============================================================================

.PHONY: clean
clean: ## Remove generated files and downloaded tools
	rm -rf ./bundles ./catalogs ./projects ./tools

.PHONY: help
help: ## Show this help message
	@awk 'BEGIN {FS = ": ## "; printf "\n$(GREEN)Usage:$(RESET)\n  make [target]\n\n$(GREEN)Targets:$(RESET)\n"} /^[a-zA-Z0-9_\/%-]+: ## / {printf "  %-28s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
