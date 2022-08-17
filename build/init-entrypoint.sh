#!/bin/bash

set -o errexit
set -o xtrace

install -o "$(id -u)" -g "$(id -g)" -m 0755 -D /ps-entry.sh /data/db/ps-entry.sh
install -o "$(id -u)" -g "$(id -g)" -m 0755 -D /mongodb-healthcheck /data/db/mongodb-healthcheck
install -o "$(id -u)" -g "$(id -g)" -m 0755 -D /pbm-entry.sh /opt/percona/pbm-entry.sh
