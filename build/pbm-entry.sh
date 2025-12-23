#!/bin/bash

if [ -d /etc/s3/certs-in ] && [ -n "$(ls -A /etc/s3/certs-in/*.crt 2>/dev/null)" ]; then
    cat /etc/s3/certs-in/*.crt > /etc/s3/certs/ca-bundle.crt
    chmod 0644 /etc/s3/certs/ca-bundle.crt
fi

if [[ -z ${PBM_AGENT_TLS_ENABLED} ]] || [[ ${PBM_AGENT_TLS_ENABLED} == "true" ]]; then
	MONGO_SSL_DIR=/etc/mongodb-ssl
	if [[ -e "${MONGO_SSL_DIR}/tls.crt" ]] && [[ -e "${MONGO_SSL_DIR}/tls.key" ]]; then
		cat "${MONGO_SSL_DIR}/tls.key" "${MONGO_SSL_DIR}/tls.crt" >/tmp/tls.pem
	fi
fi

exec "$@"
