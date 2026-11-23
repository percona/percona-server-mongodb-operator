#!/bin/bash

if [ -d /etc/s3/certs-in ] && [ -n "$(ls -A /etc/s3/certs-in/*.crt 2>/dev/null)" ]; then
    cat /etc/s3/certs-in/*.crt > /etc/s3/certs/ca-bundle.crt
    chmod 0644 /etc/s3/certs/ca-bundle.crt
fi

if [[ -z ${PBM_AGENT_TLS_ENABLED} ]] || [[ ${PBM_AGENT_TLS_ENABLED} == "true" ]]; then
	MONGO_SSL_DIR=/etc/mongodb-ssl
	PEM_FILE=/tmp/tls.pem
	if [[ -n ${PBM_AGENT_TLS_DIR} ]]; then
		# /tmp can be shared with mongod, which writes its own certificate to /tmp/tls.pem
		MONGO_SSL_DIR=${PBM_AGENT_TLS_DIR}
		PEM_FILE=/tmp/pbm-tls.pem
	fi
	if [[ -e "${MONGO_SSL_DIR}/tls.crt" ]] && [[ -e "${MONGO_SSL_DIR}/tls.key" ]]; then
		cat "${MONGO_SSL_DIR}/tls.key" "${MONGO_SSL_DIR}/tls.crt" >"${PEM_FILE}"
	fi
fi

# shellcheck disable=SC1091
test -e /opt/percona/pbm-hookscript/hook.sh && source /opt/percona/pbm-hookscript/hook.sh

exec "$@"
