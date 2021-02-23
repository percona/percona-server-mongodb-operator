.. _K8SPSMDB-1.7.0:

================================================================================
*Percona Kubernetes Operator for Percona Server for MongoDB* 1.7.0
================================================================================

:Date: March 2, 2021
:Installation: `Installing Percona Kubernetes Operator for Percona Server for MongoDB <https://www.percona.com/doc/kubernetes-operator-for-psmongodb/index.html#installation>`_

New Features
================================================================================

* :jirabug:`K8SPSMDB-260`: The possibility to delete PVCs automatically when destroying the cluster <link>
* :jirabug:`K8SPSMDB-121`: Support for multiple shards <link>
* :jirabug:`K8SPSMDB-294`: Support for sidecar containers <link>

Improvements
================================================================================

* :jirabug:`K8SPSMDB-335`: Add support for :ref:`retention of backups<backup-tasks-keep>` stored on S3
* :jirabug:`K8SPSMDB-330`: Add support for runtimeClassName
* :jirabug:`K8SPSMDB-306`: Keep major version for newly created clusters <link>



Bugs Fixed
================================================================================

* :jirabug:`K8SPSMDB-346`: Fix a bug which prevented adding/removing labels to mongod Pods without downtime
* :jirabug:`K8SPSMDB-366`: Fix a bug which prevented enabling PMM due to incorrect request for the recommended PMM Client image version to the Version Service
* :jirabug:`K8SPSMDB-402`: running multiple replica sets without sharding enabled should be prohibited
* :jirabug:`K8SPSMDB-382`: Fix a bug which caused mongos process fail when using ``allowUnsafeConfigurations=true``
* :jirabug:`K8SPSMDB-370`: Fix confusing log messages about no backup / restore found which were caused by Percona Backup for MongoDB waiting for the backup metadata
* :jirabug:`K8SPSMDB-364`: Fix a bug which caused liveness probe failing if MongoDB password contained special characters
* :jirabug:`K8SPSMDB-362`: Fix a bug due to which changing secrets in a single-shard mode caused mongos Pods to fail
* :jirabug:`K8SPSMDB-342`: Health check is not using tls


