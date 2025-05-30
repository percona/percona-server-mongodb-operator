#!/bin/sh
set -e
set -o xtrace

export PATH="$PATH":/opt/fluent-bit/bin

if [ "$1" = 'logrotate' ]; then
	if [[ $EUID != 1001 ]]; then
		# logrotate requires UID in /etc/passwd
		sed -e "s^x:1001:^x:$EUID:^" /etc/passwd >/tmp/passwd
		cat /tmp/passwd >/etc/passwd
		rm -rf /tmp/passwd
	fi
	exec go-cron "0 0 * * *" sh -c "logrotate -s /data/db/logs/logrotate.status /opt/percona/logcollector/logrotate/logrotate.conf;"
else
	if [ "$1" = 'fluent-bit' ]; then
		fluentbit_opt+='-c /opt/percona/logcollector/fluentbit/fluentbit.conf'
	fi

	exec "$@" $fluentbit_opt
fi
