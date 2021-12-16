#!/bin/bash
set -Eeuo pipefail

if [ "${1:0:1}" = '-' ]; then
	set -- mongod "$@"
fi

originalArgOne="$1"

# allow the container to be started with `--user`
# all mongo* commands should be dropped to the correct user
if [[ $originalArgOne == mongo* ]] && [ "$(id -u)" = '0' ]; then
	if [ "$originalArgOne" = 'mongod' ]; then
		find /data/configdb /data/db \! -user mongodb -exec chown mongodb '{}' +
	fi

	# make sure we can write to stdout and stderr as "mongodb"
	# (for our "initdb" code later; see "--logpath" below)
	chown --dereference mongodb "/proc/$$/fd/1" "/proc/$$/fd/2" || :
	# ignore errors thanks to https://github.com/docker-library/mongo/issues/149

	exec gosu mongodb:1001 "$BASH_SOURCE" "$@"
fi

# you should use numactl to start your mongod instances, including the config servers, mongos instances, and any clients.
# https://docs.mongodb.com/manual/administration/production-notes/#configuring-numa-on-linux
if [[ $originalArgOne == mongo* ]]; then
	numa='numactl --interleave=all'
	if $numa true &>/dev/null; then
		set -- "$numa" "$@"
	fi
fi

# usage: file_env VAR [DEFAULT]
#    ie: file_env 'XYZ_DB_PASSWORD' 'example'
# (will allow for "$XYZ_DB_PASSWORD_FILE" to fill in the value of
#  "$XYZ_DB_PASSWORD" from a file, especially for Docker's secrets feature)
file_env() {
	local var="$1"
	local fileVar="${var}_FILE"
	local def="${2:-}"
	if [ "${!var:-}" ] && [ "${!fileVar:-}" ]; then
		echo >&2 "error: both $var and $fileVar are set (but are exclusive)"
		exit 1
	fi
	local val="$def"
	if [ "${!var:-}" ]; then
		val="${!var}"
	elif [ "${!fileVar:-}" ]; then
		val="$(<"${!fileVar}")"
	fi
	export "$var"="$val"
	unset "$fileVar"
}

# see https://github.com/docker-library/mongo/issues/147 (mongod is picky about duplicated arguments)
_mongod_hack_have_arg() {
	local checkArg="$1"
	shift
	local arg
	for arg; do
		case "$arg" in
			"$checkArg" | "$checkArg"=*)
				return 0
				;;
		esac
	done
	return 1
}
# _mongod_hack_get_arg_val '--some-arg' "$@"
_mongod_hack_get_arg_val() {
	local checkArg="$1"
	shift
	while [ "$#" -gt 0 ]; do
		local arg="$1"
		shift
		case "$arg" in
			"$checkArg")
				echo "$1"
				return 0
				;;
			"$checkArg"=*)
				echo "${arg#$checkArg=}"
				return 0
				;;
		esac
	done
	return 1
}
declare -a mongodHackedArgs
# _mongod_hack_ensure_arg '--some-arg' "$@"
# set -- "${mongodHackedArgs[@]}"
_mongod_hack_ensure_arg() {
	local ensureArg="$1"
	shift
	mongodHackedArgs=("$@")
	if ! _mongod_hack_have_arg "$ensureArg" "$@"; then
		mongodHackedArgs+=("$ensureArg")
	fi
}
# _mongod_hack_ensure_no_arg '--some-unwanted-arg' "$@"
# set -- "${mongodHackedArgs[@]}"
_mongod_hack_ensure_no_arg() {
	local ensureNoArg="$1"
	shift
	mongodHackedArgs=()
	while [ "$#" -gt 0 ]; do
		local arg="$1"
		shift
		if [ "$arg" = "$ensureNoArg" ]; then
			continue
		fi
		mongodHackedArgs+=("$arg")
	done
}
# _mongod_hack_ensure_no_arg '--some-unwanted-arg' "$@"
# set -- "${mongodHackedArgs[@]}"
_mongod_hack_ensure_no_arg_val() {
	local ensureNoArg="$1"
	shift
	mongodHackedArgs=()
	while [ "$#" -gt 0 ]; do
		local arg="$1"
		shift
		case "$arg" in
			"$ensureNoArg")
				shift # also skip the value
				continue
				;;
			"$ensureNoArg"=*)
				# value is already included
				continue
				;;
		esac
		mongodHackedArgs+=("$arg")
	done
}
# _mongod_hack_ensure_arg_val '--some-arg' 'some-val' "$@"
# set -- "${mongodHackedArgs[@]}"
_mongod_hack_ensure_arg_val() {
	local ensureArg="$1"
	shift
	local ensureVal="$1"
	shift
	_mongod_hack_ensure_no_arg_val "$ensureArg" "$@"
	mongodHackedArgs+=("$ensureArg" "$ensureVal")
}

