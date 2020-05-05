.. rn:: 1.2.0

*Percona Kubernetes Operator for Percona Server for MongoDB* 1.2.0
==================================================================

Percona announces the *Percona Kubernetes Operator for Percona Server for
MongoDB* 1.2.0 release on September 20, 2019. This release is now the current
GA release in the 1.2 series. `Install the Kubernetes Operator for Percona
Server for MongoDB by following the instructions <https://www.percona.com/doc/kubernetes-operator-for-psmongodb/kubernetes.html>`_.

The Operator simplifies the deployment and management of the `Percona Server
for MongoDB <https://www.percona.com/software/mongo-database/percona-server-for-mongodb>`_
in Kubernetes-based environments. It extends the Kubernetes API with a new
custom resource for deploying, configuring and managing the application through
the whole life cycle.

The Operator source code is available `in our Github repository <https://github.com/percona/percona-server-mongodb-operator>`_.
All of Percona’s software is open-source and free.

**New features and improvements:**

* `A Service Broker was implemented <https://www.percona.com/doc/kubernetes-operator-for-psmongodb/broker.html>`_
  for the Operator, allowing a user to deploy Percona XtraDB Cluster on the
  OpenShift Platform, configuring it with a standard GUI, following the Open
  Service Broker API.
* Now the Operator supports `Percona Monitoring and Management 2 <https://www.percona.com/doc/percona-monitoring-and-management/2.x/index.html>`_,
  which means being able to detect and register to PMM Server of both 1.x and
  2.0 versions.
* Data-at-rest encryption is now enabled by default unless 
  ``EnableEncryption=false`` is explicitly specified in the ``deploy/cr.yaml``
  configuration file.
* Now it is possible to set the ``schedulerName`` option in the operator
  parameters. This allows using storage which depends on a custom scheduler, or
  a cloud provider which optimizes scheduling to run workloads in a
  cost-effective way.
* The resource constraint values were refined for all containers to eliminate
  the possibility of an out of memory error.

**Fixed bugs:**

* Oscillations of the cluster status between "initializing" and "ready" took
  place after an update.
* The Operator was removing other cron jobs in case of the enabled backups
  without defined tasks (contributed by `Marcel Heers <https://github.com/mheers>`_).

`Percona Server for MongoDB <https://www.percona.com/software/mongo-database/percona-server-for-mongodb>`_
is an enhanced, open source and highly-scalable database that is a
fully-compatible, drop-in replacement for MongoDB Community Edition. It supports
MongoDB protocols and drivers. Percona Server for MongoDB extends MongoDB
Community Edition functionality by including the Percona Memory Engine, as well
as several enterprise-grade features. It requires no changes to MongoDB
applications or code.

Help us improve our software quality by reporting any bugs you encounter using
`our bug tracking system <https://jira.percona.com/secure/Dashboard.jspa>`_.
