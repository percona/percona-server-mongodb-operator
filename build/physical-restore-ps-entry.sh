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

if [[ -z ${PBM_AGENT_TLS_ENABLED} ]] || [[ ${PBM_AGENT_TLS_ENABLED} == "true" ]]; then
	MONGO_SSL_DIR=/etc/mongodb-ssl
	if [[ -e "${MONGO_SSL_DIR}/tls.crt" ]] && [[ -e "${MONGO_SSL_DIR}/tls.key" ]]; then
		cat "${MONGO_SSL_DIR}/tls.key" "${MONGO_SSL_DIR}/tls.crt" >/tmp/tls.pem
	fi
fi

PATH=${PATH}:/opt/percona /opt/percona/pbm-agent-entrypoint >${PBM_AGENT_LOG} 2>&1 &
pbm_pid=$!

/opt/percona/ps-entry.sh "$@" >${MONGOD_LOG} 2>&1 &
mongod_pid=$!

set +o xtrace
echo "Physical restore in progress... pbm-agent logs: ${PBM_AGENT_LOG} mongod logs: ${MONGOD_LOG}"
echo "Script PID: $$, pbm-agent PID: $pbm_pid, mongod PID: $mongod_pid"
while true; do
    echo "Still in progress at $(date)"
    sleep 120
done
