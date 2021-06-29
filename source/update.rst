.. _operator-updates:

Update Percona Distribution for MongoDB Operator
================================================

Starting from the version 1.1.0 the Percona Distribution for MongoDB Operator
allows upgrades to newer versions. This includes upgrades of the
Operator itself, and upgrades of the Percona Server for MongoDB.

.. _operator-update:

Upgrading the Operator
----------------------

This upgrade can be done either in semi-automatic or in manual mode.

.. note:: Manual update mode is the recommended way for a production cluster.

.. _operator-update-semi-auto-updates:

Semi-automatic upgrade
**********************

.. note:: Only the incremental update to a nearest minor version is supported
   (for example, update from 1.5.0 to 1.6.0).
   To update to a newer version, which differs from the current version by more
   than one, make several incremental updates sequentially.

#. Update the Custom Resource Definition file for the Operator, taking it from
   the official repository on Github, and do the same for the Role-based access
   control:

   .. code:: bash

      kubectl apply -f https://raw.githubusercontent.com/percona/percona-server-mongodb-operator/v{{{release}}}/deploy/crd.yaml
      kubectl apply -f https://raw.githubusercontent.com/percona/percona-server-mongodb-operator/v{{{release}}}/deploy/rbac.yaml

#. Edit the ``deploy/cr.yaml`` file, setting ``updateStrategy`` key to
   ``RollingUpdate``.

#. Now you should `apply a patch <https://kubernetes.io/docs/tasks/run-application/update-api-object-kubectl-patch/>`_ to your
   deployment, supplying necessary image names with a newer version tag. This
   is done with the ``kubectl patch deployment`` command. For example, updating
   to the ``{{{release}}}`` version should look as follows::

     kubectl patch deployment percona-server-mongodb-operator \
        -p'{"spec":{"template":{"spec":{"containers":[{"name":"percona-server-mongodb-operator","image":"percona/percona-server-mongodb-operator:{{{release}}}"}]}}}}'

     kubectl patch psmdb my-cluster-name --type=merge --patch '{
        "spec": {
            "crVersion":"{{{release}}}",
            "image": "percona/percona-server-mongodb:{{{mongodb44recommended}}}",
            "backup": { "image": "percona/percona-server-mongodb-operator:{{{release}}}-backup" },
            "pmm": { "image": "percona/pmm-client:{{{pmm2recommended}}}" }
        }}'

#. The deployment rollout will be automatically triggered by the applied patch.
   You can track the rollout process in real time using the
   ``kubectl rollout status`` command with the name of your cluster::

     kubectl rollout status sts my-cluster-name-rs0

.. _operator-update-manual-updates:

Manual upgrade
**************

.. note:: Only the incremental update to a nearest minor version of the Operator
   is supported (for example, update from 1.5.0 to 1.6.0).
   To update to a newer version, which differs from the current version by more
   than one, make several incremental updates sequentially.

#. Update the Custom Resource Definition file for the Operator, taking it from
   the official repository on Github, and do the same for the Role-based access
   control:

   .. code:: bash

      kubectl apply -f https://raw.githubusercontent.com/percona/percona-server-mongodb-operator/v{{{release}}}/deploy/crd.yaml
      kubectl apply -f https://raw.githubusercontent.com/percona/percona-server-mongodb-operator/v{{{release}}}/deploy/rbac.yaml

#. Edit the ``deploy/cr.yaml`` file, setting ``updateStrategy`` key to
   ``OnDelete``.

#. Now you should `apply a patch <https://kubernetes.io/docs/tasks/run-application/update-api-object-kubectl-patch/>`_ to your
   deployment, supplying necessary image names with a newer version tag. This
   is done with the ``kubectl patch deployment`` command. For example, updating
   to the ``{{{release}}}`` version should look as follows::

     kubectl patch deployment percona-server-mongodb-operator \
        -p'{"spec":{"template":{"spec":{"containers":[{"name":"percona-server-mongodb-operator","image":"percona/percona-server-mongodb-operator:{{{release}}}"}]}}}}'

     kubectl patch psmdb my-cluster-name --type=merge --patch '{
        "spec": {
            "crVersion":"{{{release}}}",
            "image": "percona/percona-server-mongodb:{{{mongodb44recommended}}}",
            "backup": { "image": "percona/percona-server-mongodb-operator:{{{release}}}-backup" },
            "pmm": { "image": "percona/pmm-client:{{{pmm2recommended}}}" }
        }}'

#. Pod with the newer Percona Server for MongoDB image will start after you
   delete it. Delete targeted Pods manually one by one to make them restart in
   the desired order:

   #. Delete the Pod using its name with the command like the following one::

         kubectl delete pod my-cluster-name-rs0-2


   #. Wait until Pod becomes ready::

         kubectl get pod my-cluster-name-rs0-2


      The output should be like this::

         NAME                    READY   STATUS    RESTARTS   AGE
         my-cluster-name-rs0-2   1/1     Running   0          3m33s

#. The update process is successfully finished when all Pods have been
   restarted.

.. _operator-update-smartupdates:

Upgrading Percona Server for MongoDB
------------------------------------

Starting from version 1.5.0, the Operator can do fully automatic upgrades to
the newer versions of Percona Server for MongoDB within the method named *Smart
Updates*.

To have this upgrade method enabled, make sure that the ``updateStrategy`` key
in the ``deploy/cr.yaml`` configuration file is set to ``SmartUpdate``.

