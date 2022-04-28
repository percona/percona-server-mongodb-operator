.. _K8SPSMDB-1.12.0:

================================================================================
*Percona Kubernetes Operator for Percona Server for MongoDB* 1.12.0
================================================================================

:Date: April 28, 2022
:Installation: `Installing Percona Kubernetes Operator for Percona Server for MongoDB <https://www.percona.com/doc/kubernetes-operator-for-psmongodb/index.html#installation>`_

New Features
================================================================================

* :jirabug:`K8SPSMDB-185`: allow using AWS EC2 instances for backups :ref:`with IAM roles assigned to the instance<backups.scheduled-s3-iam>` instead of using stored IAM credentials (Thanks to Oleksii for reporting this issue)
* :jirabug:`K8SPSMDB-625`: Integrate the Operator with Multi Cluster Services (MCS)
* :jirabug:`K8SPSMDB-668`: Adding support for enabling replication over a service mesh (Thanks to Jo Lyshoel  for contribution)



Improvements
================================================================================

* :jirabug:`K8SPSMDB-473`: Allow to :ref:`skip TLS verification for backup storage<backup-storages-verifytls>`, useful for self-hosted S3-compatible storage with a self-issued certificate
* :jirabug:`K8SPSMDB-644`: Helm chart psmdb-db-1.11.0 - cacheSizeRatio parameter not available as custom value (Thanks to Richard CARRE for reporting this issue)
* :jirabug:`K8SPSMDB-574`: Allow user to :ref:`choose the validity duration of the external certificate<tls-certvalidityduration>` for cert manager
* :jirabug:`K8SPSMDB-634`: Support :ref:`point-in-time recovery compression levels<backup-pitr-compressiontype>` for backups (Thanks to Damiano Albani for reporting this issue)
* :jirabug:`K8SPSMDB-570`: The Operator documentation now includes a How-To on :ref:`using Percona Server for MongoDB with LDAP authentication and authorization<howto-ldap>`
* :jirabug:`K8SPSMDB-537`: PMM container does not cause the crash of the whole database Pod if pmm-agent is not working properly

Bugs Fixed
================================================================================

* :jirabug:`K8SPSMDB-597`: Fix a bug in the Operator helm chart which caused deleting the watched Namespace on uninstall (Thanks to Andrei Nistor for reporting this issue)
* :jirabug:`K8SPSMDB-640`: Fix a regression which prevented labels from being applied to Pods after the Custom Resource change
* :jirabug:`K8SPSMDB-583`: Fix a bug which caused backup crashing if ``spec.mongod.net.port`` not set or set to zero
* :jirabug:`K8SPSMDB-540` and :jirabug:`K8SPSMDB-563`: Fix a bug which could cause a cluster crash when reducing the configured Replicaset size between deletion and re-creation of the cluster
* :jirabug:`K8SPSMDB-608`:  Fix a bug due to which the password of backup user was printed in backup agent logs (Thanks to Antoine Ozenne for reporting this issue)
* :jirabug:`K8SPSMDB-599`: A new :ref:`mongos.expose.servicePerPod<sharding-mongos-expose-serviceperpod>` option allows to deploy a separate ClusterIP Service for each mongos instance, which prevents the failure of a multi-threaded transaction executed with the same driver instance and ended up on a different mongos
* :jirabug:`K8SPSMDB-656`: Fix a bug which caused cluster name not displayed in the backup Custom Resource output with psmdbCluster set in the backup spec
* :jirabug:`K8SPSMDB-653`: Fix a bug due to which ``spec.ImagePullPolicy`` options from ``deploy/cr.yaml`` wasn’t applied to backup and pmm-client images
* :jirabug:`K8SPSMDB-632`: Fix a bug which caused the Operator to performs Smart Update on the initial deployment
* :jirabug:`K8SPSMDB-624`: Fix a bug due to which the Operator didn't grant enough permissions to the Cluster Monitor user necessary for Percona Monitoring and Management (PMM) (Thanks to Richard CARRE for reporting this issue)
* :jirabug:`K8SPSMDB-618`: Build MongoDB operator based on UBI8
* :jirabug:`K8SPSMDB-602`: Fix a thread leak in a mongod container of the Replica Set Pods which occurred when setting ``setFCV`` flag to ``true`` in Custom Resource
* :jirabug:`K8SPSMDB-560`: Fix a bug due to which ``serviceName`` tag was not set to all members in the Replica Set
* :jirabug:`K8SPSMDB-533`: Fix a bug due to which setting password with a special character for a system user was breaking the cluster

Deprecation, Rename and Removal
================================================================================

* :jirabug:`K8SPSMDB-596`: The ``spec.mongod`` section is removed from the Custom Resource configuration except the ``mongod.security.encryptionKeySecret`` key, left in a deprecated state in favor of the new ``spec.secrets.encryptionKey`` option. This reorganization involves using ``spec.replsets.[].configuration`` to specify mongod options to Replica Sets
* :jirabug:`K8SPSMDB-228`: The ``spec.psmdbCluster`` option in the example on-demand backup configuration file ``backup/backup.yaml`` was renamed to ``spec.clusterName`` (``psmdbCluster`` will be valid till 1.15 version)
