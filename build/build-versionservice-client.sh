#!/bin/bash

set -o errexit
set -o xtrace

if ! swagger version &>/dev/null; then
	go install github.com/go-swagger/go-swagger/cmd/swagger@v0.24.0
fi

rm -f version/version.swagger.yaml
curl https://raw.githubusercontent.com/Percona-Lab/percona-version-service/main/api/version.swagger.yaml --output version/version.swagger.yaml
rm -rf pkg/versionservice/client

swagger generate client -f version/version.swagger.yaml -c pkg/versionservice/client -m pkg/versionservice/client/models
