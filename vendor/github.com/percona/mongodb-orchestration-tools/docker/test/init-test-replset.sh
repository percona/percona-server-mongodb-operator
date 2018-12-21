#!/bin/bash

set -x

tries=1
max_tries=10
sleep_secs=5

cp /rootCA.crt /tmp/rootCA.crt
cp /client.pem /tmp/client.pem
chmod 400 /tmp/rootCA.crt /tmp/client.pem

mongo_flags="--quiet --ssl --sslCAFile=/tmp/rootCA.crt --sslPEMKeyFile=/tmp/client.pem"

sleep $sleep_secs
while [ $tries -lt $max_tries ]; do
	/usr/bin/mongo $mongo_flags \
		--port ${TEST_PRIMARY_PORT} \
		--eval 'rs.initiate({
			_id: "'${TEST_RS_NAME}'",
			version: 1,
			members: [
				{ _id: 0, host: "127.0.0.1:'${TEST_PRIMARY_PORT}'", priority: 2 },
				{ _id: 1, host: "127.0.0.1:'${TEST_SECONDARY1_PORT}'", priority: 1 },
				{ _id: 2, host: "127.0.0.1:'${TEST_SECONDARY2_PORT}'", priority: 1 }
			]})'
	[ $? == 0 ] && break
	echo "# INFO: retrying rs.initiate() in $sleep_secs secs (try $tries/$max_tries)"
	sleep $sleep_secs
	tries=$(($tries + 1))
done
if [ $tries -ge $max_tries ]; then
	echo "# ERROR: reached max tries $max_tries, exiting"
	exit 1
fi

sleep $sleep_secs
tries=1
while [ $tries -lt $max_tries ]; do
	ISMASTER=$(/usr/bin/mongo $mongo_flags \
		--port ${TEST_PRIMARY_PORT} \
		--eval 'printjson(db.isMaster().ismaster)' 2>/dev/null)
	[ "$ISMASTER" == "true" ] && break
	echo "# INFO: retrying db.isMaster() check in $sleep_secs secs (try $tries/$max_tries)"
	sleep $sleep_secs
	tries=$(($tries + 1))
done
if [ $tries -ge $max_tries ]; then
	echo "# ERROR: reached max tries $max_tries, exiting"
	exit 1
fi

tries=1
while [ $tries -lt $max_tries ]; do
	/usr/bin/mongo $mongo_flags \
		--port ${TEST_PRIMARY_PORT} \
		--eval 'db.getSiblingDB("admin").createUser({
				user: "'${TEST_ADMIN_USER}'",
				pwd: "'${TEST_ADMIN_PASSWORD}'",
				roles: [
					{ db: "admin", role: "root" },
				]
			})'
	[ $? == 0 ] && break
	echo "# INFO: retrying db.createUser() in $sleep_secs secs (try $tries/$max_tries)"
	sleep $sleep_secs
	tries=$(($tries + 1))
done
if [ $tries -ge $max_tries ]; then
	echo "# ERROR: reached max tries $max_tries, exiting"
	exit 1
fi

echo "# INFO: done init"
