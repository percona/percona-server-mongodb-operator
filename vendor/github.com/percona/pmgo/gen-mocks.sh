#!/bin/bash
CURDIR=$(pwd)
for file in *.go
do
    echo "mockgen -source ${CURDIR}/${file} -destination=${CURDIR}/pmgomock/${file} -package pmgomock -imports \".=github.com/percona/pmgo\""
    mockgen -source ${CURDIR}/${file} -destination=${CURDIR}/pmgomock/${file} -package pmgomock -imports ".=github.com/percona/pmgo"
done
