Users
=====

As it is written in the installation part, the Operator requires
Kubernetes Secrets to be deployed before it is started. The name of the
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

**Warning:** *These users should not be used to run an application.*

+----------+---------------+------------------+------------------------+
| User     | Username      | Password Secret  | MongoDB Role           |
| Purpose  | Secret Key    | Key              |                        |
+==========+===============+==================+========================+
| Backup/R | MONGODB_BACKU | MONGODB_BACKUP_P | `backup <https://docs. |
| estore   | P_USER        | ASSWORD          | mongodb.com/manual/ref |
|          |               |                  | erence/built-in-roles/ |
|          |               |                  | #backup>`__,           |
|          |               |                  | `clusterMonitor <https |
|          |               |                  | ://docs.mongodb.com/ma |
|          |               |                  | nual/reference/built-i |
|          |               |                  | n-roles/#clusterMonito |
|          |               |                  | r>`__,                 |
|          |               |                  | `restore <https://docs |
|          |               |                  | .mongodb.com/manual/re |
|          |               |                  | ference/built-in-roles |
|          |               |                  | /#restore>`__          |
+----------+---------------+------------------+------------------------+
| Cluster  | MONGODB_CLUST | MONGODB_CLUSTER_ | `clusterAdmin <https:/ |
| Admin    | ER_ADMIN_USER | ADMIN_PASSWORD   | /docs.mongodb.com/manu |
|          |               |                  | al/reference/built-in- |
|          |               |                  | roles/#clusterAdmin>`_ |
|          |               |                  | _                      |
+----------+---------------+------------------+------------------------+
| Cluster  | MONGODB_CLUST | MONGODB_CLUSTER_ | `clusterMonitor <https |
| Monitor  | ER_MONITOR_US | MONITOR_PASSWORD | ://docs.mongodb.com/ma |
|          | ER            |                  | nual/reference/built-i |
|          |               |                  | n-roles/#clusterMonito |
|          |               |                  | r>`__                  |
+----------+---------------+------------------+------------------------+
| User     | MONGODB_USER_ | MONGODB_USER_ADM | `userAdmin <https://do |
| Admin    | ADMIN_USER    | IN_PASSWORD      | cs.mongodb.com/manual/ |
|          |               |                  | reference/built-in-rol |
|          |               |                  | es/#userAdmin>`__      |
+----------+---------------+------------------+------------------------+

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

**Warning:** *Do not use the default MongoDB Users in production!*

MongoDB Internal Authentication Key (optional)
----------------------------------------------

*Default Secret name:* ``my-cluster-name-mongodb-key``

*Secret name field:* ``spec.secrets.key``

By default, the operator will create a random, 1024-byte key for
`MongoDB Internal
Authentication <https://docs.mongodb.com/manual/core/security-internal-authentication/>`__
if it does not already exist. If you would like to deploy a different
key, create the secret manually before starting the operator.
