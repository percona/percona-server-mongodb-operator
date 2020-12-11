.. _K8SPSMDB-1.6.0:

================================================================================
*Percona Kubernetes Operator for Percona Server for MongoDB* 1.6.0
================================================================================

:Date: December 17, 2020
:Installation: `Installing Percona Kubernetes Operator for Percona Server for MongoDB <https://www.percona.com/doc/kubernetes-operator-for-psmongodb/index.html#installation>`_

Starting from this version, the already deprecated `MMAPv1 storage engine <https://docs.mongodb.com/manual/core/storage-engines/>`_
for Percona Server for MongoDB 3.6 and 4.0 is not supported by the Operator.

New Features
================================================================================

* :jirabug:`K8SPSMDB-273`: Initial :ref:`sharding` support: for now it is limited
   by one Replica Set, but it already provides ``mongos`` instance as a single
   entry point
* :jirabug:`K8SPSMDB-282`: Official support for :ref:`Percona Monitoring and Management<operator.monitoring>`

Improvements
================================================================================

* :jirabug:`K8SPSMDB-258`: Percona Server for MongoDB version 4.4 is now supported
* :jirabug:`K8SPSMDB-319`: Show Endpoint to connect to a MongoDB cluster in the ``kubectl get psmdb`` command output
* :jirabug:`K8SPSMDB-257`: Store the Operator versiuon as a ``crVersion`` field in the ``deploy/cr.yaml`` configuration file
* :jirabug:`K8SPSMDB-266`: Use plain-text passwords instead of base64-encoded ones when creating :ref:`users.system-users` secrets for simplicity

Bugs Fixed
================================================================================

* :jirabug:`K8SPSMDB-271`: Fix cluster initialization with CFSSL certificates
* :jirabug:`K8SPSMDB-268`: Make TLS to work with certmanager
* :jirabug:`K8SPSMDB-261`: Fix pause/resume cluster
* :jirabug:`K8SPSMDB-292`: Fix the automatic update not upgrading all clusters managed by the Operator
* :jirabug:`K8SPSMDB-325`: Prevent the Operator from repeatedly flooding ping mongo errors in logs in case of a paused cluster

