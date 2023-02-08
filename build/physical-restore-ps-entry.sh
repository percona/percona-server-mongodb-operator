#!/bin/bash
set -Eeuo pipefail

set -o xtrace

/opt/percona/pbm-agent &

/opt/percona/ps-entry.sh "$@"

echo "Physical restore in progress"

sleep infinity