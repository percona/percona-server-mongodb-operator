.. _K8SPSMDB-1.5.0:

================================================================================
*Percona Kubernetes Operator for Percona Server for MongoDB* 1.5.0
================================================================================

:Date: September 7, 2020
:Installation: `Installing Percona Kubernetes Operator for Percona Server for MongoDB <https://www.percona.com/doc/kubernetes-operator-for-psmongodb/index.html#installation>`_

New Features
================================================================================

* :jirabug:`K8SPSMDB-233`: Automatic management of system users for MongoDB on password rotation via Secret
* :jirabug:`K8SPSMDB-226`: Official Helm chart for the Operator
* :jirabug:`K8SPSMDB-199`: Support multiple PSMDB minor versions by the Operator
* :jirabug:`K8SPSMDB-198`: Fully Automate Minor Version Updates (Smart Update)

Improvements
================================================================================

* :jirabug:`K8SPSMDB-192`: The ability to set the mongod cursorTimeoutMillis parameter in YAML (Thanks to user xprt64 for the contribution)
* :jirabug:`K8SPSMDB-234`: OpenShift 4.5 support
* :jirabug:`K8SPSMDB-197`: Additional certificate SANs useful for reverse DNS lookups (Thanks to user phin1x for the contribution)
* :jirabug:`K8SPSMDB-190`: Direct API quering with "curl" instead of using "kubectl" tool in scheduled backup jobs (Thanks to user phin1x for the contribution)
* :jirabug:`K8SPSMDB-133`: A special PSMDB debug image which avoids restarting on fail and contains additional tools useful for debugging
* :jirabug:`CLOUD-556`: Kubernetes 1.17 / Google Kubernetes Engine 1.17 support

Bugs Fixed
================================================================================

* :jirabug:`K8SPSMDB-213`: Installation instruction not reflecting recent changes in git tags (Thanks to user geraintj for reporting this issue)
* :jirabug:`K8SPSMDB-210`: Backup documentation not reflecting changes in PBM
* :jirabug:`K8SPSMDB-180`: Replset and cluster having "ready" status set before mongo initialization and replicasets configuration finished
* :jirabug:`K8SPSMDB-179`: The "error" cluster status instead of the "initializing" one during the replset initialization
* :jirabug:`CLOUD-531`: Wrong usage of ``strings.TrimLeft`` when processing apiVersion
