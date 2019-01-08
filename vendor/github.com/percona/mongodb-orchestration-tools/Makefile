NAME?=mongodb-orchestration-tools
PLATFORM?=linux
BASE_DIR?=$(shell readlink -f $(CURDIR))
VERSION?=$(shell grep -oP '"\d+\.\d+\.\d+(-\S+)?"' version.go | tr -d \")
GIT_COMMIT?=$(shell git rev-parse HEAD)
GIT_BRANCH?=$(shell git rev-parse --abbrev-ref HEAD)
GITHUB_REPO?=percona/$(NAME)
RELEASE_CACHE_DIR?=/tmp/$(NAME)_release.cache

DCOS_DOCKERHUB_REPO?=perconalab/$(NAME)
DCOS_DOCKERHUB_TAG?=$(VERSION)-dcos
MONGOD_DOCKERHUB_REPO?=perconalab/$(NAME)
MONGOD_DOCKERHUB_TAG?=$(VERSION)-mongod
ifneq ($(GIT_BRANCH), master)
	DCOS_DOCKERHUB_TAG=$(VERSION)-dcos_$(GIT_BRANCH)
	MONGOD_DOCKERHUB_TAG=$(VERSION)-mongod_$(GIT_BRANCH)
endif

GO_VERSION?=1.11
GO_VERSION_MAJ_MIN=$(shell echo $(GO_VERSION) | cut -d. -f1-2)
GO_LDFLAGS?=-s -w
GO_LDFLAGS_FULL="${GO_LDFLAGS} -X main.GitCommit=${GIT_COMMIT} -X main.GitBranch=${GIT_BRANCH}"
GO_TEST_PATH?=./...
GOCACHE?=

ENABLE_MONGODB_TESTS?=false
TEST_PSMDB_VERSION?=3.6
TEST_RS_NAME?=rs
TEST_MONGODB_DOCKER_UID?=1001
TEST_ADMIN_USER?=admin
TEST_ADMIN_PASSWORD?=123456
TEST_PRIMARY_PORT?=65217
TEST_SECONDARY1_PORT?=65218
TEST_SECONDARY2_PORT?=65219

TEST_CODECOV?=false
TEST_GO_EXTRA?=
ifeq ($(TEST_CODECOV), true)
	TEST_GO_EXTRA=-coverprofile=cover.out -covermode=atomic
endif

all: bin/mongodb-executor bin/mongodb-healthcheck bin/dcos-mongodb-controller bin/dcos-mongodb-watchdog bin/k8s-mongodb-initiator

dcos: bin/dcos-mongodb-controller bin/dcos-mongodb-watchdog

$(GOPATH)/bin/glide:
	go get github.com/Masterminds/glide

vendor: $(GOPATH)/bin/glide glide.yaml glide.lock
	$(GOPATH)/bin/glide install --strip-vendor

bin/mongodb-healthcheck: vendor cmd/mongodb-healthcheck/main.go healthcheck/*.go internal/*.go internal/*/*.go internal/*/*/*.go pkg/*.go
	CGO_ENABLED=0 GOCACHE=$(GOCACHE) GOOS=$(PLATFORM) GOARCH=386 go build -ldflags=$(GO_LDFLAGS_FULL) -o bin/mongodb-healthcheck cmd/mongodb-healthcheck/main.go

bin/mongodb-executor: vendor cmd/mongodb-executor/main.go executor/*.go executor/*/*.go internal/*.go internal/*/*.go internal/*/*/*.go pkg/*.go
	CGO_ENABLED=0 GOCACHE=$(GOCACHE) GOOS=$(PLATFORM) GOARCH=386 go build -ldflags=$(GO_LDFLAGS_FULL) -o bin/mongodb-executor cmd/mongodb-executor/main.go

bin/dcos-mongodb-controller: vendor cmd/dcos-mongodb-controller/main.go controller/*.go controller/*/*.go controller/*/*/*.go internal/*.go internal/*/*.go internal/*/*/*.go pkg/*.go
	CGO_ENABLED=0 GOCACHE=$(GOCACHE) GOOS=$(PLATFORM) GOARCH=386 go build -ldflags=$(GO_LDFLAGS_FULL) -o bin/dcos-mongodb-controller cmd/dcos-mongodb-controller/main.go

bin/dcos-mongodb-watchdog: vendor cmd/dcos-mongodb-watchdog/main.go watchdog/*.go watchdog/*/*.go internal/*.go internal/*/*.go internal/*/*/*.go pkg/*.go pkg/*/*.go
	CGO_ENABLED=0 GOCACHE=$(GOCACHE) GOOS=$(PLATFORM) GOARCH=386 go build -ldflags=$(GO_LDFLAGS_FULL) -o bin/dcos-mongodb-watchdog cmd/dcos-mongodb-watchdog/main.go

bin/k8s-mongodb-initiator: vendor cmd/k8s-mongodb-initiator/main.go controller/*.go controller/replset/initiator*.go internal/*.go internal/*/*.go internal/*/*/*.go pkg/*.go pkg/*/*.go pkg/pod/k8s/*.go
	CGO_ENABLED=0 GOCACHE=$(GOCACHE) GOOS=$(PLATFORM) GOARCH=386 go build -ldflags=$(GO_LDFLAGS_FULL) -o bin/k8s-mongodb-initiator cmd/k8s-mongodb-initiator/main.go

test: vendor
	GOCACHE=$(GOCACHE) ENABLE_MONGODB_TESTS=$(ENABLE_MONGODB_TESTS) go test -v $(TEST_GO_EXTRA) $(GO_TEST_PATH)

test-race: vendor
	GOCACHE=$(GOCACHE) ENABLE_MONGODB_TESTS=$(ENABLE_MONGODB_TESTS) go test -v -race $(TEST_GO_EXTRA) $(GO_TEST_PATH)

test-full-prepare:
	TEST_RS_NAME=$(TEST_RS_NAME) \
	TEST_PSMDB_VERSION=$(TEST_PSMDB_VERSION) \
	TEST_ADMIN_USER=$(TEST_ADMIN_USER) \
	TEST_ADMIN_PASSWORD=$(TEST_ADMIN_PASSWORD) \
	TEST_PRIMARY_PORT=$(TEST_PRIMARY_PORT) \
	TEST_SECONDARY1_PORT=$(TEST_SECONDARY1_PORT) \
	TEST_SECONDARY2_PORT=$(TEST_SECONDARY2_PORT) \
	docker-compose up -d \
	--force-recreate \
	--remove-orphans
	docker/test/init-test-replset-wait.sh

test-full-clean:
	docker-compose down --volumes

test-full: vendor
	ENABLE_MONGODB_TESTS=true \
	TEST_RS_NAME=$(TEST_RS_NAME) \
	TEST_ADMIN_USER=$(TEST_ADMIN_USER) \
	TEST_ADMIN_PASSWORD=$(TEST_ADMIN_PASSWORD) \
	TEST_PRIMARY_PORT=$(TEST_PRIMARY_PORT) \
	TEST_SECONDARY1_PORT=$(TEST_SECONDARY1_PORT) \
	TEST_SECONDARY2_PORT=$(TEST_SECONDARY2_PORT) \
	GOCACHE=$(GOCACHE) go test -v -race $(TEST_GO_EXTRA) $(GO_TEST_PATH)
ifeq ($(TEST_CODECOV), true)
	curl -s https://codecov.io/bash | bash -s - -t ${CODECOV_TOKEN}
endif

release: clean
	docker build --build-arg GOLANG_DOCKERHUB_TAG=$(GO_VERSION_MAJ_MIN)-stretch -t $(NAME)_release -f docker/Dockerfile.release .
	docker run --rm --network=host \
	-v $(BASE_DIR)/bin:/go/src/github.com/$(GITHUB_REPO)/bin \
	-v $(RELEASE_CACHE_DIR)/glide:/root/.glide/cache \
	-e ENABLE_MONGODB_TESTS=$(ENABLE_MONGODB_TESTS) \
	-e TEST_CODECOV=$(TEST_CODECOV) \
	-e CODECOV_TOKEN=$(CODECOV_TOKEN) \
	-e TEST_RS_NAME=$(TEST_RS_NAME) \
	-e TEST_ADMIN_USER=$(TEST_ADMIN_USER) \
	-e TEST_ADMIN_PASSWORD=$(TEST_ADMIN_PASSWORD) \
	-e TEST_PRIMARY_PORT=$(TEST_PRIMARY_PORT) \
	-e TEST_SECONDARY1_PORT=$(TEST_SECONDARY1_PORT) \
	-e TEST_SECONDARY2_PORT=$(TEST_SECONDARY2_PORT) \
	-i $(NAME)_release

release-clean:
	rm -rf $(RELEASE_CACHE_DIR) 2>/dev/null
	docker rmi -f $(NAME)_release 2>/dev/null

docker-clean:
	docker rmi -f $(NAME)_release 2>/dev/null
	docker rmi -f $(NAME):$(DCOS_DOCKERHUB_TAG) 2>/dev/null
	docker rmi -f $(NAME):$(MONGOD_DOCKERHUB_TAG) 2>/dev/null

docker-dcos: release
	docker build -t $(NAME):$(DCOS_DOCKERHUB_TAG) -f docker/dcos/Dockerfile .
	docker run --rm -i $(NAME):$(DCOS_DOCKERHUB_TAG) dcos-mongodb-controller --version
	docker run --rm -i $(NAME):$(DCOS_DOCKERHUB_TAG) dcos-mongodb-watchdog --version

docker-dcos-push:
	docker tag $(NAME):$(DCOS_DOCKERHUB_TAG) $(DCOS_DOCKERHUB_REPO):$(DCOS_DOCKERHUB_TAG)
	docker push $(DCOS_DOCKERHUB_REPO):$(DCOS_DOCKERHUB_TAG)
ifeq ($(GIT_BRANCH), master)
	docker tag $(NAME):$(DCOS_DOCKERHUB_TAG) $(DCOS_DOCKERHUB_REPO):latest
	docker push $(DCOS_DOCKERHUB_REPO):latest
endif

docker-mongod: release
	docker build -t $(NAME):$(MONGOD_DOCKERHUB_TAG) -f docker/mongod/Dockerfile .
	docker run --rm -i $(NAME):$(MONGOD_DOCKERHUB_TAG) mongod --version
	docker run --rm -i $(NAME):$(MONGOD_DOCKERHUB_TAG) mongodb-executor --version
	docker run --rm -i $(NAME):$(MONGOD_DOCKERHUB_TAG) mongodb-healthcheck --version

docker-mongod-push:
	docker tag $(NAME):$(MONGOD_DOCKERHUB_TAG) $(MONGOD_DOCKERHUB_REPO):$(MONGOD_DOCKERHUB_TAG)
	docker push $(MONGOD_DOCKERHUB_REPO):$(MONGOD_DOCKERHUB_TAG)
ifeq ($(GIT_BRANCH), master)
	docker tag $(NAME):$(MONGOD_DOCKERHUB_TAG) $(MONGOD_DOCKERHUB_REPO):latest
	docker push $(MONGOD_DOCKERHUB_REPO):latest
endif

mocks:
	bash $(CURDIR)/genmocks.sh

clean:
	rm -rf bin cover.out vendor 2>/dev/null || true
