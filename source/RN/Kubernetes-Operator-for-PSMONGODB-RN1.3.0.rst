.. rn:: 1.3.0

Percona Kubernetes Operator for Percona Server for MongoDB 1.3.0
================================================================

Percona announces the *Percona Kubernetes Operator for Percona Server for
MongoDB* 1.3.0 release on December 24, 2019. This release is now the current
GA release in the 1.3 series. `Install the Kubernetes Operator for Percona
Server for MongoDB by following the instructions <https://www.percona.com/doc/kubernetes-operator-for-psmongodb/kubernetes.html>`_.

The Operator simplifies the deployment and management of the `Percona Server
for MongoDB <https://www.percona.com/software/mongo-database/percona-server-for-mongodb>`_
in Kubernetes-based environments. It extends the Kubernetes API with a new
custom resource for deploying, configuring and managing the application through
the whole life cycle.

The Operator source code is available `in our Github repository <https://github.com/percona/percona-server-mongodb-operator>`_.
All of Percona’s software is open-source and free.

**New features and improvements:**
* :cloudbug:`415`: Non-default cluster domain can now be specified with the new
  ``ClusterServiceDNSSuffix`` operator option.
* :cloudbug:`411`: Now user is able to to adjust securityContext, thus replacing
  the automatically generated securityContext with the customized one.
* :cloudbug:`395`: Decrease PSMDB images size by half, removing non-necessary
  dependencies and modules.
* :cloudbug:`390`: Helm chart for PMM 2.0 have been provided

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
