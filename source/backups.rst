Providing Backups
=================

The Operator usually stores Server for MongoDB backups on `Amazon S3 or S3-compatible storage <https://en.wikipedia.org/wiki/Amazon_S3#S3_API_and_competing_services>`_ outside the Kubernetes cluster:

.. figure:: assets/images/backup-s3.*
   :align: center
   :alt: Backup on S3-compatible storage

The Operator allows doing cluster backup in two
ways. *Scheduled backups* are configured in the
`deploy/cr.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/main/deploy/cr.yaml>`_
file to be executed automatically in proper time. *On-demand backups*
can be done manually at any moment. Both ways use the `Percona
Backup for MongoDB <https://github.com/percona/percona-backup-mongodb>`_ tool.

.. contents:: :local:

.. _backups.scheduled:

Making scheduled backups
------------------------

Since backups are stored separately on the Amazon S3, a secret with
``AWS_ACCESS_KEY_ID`` and ``AWS_SECRET_ACCESS_KEY`` should be present on
the Kubernetes cluster. The secrets file with these base64-encoded keys should
be created: for example ``deploy/backup-s3.yaml`` file with the following
contents.

.. code:: yaml

   apiVersion: v1
   kind: Secret
   metadata:
     name: my-cluster-name-backup-s3
   type: Opaque
   data:
     AWS_ACCESS_KEY_ID: UkVQTEFDRS1XSVRILUFXUy1BQ0NFU1MtS0VZ
     AWS_SECRET_ACCESS_KEY: UkVQTEFDRS1XSVRILUFXUy1TRUNSRVQtS0VZ

.. note:: The following command can be used to get a base64-encoded string from
   a plain text one: ``$ echo -n 'plain-text-string' | base64``

The ``name`` value is the `Kubernetes
secret <https://kubernetes.io/docs/concepts/configuration/secret/>`_
name which will be used further, and ``AWS_ACCESS_KEY_ID`` and
``AWS_SECRET_ACCESS_KEY`` are the keys to access S3 storage (and
obviously they should contain proper values to make this access
possible). To have effect secrets file should be applied with the
appropriate command to create the secret object,
e.g. ``kubectl apply -f deploy/backup-s3.yaml`` (for Kubernetes).

Backups schedule is defined in the ``backup`` section of the
`deploy/cr.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/main/deploy/cr.yaml>`__
file. This section contains following subsections:

* ``storages`` subsection contains data needed to access the S3-compatible cloud
  to store backups.
* ``tasks`` subsection allows to actually schedule backups (the schedule is
  specified in crontab format).

Here is an example of `deploy/cr.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/main/deploy/cr.yaml>`__ which uses Amazon S3 storage for backups:

.. code:: yaml

   ...
   backup:
     enabled: true
     ...
     storages:
       s3-us-west:
         type: s3
         s3:
           bucket: S3-BACKUP-BUCKET-NAME-HERE
           region: us-west-2
           credentialsSecret: my-cluster-name-backup-s3
     ...
     tasks:
      - name: "sat-night-backup"
        schedule: "0 0 * * 6"
        keep: 3
        storageName: s3-us-west
     ...

if you use some S3-compatible storage instead of the original
Amazon S3, the `endpointURL <https://docs.min.io/docs/aws-cli-with-minio.html>`_ is needed in the ``s3`` subsection which points to the actual cloud used for backups and
is specific to the cloud provider. For example, using `Google Cloud <https://cloud.google.com>`_ involves the `following <https://storage.googleapis.com>`_ endpointUrl:

.. code:: yaml

   endpointUrl: https://storage.googleapis.com

The options within these three subsections are further explained in the
:ref:`Operator Custom Resource options<operator.backup-section>`.

One option which should be mentioned separately is
``credentialsSecret`` which is a `Kubernetes
secret <https://kubernetes.io/docs/concepts/configuration/secret/>`_
for backups. Value of this key should be the same as the name used to
create the secret object (``my-cluster-name-backup-s3`` in the last
example).

The schedule is specified in crontab format as explained in
:ref:`Operator Custom Resource options<operator.backup-section>`.

.. _backups-manual:

Making on-demand backup
-----------------------

