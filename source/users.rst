Users
=====

During installation, the Operator requires
Kubernetes Secrets to be deployed before the Operator is started. The name of the
required secrets can be set in ``deploy/cr.yaml`` under the
``spec.secrets`` section.

Unprivileged users
------------------

There are no unprivileged (general purpose) user accounts created by
default. If you need general purpose users, please run commands below:

.. code:: bash

   $ kubectl run -i --rm --tty percona-client --image=percona/percona-server-mongodb:3.6 --restart=Never -- bash -il
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

   $ kubectl run -i --rm --tty percona-client --image=percona/percona-server-mongodb:3.6 --restart=Never -- bash -il
   mongodb@percona-client:/$ mongo "mongodb+srv://myApp:myAppPassword@my-cluster-name-rs0.psmdb.svc.cluster.local/admin?replicaSet=rs0&ssl=false"
   rs0:PRIMARY> use myApp
   rs0:PRIMARY> db.test.insert({ x: 1 })
   rs0:PRIMARY> db.test.findOne()

MongoDB System Users
--------------------

*Default Secret name:* ``my-cluster-name-mongodb-users``

*Secret name field:* ``spec.secrets.users``

The operator requires system-level MongoDB users to automate the MongoDB
deployment.

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

`Backup/Restore` - MongoDB Role: `backup <https://docs.mongodb.com/manual/reference/built-in-roles/#backup>`__, `clusterMonitor <https://docs.mongodb.com/manual/reference/built-in-roles/#clusterMonitor>`__, `restore <https://docs.mongodb.com/manual/reference/built-in-roles/#restore>`__   

`Cluster Admin` - MongoDB Role: `clusterAdmin <https://docs.mongodb.com/manual/reference/built-in-roles/#clusterAdmin>`__  

`Cluster Monitor` - MongoDB Role: `clusterMonitor <https://docs.mongodb.com/manual/reference/built-in-roles/#clusterMonitor>`__

`User Admin` - MongoDB Role: `userAdmin <https://docs.mongodb.com/manual/reference/built-in-roles/#userAdmin>`__


Development Mode
----------------

To make development and testing easier, ``deploy/mongodb-users.yaml``
secrets file contains default passwords for MongoDB system users.

The development-mode credentials from ``deploy/mongodb-users.yaml`` are:

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
================================ ====================

.. warning:: Do not use the default MongoDB Users in production!

MongoDB Internal Authentication Key (optional)
----------------------------------------------

*Default Secret name:* ``my-cluster-name-mongodb-key``

*Secret name field:* ``spec.secrets.key``

By default, the operator will create a random, 1024-byte key for
`MongoDB Internal
Authentication <https://docs.mongodb.com/manual/core/security-internal-authentication/>`__
if it does not already exist. If you would like to deploy a different
key, create the secret manually before starting the operator.
