GOCACHE?=off
GO_TEST_PATH?=./pkg/...
GO_TEST_EXTRA?=
GO_LDFLAGS?=-w -s
GIT_COMMIT?=$(shell git rev-parse HEAD)
GIT_BRANCH?=$(shell git rev-parse --abbrev-ref HEAD)
UPX_PATH?=$(shell whereis -b upx|awk '{print $$(NF-0)}')

VERSION?=$(shell awk '/Version =/{print $$3}' $(CURDIR)/version/version.go | tr -d \")
IMAGE?=perconalab/percona-server-mongodb-operator:$(VERSION)

all: build

test:
	GOCACHE=$(GOCACHE) go test -covermode=atomic -race -v $(GO_TEST_EXTRA) $(GO_TEST_PATH)

test-cover:
	GOCACHE=$(GOCACHE) go test -covermode=atomic -coverprofile=cover.out -race -v $(GO_TEST_EXTRA) $(GO_TEST_PATH)

pkg/apis/psmdb/v1alpha1/zz_generated.deepcopy.go: pkg/apis/psmdb/v1alpha1/doc.go pkg/apis/psmdb/v1alpha1/register.go pkg/apis/psmdb/v1alpha1/types.go       
	$(GOPATH)/bin/operator-sdk generate k8s

tmp/_output/bin/percona-server-mongodb-operator: pkg/apis/psmdb/v1alpha1/zz_generated.deepcopy.go pkg/apis/psmdb/v1alpha1/*.go pkg/stub/*.go version/version.go cmd/percona-server-mongodb-operator/main.go
	GO_LDFLAGS="$(GO_LDFLAGS)" GIT_COMMIT=$(GIT_COMMIT) GIT_BRANCH=$(GIT_BRANCH) /bin/bash $(CURDIR)/tmp/build/build.sh
	[ -x $(UPX_PATH) ] && $(UPX_PATH) -q tmp/_output/bin/percona-server-mongodb-operator

build: tmp/_output/bin/percona-server-mongodb-operator

docker: tmp/_output/bin/percona-server-mongodb-operator
	IMAGE=$(IMAGE) /bin/bash $(CURDIR)/tmp/build/docker_build.sh

clean:
	rm -rf cover.out tmp/_output 2>/dev/null || true
