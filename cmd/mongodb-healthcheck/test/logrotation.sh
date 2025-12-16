#!/usr/bin/env bash

sed=$(which gsed || which sed)
date=$(which gdate || which date)

set -o errexit

temp_dir=$(mktemp -d)

go build -a -o "$temp_dir/mongodb-healthcheck" "$(git rev-parse --show-toplevel)/cmd/mongodb-healthcheck"

cd "$temp_dir"

for _ in {0..30}; do
	./mongodb-healthcheck || true
done

logs_count=$(find mongod-data/logs -type f -name "*.log*" | wc -l)
if [[ $logs_count -ne "32" ]]; then
	echo "Current amount of log files is $logs_count. Expected 32"
	exit 1
fi

current_date=$($date +"%Y-%m-%d")
new_date=$($date -d "yesterday -1 day" +"%Y-%m-%d")

for file in mongod-data/logs/*.log.gz; do
	new_file=$(echo "$file" | $sed "s/$current_date/$new_date/")
	mv "$file" "$new_file"
	echo "Renamed: $file → $new_file"
done

./mongodb-healthcheck || true

logs_count_new=$(find mongod-data/logs -type f -name "*.log*" | wc -l)
if [[ $logs_count_new -ne "2" ]]; then
	echo "Current amount of log files is $logs_count_new. Expected 2"
	exit 1
fi

echo "The test was successful. There were $logs_count log files initially. After 2 days, the logs were rotated, and there are now $logs_count_new log files"
