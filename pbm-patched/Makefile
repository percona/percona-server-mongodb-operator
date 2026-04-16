.PHONY: build-pbm build-agent build install install-pbm install-agent test completion completion-bash completion-zsh

GOOS?=$(shell go env GOOS)
GOMOD?=on
CGO_ENABLED?=0
GITCOMMIT?=$(shell git rev-parse HEAD 2>/dev/null)
GITBRANCH?=$(shell git rev-parse --abbrev-ref HEAD 2>/dev/null)
BUILDTIME?=$(shell TZ=UTC date "+%Y-%m-%d_%H:%M_UTC")
MONGO_TEST_VERSION?=8.0

define ENVS
	GO111MODULE=$(GOMOD) \
	GOOS=$(GOOS) \
	GOFLAGS='-buildvcs=false'
endef

define ENVS_STATIC
	$(ENVS) \
	CGO_ENABLED=$(CGO_ENABLED)
endef

BUILD_FLAGS=-mod=vendor -tags gssapi
versionpath?=github.com/percona/percona-backup-mongodb/pbm/version
LDFLAGS= -X $(versionpath).gitCommit=$(GITCOMMIT) -X $(versionpath).gitBranch=$(GITBRANCH) -X $(versionpath).buildTime=$(BUILDTIME) -X $(versionpath).version=$(VERSION)
LDFLAGS_STATIC=$(LDFLAGS) -extldflags "-static"
LDFLAGS_TESTS_BUILD=$(LDFLAGS)

default: install-pbm install-agent

test:
	MONGODB_VERSION=$(MONGO_TEST_VERSION) e2e-tests/run-all

build: build-pbm build-agent build-stest
build-all: build build-entrypoint completion
build-k8s: build-all
build-pbm:
	$(ENVS) go build -ldflags="$(LDFLAGS)" $(BUILD_FLAGS) -o ./bin/pbm ./cmd/pbm
build-agent:
	$(ENVS) go build -ldflags="$(LDFLAGS)" $(BUILD_FLAGS) -o ./bin/pbm-agent ./cmd/pbm-agent
build-stest:
	$(ENVS) go build -ldflags="$(LDFLAGS)" $(BUILD_FLAGS) -o ./bin/pbm-speed-test ./cmd/pbm-speed-test
build-entrypoint:
	$(ENVS) go build -ldflags="$(LDFLAGS)" $(BUILD_FLAGS) -o ./bin/pbm-agent-entrypoint ./cmd/pbm-agent-entrypoint

install: install-pbm install-agent install-stest
install-all: install install-entrypoint
install-k8s: install-all
install-pbm:
	$(ENVS) go install -ldflags="$(LDFLAGS)" $(BUILD_FLAGS) ./cmd/pbm
install-agent:
	$(ENVS) go install -ldflags="$(LDFLAGS)" $(BUILD_FLAGS) ./cmd/pbm-agent
install-stest:
	$(ENVS) go install -ldflags="$(LDFLAGS)" $(BUILD_FLAGS) ./cmd/pbm-speed-test
install-entrypoint:
	$(ENVS) go install -ldflags="$(LDFLAGS)" $(BUILD_FLAGS) ./cmd/pbm-agent-entrypoint

# RACE DETECTOR ON
build-race: build-pbm-race build-agent-race build-stest-race
build-pbm-race:
	$(ENVS) go build -race -ldflags="$(LDFLAGS)" $(BUILD_FLAGS) -o ./bin/pbm ./cmd/pbm
build-agent-race:
	$(ENVS) go build -race -ldflags="$(LDFLAGS)" $(BUILD_FLAGS) -o ./bin/pbm-agent ./cmd/pbm-agent
build-stest-race:
	$(ENVS) go build -race -ldflags="$(LDFLAGS)" $(BUILD_FLAGS) -o ./bin/pbm-speed-test ./cmd/pbm-speed-test

install-race: install-pbm-race install-agent-race install-stest-race
install-pbm-race:
	$(ENVS) go install -race -ldflags="$(LDFLAGS)" $(BUILD_FLAGS) ./cmd/pbm
install-agent-race:
	$(ENVS) go install -race -ldflags="$(LDFLAGS)" $(BUILD_FLAGS) ./cmd/pbm-agent
install-stest-race:
	$(ENVS) go install -race -ldflags="$(LDFLAGS)" $(BUILD_FLAGS) ./cmd/pbm-speed-test

# CI TESTS BUILD: RACE DETECTOR ON & PITR FRAME = 30sec
build-tests: build-pbm-tests build-agent-tests build-stest-tests
build-pbm-tests:
	$(ENVS) go build -race -ldflags="$(LDFLAGS_TESTS_BUILD)" $(BUILD_FLAGS) -o ./bin/pbm ./cmd/pbm
build-agent-tests:
	$(ENVS) go build -race -ldflags="$(LDFLAGS_TESTS_BUILD)" $(BUILD_FLAGS) -o ./bin/pbm-agent ./cmd/pbm-agent