When automatic updates are enabled, the Operator will carry on upgrades
according to the following algorithm. It will query a special *Version Service* 
server at scheduled times to obtain fresh information about version numbers and
valid image paths needed for the upgrade. If the current version should be
upgraded, the Operator updates the CR to reflect the new image paths and carries
on sequential Pods deletion in a safe order, allowing StatefulSet to redeploy
the cluster Pods with the new image.

.. note:: Being enabled, Smart Update will force the Operator to take MongoDB
   version from Version Service and not from the ``mongod.image`` option during
   the very first start of the cluster.

The upgrade details are set in the ``upgradeOptions`` section of the 
``deploy/cr.yaml`` configuration file. Make the following edits to configure
updates:

#. Set the ``apply`` option to one of the following values:

   * ``Recommended`` - automatic upgrade will choose the most recent version
     of software flagged as Recommended (for clusters created from scratch,
     the Percona Server for MongoDB 4.4 version will be selected instead of the
     Percona Server for MongoDB 4.2 or 4.0 version regardless of the image
     path; for already existing clusters, the 4.4 vs. 4.2 or 4.0 branch
     choice will be preserved),
   * ``4.4-recommended``, ``4.2-recommended``, ``4.0-recommended`` -
     same as above, but preserves specific major MongoDB
     version for newly provisioned clusters (ex. 4.4 will not be automatically
     used instead of 4.2),
   * ``Latest`` - automatic upgrade will choose the most recent version of
     the software available (for clusters created from scratch,
     the Percona Server for MongoDB 4.4 version will be selected instead of the
     Percona Server for MongoDB 4.2 or 4.0 version regardless of the image
     path; for already existing clusters, the 4.4 vs. 4.2 or 4.0 branch
     choice will be preserved),
   * ``4.4-latest``, ``4.2-latest``, ``4.0-latest`` - same as
     above, but preserves specific major MongoDB version for newly provisioned
     clusters (ex. 4.4 will not be automatically used instead of 4.2),
   * *version number* - specify the desired version explicitly
     (version numbers are specified as {{{mongodb44recommended}}},
     {{{mongodb42recommended}}}, etc.),
   * ``Never`` or ``Disabled`` - disable automatic upgrades.

     .. note:: When automatic upgrades are disabled by the ``apply`` option, 
        Smart Update functionality will continue working for changes triggered
        by other events, such as rotating a password, or
        changing resource values.

#. Make sure the ``versionServiceEndpoint`` key is set to a valid Version
   Server URL (otherwise Smart Updates will not occur).

   A. You can use the URL of the official Percona's Version Service (default).
      Set ``versionServiceEndpoint`` to ``https://check.percona.com``.

   B. Alternatively, you can run Version Service inside your cluster. This
      can be done with the ``kubectl`` command as follows:
      
      .. code:: bash
      
         kubectl run version-service --image=perconalab/version-service --env="SERVE_HTTP=true" --port 11000 --expose

   .. note:: Version Service is never checked if automatic updates are disabled.
      If automatic updates are enabled, but Version Service URL can not be
      reached, upgrades will not occur.

#. Use the ``schedule`` option to specify the update checks time in CRON format.

The following example sets the midnight update checks with the official
Percona's Version Service:

.. code:: yaml

   spec:
     updateStrategy: SmartUpdate
     upgradeOptions:
       apply: Recommended
       versionServiceEndpoint: https://check.percona.com
       schedule: "0 0 * * *"
   ...

.. _operator-update-smartupdates-major:

Percona Server for MongoDB major version upgrades
*************************************************

Normally automatic upgrade takes place within minor versions (for example,
from ``4.2.11-12`` to ``4.2.12-13``) of MongoDB. Major versions upgrade (for
example moving from ``4.2-recommended`` to ``4.4-recommended``) is more
complicated task which might potentially affect how data is stored and how
applications interacts with the database (in case of some API changes). 

Such upgrade is supported by the Operator within one major version at a time:
for example, to change Percona Server for MongoDB major version from 4.0 to 4.4,
you should first upgrade it to 4.2, and later make a separate upgrade from 4.2
to 4.4. The same is true for major version downgrades.

.. note:: It is recommended to take a backup before upgrade, as well as to
   perform upgrade on staging environment.

Major version upgrade can be initiated using the :ref:`upgradeOptions.apply<upgradeoptions-apply>`
key in the ``deploy/cr.yaml`` configuration file:

.. code:: yaml

   spec:
     upgradeOptions:
       apply: 4.4-recommended

.. note:: When making downgrades (e.g. changing version from 4.4 to 4.2), make
   sure to remove incompatible features that are persisted and/or update
   incompatible configuration settings. Compatibility issues between major
   MongoDB versions can be found in `upstream documentation <https://docs.mongodb.com/manual/release-notes/4.4-downgrade-standalone/#prerequisites>`_.

By default the Operator doesn't set `FeatureCompatibilityVersion (FCV) <https://docs.mongodb.com/manual/reference/command/setFeatureCompatibilityVersion/>`_
to match the new version, thus making sure that backwards-incompatible features
are not automatically enabled with the major version upgrade (which is
recommended and safe behavior). You can turn this backward compatibility off at
any moment (after the upgrade or even before it) by setting the :ref:`upgradeOptions.setFCV<upgradeoptions-setfcv>` flag in the
``deploy/cr.yaml`` configuration file to ``true``.

.. note:: With setFeatureCompatibilityVersion set major version rollback is not
   currently supported by the Operator. Therefore it is recommended to stay
   without enabling this flag for some time after the major upgrade to ensure
   the likelihood of downgrade is minimal. Setting ``setFCV`` flag to ``true``
   simultaneously with the ``apply`` flag should be done only if the whole
   procedure is tested on staging and you are 100% sure about it.
