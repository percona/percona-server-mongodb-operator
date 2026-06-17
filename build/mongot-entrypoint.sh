#!/bin/bash

set -o errexit

MONGO_SSL_DIR=${MONGO_SSL_DIR:-/etc/mongodb-ssl}

if [ -f "${MONGO_SSL_DIR}/tls.key" ] && [ -f "${MONGO_SSL_DIR}/tls.crt" ]; then
	cat "${MONGO_SSL_DIR}/tls.key" "${MONGO_SSL_DIR}/tls.crt" >/tmp/tls.pem
fi

exec "$@"
