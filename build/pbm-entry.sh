#!/bin/bash

set -o xtrace

PBM_MONGODB_URI="mongodb://${PBM_AGENT_MONGODB_USERNAME}:${PBM_AGENT_MONGODB_PASSWORD}@localhost:${PBM_MONGODB_PORT}/?replicaSet=${PBM_MONGODB_REPLSET}"

PBM_MONGO_OPTS=""
MONGO_SSL_DIR=/etc/mongodb-ssl
if [[ -f "${MONGO_SSL_DIR}/tls.crt" ]] && [[ -f "${MONGO_SSL_DIR}/tls.key" ]]; then
    cat "${MONGO_SSL_DIR}/tls.key" "${MONGO_SSL_DIR}/tls.crt" >/tmp/tls.pem
    PBM_MONGO_OPTS="--tls --tlsAllowInvalidHostnames --tlsCertificateKeyFile=/tmp/tls.pem --tlsCAFile=${MONGO_SSL_DIR}/ca.crt"
    PBM_MONGODB_URI="${PBM_MONGODB_URI}&tls=true&tlsCertificateKeyFile=%2Ftmp%2Ftls.pem&tlsCAFile=${MONGO_SSL_DIR}%2Fca.crt&tlsAllowInvalidCertificates=true"
fi

export PBM_MONGODB_URI

if [ "${1:0:9}" = "pbm-agent" ]; then
	OUT="$(mktemp)"
	OUT_CFG="$(mktemp)"
	timeout=5
	for i in {1..10}; do
		if [ "${SHARDED}" ]; then
			echo "waiting for sharded scluster"

			# check in case if shard has role 'shardsrv'
			set +o xtrace
			mongo ${PBM_MONGO_OPTS} "${PBM_MONGODB_URI}" --eval="db.isMaster().\$configServerState.opTime.ts" --quiet | tee "$OUT"
			set -o xtrace
			exit_status=$?

			# check in case if shard has role 'configsrv'
			set +o xtrace
			mongo ${PBM_MONGO_OPTS} "${PBM_MONGODB_URI}" --eval="db.isMaster().configsvr" --quiet | tail -n 1 | tee "$OUT_CFG"
			set -o xtrace
			exit_status_cfg=$?

			ts=$(grep -E '^Timestamp\([0-9]+, [0-9]+\)$' "$OUT")
			isCfg=$(grep -E '^2$' "$OUT_CFG")

			if [[ ${exit_status} == 0 && ${ts} ]] || [[ ${exit_status_cfg} == 0 && ${isCfg} ]]; then
				break
			else
				sleep "$((timeout * i))"
			fi
		else
			set +o xtrace
			mongo ${PBM_MONGO_OPTS} "${PBM_MONGODB_URI}" --eval="(db.isMaster().hosts).length" --quiet | tee "$OUT"
			set -o xtrace
			exit_status=$?
			rs_size=$(grep -E '^([0-9]+)$' "$OUT")
			if [[ ${exit_status} == 0 ]] && [[ $rs_size -ge 1 ]]; then
				break
			else
				sleep "$((timeout * i))"
			fi
		fi
	done

	rm "$OUT"
fi

exec "$@"
