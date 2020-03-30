.. _K8SPSMDB-1.4.0:

================================================================================
*Percona Kubernetes Operator for PSMDB* 1.4.0
================================================================================

:Date: March 31, 2020

:Installation: `Installing Percona Kubernetes Operator for PSMDB <https://www.percona.com/doc/kubernetes-operator-for-psmongodb/index.html#installation>`_

New Features
================================================================================

* :jirabug:`K8SPSMDB-89`: Amazon Elastic Container Service for Kubernetes (EKS)
  was added to the list of the officially supported platforms
* :jirabug:`K8SPSMDB-113`: Percona Server for MongoDB 4.2 is now supported

Improvements
================================================================================

* :jirabug:`K8SPSMDB-79`: The health check algorithm improvements have increased the overall stability of the Operator
* :jirabug:`K8SPSMDB-176`: The Operator was updated to use Percona Backup for MongoDB version 1.1
* :jirabug:`K8SPSMDB-153`: Now the user can adjust securityContext, replacing the automatically generated securityContext with the customized one
* :jirabug:`K8SPSMDB-175`: Operator now updates observedGeneration status message to allow better monitoring of the cluster rollout or backups/restore process
* The OpenShift Container Platform 4.3 is now supported.

Bugs Fixed
================================================================================

* :jirabug:`K8SPSMDB-182`: Setting the ``updateStrategy: OnDelete`` didn't work if was not specified from scratch in CR
* :jirabug:`K8SPSMDB-174`: The inability to update or delete existing CRD was possible because of too large records in etcd, resulting in "request is too large" errors. Only 20 last status changes are now stored in etcd to avoid this problem.

Help us improve our software quality by reporting any bugs you encounter using
`our bug tracking system <https://jira.percona.com/secure/Dashboard.jspa>`_.
