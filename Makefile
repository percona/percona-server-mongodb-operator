GOCACHE?=off
GO_TEST_PATH?=./...
GO_TEST_EXTRA?=
GO_BUILD_LDFLAGS?=-w -s

VERSION?=$(shell awk '/Version =/{print $$3}' $(CURDIR)/version/version.go | tr -d \")
IMAGE?=percona/percona-server-mongodb-operator:$(VERSION)

all: build

test:
	GOCACHE=$(GOCACHE) go test -race -v $(GO_TEST_EXTRA) $(GO_TEST_PATH)

test-cover:
	GOCACHE=$(GOCACHE) go test -covermode=atomic -coverprofile=cover.out -race -v $(GO_TEST_EXTRA) $(GO_TEST_PATH)

pkg/apis/cache/v1alpha1/zz_generated.deepcopy.go: pkg/apis/cache/v1alpha1/doc.go pkg/apis/cache/v1alpha1/register.go pkg/apis/cache/v1alpha1/types.go       
	$(GOPATH)/bin/operator-sdk generate k8s

tmp/_output/bin/percona-server-mongodb-operator: pkg/apis/cache/v1alpha1/zz_generated.deepcopy.go pkg/stub/handler.go version/version.go cmd/percona-server-mongodb-operator/main.go
	/bin/bash $(CURDIR)/tmp/build/build.sh

build: tmp/_output/bin/percona-server-mongodb-operator

docker:
	IMAGE=$(IMAGE) /bin/bash $(CURDIR)/tmp/build/docker_build.sh

clean:
	rm -f cover.out 2>/dev/null || true
