Creating a private S3-compatible cloud for backups
===============================================================================

As it is mentioned in [backups](https://percona-lab.github.io/percona-server-mongodb-operator/configure/backups) any cloud storage which implements the S3 API can be used for backups - as third-party one, so your own.
The most simple way to setup and use such storage on Kubernetes or OpenShift is [Minio](https://www.minio.io/) - the S3-compatible object storage server deployed via Docker on your own infrastructure.

Setting up Minio to be used with Percona Server for MongoDB Operator backups involves following steps:

1. First of all, install Minio in your Kubernetes or OpenShift environment and create the correspondent Kubernetes Service as follows:

   ```bash
      helm install \
        --name minio-service \
        --set accessKey=some-access-key \
        --set secretKey=some-secret-key \
        --set service.type=ClusterIP \
        --set configPath=/tmp/.minio/ \
        --set persistence.size=2G \
        --set persistence.storageClass=aws-io1 \
        --set environment.MINIO_REGION=us-east-1 \
        stable/minio
   ```

   Don't forget to substitute default `some-access-key` and `some-secret-key` strings in this command with some actual unique key values which can be used later for the access control.

   You may also notice `MINIO_REGION` value which is not of much sense within the private cloud. Just use the same region value here and on later steps (`us-east-1` is a good default choice).

2. The next thing to do is to create an S3 bucket for backups:

   ```bash
      kubectl run -i --rm aws-cli --image=perconalab/awscli --restart=Never -- \
       /usr/bin/env AWS_ACCESS_KEY_ID=some-access-key AWS_SECRET_ACCESS_KEY=some-secret-key AWS_DEFAULT_REGION=us-east-1 \
       /usr/bin/aws --endpoint-url http://minio-service:9000 s3 mb s3://operator-testing
   ```

   This command creates the bucket named `operator-testing` with already chosen access and secret keys (substitute `some-access-key` and `some-secret-key` with the values used on the previous step).

3. Now edit the backup section of the [deploy/cr.yaml](https://github.com/Percona-Lab/percona-server-mongodb-operator/blob/master/deploy/cr.yaml) file to set proper values for the `bucket` (the S3 bucket for backups created on the previous step), `region`, `credentialsSecret` and the `endpointUrl` (which should point to the previously created Minio Service).

   ```
   ...
   backup:
     enabled: true
     version: 0.3.0
     ...
     storages:
       minio:
         type: s3
         s3:
           bucket: operator-testing
           region: us-west-1
           credentialsSecret: my-cluster-name-backup-minio
           endpointUrl: http://minio-service:9000
     ...
   ```

4. When the setup process is over, making backup is rather simple. Following example illustrates how to make an on-demand backup:

   ```bash
       kubectl run -it --rm pbmctl --image=percona/percona-server-mongodb-operator:0.3.0-backup-pbmctl --restart=Never -- \
         run backup \
         --server-address=<cluster-name>-backup-coordinator:10001 \
         --storage <storage> \
         --compression-algorithm=gzip \
         --description=my-backup```
   ```

   Don't forget to specify the name of your cluster instead of the `<cluster-name>` part of the Backup Coordinator URL (the same cluster name which is specified in the [deploy/cr.yaml](https://github.com/Percona-Lab/percona-server-mongodb-operator/blob/master/deploy/cr.yaml) file). Also `<storage>` should be substituted with the actual storage name, which is featured as a subsection inside of the `backups` one in [deploy/cr.yaml](https://github.com/Percona-Lab/percona-server-mongodb-operator/blob/master/deploy/cr.yaml) file. In the upper example it is `minio`.

5. To restore a previously saved backup you will need to specify the backup name. List of available backups can be obtained from the Backup Coordinator as follows (supposedly that you once again use the Backup Coordinator's proper URL and the storage name like you did in previous step):

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

