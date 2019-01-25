#!/bin/bash

[ -z $BUCKET_NAME ] && BUCKET_NAME=$1
[ -z $BACKUP_NAME ] && BACKUP_NAME=$2
[ -z $MONGODB_DSN ] && MONGODB_DSN=$3

[ -z $AWS_ACCESS_KEY_ID ] && echo "AWS_ACCESS_KEY_ID environment variable must be set!" && exit 1
[ -z $AWS_SECRET_ACCESS_KEY ] && echo "AWS_SECRET_ACCESS_KEY environment variable must be set!" && exit 1

if [ -z $BUCKET_NAME ] || [ -z $BACKUP_NAME ] || [ -z $MONGODB_DSN ]; then
  echo "Usage: $0 [S3-BUCKET-NAME] [BACKUP-NAME] [MONGODB_URI]"
  exit 1
fi

# check if the backup is gzipped or not
aws s3 ls s3://${BUCKET_NAME}/${BACKUP_NAME}.dump.gz >/dev/null
if [ $? = 0 ]; then
  DUMP_FILE=${BACKUP_NAME}.dump.gz
  OPLOG_FILE=${BACKUP_NAME}.oplog.gz
  GZIP_FLAG="--gzip"
else
  DUMP_FILE=${BACKUP_NAME}.dump
  OPLOG_FILE=${BACKUP_NAME}.oplog
fi

set -e

# download database dump file
echo "# Fetching database backup: s3://${BUCKET_NAME}/${DUMP_FILE}"
aws s3 cp s3://${BUCKET_NAME}/${DUMP_FILE} ${DUMP_FILE}

# restore dump file
echo "# Restoring database backup ${DUMP_FILE} to: ${MONGODB_DSN}"
cat ${DUMP_FILE} | mongorestore $GZIP_FLAG --archive --drop --stopOnError --uri=$MONGODB_DSN

# download and apply the oplog file, if it exists.
if [ ! -z $OPLOG_FILE ]; then
  set +e
  aws s3 ls s3://${BUCKET_NAME}/${OPLOG_FILE} >/dev/null
  if [ $? -gt 0 ]; then
    echo "# Found no oplog at s3://${BUCKET_NAME}/${OPLOG_FILE}, skipping oplog restore"
    exit 0
  fi
  set -e

  echo "# Fetching database oplog s3://${BUCKET_NAME}}/${OPLOG_FILE}"
  OPLOG_DIR=$(mktemp -d)
  cd $OPLOG_DIR
  aws s3 cp s3://${BUCKET_NAME}/${OPLOG_FILE} oplog.bson

  echo "# Restoring database oplog ${OPLOG_FILE} to: ${MONGODB_DSN}"
  mongorestore $GZIP_FLAG --dir=. --oplogReplay --stopOnError --uri=$MONGODB_DSN 
  rm -rf $OPLOG_DIR
fi

echo "# Done"
