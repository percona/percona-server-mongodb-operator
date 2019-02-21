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

Here is an example which uses Amazon S3 storage for backups:

   ```
   ...
   backup:
     enabled: true
     version: 0.3.0
     ...
     storages:
       s3-us-west:
         type: s3
         s3:
           bucket: S3-BACKUP-BUCKET-NAME-HERE
           region: us-west-2
           credentialsSecret: my-cluster-name-backup-s3
     ...
   ```

   **Note:** *if you use some S3-compatible storage instead of the original Amazon S3, one more key is needed in the `s3` subsection: the `endpointUrl`, which points to the actual cloud used for backups and is specific to the cloud provider. For example, using [Google Cloud](https://cloud.google.com) involves the following one: `endpointUrl: https://storage.googleapis.com`.

The options within these three subsections are further explained in the [Operator Options](https://percona-lab.github.io/percona-xtradb-cluster-operator/configure/operator).

## Making on-demand backup

To make on-demand backup, user should run [the PBM Control tool](https://github.com/percona/percona-backup-mongodb#pbm-control-pbmctl) inside of the coordinator container, supplying it with needed options, like in the following example:

```bash
    kubectl run -it --rm pbmctl --image=percona/percona-server-mongodb-operator:0.3.0-backup-pbmctl --restart=Never -- \
      run backup \
      --server-address=<cluster-name>-backup-coordinator:10001 \
      --storage $storage \
      --compression-algorithm=gzip \
      --description=my-backup```
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


