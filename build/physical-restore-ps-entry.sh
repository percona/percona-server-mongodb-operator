#!/bin/bash

set -Eeuo pipefail
set -o xtrace

log=/tmp/pbm-agent.log

touch /opt/percona/restore-in-progress

/opt/percona/pbm-agent 1>&2 2>${log} &
/opt/percona/ps-entry.sh "$@" 1>&2 2>/tmp/mongod.log

echo "Physical restore in progress"
tail -n +1 -f ${log}
sleep infinity
