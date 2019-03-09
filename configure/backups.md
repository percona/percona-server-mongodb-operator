Providing Backups
==============================================================

Percona Server for MongoDB Operator allows doing cluster backup in two ways.
*Scheduled backups* are configured in the [deploy/cr.yaml](https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/cr.yaml) file to be executed automatically in proper time.
*On-demand backups* can be done manually at any moment.
Both ways are using the [Percona Backup for MongoDB](https://github.com/percona/percona-backup-mongodb) tool to do the job.

The backup process is controlled by the [Backup Coordinator](https://github.com/percona/percona-backup-mongodb#coordinator) daemon residing in the Kubernetes cluster alongside the Percona Server for MongoDB, while actual backup images are stored separately on any [Amazon S3 or S3-compatible storage](https://en.wikipedia.org/wiki/Amazon_S3#S3_API_and_competing_services).

## Making scheduled backups

Since backups are stored separately on the Amazon S3, a secret with `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` should be present on the Kubernetes cluster. These keys should be saved to the [deploy/backup-s3.yaml](https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/backup-s3.yaml) file and applied with the appropriate command, e.g. `kubectl apply -f deploy/backup-s3.yaml` (for Kubernetes).

Backups schedule is defined in the  ``backup`` section of the [deploy/cr.yaml](https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/cr.yaml) file. 
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
     tasks:
      - name: daily-s3-us-west
        enabled: true
        schedule: "0 0 * * *"
        storageName: s3-us-west
        compressionType: gzip
     ...
   ```

**Note:** *if you use some S3-compatible storage instead of the original Amazon S3, one more key is needed in the `s3` subsection: the `endpointUrl`, which points to the actual cloud used for backups and is specific to the cloud provider. For example, using [Google Cloud](https://cloud.google.com) involves the following one: `endpointUrl: https://storage.googleapis.com`.*

The options within these three subsections are further explained in the [Operator Options](https://percona-lab.github.io/percona-xtradb-cluster-operator/configure/operator).

The only option which should be mentioned separately is `credentialsSecret` which is a [Kubernetes secret](https://kubernetes.io/docs/concepts/configuration/secret/) for backups. Sample [backup-s3.yaml](https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/backup-s3.yaml) can be used to create this secret object. Check that it contains proper `name` value (equal to the one specified for `credentialsSecret`, i.e. `my-cluster-name-backup-s3` in the last example), and also proper `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` keys. After editing is finished, secrets object should be created (or updated with the new name and/or keys) using the following command:

   ```bash
   $ kubectl apply -f deploy/backup-s3.yaml
   ```

## Making on-demand backup

To make on-demand backup, user should run [the PBM Control tool](https://github.com/percona/percona-backup-mongodb#pbm-control-pbmctl) inside of the coordinator container, supplying it with needed options, like in the following example:

```bash
    kubectl run -it --rm pbmctl --image=percona/percona-server-mongodb-operator:0.3.0-backup-pbmctl --restart=Never -- \
      run backup \
      --server-address=<cluster-name>-backup-coordinator:10001 \
      --storage <storage> \
      --compression-algorithm=gzip \
      --description=my-backup```
```

Don't forget to specify the name of your cluster instead of the `<cluster-name>` part of the Backup Coordinator URL (the same cluster name which is specified in the [deploy/cr.yaml](https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/cr.yaml) file). Also `<storage>` should be substituted with the actual storage name, which is featured as a subsection inside of the `backups` one in [deploy/cr.yaml](https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/cr.yaml) file. In the upper example it is `s3-us-west`.

## Restore the cluster from a previously saved backup

To restore a previously saved backup you will need to specify the backup name. List of available backups can be obtained from the Backup Coordinator as follows (supposedly that you once again use the Backup Coordinator's proper URL and the storage name like you did to make on-demand backup):

   ```bash
      kubectl run -it --rm pbmctl --image=percona/percona-server-mongodb-operator:0.3.0-backup-pbmctl --restart=Never -- list backups --server-address=<cluster-name>-backup-coordinator:10001
   ```
   Now, restore the backup, using its name instead of the `backup-name` parameter:

   ```bash
      kubectl run -it --rm pbmctl --image=percona/percona-server-mongodb-operator:0.3.0-backup-pbmctl --restart=Never -- \
        run restore \
        --server-address=<cluster-name>-backup-coordinator:10001 \
        --storage <storage> \
        backup-name
   ```

