#!/usr/bin/env bash

GO_LDFLAGS="$GO_LDFLAGS"

set -o errexit
set -o nounset
set -o pipefail

if ! which go > /dev/null; then
	echo "golang needs to be installed"
	exit 1
fi

BIN_DIR="$(pwd)/tmp/_output/bin"
mkdir -p ${BIN_DIR}
PROJECT_NAME="percona-server-mongodb-operator"
REPO_PATH="github.com/Percona-Lab/percona-server-mongodb-operator"
BUILD_PATH="${REPO_PATH}/cmd/manager"
echo "building "${PROJECT_NAME}"..."
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "${GO_LDFLAGS} -X main.GitCommit=${GIT_COMMIT} -X main.GitBranch=${GIT_BRANCH}" -o ${BIN_DIR}/${PROJECT_NAME} $BUILD_PATH
