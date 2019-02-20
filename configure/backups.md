Providing Backups
==============================================================

Percona Server for MongoDB Operator allows doing cluster backup in two ways.
*Scheduled backups* are configured in the [deploy/cr.yaml](https://github.com/Percona-Lab/percona-server-mongodb-operator/blob/master/deploy/cr.yaml) file to be executed automatically in proper time.
*On-demand backups* can be done manually at any moment.
Both ways are using the [Percona Backup for MongoDB](https://github.com/percona/percona-backup-mongodb) tool to do the job.

The backup process is controlled by the [Backup Coordinator](https://github.com/percona/percona-backup-mongodb#coordinator) daemon residing in the Kubernetes cluster alongside the Percona Server for MongoDB, while actual backup images are stored separately on any [Amazon S3 or S3-compatible storage](https://en.wikipedia.org/wiki/Amazon_S3#S3_API_and_competing_services).

## Making scheduled backups

Since backups are stored separately on the Amazon S3, a secret with `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` should be present on the Kubernetes cluster. These keys should be saved to the [deploy/backup-s3.yaml](https://github.com/Percona-Lab/percona-server-mongodb-operator/blob/master/deploy/backup-s3.yaml) file and applied with the appropriate command, e.g. `kubectl apply -f deploy/backup-s3.yaml` (for Kubernetes).

Backups schedule is defined in the  ``backup`` section of the [deploy/cr.yaml](https://github.com/Percona-Lab/percona-server-mongodb-operator/blob/master/deploy/cr.yaml) file. 
This section contains three subsections:
* `storages` subsection contains data needed to access the S3-compatible cloud to store backups.
* `coordinator` subsection allows to configure Kubernetes limits and claims for the Percona Backup for MongoDB Coordinator daemon.
* `tasks` subsection allows to actually schedule backups (the schedule is specified in crontab format).

The options within these three subsections are explained in the [Operator Options](https://percona-lab.github.io/percona-xtradb-cluster-operator/configure/operator).

## Making on-demand backup

To make on-demand backup, user should run [the PBM Control tool](https://github.com/percona/percona-backup-mongodb#pbm-control-pbmctl) inside of the coordinator container, supplying it with needed options, like in the following example:

   ```
   kubectl run -it --rm pbmctl --image=percona/percona-server-mongodb-operator:0.2.1-backup-pbmctl --restart=Never -- --server-address=my-cluster-name-backup-coordinator:10001 run backup --destination-type=aws --compression-algorithm=gzip --description=my-backup
   ```

## Restore the cluster from a previously saved backup

Previously saved backups can be restored with a special *backup restore job*, configured with the [deploy/backup-restore.yaml](https://github.com/Percona-Lab/percona-server-mongodb-operator/blob/master/deploy/backup-restore.yaml) file. Following keys in this file should be edited before the job can be run:

 * Both entries of the `secretKeyRef.name` key should be the same as the `s3.secret` key value in the [deploy/cr.yaml](https://github.com/Percona-Lab/percona-server-mongodb-operator/blob/master/deploy/cr.yaml) backup section
* `BUCKET_NAME` is the S3 bucket name, and it should be same as the `s3.bucket` key value in the [deploy/cr.yaml](https://github.com/Percona-Lab/percona-server-mongodb-operator/blob/master/deploy/cr.yaml) backup section
* `BACKUP_NAME` is the unique name (without the`.dump.gz` suffix) of the particular backup to be restored (the list of available backups can be seen in the S3 browser)
* `MONGODB_DSN` is a connect string starting with `mongodb+srv://BACKUP-USER-HERE:BACKUP-PASSWORD-HERE...`, where `BACKUP-USER-HERE` and `BACKUP-PASSWORD-HERE` parts should be changed to the proper username and password of the backup user, same as ones in [deploy/mongodb-users.yaml](https://github.com/Percona-Lab/percona-server-mongodb-operator/blob/master/deploy/mongodb-users.yaml) secret file.
    

When the editing is done, the restore job can be started in the following way (e.g. on the OpenShift platform):

   ```
   oc create -f deploy/backup-restore.yaml
   ```

## Backups on a private S3-compatible storage

As it was already mentioned, any cloud storage which implements the S3 API can be used for backups.
This section explains how to setup and use your own storage by example of [Minio](https://www.minio.io/) - an S3-compatible object storage server deployed via Docker on your own infrastructure.

Setting up Minio to be used with Percona Server for MongoDB Operator backups involves following steps:

1. First of all, install Minio into your Kubernetes or OpenShift environment:

   ```bash
      helm install \
        --name minio-service \
        --set accessKey=some-access-key \
        --set secretKey=some-secret-key \
        --set service.type=ClusterIP \
        --set configPath=/tmp/.minio/ \
        --set persistence.size=2G \
        --set environment.MINIO_REGION=us-east-1 \
        stable/minio
   ```
   Of course, actual keys should be used instead of `some-access-key` and `some-secret-key` parameters.

2. The next thing to do is to create an S3 bucket for backups:

   ```bash
      kubectl run -i --rm aws-cli --image=perconalab/awscli --restart=Never -- \
       /usr/bin/env AWS_ACCESS_KEY_ID=some-access-key AWS_SECRET_ACCESS_KEY=some-secret-key AWS_DEFAULT_REGION=us-east-1 \
       /usr/bin/aws --endpoint-url http://minio-service:9000 s3 mb s3://operator-testing
   ```
3. When the setup process is over, makin backup is rather simple. Following example illustrates making on-demand backup on Minio:

   ```bash
       kubectl run -it --rm pbmctl --image=percona/percona-server-mongodb-operator:0.3.0-backup-pbmctl --restart=Never -- \
         run backup \
         --server-address=some-name-backup-coordinator:10001 \
         --storage $storage \
         --compression-algorithm=gzip \
         --description=my-backup```
   ```
4. To restore a previously saved backup you will need to specify the backup name. List of available backups can be obtained as follows:

   ```bash
      kubectl run -it --rm pbmctl --image=percona/percona-server-mongodb-operator:0.3.0-backup-pbmctl --restart=Never -- list backups --server-address=some-name-backup-coordinator:10001
   ```
   Now, restore the backup, substituting its name to the `backup-name` parameter:

   ```bash
      kubectl run -it --rm pbmctl --image=percona/percona-server-mongodb-operator:0.3.0-backup-pbmctl --restart=Never -- \
        run restore \
        --server-address=some-name-backup-coordinator:10001 \
        --storage $storage \
        backup-name
   ```

