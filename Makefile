NAME=percona-server-mongodb-operator
UID?=$(shell id -u)
GOCACHE?=off
GO_TEST_PATH?=./pkg/stub/...
GO_TEST_EXTRA?=
GO_LDFLAGS?=-w -s
GIT_COMMIT?=$(shell git rev-parse HEAD)
GIT_BRANCH?=$(shell git rev-parse --abbrev-ref HEAD|grep -oP "\w+$$")
GIT_REPO=github.com/Percona-Lab/$(NAME)
UPX_PATH?=$(shell whereis -b upx|awk '{print $$(NF-0)}')

VERSION?=$(shell awk '/Version =/{print $$3}' $(CURDIR)/version/version.go | tr -d \")
IMAGE="perconalab/$(NAME):$(VERSION)"
ifneq ($(GIT_BRANCH), master)
	IMAGE="perconalab/$(NAME):$(GIT_BRANCH)"
endif

all: build

test:
	GOCACHE=$(GOCACHE) go test -covermode=atomic -race -v $(GO_TEST_EXTRA) $(GO_TEST_PATH)

test-cover:
	GOCACHE=$(GOCACHE) go test -covermode=atomic -coverprofile=cover.out -race -v $(GO_TEST_EXTRA) $(GO_TEST_PATH)

pkg/apis/psmdb/v1alpha1/zz_generated.deepcopy.go: pkg/apis/psmdb/v1alpha1/doc.go pkg/apis/psmdb/v1alpha1/register.go pkg/apis/psmdb/v1alpha1/types.go       
	$(GOPATH)/bin/operator-sdk generate k8s

tmp/_output/bin/$(NAME): pkg/apis/psmdb/v1alpha1/zz_generated.deepcopy.go pkg/apis/psmdb/v1alpha1/*.go pkg/*/*.go version/version.go cmd/$(NAME)/main.go
	GO_LDFLAGS="$(GO_LDFLAGS)" GIT_COMMIT=$(GIT_COMMIT) GIT_BRANCH=$(GIT_BRANCH) /bin/bash $(CURDIR)/tmp/build/build.sh
	[ -x $(UPX_PATH) ] && $(UPX_PATH) -q tmp/_output/bin/$(NAME)

build: tmp/_output/bin/$(NAME)

docker: tmp/_output/bin/$(NAME)
	IMAGE=$(IMAGE) /bin/bash $(CURDIR)/tmp/build/docker_build.sh

docker-push:
	docker push $(IMAGE)

release:
	mkdir -p $(CURDIR)/tmp/_output
	docker build -t $(NAME)_build --build-arg UID=$(UID) -f $(CURDIR)/tmp/build/Dockerfile.build .
	docker run --rm -v $(CURDIR)/tmp/_output:/go/src/$(GIT_REPO)/tmp/_output -it $(NAME)_build
	docker rmi -f $(NAME)_build

clean:
	rm -rf cover.out tmp/_output 2>/dev/null || true
