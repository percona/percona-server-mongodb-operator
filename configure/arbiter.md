Enabling Replica Set Arbiter nodes 
==============================================================

Percona Server for MongoDB [replication model](https://www.percona.com/blog/2018/05/17/mongodb-replica-set-transport-encryption-part-1/) is based on elections, during which nodes of the Replica Set [choose the one of them](https://docs.mongodb.com/manual/core/replica-set-elections/#replica-set-elections) to become a primary node. Elections are the reason to avoid the even number of nodes in the cluster, and that's why the cluster should have at least 3 nodes. Normally each node stores a complete copy of the data, but there is also a possibility to reduce disk IO and space used by the database still remaining the odd member number for elections. [Replica Set Arbiter nodes](https://docs.mongodb.com/manual/core/replica-set-arbiter/) are special members of the cluster which do not replicate data, but only vote in elections for the primary. Obviously, such empty node doesn't demand any persistent volume.

Percona Server for MongoDB Operator allows to create Replica Set Arbiter nodes if needed. This feature can be configured in the Replica Set section of the [deploy/cr.yaml](https://github.com/Percona-Lab/percona-server-mongodb-operator/blob/master/deploy/cr.yaml) file:

* set 'arbiter.enabled' option to 'true' to allow Arbiter nodes,
* use 'arbiter.size' option to set the desired amount of the Replica Set nodes which should be Arbiter ones instead of containing data.
