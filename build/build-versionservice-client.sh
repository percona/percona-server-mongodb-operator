#!/bin/bash

set -o errexit
set -o xtrace

rm -rf versionserviceclient

swagger generate client -f vendor/github.com/Percona-Lab/percona-version-service/api/version.swagger.yaml -c versionserviceclient -m versionserviceclient/models
