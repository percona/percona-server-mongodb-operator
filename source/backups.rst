Providing Backups
=================

Percona Server for MongoDB Operator allows taking cluster backup in two
ways. *Scheduled backups* are configured in the
`deploy/cr.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/cr.yaml>`__
file to be executed automatically at the selected time. *On-demand backups*
can be done manually at any moment. Both ways use the `Percona
Backup for
MongoDB <https://github.com/percona/percona-backup-mongodb>`__ tool.

The backup process is controlled by the `Backup
Coordinator <https://github.com/percona/percona-backup-mongodb#coordinator>`__
daemon residing in the Kubernetes cluster alongside the Percona Server
for MongoDB, while actual backup images are stored separately on any
`Amazon S3 or S3-compatible
storage <https://en.wikipedia.org/wiki/Amazon_S3#S3_API_and_competing_services>`__.

Making scheduled backups
------------------------

Since backups are stored separately on the Amazon S3, a secret with
``AWS_ACCESS_KEY_ID`` and ``AWS_SECRET_ACCESS_KEY`` should be present on
the Kubernetes cluster. These keys should be saved to the
`deploy/backup-s3.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/backup-s3.yaml>`__
file and applied with the appropriate command,
e.g. \ ``kubectl apply -f deploy/backup-s3.yaml`` (for Kubernetes).

A backup schedule is defined in the ``backup`` section of the
`deploy/cr.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/cr.yaml>`__
file. This section contains three subsections:

  * ``storages`` contains data needed to access the S3-compatible cloud to store backups.
  * ``coordinator`` configures the Kubernetes limits and claims for the Percona Backup for MongoDB Coordinator daemon.
  * ``tasks`` schedules backups (the schedule is specified in crontab format).

This example uses Amazon S3 storage for backups:

::

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

.. note:: If you use some S3-compatible storage instead of the original
   Amazon S3, one more key is needed in the ``s3`` subsection: the
   ``endpointUrl``, which points to the actual cloud used for backups and
   is specific to the cloud provider. 

   For example, the `Google
   Cloud <https://cloud.google.com>`__ key is the following::

      endpointUrl: https://storage.googleapis.com


The options within these three subsections are further explained in the
`Operator Options <operator.html>`_.

One option which should be mentioned separately is
``credentialsSecret`` which is a `Kubernetes
secret <https://kubernetes.io/docs/concepts/configuration/secret/>`__
for backups. The Sample
`backup-s3.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/backup-s3.yaml>`__
can be used as a template to create this secret object. Verify that the yaml contains the proper
``name`` value which must be equal to the one specified for ``credentialsSecret``,
i.e. \ ``my-cluster-name-backup-s3`` for example, and also
proper ``AWS_ACCESS_KEY_ID`` and ``AWS_SECRET_ACCESS_KEY`` keys. After
editing is finished, secrets object should be created or updated using the following command:

.. code:: bash

   $ kubectl apply -f deploy/backup-s3.yaml

Making on-demand backup
-----------------------

To make on-demand backup, user should run the `PBM Control
tool <https://github.com/percona/percona-backup-mongodb#pbm-control-pbmctl>`__
inside of the coordinator container, supplying it with needed options,
like in the following example:

.. code:: bash

       kubectl run -it --rm pbmctl --image=percona/percona-server-mongodb-operator:0.3.0-backup-pbmctl --restart=Never -- \
         run backup \
         --server-address=<cluster-name>-backup-coordinator:10001 \
         --storage <storage> \
         --compression-algorithm=gzip \
         --description=my-backup```

Don’t forget to specify the name of your cluster instead of the
``<cluster-name>`` part of the Backup Coordinator URL (the same cluster
name is specified in the
`deploy/cr.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/cr.yaml>`__
file). Also the ``<storage>`` placeholder should be substituted with the storage
name, which is located in the  ``backups\storages`` subsection in
`deploy/cr.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/cr.yaml>`__
file.

Restore the cluster from a previously saved backup
--------------------------------------------------

To restore a previously saved backup you must specify the backup
name. A list of the available backups can be obtained from the Backup
Coordinator as follows (you must use the correct Backup
Coordinator’s URL and the correct storage name for your environment):

.. code:: bash

      kubectl run -it --rm pbmctl --image=percona/percona-server-mongodb-operator:0.3.0-backup-pbmctl --restart=Never -- list backups --server-address=<cluster-name>-backup-coordinator:10001

Now, restore the backup, substituting the cluster-name and storage values and using the selected backup name instead of ``backup-name``:

.. code:: bash

      kubectl run -it --rm pbmctl --image=percona/percona-server-mongodb-operator:0.3.0-backup-pbmctl --restart=Never -- \
        run restore \
        --server-address=<cluster-name>-backup-coordinator:10001 \
        --storage <storage> \
        backup-name
