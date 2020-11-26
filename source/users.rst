.. _users:

Users
=====

MongoDB user accounts within the Cluster can be divided into two different groups:

* *application-level users*: the unprivileged user accounts,
* *system-level users*: the accounts needed to automate the cluster deployment
  and management tasks, such as MongoDB Health checks.

As these two groups of user accounts serve different purposes, they are
considered separately in the following sections.

.. contents:: :local:

.. _users.unprivileged-users:

`Unprivileged users <users.html#unprivileged-users>`_
------------------------------------------------------

There are no unprivileged (general purpose) user accounts created by
default. If you need general purpose users, please run commands below:

.. code:: bash

   $ kubectl run -i --rm --tty percona-client --image=percona/percona-server-mongodb:{{{mongodb42recommended}}} --restart=Never -- bash -il
   mongodb@percona-client:/$ mongo "mongodb+srv://userAdmin:userAdmin123456@my-cluster-name-rs0.psmdb.svc.cluster.local/admin?replicaSet=rs0&ssl=false"
   rs0:PRIMARY> db.createUser({
       user: "myApp",
       pwd: "myAppPassword",
       roles: [
         { db: "myApp", role: "readWrite" }
       ],
       mechanisms: [
          "SCRAM-SHA-1"
       ]

   })

Now check the newly created user:

.. code:: bash

   $ kubectl run -i --rm --tty percona-client --image=percona/percona-server-mongodb:{{{mongodb42recommended}}} --restart=Never -- bash -il
   mongodb@percona-client:/$ mongo "mongodb+srv://myApp:myAppPassword@my-cluster-name-rs0.psmdb.svc.cluster.local/admin?replicaSet=rs0&ssl=false"
   rs0:PRIMARY> use myApp
   rs0:PRIMARY> db.test.insert({ x: 1 })
   rs0:PRIMARY> db.test.findOne()

.. _users.system-users:

`System Users <users.html#system-users>`_
-----------------------------------------

To automate the deployment and management of the cluster components,
the Operator requires system-level MongoDB users.

During installation, the Operator requires
Kubernetes Secrets to be deployed before the Operator is started. The name of the
required secrets can be set in ``deploy/cr.yaml`` under the
``spec.secrets`` section.

*Default Secret name:* ``my-cluster-name-secrets``

*Secret name field:* ``spec.secrets.users``

.. warning:: These users should not be used to run an application.

.. list-table::
   :header-rows: 1

   * - User Purpose
     - Username Secret Key
     - Password Secret Key

   * - Backup/Restore
     - MONGODB_BACKUP_USER
     - MONGODB_BACKUP_PASSWORD

   * - Cluster Admin
     - MONGODB_CLUSTER_ADMIN_USER
     - MONGODB_CLUSTER_ADMIN_PASSWORD

   * - Cluster Monitor
     - MONGODB_CLUSTER_MONITOR_USER
     - MONGODB_CLUSTER_MONITOR_PASSWORD

   * - User Admin
     - MONGODB_USER_ADMIN_USER
     - MONGODB_USER_ADMIN_PASSWORD

   * - PMM Server
     - PMM_SERVER_USER
     - PMM_SERVER_PASSWORD

`Backup/Restore` - MongoDB Role: `backup <https://docs.mongodb.com/manual/reference/built-in-roles/#backup>`__, `clusterMonitor <https://docs.mongodb.com/manual/reference/built-in-roles/#clusterMonitor>`__, `restore <https://docs.mongodb.com/manual/reference/built-in-roles/#restore>`__

`Cluster Admin` - MongoDB Role: `clusterAdmin <https://docs.mongodb.com/manual/reference/built-in-roles/#clusterAdmin>`__

`Cluster Monitor` - MongoDB Role: `clusterMonitor <https://docs.mongodb.com/manual/reference/built-in-roles/#clusterMonitor>`__

`User Admin` - MongoDB Role: `userAdmin <https://docs.mongodb.com/manual/reference/built-in-roles/#userAdmin>`__

YAML Object Format
******************