# required by mongodb 4.2
# _mongod_hack_rename_arg_save_val '--arg-to-rename' '--arg-to-rename-to' "$@"
# set -- "${mongodHackedArgs[@]}"
_mongod_hack_rename_arg_save_val() {
	local oldArg="$1"
	shift
	local newArg="$1"
	shift
	if ! _mongod_hack_have_arg "$oldArg" "$@"; then
		return 0
	fi
	local val=""
	mongodHackedArgs=()
	while [ "$#" -gt 0 ]; do
		local arg="$1"
		shift
		if [ "$arg" = "$oldArg" ]; then
			val="$1"
			shift
			continue
		elif [[ $arg =~ "$oldArg"=(.*) ]]; then
			val=${BASH_REMATCH[1]}
			continue
		fi
		mongodHackedArgs+=("$arg")
	done
	mongodHackedArgs+=("$newArg" "$val")
}

# required by mongodb 4.2
# _mongod_hack_rename_arg'--arg-to-rename' '--arg-to-rename-to' "$@"
# set -- "${mongodHackedArgs[@]}"
_mongod_hack_rename_arg() {
	local oldArg="$1"
	shift
	local newArg="$1"
	shift
	if _mongod_hack_have_arg "$oldArg" "$@"; then
		_mongod_hack_ensure_no_arg "$oldArg" "$@"
		_mongod_hack_ensure_arg "$newArg" "${mongodHackedArgs[@]}"
	fi
}

# _js_escape 'some "string" value'
_js_escape() {
	jq --null-input --arg 'str' "$1" '$str'
}

jsonConfigFile="${TMPDIR:-/tmp}/docker-entrypoint-config.json"
tempConfigFile="${TMPDIR:-/tmp}/docker-entrypoint-temp-config.json"
_parse_config() {
	if [ -s "$tempConfigFile" ]; then
		return 0
	fi

	local configPath
	if configPath="$(_mongod_hack_get_arg_val --config "$@")"; then
		# if --config is specified, parse it into a JSON file so we can remove a few problematic keys (especially SSL-related keys)
		# see https://docs.mongodb.com/manual/reference/configuration-options/
		mongo --norc --nodb --quiet --eval "load('/js-yaml.js'); printjson(jsyaml.load(cat($(_js_escape "$configPath"))))" >"$jsonConfigFile"
		jq 'del(.systemLog, .processManagement, .net, .security)' "$jsonConfigFile" >"$tempConfigFile"
		return 0
	fi

	return 1
}
dbPath=
_dbPath() {
	if [ -n "$dbPath" ]; then
		echo "$dbPath"
		return
	fi

	if ! dbPath="$(_mongod_hack_get_arg_val --dbpath "$@")"; then
		if _parse_config "$@"; then
			dbPath="$(jq -r '.storage.dbPath // empty' "$jsonConfigFile")"
		fi
	fi

	: "${dbPath:=/data/db}"

	echo "$dbPath"
}

