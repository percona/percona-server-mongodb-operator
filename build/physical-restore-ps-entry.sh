#!/bin/bash

set -Eeuo pipefail
set -o xtrace

log=/tmp/pbm-agent.log

touch /opt/percona/restore-in-progress

/opt/percona/pbm-agent 2>${log} &
/opt/percona/ps-entry.sh "$@"

echo "Physical restore in progress"
cat ${log}
sleep infinity