To make an on-demand backup, the user should first configure the backup storage
in the ``backup.storages`` subsection of the ``deploy/cr.yaml`` configuration
file in a same way it was done for scheduled backups. When the
``deploy/cr.yaml`` file contains correctly configured storage and is applied
with ``kubectl`` command, use *a special backup configuration YAML file* with
the following contents:

* **backup name** in the ``metadata.name`` key,
* **Percona Server for MongoDB Cluster name** in the ``spec.psmdbCluster`` key,
* **storage name** from ``deploy/cr.yaml`` in the ``spec.storageName`` key.

 The example of such file is
`deploy/backup/backup.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/main/deploy/backup/backup.yaml>`_.

When the backup destination is configured and applied with `kubectl apply -f deploy/cr.yaml` command, the actual backup command is executed:

.. code:: bash

   kubectl apply -f deploy/backup/backup.yaml

.. note:: Storing backup settings in a separate file can be replaced by
   passing its content to the ``kubectl apply`` command as follows:

   .. code:: bash

      cat <<EOF | kubectl apply -f-
      apiVersion: psmdb.percona.com/v1
      kind: PerconaServerMongoDBBackup
      metadata:
        name: backup1
      spec:
        psmdbCluster: my-cluster-name
        storageName: s3-us-west
      EOF

.. _backups-pitr-oplog:

Storing operations logs for point-in-time recovery
--------------------------------------------------

Point-in-time recovery functionality allows users to roll back the cluster to a
specific date and time.
Technically, this feature involves saving operations log updates to
the S3-compatible backup storage.

To be used, it requires setting the
:ref:`backup.pitr.enabled<backup-pitr-enabled>` key in the ``deploy/cr.yaml``
configuration file:

.. code:: yaml

   backup:
     ...
     pitr:
       enabled: true

.. note:: It is necessary to have at least one full backup to use point-in-time
   recovery. Percona Backup for MongoDB will not upload operations logs if there
   is no full backup. This is true for new clusters and also true for clusters
   which have been just recovered from backup.


Percona Backup for MongoDB uploads operations logs to the same bucket where
full backup is stored. This makes point-in-time recovery functionality available
only if there is a single bucket in :ref:`spec.backup.storages<backup-storages-type>`.
Otherwise point-in-time recovery will not be enabled and there will be an error
message in the operator logs.

.. note:: Adding a new bucket when point-in-time recovery is enabled will not
   break it, but put error message about the additional bucket in the operator
   logs as well.

.. _backups-restore:

Restore the cluster from a previously saved backup
--------------------------------------------------

Backup can be restored not only on the Kubernetes cluster where it was made, but
also on any Kubernetes-based environment with the installed Operator.

.. note:: When restoring to a new Kubernetes-based environment, make sure it
   has a Secrets object with the same user passwords as in the original cluster.
   More details about secrets can be found in :ref:`users.system-users`.

Following things are needed to restore a previously saved backup:

* Make sure that the cluster is running.

* Find out correct names for the **backup** and the **cluster**. Available
  backups can be listed with the following command:

  .. code:: bash

     kubectl get psmdb-backup

  .. note:: Obviously, you can make this check only on the same cluster on
     which you have previously made the backup.

  And the following command will list available clusters:

  .. code:: bash

     kubectl get psmdb

.. _backups-no-pitr-restore:

Restoring without point-in-time recovery
****************************************

When the correct names for the backup and the cluster are known, backup
restoration can be done in the following way.

1. Set appropriate keys in the `deploy/backup/restore.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/main/deploy/backup/restore.yaml>`_ file.

   * set ``spec.clusterName`` key to the name of the target cluster to restore
     the backup on,
   * if you are restoring backup on the *same* Kubernetes-based cluster you have
      used to save this backup, set ``spec.backupName`` key to the name of your
      backup,
   * if you are restoring backup on the Kubernetes-based cluster *different*
     from one you have used to save this backup, set ``spec.backupSource``
     subsection instead of ``spec.backupName`` field to point on the appropriate
     S3-compatible storage. This ``backupSource`` subsection should contain
     a ``destination`` key equal to the s3 bucket with a special ``s3://``
     prefix, followed by necessary S3 configuration keys, same as in
     ``deploy/cr.yaml`` file:

     .. code-block:: yaml

        ...
        backupSource:
          destination: s3://S3-BUCKET-NAME/BACKUP-NAME
          s3:
            credentialsSecret: my-cluster-name-backup-s3
            region: us-west-2
            endpointUrl: https://URL-OF-THE-S3-COMPATIBLE-STORAGE
   * you can also use a ``storageName`` key to specify the exact name of the
     storage (the actual storage should be already defined in the
     ``backup.storages`` subsection of the ``deploy/cr.yaml`` file):

     .. code-block:: yaml

        ...
        storageName: s3-us-west
        backupSource:
          destination: s3://S3-BUCKET-NAME/BACKUP-NAME

