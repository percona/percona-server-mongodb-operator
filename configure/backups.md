Providing Backups
==============================================================

Percona Server for MongoDB Operator allows doing cluster backup in two ways.
*Scheduled backups* are configured in the [deploy/cr.yaml](https://github.com/Percona-Lab/percona-xtradb-cluster-operator/blob/master/deploy/cr.yaml) file to be executed automatically in proper time.
*On-demand backups* can be done manually at any moment.
Both ways are using the [Percona Backup for MongoDB](https://github.com/percona/percona-backup-mongodb) tool to do the job.

The backup process is controlled by the [Backup Coordinator](https://github.com/percona/percona-backup-mongodb#coordinator) daemon residing in the Kubernetes cluster alongside the Percona Server for MongoDB, while actual backup images are stored separately on any [Amazon S3 compatible storage](https://en.wikipedia.org/wiki/Amazon_S3#S3_API_and_competing_services).

## Making scheduled backups

Since backups are stored separately on the Amazon S3, a secret with S3 access key and key secret should be present on the Kubernetes cluster. These keys should be saved to the [deploy/backup-s3.yaml](https://github.com/Percona-Lab/percona-server-mongodb-operator/blob/master/deploy/backup-s3.yaml) file, which should be applied to make effect, for example with an `kubectl apply -f deploy/backup-s3.yaml` command (for Kubernetes).

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
