.. _K8SPSMDB-1.6.0:

================================================================================
*Percona Kubernetes Operator for Percona Server for MongoDB* 1.6.0
================================================================================

:Date: December 17, 2020
:Installation: `Installing Percona Kubernetes Operator for Percona Server for MongoDB <https://www.percona.com/doc/kubernetes-operator-for-psmongodb/index.html#installation>`_

New Features
================================================================================

* :jirabug:`K8SPSMDB-273`: Add support for ``mongos`` service to expose a single
  :ref:`shard<shard>` of a MongoDB cluster through one entry point. In the
  following release, we will add support for multiple shards.
* :jirabug:`K8SPSMDB-282`: Official support for :ref:`Percona Monitoring and Management (PMM) v.2<operator.monitoring>`

Improvements
================================================================================

* :jirabug:`K8SPSMDB-258`: Add support for Percona Server for MongoDB version 4.4
* :jirabug:`K8SPSMDB-319`: Show Endpoint in the ``kubectl get psmdb`` command output to connect to a MongoDB cluster easily
* :jirabug:`K8SPSMDB-257`: Store the Operator version as a ``crVersion`` field in the ``deploy/cr.yaml`` configuration file
* :jirabug:`K8SPSMDB-266`: Use plain-text passwords instead of base64-encoded ones when creating :ref:`users.system-users` secrets for simplicity

Bugs Fixed
================================================================================

* :jirabug:`K8SPSMDB-271`: Fix cluster initialization with CloudFlare SSL certificates
* :jirabug:`K8SPSMDB-268`: Fix support of TLS certificates issued by `cert-manager <https://github.com/jetstack/cert-manager>`_
* :jirabug:`K8SPSMDB-261`: Fix a bug in cluster pause/resume functionality
* :jirabug:`K8SPSMDB-292`: Fix a bug due to which not all clusters managed by the Operator were upgraded by the automatic update

Deprecation
================================================================================

* The `MMAPv1 storage engine <https://docs.mongodb.com/manual/core/storage-engines/>`_ is no longer supported for all MongoDB versions starting from this version of the Operator. MMAPv1 was already deprecated by MongoDB for a long time. WiredTiger is the default storage engine since MongoDB 3.2, and MMAPv1 was completely removed in 4.2.
