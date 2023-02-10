#!/bin/bash

set -o xtrace

PBM_MONGODB_URI="mongodb://${PBM_AGENT_MONGODB_USERNAME}:${PBM_AGENT_MONGODB_PASSWORD}@localhost:${PBM_MONGODB_PORT}/?replicaSet=${PBM_MONGODB_REPLSET}"

MONGO_SSL_DIR=/etc/mongodb-ssl
if [[ -f "${MONGO_SSL_DIR}/tls.crt" ]] && [[ -f "${MONGO_SSL_DIR}/tls.key" ]]; then
	cat "${MONGO_SSL_DIR}/tls.key" "${MONGO_SSL_DIR}/tls.crt" >/tmp/tls.pem
	PBM_MONGODB_URI="${PBM_MONGODB_URI}&tls=true&tlsCertificateKeyFile=%2Ftmp%2Ftls.pem&tlsCAFile=${MONGO_SSL_DIR}%2Fca.crt&tlsInsecure=true"
fi

export PBM_MONGODB_URI

exec "$@"
