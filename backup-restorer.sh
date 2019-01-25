#!/bin/bash

set -e

BUCKET=$1
BACKUP_NAME=$2
MONGODB_DSN=$3

DUMP_FILE=${BACKUP_NAME}.dump.gz
OPLOG_FILE=${BACKUP_NAME}.oplog.gz

[ -z $AWS_ACCESS_KEY_ID ] && echo "AWS_ACCESS_KEY_ID environment variable must be set!" && exit 1
[ -z $AWS_SECRET_ACCESS_KEY ] && echo "AWS_SECRET_ACCESS_KEY environment variable must be set!" && exit 1

if [ -z $BUCKET ] || [ -z $BACKUP_NAME ] || [ -z $MONGODB_DSN ]; then
  echo "Usage: $0 [S3-BUCKET-NAME] [BACKUP-NAME] [MONGODB_URI]"
  exit 1
fi

# download database dump file
echo "# Fetching database backup: s3://${BUCKET}/${DUMP_FILE}"
aws s3 cp s3://${BUCKET}/${DUMP_FILE} ${DUMP_FILE}

# setup mongorestore gzip flag, if required
echo ${DUMP_FILE} | grep -qP "\.gz(\s+)?$"
[ $? = 0 ] && GZIP_FLAG="--gzip"

# restore dump file
echo "# Restoring database backup ${DUMP_FILE} to: ${MONGODB_DSN}"
cat ${DUMP_FILE} | mongorestore $GZIP_FLAG --archive --drop --stopOnError --uri=$MONGODB_DSN

# download and apply the oplog file, if it exists.
if [ ! -z $OPLOG_FILE ]; then
  set +e
  aws s3 ls s3://${BUCKET}/${OPLOG_FILE} >/dev/null
  [ $? -gt 0 ] && echo "# Found no oplog at s3://${BUCKET}/${OPLOG_FILE}, skipping oplog restore" && exit 1
  set -e

  echo "# Fetching database oplog s3://${BUCKET}}/${OPLOG_FILE}"
  OPLOG_DIR=$(mktemp -d)
  cd $OPLOG_DIR
  aws s3 cp s3://${BUCKET}/${OPLOG_FILE} oplog.bson

  echo "# Restoring database oplog ${OPLOG_FILE} to: ${MONGODB_DSN}"
  mongorestore $GZIP_FLAG --dir=. --oplogReplay --stopOnError --uri=$MONGODB_DSN 
  rm -rf $OPLOG_DIR
fi

echo "# Done"
