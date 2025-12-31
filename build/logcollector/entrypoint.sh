#!/bin/bash
set -e
set -o xtrace

export PATH="$PATH":/opt/fluent-bit/bin

LOGROTATE_SCHEDULE="${LOGROTATE_SCHEDULE:-0 0 * * *}"

LOGROTATE_CONF_FILE="/opt/percona/logcollector/logrotate/logrotate.conf"
if [ -f /opt/percona/logcollector/logrotate/conf.d/mongodb.conf ]; then
	LOGROTATE_CONF_FILE="/opt/percona/logcollector/logrotate/conf.d/mongodb.conf"
	logrotate -d $LOGROTATE_CONF_FILE || EC=$?
	if [ -n "$EC" ]; then
		echo "Logrotate configuration is invalid, fallback to default configuration"
		LOGROTATE_CONF_FILE="/opt/percona/logcollector/logrotate/logrotate.conf"
	fi
fi

LOGROTATE_CUSTOM_CONF_FILE=""
if [ -f /opt/percona/logcollector/logrotate/conf.d/custom.conf ]; then
	LOGROTATE_CUSTOM_CONF_FILE="/opt/percona/logcollector/logrotate/conf.d/custom.conf"
	logrotate -d $LOGROTATE_CUSTOM_CONF_FILE || EC=$?
	if [ -n "$EC" ]; then
		echo "Logrotate additional configuration is invalid, it will be ignored"
		LOGROTATE_CUSTOM_CONF_FILE=""
	fi
fi

if [ "$1" = 'logrotate' ]; then
	if [[ $EUID != 1001 ]]; then
		# logrotate requires UID in /etc/passwd
		sed -e "s^x:1001:^x:$EUID:^" /etc/passwd >/tmp/passwd
		cat /tmp/passwd >/etc/passwd
		rm -rf /tmp/passwd
	fi
	exec go-cron "$LOGROTATE_SCHEDULE" sh -c "logrotate -s /data/db/logs/logrotate.status $LOGROTATE_CONF_FILE $LOGROTATE_CUSTOM_CONF_FILE;"
else
	fluentbit_opt=()
	if [ "$1" = 'fluent-bit' ]; then
		fluentbit_opt=(-c /opt/percona/logcollector/fluentbit/fluentbit.conf)
	fi

	exec "$@" "${fluentbit_opt[@]}"
fi
