#!/bin/bash
set -e

export PATH="$PATH:/opt/fluent-bit/bin"

LOGROTATE_SCHEDULE="${LOGROTATE_SCHEDULE:-0 0 * * *}"

is_logrotate_config_invalid() {
	local config_file="$1"
	if [ -z "$config_file" ] || [ ! -f "$config_file" ]; then
		return 1
	fi
	# Specifying -d runs in debug mode, so even in case of errors, it will exit with 0.
	# We need to check the output for "error" but skip those lines that are related to the missing logrotate.status file.
	# Filter out logrotate.status lines first, then check for remaining errors
	(
		set +e
		logrotate -d "$config_file" 2>&1 | grep -v "logrotate.status" | grep -qi "error"
	)
	return $?
}

run_logrotate() {
	local logrotate_status_file="/data/db/logs/logrotate.status"
	local logrotate_conf_file="/opt/percona/logcollector/logrotate/logrotate.conf"
	local logrotate_custom_conf_file=""

	# Check if mongodb.conf exists and validate it
	if [ -f /opt/percona/logcollector/logrotate/conf.d/mongodb.conf ]; then
		logrotate_conf_file="/opt/percona/logcollector/logrotate/conf.d/mongodb.conf"
		if is_logrotate_config_invalid "$logrotate_conf_file"; then
			echo "Logrotate configuration is invalid, fallback to default configuration"
			logrotate_conf_file="/opt/percona/logcollector/logrotate/logrotate.conf"
		fi
	fi

	# Check if custom.conf exists and validate it
	if [ -f /opt/percona/logcollector/logrotate/conf.d/custom.conf ]; then
		logrotate_custom_conf_file="/opt/percona/logcollector/logrotate/conf.d/custom.conf"
		if is_logrotate_config_invalid "$logrotate_custom_conf_file"; then
			echo "Logrotate additional configuration is invalid, it will be ignored"
			logrotate_custom_conf_file=""
		fi
	fi
	
	# Ensure logrotate can run with current UID
	if [[ $EUID != 1001 ]]; then
		# logrotate requires UID in /etc/passwd
		sed -e "s^x:1001:^x:$EUID:^" /etc/passwd >/tmp/passwd
		cat /tmp/passwd >/etc/passwd
		rm -rf /tmp/passwd
	fi

	local logrotate_cmd="logrotate -s $logrotate_status_file \"$logrotate_conf_file\""
	if [ -n "$logrotate_custom_conf_file" ]; then
		logrotate_cmd="$logrotate_cmd \"$logrotate_custom_conf_file\""
	fi

	set -o xtrace
	exec go-cron "$LOGROTATE_SCHEDULE" sh -c "$logrotate_cmd"
}

run_fluentbit() {
	local fluentbit_opt=()
	if [ "$1" = 'fluent-bit' ]; then
		fluentbit_opt=(-c /opt/percona/logcollector/fluentbit/fluentbit.conf)
	fi

	set -o xtrace
	exec "$@" "${fluentbit_opt[@]}"
}

if [ "$1" = 'logrotate' ]; then
	run_logrotate
else
	run_fluentbit "$@"
fi