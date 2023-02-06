#!/bin/bash
set -Eeuo pipefail

set -o xtrace

/opt/percona/pbm-agent 2>&1 > /tmp/pbm-agent.log &

/opt/percona/ps-entry.sh "$@"

echo "Physical restore in progress"

tail -n +1 -f /tmp/pbm-agent.log

sleep infinity