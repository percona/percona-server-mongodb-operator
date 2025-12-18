#!/bin/sh
set -e
set -o xtrace

export PATH="$PATH":/opt/fluent-bit/bin

LOGROTATE_SCHEDULE="${LOGROTATE_SCHEDULE:-0 0 * * *}"

LOGROTATE_CONF_FILE="/opt/percona/logcollector/logrotate/logrotate.conf"
if [ -f /opt/percona/logcollector/logrotate/conf.d/mongodb.conf ]; then
	LOGROTATE_CONF_FILE="/opt/percona/logcollector/logrotate/conf.d/mongodb.conf"
fi

if [ "$1" = 'logrotate' ]; then
	if [[ $EUID != 1001 ]]; then
		# logrotate requires UID in /etc/passwd
		sed -e "s^x:1001:^x:$EUID:^" /etc/passwd >/tmp/passwd
		cat /tmp/passwd >/etc/passwd
		rm -rf /tmp/passwd
	fi
	echo "Running logrotate with schedule $LOGROTATE_SCHEDULE and config file $LOGROTATE_CONF_FILE"
	exec go-cron "$LOGROTATE_SCHEDULE" sh -c "logrotate -s /data/db/logs/logrotate.status $LOGROTATE_CONF_FILE;"
else
	if [ "$1" = 'fluent-bit' ]; then
		fluentbit_opt+='-c /opt/percona/logcollector/fluentbit/fluentbit.conf'
	fi

	exec "$@" $fluentbit_opt
fi