The default name of the Secrets object for these users is
``my-cluster-name-secrets`` and can be set in the CR for your cluster in
``spec.secrets.users`` to something different. When you create the object
yourself, it should match the following simple format:

.. code:: yaml

   apiVersion: v1
   kind: Secret
   metadata:
     name: my-cluster-name-secrets
   type: Opaque
   stringData:
     MONGODB_BACKUP_USER: backup
     MONGODB_BACKUP_PASSWORD: backup123456
     MONGODB_CLUSTER_ADMIN_USER: clusterAdmin
     MONGODB_CLUSTER_ADMIN_PASSWORD: clusterAdmin123456
     MONGODB_CLUSTER_MONITOR_USER: clusterMonitor
     MONGODB_CLUSTER_MONITOR_PASSWORD: clusterMonitor123456
     MONGODB_USER_ADMIN_USER: userAdmin
     MONGODB_USER_ADMIN_PASSWORD: userAdmin123456
     PMM_SERVER_USER: pmm
     PMM_SERVER_PASSWORD: supa|^|pazz

The example above matches
:ref:`what is shipped in deploy/secrets.yaml<users.development-mode>` which
contains default passwords. You should NOT use these in production, but they are
present to assist in automated testing or simple use in a development
environment.

As you can see, because we use the ``stringData`` type in the Secrets object, all
values for each key/value pair are stated in plain text format for the user convenience.
If you want to update any field, you'll need to encode the value into base64 format.
To do this you can simply run ``echo -n "password" | base64`` in your local shell to get valid
values.

.. note:: The operator creates and updates an additional Secrets object named
   based on the cluster name, like ``internal-my-cluster-name-users``. It is
   used only by the Operator and should undergo no manual changes by the user.
   This object contains secrets with the same passwords as the one specified
   in ``spec.secrets.users`` (e.g. ``my-cluster-name-secrets``). When the user
   updates ``my-cluster-name-secrets``, the Operator propagates these changes to
   the internal ``internal-my-cluster-name-users`` Secrets object.

Password Rotation Policies and Timing
*************************************

When there is a change in user secrets, the Operator
creates the necessary transaction to change passwords. This rotation happens
almost instantly (the delay can be up to a few seconds), and it's not needed to
take any action beyond changing the password.

.. note:: Please don't change ``secrets.users`` option in CR, make changes
   inside the secrets object itself.

.. _users.development-mode:

`Development Mode <users.html#development-mode>`_
--------------------------------------------------

To make development and testing easier, ``deploy/secrets.yaml`` secrets
file contains default passwords for MongoDB system users.

These development-mode credentials from ``deploy/secrets.yaml`` are:

================================ ====================
Secret Key                       Secret Value
================================ ====================
MONGODB_BACKUP_USER              backup
MONGODB_BACKUP_PASSWORD          backup123456
MONGODB_CLUSTER_ADMIN_USER       clusterAdmin
MONGODB_CLUSTER_ADMIN_PASSWORD   clusterAdmin123456
MONGODB_CLUSTER_MONITOR_USER     clusterMonitor
MONGODB_CLUSTER_MONITOR_PASSWORD clusterMonitor123456
MONGODB_USER_ADMIN_USER          userAdmin
MONGODB_USER_ADMIN_PASSWORD      userAdmin123456
PMM_SERVER_USER                  pmm
PMM_SERVER_PASSWORD              supa|^|pazz
================================ ====================

.. warning:: Do not use the default MongoDB Users in production!

.. _users.internal-authentication-key:

`MongoDB Internal Authentication Key (optional) <users.html#internal-authentication-key>`_
-------------------------------------------------------------------------------------------

*Default Secret name:* ``my-cluster-name-mongodb-key``

*Secret name field:* ``spec.secrets.key``

By default, the operator will create a random, 1024-byte key for
`MongoDB Internal
Authentication <https://docs.mongodb.com/manual/core/security-internal-authentication/>`__
if it does not already exist. If you would like to deploy a different
key, create the secret manually before starting the operator.
