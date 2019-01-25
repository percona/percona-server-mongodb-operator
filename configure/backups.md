Providing Backups
==============================================================

Percona Server for MongoDB Operator allows doing cluster backup in two ways.
*Scheduled backups* are configured in the [deploy/cr.yaml](https://github.com/Percona-Lab/percona-xtradb-cluster-operator/blob/master/deploy/cr.yaml) file to be executed automatically in proper time.
*On-demand backups* can be done manually at any moment.
Both ways are using the [Percona Backup for MongoDB](https://github.com/percona/percona-backup-mongodb) tool to do the job.

The backup process is controlled by the [Backup Coordinator](https://github.com/percona/percona-backup-mongodb#coordinator) daemon residing in the Kubernetes cluster alongside the Percona Server for MongoDB, while actual backup images are stored separately on any [Amazon S3 compatible storage](https://en.wikipedia.org/wiki/Amazon_S3#S3_API_and_competing_services).

## Making scheduled backups

Since backups are stored separately on the Amazon S3, a secret with `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` should be present on the Kubernetes cluster. These keys should be saved to the [deploy/backup-s3.yaml](https://github.com/Percona-Lab/percona-server-mongodb-operator/blob/master/deploy/backup-s3.yaml) file and applied with the appropriate command, e.g. `kubectl apply -f deploy/backup-s3.yaml` (for Kubernetes).

Backups schedule is defined in the  ``backup`` section of the [deploy/cr.yaml](https://github.com/Percona-Lab/percona-xtradb-cluster-operator/blob/master/deploy/cr.yaml) file. 
This section contains three subsections:
* `s3` subsection contains data needed to access the Amazon S3 cloud to store backups.
* `coordinator` subsections allows to configure Kubernetes limits and claims for the Percona Backup for MongoDB Coordinator daemon.
* `tasks` subsection allows to actually schedule backups (the schedule is specified in crontab format).

The options within these three subsections are explained in the [Operator Options](https://percona-lab.github.io/percona-xtradb-cluster-operator/configure/operator).

## Making on-demand backup

To make on-demand backup, user should run [the PBM Control tool](https://github.com/percona/percona-backup-mongodb#pbm-control-pbmctl) inside of the coordinator container, supplying it with needed options, like in the following example:

   ```
   oc run -i --quiet --rm --tty pbmctl --image=percona/percona-backup-mongodb:pbmctl --restart=Never -- $* --server-address=my-cluster-name-backup-coordinator.myproject.svc.cluster.local:10001
   ```

## Restore the cluster from a previously saved backup

Previously saved backups can be restored with a special *backup restorer job*, configured with the [deploy/backup-restorer.yaml](https://github.com/Percona-Lab/percona-server-mongodb-operator/blob/master/deploy/backup-restore.yaml) file. Following keys in this file should be edited before the job can be run:

* `secretKeyRef.name` for the `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` is the name of the Kubernetes secret with S3 credentials, and it should be the same as the `s3.secret` key value in the [deploy/cr.yaml](https://github.com/Percona-Lab/percona-server-mongodb-operator/blob/master/deploy/cr.yaml) backup section
* `BUCKET_NAME` is the S3 bucket name, and it should be same as the `s3.bucket` key value in the [deploy/cr.yaml](https://github.com/Percona-Lab/percona-server-mongodb-operator/blob/master/deploy/cr.yaml) backup section
* `BACKUP_NAME` is the unique name of the particular backup to be restored (the list of available backups can be seen in the S3 browser)
* `MONGODB_DSN` is a connect string starting with `mongodb+srv://BACKUP-USER-HERE:BACKUP-PASSWORD-HERE...`, where `BACKUP-USER-HERE` and `BACKUP-PASSWORD-HERE` parts should be changed to the proper username and password of the backup user, same as ones in [deploy/mongodb-users.yaml](deploy/backup-restorer.yaml](https://github.com/Percona-Lab/percona-server-mongodb-operator/blob/master/deploy/mongodb-users.yaml) secret file.
    

When the editing is done, the restore job can be started in the following way (e.g. on the OpenShift platform):

   ```
   oc create -f deploy/backup-restorer.yaml
   ```

