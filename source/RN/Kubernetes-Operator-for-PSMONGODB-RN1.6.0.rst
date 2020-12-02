.. _K8SPSMDB-1.6.0:

================================================================================
*Percona Kubernetes Operator for Percona Server for MongoDB* 1.6.0
================================================================================

:Date: December 2, 2020
:Installation: `Installing Percona Kubernetes Operator for Percona Server for MongoDB <https://www.percona.com/doc/kubernetes-operator-for-psmongodb/index.html#installation>`_

New Features
================================================================================

* :jirabug:`K8SPSMDB-317`: [sharding] no arbiters in configreplset
* :jirabug:`K8SPSMDB-274`: Run mongo with configsvr in the operator
* :jirabug:`K8SPSMDB-273`: Run mongos service in the operator
* :jirabug:`K8SPSMDB-259`: add PSMDB 4.4 support

Improvements
================================================================================

* :jirabug:`K8SPSMDB-211`: Mongo Operator TLS documentation is Incorrect for providing own secrets (Thanks to user abutch3r for reporting this issue)
* :jirabug:`K8SPSMDB-319`: Show Endpoint in psmbd custom resource output
* :jirabug:`K8SPSMDB-312`: fix antiAffinity for mongos
* :jirabug:`K8SPSMDB-299`: Smart update for sharded PSMDB cluster
* :jirabug:`K8SPSMDB-298`: Move Percona cetrified images list from the custom registry doc to a separate page
* :jirabug:`K8SPSMDB-267`: Version service request with generated code
* :jirabug:`K8SPSMDB-257`: Introduce CR version field in custom resouce
* :jirabug:`K8SPSMDB-308`: allow to set parameters for mongos in cr.yaml
* :jirabug:`K8SPSMDB-223`: research potential race condition between backups and updates
* :jirabug:`CLOUD-535`: research standard debug protocol
* :jirabug:`K8SPSMDB-258`: add psmdb v4.4 support



Bugs Fixed
================================================================================

* :jirabug:`K8SPSMDB-271`: TLS not initializing replicas with cfssl certificates
* :jirabug:`K8SPSMDB-324`: mongos is missing pod disruption budget
* :jirabug:`K8SPSMDB-316`: config server service is not removed when converting from sharding to replica
* :jirabug:`K8SPSMDB-311`: Remove 'operationProfiling' section from deploy/cr.yaml for mongos
* :jirabug:`K8SPSMDB-309`: Can not create k8s service for mongos
* :jirabug:`K8SPSMDB-303`: user credentials rotation doesn't work with sharding
* :jirabug:`K8SPSMDB-301`: configsvr replica status is wrong
* :jirabug:`K8SPSMDB-296`: backup fails if backup user password rotated
* :jirabug:`K8SPSMDB-293`: Use correct init operator image for PSMDBO
* :jirabug:`K8SPSMDB-269`: Fix users e2e test
* :jirabug:`K8SPSMDB-268`: Certmanager not working
* :jirabug:`K8SPSMDB-261`: pause doesn't work
* :jirabug:`K8SPSMDB-314`: pause doesn't work for mongos
* :jirabug:`K8SPSMDB-292`: automatic upgrade doesn't upgrade all clusters
* :jirabug:`K8SPSMDB-266`: Cluster not initialized correctly with line end in secret.yaml passwords
* :jirabug:`K8SPSMDB-326`: smartupdate re-enables balancer before mongos is upgraded
* :jirabug:`K8SPSMDB-325`: operator is pinging mongo when pause set to true
* :jirabug:`K8SPSMDB-305`: config replica set runs with storage engine selected for data replica set


