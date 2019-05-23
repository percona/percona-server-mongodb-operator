Enabling Replica Set Arbiter nodes
==================================

Percona Server for MongoDB `replication
model <https://www.percona.com/blog/2018/05/17/mongodb-replica-set-transport-encryption-part-1/>`__
is based on elections, when nodes of the Replica Set `choose which
node <https://docs.mongodb.com/manual/core/replica-set-elections/#replica-set-elections>`__
becomes the primary node. Elections are the reason to avoid an even
number of nodes in the cluster. The cluster should have
at least three nodes. Normally, each node stores a complete copy of the data,
but there is also a possibility, to reduce disk IO and space used by the
database, to add an `arbiter node <https://docs.mongodb.com/manual/core/replica-set-arbiter/>`__. An arbiter cannot become a primary and does not have a complete copy of the data. The arbiter does have one election vote and can be the odd number for elections. The arbiter does not demand a persistent volume.

Percona Server for MongoDB Operator has the ability to create Replica Set Arbiter
nodes if needed. This feature can be configured in the Replica Set
section of the
`deploy/cr.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/cr.yaml>`__
file:

-  set ``arbiter.enabled`` option to ``true`` to allow Arbiter nodes,
-  use ``arbiter.size`` option to set the desired amount of the Replica
   Set nodes which should be Arbiter ones instead of containing data.
