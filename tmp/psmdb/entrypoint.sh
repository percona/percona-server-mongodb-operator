#!/bin/bash
set -e

# if command starts with an option, prepend mongod
if [ "${1:0:1}" = '-' ]; then
	set -- mongod "$@"
fi

# install keyFile to /data/db
install -m 0400 /etc/mongodb-secrets/mongodb-key /data/db/.mongod.key

exec "$@"
