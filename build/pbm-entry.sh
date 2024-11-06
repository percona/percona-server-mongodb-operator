#!/bin/bash

function urlencode {
	uri="$1"
	echo -n "$uri" | jq -s -R -r @uri
}

if [[ -z $POD_NAME ]]; then
	PBM_MONGODB_URI="mongodb://$(urlencode "$PBM_AGENT_MONGODB_USERNAME"):$(urlencode "$PBM_AGENT_MONGODB_PASSWORD")@localhost:${PBM_MONGODB_PORT}/?replicaSet=${PBM_MONGODB_REPLSET}"
	if [[ -z ${PBM_AGENT_TLS_ENABLED} ]] || [[ ${PBM_AGENT_TLS_ENABLED} == "true" ]]; then
		MONGO_SSL_DIR=/etc/mongodb-ssl
		if [[ -f "${MONGO_SSL_DIR}/tls.crt" ]] && [[ -f "${MONGO_SSL_DIR}/tls.key" ]]; then
			PBM_MONGODB_URI="${PBM_MONGODB_URI}&tls=true&tlsCertificateKeyFile=%2Ftmp%2Ftls.pem&tlsCAFile=${MONGO_SSL_DIR}%2Fca.crt&tlsInsecure=true"
			cat "${MONGO_SSL_DIR}/tls.key" "${MONGO_SSL_DIR}/tls.crt" >/tmp/tls.pem
		fi
	fi
else
	PBM_MONGODB_URI="mongodb://$(urlencode "$PBM_AGENT_MONGODB_USERNAME"):$(urlencode "$PBM_AGENT_MONGODB_PASSWORD")@$POD_NAME"
fi

export PBM_MONGODB_URI

exec "$@"
