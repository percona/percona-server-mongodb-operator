.. rn:: 1.11.0

================================================================================
*Percona Distribution for MongoDB Operator* 1.11.0
================================================================================

:Date: December 16, 2021
:Installation: For installation please refer to `the documentation page <https://www.percona.com/doc/kubernetes-operator-for-psmongodb/index.html#installation>`_

Release Highlights
================================================================================

* In addition to Amazon S3 and S3-compatible cloud storages, you can now configure backups to use Microsoft Azure Blob storage. This feature makes the Operator fully compatible with Azure Cloud.

New Features
================================================================================

* :jirabug:`K8SPSMDB-513`: Add support of Microsoft Azure Blob storage for backups

Improvements
================================================================================

* :jirabug:`K8SPSMDB-422`: Add annotations to backup cron jobs (Thanks to Aliaksandr Karavai for reporting this issue)
* :jirabug:`K8SPSMDB-574`: Set certificate duration for external certificates to 100 years
* :jirabug:`K8SPSMDB-534`: Replace listDatabases in a mongos readiness probe
* :jirabug:`K8SPSMDB-527`: Allow tuning timeout parameters for probes Allow users to set custom timeout for liveness and readiness container probes to avoid false-positives.
* :jirabug:`K8SPSMDB-520`: Add more customization to sidecar containers Custom sidecar containers are a great tool to customize our operator without code changes. This improvement enables users to customize sidecars even more by mounting volumes such as Secrets, ConfigMaps and Persistent Volume Claims.
* :jirabug:`K8SPSMDB-463`: Update backup status as error if it's not started
* :jirabug:`K8SPSMDB-388`: [PITR] Control how often oplogs are uploaded to S3

Bugs Fixed
================================================================================

* :jirabug:`K8SPSMDB-603`: Operator checks presence of CPU limit when deciding whether to set wiredTigerCacheSizeGB
* :jirabug:`K8SPSMDB-511`: NodePort Port changes every 20 seconds when exposeType to NodePort and enabled set to true (Thanks to Rajshekar Reddy for reporting this issue)
* :jirabug:`K8SPSMDB-608`: The password of backup user is printed in backup agent logs (Thanks to Antoine Ozenne for reporting this issue)
* :jirabug:`K8SPSMDB-594`: 'init-deploy' test fails on main PSMDB operator image (Thanks to Andrii Dema for reporting this issue)
* :jirabug:`K8SPSMDB-584`: backup doesn't start with current main images
* :jirabug:`K8SPSMDB-607`: operator crashes if restore is tried with backupSource and azure storage
* :jirabug:`K8SPSMDB-605`: custom sidecar volumes not supported in mongos pods
* :jirabug:`K8SPSMDB-604`: startupDelaySeconds option for liveness probe is not passed for mongos
* :jirabug:`K8SPSMDB-592`: sharding exposure in helm has broken vars
* :jirabug:`K8SPSMDB-568`: upgrading to 5.0 fails when using upgradeOptions:apply
* :jirabug:`K8SPSMDB-558`: Ignore annotations for object updates

Supported Platforms
================================================================================

The following platforms were tested and are officially supported by the Operator 1.11.0:

* OpenShift 4.6 - 4.8
* Google Kubernetes Engine (GKE) 1.17 - 1.21
* Amazon Elastic Container Service for Kubernetes (EKS) 1.16 - 1.21
* Minikube 1.22

This list only includes the platforms that the Percona Operators are specifically tested on as part of the release process. Other Kubernetes flavors and versions depend on the backward compatibility offered by Kubernetes itself.
