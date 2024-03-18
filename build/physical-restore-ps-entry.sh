#!/bin/bash

set -Eeuo pipefail
set -o xtrace

PBM_LOG=/tmp/pbm-agent.log

/opt/percona/pbm-agent 2>${PBM_LOG} &
/opt/percona/ps-entry.sh "$@"

echo "Physical restore in progress"
cat ${PBM_LOG}
sleep infinity
