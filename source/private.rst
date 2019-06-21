Creating a private S3-compatible cloud for backups
==================================================

As it is mentioned in
`backups <backups.html>`__
any cloud storage which implements the S3 API can be used for backups. The one way to setup and implement the S3 API storage on Kubernetes or OpenShift is
`Minio <https://www.minio.io/>`__ - the S3-compatible object storage
server deployed via Docker on your own infrastructure.

Setting up Minio to be used with Percona Server for MongoDB Operator
backups involves following steps:

1. Install Minio in your Kubernetes or OpenShift
   environment and create the correspondent Kubernetes Service as
   follows:

   .. code:: bash

         helm install \
           --name minio-service \
           --set accessKey=some-access-key \
           --set secretKey=some-secret-key \
           --set service.type=ClusterIP \
           --set configPath='~/.minio' \
           --set persistence.size=2G \
           --set environment.MINIO_REGION=us-west-1 \
           stable/minio

   Don’t forget to substitute default ``some-access-key`` and
   ``some-secret-key`` strings in this command with actual unique
   key values. The values can be used later for access control. The ``storageClass`` option is needed if you are using the special
   `Kubernetes Storage
   Class <https://kubernetes.io/docs/concepts/storage/storage-classes/>`__
   for backups. Otherwise, this setting may be omitted. You may also notice the
   ``MINIO_REGION`` value which is may not be used within a private
   cloud. Use the same region value here and on later steps
   (``us-west-1`` is a good default choice).

2. Create an S3 bucket for backups:

   .. code:: bash

         kubectl run -i --rm aws-cli --image=perconalab/awscli --restart=Never -- \
          bash -c 'AWS_ACCESS_KEY_ID=some-access-key \
          AWS_SECRET_ACCESS_KEY=some-secret-key \
          AWS_DEFAULT_REGION=us-west-1 \
           /usr/bin/aws \
           --endpoint-url http://minio-service:9000 \
           s3 mb s3://operator-testing'

   This command creates the bucket named ``operator-testing`` with
   the selected access and secret keys (substitute ``some-access-key``
   and ``some-secret-key`` with the values used on the previous step).

3. Now edit the backup section of the
   `deploy/cr.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/cr.yaml>`__
   file to set proper values for the ``bucket`` (the S3 bucket for
   backups created on the previous step), ``region``,
   ``credentialsSecret`` and the ``endpointUrl`` (which should point to
   the previously created Minio Service).

   ::

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

   The option which should be specially mentioned is
   ``credentialsSecret`` which is a `Kubernetes
   secret <https://kubernetes.io/docs/concepts/configuration/secret/>`__
   for backups. Sample
   `backup-s3.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/backup-s3.yaml>`__
   can be used to create this secret object. Check that the object contains the
   proper ``name`` value and is equal to the one specified for
   ``credentialsSecret``, i.e. \ ``my-cluster-name-backup-minio`` in the
   backup to Minio example, and also contains the proper ``AWS_ACCESS_KEY_ID`` and
   ``AWS_SECRET_ACCESS_KEY`` keys. After you have finished editing the file, the secrets
   object are created or updated when you run the following command:

   .. code:: bash

      $ kubectl apply -f deploy/backup-s3.yaml

4. When the setup process is completed, making the backup is based on a script.
   Following example illustrates how to make an on-demand backup:

   .. code:: bash

          kubectl run -it --rm pbmctl --image=percona/percona-server-mongodb-operator:0.3.0-backup-pbmctl --restart=Never -- \
            run backup \
            --server-address=<cluster-name>-backup-coordinator:10001 \
            --storage <storage> \
            --compression-algorithm=gzip \
            --description=my-backup```

   Don’t forget to specify the name of your cluster instead of the
   ``<cluster-name>`` part of the Backup Coordinator URL (the
   cluster name is specified in the
   `deploy/cr.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/cr.yaml>`__
   file). Also substitute ``<storage>`` with the actual
   storage name located in a subsection inside of the
   ``backups`` in the
   `deploy/cr.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/cr.yaml>`__
   file. In the earlier example this value is ``minio``.

5. To restore a previously saved backup you must specify the
   backup name. With the proper Backup Coordinator URL and storage name, you can obtain a list of the available backups:

   .. code:: bash

         kubectl run -it --rm pbmctl --image=percona/percona-server-mongodb-operator:0.3.0-backup-pbmctl --restart=Never -- list backups --server-address=<cluster-name>-backup-coordinator:10001

   Now, restore the backup, using backup name instead of the
   ``backup-name`` parameter:

   .. code:: bash

         kubectl run -it --rm pbmctl --image=percona/percona-server-mongodb-operator:0.3.0-backup-pbmctl --restart=Never -- \
           run restore \
           --server-address=<cluster-name>-backup-coordinator:10001 \
           --storage <storage> \
           backup-name
