#!/bin/bash

set -o errexit
set -o xtrace

install -o "$(id -u)" -g "$(id -g)" -m 0755 -D /ps-entry.sh /data/db/ps-entry.sh
