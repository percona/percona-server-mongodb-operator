.. _sharding:

Percona Server for MongoDB Sharding
===================================

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

Sharding is controlled by the ``sharding`` section of the ``deploy/cr.yaml``
configuration file and is turned on by default.

To enable sharding, set the ``sharding.enabled`` key ``true`` (this will turn
existing MongoDB replica set nodes into sharded ones).

When the sharding is turned on, the Operator runs replica sets with config
servers and mongos instances. Their numbers are controlled by 
``configsvrReplSet.size`` and ``mongos.size`` keys respectively.

.. note:: Config servers for now can properly work only with WiredTiger engine.
