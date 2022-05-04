.. _K8SPSMDB-1.12.0:

================================================================================
*Percona Operator for MongoDB* 1.12.0
================================================================================

:Date: May 5, 2022
:Installation: `Installing Percona Operator for MongoDB <https://www.percona.com/doc/kubernetes-operator-for-psmongodb/index.html#installation>`_

Release Highlights
================================================================================

* With this release, the Operator turns to a simplified naming convention and
  changes its official name to **Percona Operator for MongoDB**
* The Operator is able now to use the Amazon Web Services feature of
  authenticating applications running on EC2 instances based on
  :ref:`Identity and Access Management (IAM) roles assigned to the instance<backups.scheduled-s3-iam>`;
  this  makes it possible to configure S3 backup on AWS without using IAM keys
  saved in Secrets
* This release brings :ref:`support for the Multi Cluster Services (MCS)<operator-replication-mcs>`.
  This allows users to deploy MongoDB with Percona Operator across multiple
  Kubernetes clusters using MCS, which extends the reach of the Service object
  beyond one cluster, so one Service can be used across multiple clusters. It
  can be used to provide disaster recovery or perform a migration for MongoDB
  clusters.
* The OpenAPI schema is now generated for the Operator :abbr:`CRD (Custom Resource Definition)`,
  which allows Kubernetes to perform Custom Resource validation and saves user
  from occasionally applying ``deploy/cr.yaml`` with syntax typos

New Features
================================================================================

* :jirabug:`K8SPSMDB-185`: Allow using AWS EC2 instances for backups with IAM
  roles assigned to the instance instead of using stored IAM credentials (Thanks
  to Oleksii for reporting this issue)
* :jirabug:`K8SPSMDB-625`: Integrate the Operator with Multi Cluster Services
  (MCS)
* :jirabug:`K8SPSMDB-668`: Adding :ref:`support<clusterservicednsmode>` for
  enabling replication over a service mesh (Thanks to Jo Lyshoel  for
  contribution)

Improvements
================================================================================

* :jirabug:`K8SPSMDB-473`: Allow to :ref:`skip TLS verification for backup storage<backup-storages-verifytls>`,
  useful for self-hosted S3-compatible storage with a self-issued certificate
* :jirabug:`K8SPSMDB-644`: Make ``cacheSizeRatio`` parameter available as a
  custom value in psmdb-db-1.11.0 helm chart (Thanks to Richard CARRE for
  reporting this issue)
* :jirabug:`K8SPSMDB-574`: Allow user to :ref:`choose the validity duration of the external certificate<tls-certvalidityduration>`
  for cert manager
* :jirabug:`K8SPSMDB-634`: Support :ref:`point-in-time recovery compression levels<backup-pitr-compressiontype>`
  for backups (Thanks to Damiano Albani for reporting this issue)
* :jirabug:`K8SPSMDB-570`: The Operator documentation now includes a How-To on
  :ref:`using Percona Server for MongoDB with LDAP authentication and authorization<howto-ldap>`
* :jirabug:`K8SPSMDB-537`: PMM container does not cause the crash of the whole
  database Pod if pmm-agent is not working properly
* :jirabug:`K8SPSMDB-684`: Generate OpenAPI schema for :abbr:`CRD (Custom Resource Definition)`
  and validate Custom Resource

Bugs Fixed
================================================================================

* :jirabug:`K8SPSMDB-597`: Fix a bug in the Operator helm chart which caused
  deleting the watched Namespace on uninstall (Thanks to Andrei Nistor for
  reporting this issue)
* :jirabug:`K8SPSMDB-640`: Fix a regression which prevented labels from being
  applied to Pods after the Custom Resource change
* :jirabug:`K8SPSMDB-583`: Fix a bug which caused backup crashing if
  ``spec.mongod.net.port`` not set or set to zero
* :jirabug:`K8SPSMDB-540` and :jirabug:`K8SPSMDB-563`: Fix a bug which could
  cause a cluster crash when reducing the configured Replicaset size between
  deletion and re-creation of the cluster
