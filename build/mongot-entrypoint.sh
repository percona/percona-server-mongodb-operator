#!/bin/bash

set -o errexit
set -o xtrace

MONGO_SSL_DIR=${MONGO_SSL_DIR:-/etc/mongodb-ssl}

if [[ -f "${MONGO_SSL_DIR}/tls.key" ]] && [[ -f "${MONGO_SSL_DIR}/tls.crt" ]]; then
	cat "${MONGO_SSL_DIR}/tls.key" "${MONGO_SSL_DIR}/tls.crt" >/tmp/tls.pem
fi

# mongot requires passwordFile only be readable by the owner
# but K8s doesn't allow us to set ownership for the mounted secret
if [[ -f /etc/users-secret/MONGODB_SEARCH_PASSWORD ]]; then
	cp /etc/users-secret/MONGODB_SEARCH_PASSWORD /tmp/MONGODB_SEARCH_PASSWORD
	chmod 400 /tmp/MONGODB_SEARCH_PASSWORD
fi

exec "$@"