build-stest-tests:
	$(ENVS) go build -race -ldflags="$(LDFLAGS_TESTS_BUILD)" $(BUILD_FLAGS) -o ./bin/pbm-speed-test ./cmd/pbm-speed-test

install-tests: install-pbm-tests install-agent-tests install-stest-tests
install-pbm-tests:
	$(ENVS) go install -race -ldflags="$(LDFLAGS_TESTS_BUILD)" $(BUILD_FLAGS) ./cmd/pbm
install-agent-tests:
	$(ENVS) go install -race -ldflags="$(LDFLAGS_TESTS_BUILD)" $(BUILD_FLAGS) ./cmd/pbm-agent
install-stest-tests:
	$(ENVS) go install -race -ldflags="$(LDFLAGS_TESTS_BUILD)" $(BUILD_FLAGS) ./cmd/pbm-speed-test

# STATIC BUILDS
build-static: build-pbm-static build-agent-static build-stest-static
build-static-all: build-static build-static-entrypoint
build-static-k8s: build-static-all
build-pbm-static:
	$(ENVS_STATIC) go build -ldflags="$(LDFLAGS_STATIC)" $(BUILD_FLAGS) -o ./bin/pbm ./cmd/pbm
build-agent-static:
	$(ENVS_STATIC) go build -ldflags="$(LDFLAGS_STATIC)" $(BUILD_FLAGS) -o ./bin/pbm-agent ./cmd/pbm-agent
build-stest-static:
	$(ENVS_STATIC) go build -ldflags="$(LDFLAGS_STATIC)" $(BUILD_FLAGS) -o ./bin/pbm-speed-test ./cmd/pbm-speed-test
build-static-entrypoint:
	$(ENVS_STATIC) go build -ldflags="$(LDFLAGS_STATIC)" $(BUILD_FLAGS) -o ./bin/pbm-agent-entrypoint ./cmd/pbm-agent-entrypoint

install-static: install-pbm-static install-agent-static install-stest-static
install-static-all: install-static install-static-entrypoint
install-static-k8s: install-static-all
install-pbm-static:
	$(ENVS_STATIC) go install -ldflags="$(LDFLAGS_STATIC)" $(BUILD_FLAGS) ./cmd/pbm
install-agent-static:
	$(ENVS_STATIC) go install -ldflags="$(LDFLAGS_STATIC)" $(BUILD_FLAGS) ./cmd/pbm-agent
install-stest-static:
	$(ENVS_STATIC) go install -ldflags="$(LDFLAGS_STATIC)" $(BUILD_FLAGS) ./cmd/pbm-speed-test
install-static-entrypoint:
	$(ENVS_STATIC) go install -ldflags="$(LDFLAGS_STATIC)" $(BUILD_FLAGS) ./cmd/pbm-agent-entrypoint

# BUILD WITH COVERAGE PROFILING
build-cover: build-pbm-cover build-agent-cover build-stest-cover
build-pbm-cover:
	$(ENVS) go build -cover -ldflags="$(LDFLAGS)" $(BUILD_FLAGS) -o ./bin/pbm ./cmd/pbm
build-agent-cover:
	$(ENVS) go build -cover -ldflags="$(LDFLAGS)" $(BUILD_FLAGS) -o ./bin/pbm-agent ./cmd/pbm-agent
build-stest-cover:
	$(ENVS) go build -cover -ldflags="$(LDFLAGS)" $(BUILD_FLAGS) -o ./bin/pbm-speed-test ./cmd/pbm-speed-test

install-cover: install-pbm-cover install-agent-cover install-stest-cover
install-pbm-cover:
	$(ENVS) go install -cover -ldflags="$(LDFLAGS)" $(BUILD_FLAGS) ./cmd/pbm
install-agent-cover:
	$(ENVS) go install -cover -ldflags="$(LDFLAGS)" $(BUILD_FLAGS) ./cmd/pbm-agent
install-stest-cover:
	$(ENVS) go install -cover -ldflags="$(LDFLAGS)" $(BUILD_FLAGS) ./cmd/pbm-speed-test

# COMPLETION SCRIPTS
completion: completion-bash completion-zsh

completion-dir-bash:
	mkdir -p ./bin/completions/bash

completion-dir-zsh:
	mkdir -p ./bin/completions/zsh

completion-bash: completion-dir-bash completion-bash-pbm completion-bash-agent completion-bash-stest
completion-bash-pbm:
	./bin/pbm completion bash > ./bin/completions/bash/pbm
completion-bash-agent:
	./bin/pbm-agent completion bash > ./bin/completions/bash/pbm-agent
completion-bash-stest:
	./bin/pbm-speed-test completion bash > ./bin/completions/bash/pbm-speed-test

completion-zsh: completion-dir-zsh completion-zsh-pbm completion-zsh-agent completion-zsh-stest
completion-zsh-pbm:
	./bin/pbm completion zsh > ./bin/completions/zsh/_pbm
completion-zsh-agent:
	./bin/pbm-agent completion zsh > ./bin/completions/zsh/_pbm-agent
completion-zsh-stest:
	./bin/pbm-speed-test completion zsh > ./bin/completions/zsh/_pbm-speed-test