* :jirabug:`K8SPSMDB-608`: Fix a bug due to which the password of backup user
  was printed in backup agent logs (Thanks to Antoine Ozenne for reporting this
  issue)
* :jirabug:`K8SPSMDB-599`: A new :ref:`mongos.expose.servicePerPod<sharding-mongos-expose-serviceperpod>`
  option allows deploying a separate ClusterIP Service for each mongos instance,
  which prevents the failure of a multi-threaded transaction executed with the
  same driver instance and ended up on a different mongos. Starting from this
  release, mongos is deployed by StatefulSet instead of Deployment object
* :jirabug:`K8SPSMDB-656`: Fix a bug which caused cluster name being not
  displayed in the backup Custom Resource output with ``psmdbCluster`` set in
  the backup spec
* :jirabug:`K8SPSMDB-653`: Fix a bug due to which ``spec.ImagePullPolicy``
  options from ``deploy/cr.yaml`` wasn’t applied to backup and pmm-client images
* :jirabug:`K8SPSMDB-632`: Fix a bug which caused the Operator to perform Smart
  Update on the initial deployment
* :jirabug:`K8SPSMDB-624`: Fix a bug due to which the Operator didn't grant
  enough permissions to the Cluster Monitor user necessary for Percona
  Monitoring and Management (PMM) (Thanks to Richard CARRE for reporting this
  issue)
* :jirabug:`K8SPSMDB-618`: Improve security and meet compliance requirements by
  building MongoDB Operator based on Red Hat Universal Base Image (UBI) 8
  instead of UBI 7
* :jirabug:`K8SPSMDB-602`: Fix a thread leak in a mongod container of the
  Replica Set Pods, which occurred when setting ``setFCV`` flag to ``true`` in
  Custom Resource
* :jirabug:`K8SPSMDB-560`: Fix a bug due to which ``serviceName`` tag was not
  set to all members in the Replica Set
* :jirabug:`K8SPSMDB-533`: Fix a bug due to which setting password with a
  special character for a system user was breaking the cluster

Known Issues
================================================================================

* :jirabug:`K8SPSMDB-686`: The Operator versions 1.11.0 and 1.12.0 can not be
  downscaled from a sharding to non-sharding/Replica Set configuration on
  Google Kubernetes Engine (GKE) 1.19-1.21 (GKE 1.22 is not affected)

Deprecation, Rename and Removal
================================================================================

* :jirabug:`K8SPSMDB-596`: The ``spec.mongod`` section is removed from the
  Custom Resource configuration. Starting from now, mongod options should be
  passed to Replica Sets using ``spec.replsets.[].configuration`` key, except
  the following 3 options:
  
  * ``mongod.security.encryptionKeySecret`` key was left in a deprecated state
    in favor of the new ``spec.secrets.encryptionKey`` option
  * ``mongod.storage.wiredTiger.engineConfig.cacheSizeRatio`` and
    ``mongod.storage.inMemory.engineConfig.inMemorySizeRatio`` options are now
    only available from the ``replsets.storage`` section

  Before the upgrade, please ensure that you have moved all custom MongoDB
  parameters to proper places!

* :jirabug:`K8SPSMDB-228`: The ``spec.psmdbCluster`` option in the example
  on-demand backup configuration file ``backup/backup.yaml`` was renamed to
  ``spec.clusterName`` (``psmdbCluster`` will be valid till 1.15 version)

Supported Platforms
================================================================================

The following platforms were tested and are officially supported by the Operator
1.12.0:

* OpenShift 4.7 - 4.10
* Google Kubernetes Engine (GKE) 1.19 - 1.22
* Amazon Elastic Container Service for Kubernetes (EKS) 1.19 - 1.22
* Minikube 1.23

This list only includes the platforms that the Percona Operators are
specifically tested on as part of the release process. Other Kubernetes flavors
and versions depend on the backward compatibility offered by Kubernetes itself.
