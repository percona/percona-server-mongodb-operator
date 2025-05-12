#!/bin/bash

set -e
set -o xtrace

PBM_AGENT_LOG=/tmp/pbm-agent.log
MONGOD_LOG=/tmp/mongod.log
PHYSICAL_RESTORE_DIR=/data/db/pbm-restore-logs

function handle_sigterm() {
	echo "Received SIGTERM, cleaning up..."

	mkdir ${PHYSICAL_RESTORE_DIR}
	mv pbm.restore.log.* ${PBM_AGENT_LOG} ${MONGOD_LOG} ${PHYSICAL_RESTORE_DIR}/

	echo "Restore finished, you can find logs in ${PHYSICAL_RESTORE_DIR}"
	exit 0
}

trap 'handle_sigterm' 15

touch /opt/percona/restore-in-progress

/opt/percona/pbm-agent >${PBM_AGENT_LOG} 2>&1 &
pbm_pid=$!

/opt/percona/ps-entry.sh "$@" >${MONGOD_LOG} 2>&1 &
mongod_pid=$!

set +o xtrace
echo "Physical restore in progress... pbm-agent logs: ${PBM_AGENT_LOG} mongod logs: ${MONGOD_LOG}"
echo "Script PID: $$, pbm-agent PID: $pbm_pid, mongod PID: $mongod_pid"
while true; do
    echo "Still in progress at $(date)"
    sleep 10
done