if [ "$originalArgOne" = 'mongod' ]; then
	file_env 'MONGO_INITDB_ROOT_USERNAME'
	file_env 'MONGO_INITDB_ROOT_PASSWORD'
	# pre-check a few factors to see if it's even worth bothering with initdb
	shouldPerformInitdb=
	if [ "$MONGO_INITDB_ROOT_USERNAME" ] && [ "$MONGO_INITDB_ROOT_PASSWORD" ]; then
		# if we have a username/password, let's set "--auth"
		_mongod_hack_ensure_arg '--auth' "$@"
		set -- "${mongodHackedArgs[@]}"
		shouldPerformInitdb='true'
	elif [ "$MONGO_INITDB_ROOT_USERNAME" ] || [ "$MONGO_INITDB_ROOT_PASSWORD" ]; then
		cat >&2 <<-'EOF'

			error: missing 'MONGO_INITDB_ROOT_USERNAME' or 'MONGO_INITDB_ROOT_PASSWORD'
			       both must be specified for a user to be created

		EOF
		exit 1
	fi

	if [ -z "$shouldPerformInitdb" ]; then
		# if we've got any /docker-entrypoint-initdb.d/* files to parse later, we should initdb
		for f in /docker-entrypoint-initdb.d/*; do
			case "$f" in
				*.sh | *.js) # this should match the set of files we check for below
					shouldPerformInitdb="$f"
					break
					;;
			esac
		done
	fi

	# check for a few known paths (to determine whether we've already initialized and should thus skip our initdb scripts)
	if [ -n "$shouldPerformInitdb" ]; then
		dbPath="$(_dbPath "$@")"
		for path in \
			"$dbPath/WiredTiger" \
			"$dbPath/journal" \
			"$dbPath/local.0" \
			"$dbPath/storage.bson"; do
			if [ -e "$path" ]; then
				shouldPerformInitdb=
				break
			fi
		done
	fi

	if [ -n "$shouldPerformInitdb" ]; then
		mongodHackedArgs=("$@")
		if _parse_config "$@"; then
			_mongod_hack_ensure_arg_val --config "$tempConfigFile" "${mongodHackedArgs[@]}"
		fi
		_mongod_hack_ensure_arg_val --bind_ip 127.0.0.1 "${mongodHackedArgs[@]}"
		_mongod_hack_ensure_arg_val --port 27017 "${mongodHackedArgs[@]}"
		_mongod_hack_ensure_no_arg --bind_ip_all "${mongodHackedArgs[@]}"

		# remove "--auth" and "--replSet" for our initial startup (see https://docs.mongodb.com/manual/tutorial/enable-authentication/#start-mongodb-without-access-control)
		# https://github.com/docker-library/mongo/issues/211
		_mongod_hack_ensure_no_arg --auth "${mongodHackedArgs[@]}"
		if [ "$MONGO_INITDB_ROOT_USERNAME" ] && [ "$MONGO_INITDB_ROOT_PASSWORD" ]; then
			_mongod_hack_ensure_no_arg_val --replSet "${mongodHackedArgs[@]}"
		fi

		# "BadValue: need sslPEMKeyFile when SSL is enabled" vs "BadValue: need to enable SSL via the sslMode flag when using SSL configuration parameters"
		tlsMode='disabled'
		if _mongod_hack_have_arg '--tlsCertificateKeyFile' "${mongodHackedArgs[@]}"; then
			tlsMode='preferTLS'
		elif _mongod_hack_have_arg '--sslPEMKeyFile' "${mongodHackedArgs[@]}"; then
			tlsMode='preferSSL'
		fi
		# 4.2 switched all configuration/flag names from "SSL" to "TLS"
		if [ "$tlsMode" = 'preferTLS' ] || mongod --help 2>&1 | grep -q -- ' --tlsMode '; then
			_mongod_hack_ensure_arg_val --tlsMode "$tlsMode" "${mongodHackedArgs[@]}"
		else
			_mongod_hack_ensure_arg_val --sslMode "$tlsMode" "${mongodHackedArgs[@]}"
		fi

		if stat "/proc/$$/fd/1" >/dev/null && [ -w "/proc/$$/fd/1" ]; then
			# https://github.com/mongodb/mongo/blob/38c0eb538d0fd390c6cb9ce9ae9894153f6e8ef5/src/mongo/db/initialize_server_global_state.cpp#L237-L251
			# https://github.com/docker-library/mongo/issues/164#issuecomment-293965668
			_mongod_hack_ensure_arg_val --logpath "/proc/$$/fd/1" "${mongodHackedArgs[@]}"
		else
			initdbLogPath="$(_dbPath "$@")/docker-initdb.log"
			echo >&2 "warning: initdb logs cannot write to '/proc/$$/fd/1', so they are in '$initdbLogPath' instead"
			_mongod_hack_ensure_arg_val --logpath "$initdbLogPath" "${mongodHackedArgs[@]}"
		fi
		_mongod_hack_ensure_arg --logappend "${mongodHackedArgs[@]}"

		pidfile="${TMPDIR:-/tmp}/docker-entrypoint-temp-mongod.pid"
		rm -f "$pidfile"
		_mongod_hack_ensure_arg_val --pidfilepath "$pidfile" "${mongodHackedArgs[@]}"

		"${mongodHackedArgs[@]}" --fork

		mongo=(mongo --host 127.0.0.1 --port 27017 --quiet)

		# check to see that our "mongod" actually did start up (catches "--help", "--version", MongoDB 3.2 being silly, slow prealloc, etc)
		# https://jira.mongodb.org/browse/SERVER-16292
		tries=30
		while true; do
			if ! { [ -s "$pidfile" ] && ps "$(<"$pidfile")" &>/dev/null; }; then
				# bail ASAP if "mongod" isn't even running
				echo >&2
				echo >&2 "error: $originalArgOne does not appear to have stayed running -- perhaps it had an error?"
				echo >&2
				exit 1
			fi
			if "${mongo[@]}" 'admin' --eval 'quit(0)' &>/dev/null; then
				# success!
				break
			fi
			((tries--))
			if [ "$tries" -le 0 ]; then
				echo >&2
				echo >&2 "error: $originalArgOne does not appear to have accepted connections quickly enough -- perhaps it had an error?"
				echo >&2
				exit 1
			fi
			sleep 1
		done

		if [ "$MONGO_INITDB_ROOT_USERNAME" ] && [ "$MONGO_INITDB_ROOT_PASSWORD" ]; then
			rootAuthDatabase='admin'

			"${mongo[@]}" "$rootAuthDatabase" <<-EOJS
				db.createUser({
					user: $(_js_escape "$MONGO_INITDB_ROOT_USERNAME"),
					pwd: $(_js_escape "$MONGO_INITDB_ROOT_PASSWORD"),
					roles: [ { role: 'root', db: $(_js_escape "$rootAuthDatabase") } ]
				})
			EOJS
		fi

		export MONGO_INITDB_DATABASE="${MONGO_INITDB_DATABASE:-test}"

		echo
		for f in /docker-entrypoint-initdb.d/*; do
			case "$f" in
				*.sh)
					echo "$0: running $f"
					. "$f"
					;;
				*.js)
					echo "$0: running $f"
					"${mongo[@]}" "$MONGO_INITDB_DATABASE" "$f"
					echo
					;;
				*) echo "$0: ignoring $f" ;;
			esac
			echo
		done

		"${mongodHackedArgs[@]}" --shutdown
		rm -f "$pidfile"

		echo
		echo 'MongoDB init process complete; ready for start up.'
		echo
	fi
fi

if [[ $originalArgOne == mongo* ]]; then
	mongodHackedArgs=("$@")
	MONGO_SSL_DIR=${MONGO_SSL_DIR:-/etc/mongodb-ssl}
	CA=/var/run/secrets/kubernetes.io/serviceaccount/ca.crt
	if [ -f /var/run/secrets/kubernetes.io/serviceaccount/service-ca.crt ]; then
		CA=/var/run/secrets/kubernetes.io/serviceaccount/service-ca.crt
	fi
	if [ -f "${MONGO_SSL_DIR}/ca.crt" ]; then
		CA="${MONGO_SSL_DIR}/ca.crt"
	fi
	if [ -f "${MONGO_SSL_DIR}/tls.key" ] && [ -f "${MONGO_SSL_DIR}/tls.crt" ]; then
		cat "${MONGO_SSL_DIR}/tls.key" "${MONGO_SSL_DIR}/tls.crt" >/tmp/tls.pem
		_mongod_hack_ensure_arg_val --sslPEMKeyFile /tmp/tls.pem "${mongodHackedArgs[@]}"
		if [ -f "${CA}" ]; then
			_mongod_hack_ensure_arg_val --sslCAFile "${CA}" "${mongodHackedArgs[@]}"
		fi
	fi
	MONGO_SSL_INTERNAL_DIR=${MONGO_SSL_INTERNAL_DIR:-/etc/mongodb-ssl-internal}
	if [ -f "${MONGO_SSL_INTERNAL_DIR}/tls.key" ] && [ -f "${MONGO_SSL_INTERNAL_DIR}/tls.crt" ]; then
		cat "${MONGO_SSL_INTERNAL_DIR}/tls.key" "${MONGO_SSL_INTERNAL_DIR}/tls.crt" >/tmp/tls-internal.pem
		_mongod_hack_ensure_arg_val --sslClusterFile /tmp/tls-internal.pem "${mongodHackedArgs[@]}"
		if [ -f "${MONGO_SSL_INTERNAL_DIR}/ca.crt" ]; then
			_mongod_hack_ensure_arg_val --sslClusterCAFile "${MONGO_SSL_INTERNAL_DIR}/ca.crt" "${mongodHackedArgs[@]}"
		fi
	fi

	MONGODB_VERSION=$(mongod --version | head -1 | awk '{print $3}' | awk -F'.' '{print $1"."$2}')

	if [ "$MONGODB_VERSION" == 'v4.2' ] || [ "$MONGODB_VERSION" == 'v4.4' ]; then
		_mongod_hack_rename_arg_save_val --sslMode --tlsMode "${mongodHackedArgs[@]}"

		if _mongod_hack_have_arg '--tlsMode' "${mongodHackedArgs[@]}"; then
			tlsMode="none"
			if _mongod_hack_have_arg 'allowSSL' "${mongodHackedArgs[@]}"; then
				tlsMode='allowTLS'
			elif _mongod_hack_have_arg 'preferSSL' "${mongodHackedArgs[@]}"; then
				tlsMode='preferTLS'
			elif _mongod_hack_have_arg 'requireSSL' "${mongodHackedArgs[@]}"; then
				tlsMode='requireTLS'
			fi

			if [ "$tlsMode" != "none" ]; then
				_mongod_hack_ensure_no_arg_val --tlsMode "${mongodHackedArgs[@]}"
				_mongod_hack_ensure_arg_val --tlsMode "$tlsMode" "${mongodHackedArgs[@]}"
			fi
		fi

		_mongod_hack_rename_arg_save_val --sslPEMKeyFile --tlsCertificateKeyFile "${mongodHackedArgs[@]}"
		if ! _mongod_hack_have_arg '--tlsMode' "${mongodHackedArgs[@]}"; then
			if _mongod_hack_have_arg '--tlsCertificateKeyFile' "${mongodHackedArgs[@]}"; then
				_mongod_hack_ensure_arg_val --tlsMode "preferTLS" "${mongodHackedArgs[@]}"
			fi
		fi
		_mongod_hack_rename_arg '--sslAllowInvalidCertificates' '--tlsAllowInvalidCertificates' "${mongodHackedArgs[@]}"
		_mongod_hack_rename_arg '--sslAllowInvalidHostnames' '--tlsAllowInvalidHostnames' "${mongodHackedArgs[@]}"
		_mongod_hack_rename_arg '--sslAllowConnectionsWithoutCertificates' '--tlsAllowConnectionsWithoutCertificates' "${mongodHackedArgs[@]}"
		_mongod_hack_rename_arg '--sslFIPSMode' '--tlsFIPSMode' "${mongodHackedArgs[@]}"

		_mongod_hack_rename_arg_save_val --sslPEMKeyPassword --tlsCertificateKeyFilePassword "${mongodHackedArgs[@]}"
		_mongod_hack_rename_arg_save_val --sslClusterFile --tlsClusterFile "${mongodHackedArgs[@]}"
		_mongod_hack_rename_arg_save_val --sslCertificateSelector --tlsCertificateSelector "${mongodHackedArgs[@]}"
		_mongod_hack_rename_arg_save_val --sslClusterCertificateSelector --tlsClusterCertificateSelector "${mongodHackedArgs[@]}"
		_mongod_hack_rename_arg_save_val --sslClusterPassword --tlsClusterPassword "${mongodHackedArgs[@]}"
		_mongod_hack_rename_arg_save_val --sslCAFile --tlsCAFile "${mongodHackedArgs[@]}"
		_mongod_hack_rename_arg_save_val --sslClusterCAFile --tlsClusterCAFile "${mongodHackedArgs[@]}"
		_mongod_hack_rename_arg_save_val --sslCRLFile --tlsCRLFile "${mongodHackedArgs[@]}"
		_mongod_hack_rename_arg_save_val --sslDisabledProtocols --tlsDisabledProtocols "${mongodHackedArgs[@]}"
	fi

	set -- "${mongodHackedArgs[@]}"

	# MongoDB 3.6+ defaults to localhost-only binding
	haveBindIp=
	if _mongod_hack_have_arg --bind_ip "$@" || _mongod_hack_have_arg --bind_ip_all "$@"; then
		haveBindIp=1
	elif _parse_config "$@" && jq --exit-status '.net.bindIp // .net.bindIpAll' "$jsonConfigFile" >/dev/null; then
		haveBindIp=1
	fi
	if [ -z "$haveBindIp" ]; then
		# so if no "--bind_ip" is specified, let's add "--bind_ip_all"
		set -- "$@" --bind_ip_all
	fi

	unset "${!MONGO_INITDB_@}"
fi

rm -f "$jsonConfigFile" "$tempConfigFile"

set -o xtrace
exec "$@"
