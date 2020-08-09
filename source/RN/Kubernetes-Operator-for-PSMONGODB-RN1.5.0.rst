.. _K8SPSMDB-1.5.0:

================================================================================
*Percona Kubernetes Operator for Percona Server for MongoDB* 1.5.0
================================================================================

:Date: August 27, 2020
:Installation: `Installing Percona Kubernetes Operator for Percona Server for MongoDB <https://www.percona.com/doc/kubernetes-operator-for-psmongodb/index.html#installation>`_

New Features
================================================================================

* :jirabug:`K8SPSMDB-199`: Support multiple PSMDB minor versions by the Operator
* :jirabug:`K8SPSMDB-226`: Official Helm chart for the Operator
* :jirabug:`K8SPSMDB-198`: Fully Automate Minor Version Updates (Smart Update)

Improvements
================================================================================

* :jirabug:`K8SPSMDB-192`: The ability to set the mongod cursorTimeoutMillis parameter in YAML (Thanks to user xprt64 for reporting this issue)
* :jirabug:`K8SPSMDB-197`: Adding additional certificate SANS useful for reverse DNS lookups
* :jirabug:`K8SPSMDB-190`: Direct API quering with "curl" instead of using "kubectl" tool in scheduled backup jobs
* :jirabug:`K8SPSMDB-133`: A special PSMDB debug image which avoids restarting on fail and contains additional tools useful for debugging
* :jirabug:`CLOUD-556`: Kubernetes 1.17 added to the list of supported platforms
* :jirabug:`K8SPSMDB-146`: PSMDB backup operator needs to have tar binary added (Thanks to user sdotel for reporting this issue)

Bugs Fixed
================================================================================

* :jirabug:`K8SPSMDB-213`: Installation instruction didn't reflect recent changes in git tags (Thanks to user geraintj for reporting this issue)
* :jirabug:`K8SPSMDB-210`: Reflect PBM changes in backup documentation
* :jirabug:`K8SPSMDB-180`: Replset and cluster having "ready" status set before mongo initialization and replicasets configuration finished
* :jirabug:`K8SPSMDB-179`: The "error" cluster status instead of the "initializing" one during the replset initialization
* :jirabug:`CLOUD-563`: list of versions in crd.yaml for all resource types except the cluster is incomplete
* :jirabug:`K8SPSMDB-247`: after restore from backup cluster passwords out of sync and operator cannot authenticate
* :jirabug:`CLOUD-531`: Wrong usage of ``strings.TrimLeft`` when processing apiVersion


