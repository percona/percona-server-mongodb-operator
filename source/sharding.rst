.. _operator.sharding:

Percona Server for MongoDB Sharding
===================================

About sharding
--------------

`Sharding <https://docs.mongodb.com/manual/reference/glossary/#term-sharding>`_
provides horizontal database scaling, distributing data across multiple MongoDB
Pods. It is useful for large data sets when a single machine's overall
processing speed or storage capacity turns out to be not enough.
Sharding allows splitting data across several machines with a special routing
of each request to the necessary subset of data (so-called *shard*).

A MongoDB Sharding involves the following components:

* ``shard`` - a replica set which contains a subset of data stored in the
  database (similar to a traditional MongoDB replica set),
* ``mongos`` - a query router, which acts as an entry point for client applications,
* ``config servers`` - a replica set to store metadata and configuration
  settings for the sharded database cluster.

.. note:: Percona Distribution for MongoDB Operator 1.6.0 supported only one shard of
   a MongoDB cluster; still, this limited sharding support allowed using
   ``mongos`` as an entry point instead of provisioning a load-balancer per
   replica set node. Multiple shards are supported starting from the Operator
   1.7.0.

Turning sharding on and off
---------------------------

Sharding is controlled by the ``sharding`` section of the ``deploy/cr.yaml``
configuration file and is turned on by default.

To enable sharding, set the ``sharding.enabled`` key to ``true`` (this will turn
existing MongoDB replica set nodes into sharded ones). To disable sharding, set
the ``sharding.enabled`` key to ``false``.

When sharding is turned on, the Operator runs replica sets with config
servers and mongos instances. Their number is controlled by 
``configsvrReplSet.size`` and ``mongos.size`` keys, respectively.

.. note:: Config servers for now can properly work only with WiredTiger engine,
   and sharded MongoDB nodes can use either WiredTiger or InMemory one.

By default :ref:`replsets section<operator.replsets-section>` of the
``deploy/cr.yaml`` configuration file contains only one replica set, ``rs0``.
You can add more replica sets with different names to the ``replsets`` section
in a similar way. Please take into account that having more than one replica set
is possible only with the sharding turned on.

Checking connectivity to sharded and non-sharded cluster
--------------------------------------------------------

With sharding turned on, you have ``mongos`` service as an entry point to access
your database. If you do not use sharding, you have to access ``mongod``
processes of your replica set.

1. Run percona-client and connect its console output to your terminal (running
   it may require some time to deploy the corresponding Pod): 

   .. code:: bash

      $ kubectl run -i --rm --tty percona-client --image=percona/percona-server-mongodb:{{{mongodb44recommended}}} --restart=Never -- bash -il

2. Find the password for the admin user, which you will need to access the
   cluster. Use ``kubectl get secrets`` to see the list of Secrets objects (by
   default Secrets object you are interested in has ``my-cluster-name-secrets``
   name). Then ``kubectl get secret my-cluster-name-secrets -o yaml`` will return
   the YAML file with generated secrets, including the ``MONGODB_USER_ADMIN``
   and ``MONGODB_USER_ADMIN_PASSWORD`` strings:

   .. code:: yaml

      ...
      data:
        ...
        MONGODB_USER_ADMIN_PASSWORD: aDAzQ0pCY3NSWEZ2ZUIzS1I=
        MONGODB_USER_ADMIN_USER: dXNlckFkbWlu

   Here the actual login name and password are base64-encoded, and
   ``echo 'aDAzQ0pCY3NSWEZ2ZUIzS1I=' | base64 --decode`` will bring it back to a
   human-readable form.

3. Now run ``mongo`` tool in the percona-client command shell using the login
   (which is normally ``userAdmin``) and password obtained from the secret.

   - If sharding is turned on, the command will look as follows (with your
     database cluster namespace instead of the ``<namespace name>``
     placeholder).
   
     .. code:: bash

        $ mongo "mongodb://userAdmin:userAdminPassword@my-cluster-name-mongos.<namespace name>.svc.cluster.local/admin?ssl=false"

   - If sharding is turned off, the command will look as follows (with your
     database cluster namespace instead of the ``<namespace name>``
     placeholder).
   
     .. code:: bash

        $ mongo "mongodb+srv://userAdmin:userAdminPassword@my-cluster-name-rs0.<namespace name>.svc.cluster.local/admin?replicaSet=rs0&ssl=false"
