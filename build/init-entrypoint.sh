#!/bin/bash

set -o errexit
set -o xtrace

install -o "$(id -u)" -g "$(id -g)" -m 0755 -D /ps-entry.sh /opt/percona/ps-entry.sh
install -o "$(id -u)" -g "$(id -g)" -m 0755 -D /physical-restore-ps-entry.sh /opt/percona/physical-restore-ps-entry.sh
install -o "$(id -u)" -g "$(id -g)" -m 0755 -D /mongodb-healthcheck /opt/percona/mongodb-healthcheck
install -o "$(id -u)" -g "$(id -g)" -m 0755 -D /pbm-entry.sh /opt/percona/pbm-entry.sh
cp -a /logcollector /opt/percona/
chown -R "$(id -u)":"$(id -g)" /opt/percona/logcollector
chmod -R 0755 /opt/percona/logcollector
