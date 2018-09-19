GOCACHE?=off
GO_TEST_PATH?=./...
GO_TEST_EXTRA?=
GO_BUILD_LDFLAGS?=-w -s

all: percona-server-mongodb-operator

test:
	go test -covermode=atomic -race -v $(GO_TEST_EXTRA) $(GO_TEST_PATH)

percona-server-mongodb-operator: cmd/percona-server-mongodb-operator/main.go pkg/apis/cache/v1alpha1/*.go pkg/stub/*.go version/version.go
	go build -ldflags="$(GO_BUILD_LDFLAGS)" -o percona-server-mongodb-operator cmd/percona-server-mongodb-operator/main.go

clean:
	rm -f percona-server-mongodb-operator 2>/dev/null || true
