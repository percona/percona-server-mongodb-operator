.. _K8SPSMDB-1.7.0:

================================================================================
*Percona Kubernetes Operator for Percona Server for MongoDB* 1.7.0
================================================================================

:Date: March 4, 2021
:Installation: `Installing Percona Kubernetes Operator for Percona Server for MongoDB <https://www.percona.com/doc/kubernetes-operator-for-psmongodb/index.html#installation>`_

New Features
================================================================================

* :jirabug:`K8SPSMDB-260`: Persistent Volume Claims can now be automatically removed after MongoDB cluster deletion. Learn more about this on operator’s :ref:`documentation page<operator.scale.scale-down>`
* :jirabug:`K8SPSMDB-121`: Add support for :ref:`sharding<operator.sharding>` to scale MongoDB cluster horizontally
* :jirabug:`K8SPSMDB-294`: Support for :ref:`custom sidecar containers <How can I add custom sidecar containers to my cluster?>` to extend the Operator capabilities

Improvements
================================================================================

* :jirabug:`K8SPSMDB-335`: Operator can now automatically remove old backups from S3 if :ref:`retention period<backup-tasks-keep>` is set
* :jirabug:`K8SPSMDB-330`: Add support for runtimeClassName Kubernetes feature for selecting the container runtime
* :jirabug:`K8SPSMDB-306`: It is now possible to explicitly set the version of MongoDB for newly provisioned clusters. Before that, all new clusters were started with the latest MongoDB version if Version Service was enabled
* :jirabug:`K8SPSMDB-370`: Fix confusing log messages about no backup / restore found which were caused by Percona Backup for MongoDB waiting for the backup metadata
* :jirabug:`K8SPSMDB-342`: MongoDB container liveness probe will now use TLS to follow best practices and remove noisy log messages from mongod log

Bugs Fixed
================================================================================

* :jirabug:`K8SPSMDB-346`: Fix a bug which prevented adding/removing labels to Pods without downtime
* :jirabug:`K8SPSMDB-366`: Fix a bug which prevented enabling Percona Monitoring and Management (PMM) due to incorrect request for the recommended PMM Client image version to the Version Service
* :jirabug:`K8SPSMDB-402`: running multiple replica sets without sharding enabled should be prohibited
* :jirabug:`K8SPSMDB-382`: Fix a bug which caused mongos process to fail when using ``allowUnsafeConfigurations=true``

* :jirabug:`K8SPSMDB-364`: Fix a bug which caused liveness probe failing if MongoDB password contained special characters
* :jirabug:`K8SPSMDB-362`: Fix a bug due to which changing secrets in a single-shard mode caused mongos Pods to fail