2. After that, the actual restoration process can be started as follows:

   .. code:: bash

      kubectl apply -f deploy/backup/restore.yaml

.. note:: Storing backup settings in a separate file can be replaced by
   passing its content to the ``kubectl apply`` command as follows:

   .. code:: bash

      cat <<EOF | kubectl apply -f-
      apiVersion: psmdb.percona.com/v1
      kind: PerconaServerMongoDBRestore
      metadata:
        name: restore1
      spec:
        clusterName: my-cluster-name
        backupName: backup1
      EOF

.. _backups-pitr-restore:

Restoring backup with point-in-time recovery
********************************************

Following steps are needed to roll back the cluster to a specific date and time:

1. Set appropriate keys in the `deploy/backup/restore.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/main/deploy/backup/restore.yaml>`_ file.

   * set ``spec.clusterName`` key to the name of the target cluster to restore
     the backup on,
   * put additional restoration parameters to the ``pitr`` section:

   .. code:: yaml

      ...
      spec:
        clusterName: my-cluster-name
        pitr:
          type: date
          date: YYYY-MM-DD hh:mm:ss

   * if you are restoring backup on the *same* Kubernetes-based cluster you have
      used to save this backup, set ``spec.backupName`` key to the name of your
      backup,
   * if you are restoring backup on the Kubernetes-based cluster *different*
     from one you have used to save this backup, set ``spec.backupSource``
     subsection instead of ``spec.backupName`` field to point on the appropriate
     S3-compatible storage. This ``backupSource`` subsection should contain
     a ``destination`` key equal to the s3 bucket with a special ``s3://``
     prefix, followed by necessary S3 configuration keys, same as in
     ``deploy/cr.yaml`` file:

     .. code-block:: yaml

        ...
        backupSource:
          destination: s3://S3-BUCKET-NAME/BACKUP-NAME
          s3:
            credentialsSecret: my-cluster-name-backup-s3
            region: us-west-2
            endpointUrl: https://URL-OF-THE-S3-COMPATIBLE-STORAGE
   * you can also use a ``storageName`` key to specify the exact name of the
     storage (the actual storage should be already defined in the
     ``backup.storages`` subsection of the ``deploy/cr.yaml`` file):

     .. code-block:: yaml

        ...
        storageName: s3-us-west
        backupSource:
          destination: s3://S3-BUCKET-NAME/BACKUP-NAME

2. Run the actual restoration process:

   .. code:: bash

      kubectl apply -f deploy/backup/restore.yaml

   .. note:: Storing backup settings in a separate file can be replaced by
      passing its content to the ``kubectl apply`` command as follows:

      .. code:: bash

            cat <<EOF | kubectl apply -f-
            apiVersion: psmdb.percona.com/v1
            kind: PerconaServerMongoDBRestore
            metadata:
              name: restore1
            spec:
              clusterName: my-cluster-name
              backupName: backup1
              pitr:
                type: date
                date: YYYY-MM-DD hh:mm:ss
            EOF

Delete the unneeded backup
--------------------------

The maximum amount of stored backups is controlled by the
:ref:`backup.tasks.keep<backup-tasks-keep>` option (only successful backups are
counted). Older backups are automatically deleted, so that amount of stored
backups do not exceed this number. Setting ``keep=0`` or removing this option
from ``deploy/cr.yaml`` disables automatic deletion of backups.

Manual deleting of a previously saved backup requires not more than the backup
name. This name can be taken from the list of available backups returned
by the following command:

.. code:: bash

   kubectl get psmdb-backup

When the name is known, backup can be deleted as follows:

.. code:: bash

   kubectl delete psmdb-backup/<backup-name>
