#!/bin/bash

if [[ -z ${PBM_AGENT_TLS_ENABLED} ]] || [[ ${PBM_AGENT_TLS_ENABLED} == "true" ]]; then
	MONGO_SSL_DIR=/etc/mongodb-ssl
	if [[ -e "${MONGO_SSL_DIR}/tls.crt" ]] && [[ -e "${MONGO_SSL_DIR}/tls.key" ]]; then
		cat "${MONGO_SSL_DIR}/tls.key" "${MONGO_SSL_DIR}/tls.crt" >/tmp/tls.pem
	fi
fi

exec "$@"
