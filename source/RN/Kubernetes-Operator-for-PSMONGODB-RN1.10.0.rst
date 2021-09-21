.. rn:: 1.10.0

================================================================================
*Percona Distribution for MongoDB Operator* 1.10.0
================================================================================

:Date: September 22, 2021

:Installation: For installation please refer to `the documentation page <https://www.percona.com/doc/kubernetes-operator-for-psmongodb/index.html#installation>`_

Release Highlights
================================================================================

* Starting from this release, the Operator changes its official name to
  **Percona Distribution for MongoDB Operator**. This new name emphasizes
  graduate changes which incorporated a collection of Percona’s solutions to run
  and operate MongoDB Server, available separately as
  `Percona Distribution for MongoDB <https://www.percona.com/doc/percona-distribution-for-mongodb/4.2/index.html>`_.

* It is now possible to restore backups from S3-compatible storage
  :ref:`to a new Kubernetes-based environment<backups-restore>` with no existing
  Backup Custom Resources

* You can now customize Percona Server for MongoDB by
  :ref:`storing custom configuration<operator-configmaps>` for Replica Set,
  mongos, and Config Server instances in ConfigMaps or in Secrets

New Features
================================================================================

* :jirabug:`K8SPSMDB-479`: Allow MongoDB PODs to have non-voting members.  Non-voting member is a replicaset node which does not participate in the primary election process. This feature enables users to deploy non-voting members with Operator through Custom Resource object without manual configuration. 
  Backup Custom Resource
* :jirabug:`K8SPSMDB-265`: Cross region replication for disaster recovery  This feature enables users to add external replica set nodes into the cluster managed by the Operator to simplify migrations or deploy cross-regional clusters for Disaster Recovery. External nodes can be running by another Operator or be a regular MongoDB deployment.  

Improvements
================================================================================

* :jirabug:`K8SPSMDB-537`: pmm container should not crash in case of issues
* :jirabug:`K8SPSMDB-517`: Add support for PSMDB 5.0  This improvement adds support for Percona Distribution for MongoDB version 5.0 into the Operator.
* :jirabug:`K8SPSMDB-490`: Add validation for Custom Resource name

Bugs Fixed
================================================================================

* :jirabug:`K8SPSMDB-504`: cluster fails to become ready if replica set members exposed with LoadBalancer  
* :jirabug:`K8SPSMDB-470`: ServiceAnnotation and LoadBalancerSourceRanges fields don't propagate to k8s service   (Thanks to Aliaksandr Karavai for reporting this issue)
* :jirabug:`K8SPSMDB-531`: Percona Kubernetes operator for MongoDB doesn't work well with systems with Calico installed   (Thanks to Mykola Kruliv for reporting this issue)
* :jirabug:`K8SPSMDB-514`: Backup cronJob no resources   (Thanks to George Asenov for reporting this issue)
* :jirabug:`K8SPSMDB-512`: Add CustomWriteConcern type (Thanks to Adam Watson for contribution)
* :jirabug:`K8SPSMDB-553`: wrong S3 credentials supplied, but backup in running state
* :jirabug:`K8SPSMDB-506`: Smart update fails if replset exposed  
* :jirabug:`K8SPSMDB-496`: pods not restarted if custom mongo config is updated inside secret or configmap

