.. _K8SPSMDB-1.6.0:

================================================================================
*Percona Kubernetes Operator for Percona Server for MongoDB* 1.6.0
================================================================================

:Date: December 2, 2020
:Installation: `Installing Percona Kubernetes Operator for Percona Server for MongoDB <https://www.percona.com/doc/kubernetes-operator-for-psmongodb/index.html#installation>`_

New Features
================================================================================

* :jirabug:`K8SPSMDB-273`: :ref:`sharding` support by the Operator

Improvements
================================================================================

* :jirabug:`K8SPSMDB-258`: Percona Server for MongoDB version 4.4 is now supported
* :jirabug:`K8SPSMDB-319`: Show Endpoint to connect to a MongoDB cluster in the ``kubectl get psmdb`` command output
* :jirabug:`K8SPSMDB-257`: Store the Operator versiuon as a ``crVersion`` field in the ``deploy/cr.yaml`` configuration file
* :jirabug:`K8SPSMDB-266`: Use plain-text passwords instead of base64-encoded ones when creating :ref:`users.system-users` secrets for simplicity reasons

Bugs Fixed
================================================================================

* :jirabug:`K8SPSMDB-271`: Cluster initialization was failing with CFSSL certificates
* :jirabug:`K8SPSMDB-211`: Operator's documentation on TLS was outdated regarding the option to keep the name of the certificate generated for internal communications (Thanks to user abutch3r for reporting this issue)
* :jirabug:`K8SPSMDB-268`: TLS was not working with installed certmanager
* :jirabug:`K8SPSMDB-261`: ``pause`` option in ``deploy/cr.yaml`` configuration file didn't work
* :jirabug:`K8SPSMDB-292`: The automatic update didn't upgrade all clusters managed by the Operator
* :jirabug:`K8SPSMDB-325`: The Operator was repeatedly flooding ping mongo errors in logs if the cluster was set on pause

