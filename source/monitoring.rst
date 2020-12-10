.. _operator.monitoring:

Monitoring
==========

The Percona Monitoring and Management (PMM) `provides an excellent
solution <https://www.percona.com/doc/percona-monitoring-and-management/2.x/index.html>`_
to monitor Percona Server for MongoDB.

.. note:: Only PMM 2.x versions are supported by the Operator.

PMM is a client/server application. *PMM Client* runs on each node with the
database you wish to monitor: it collects needed metrics and sends gathered data
to *PMM Server*. As a user, you connect to PMM Server to see database metrics on
a number of dashboards.

That's why PMM Server and PMM Client are to be installed separately.

Installing the PMM Server
-------------------------

PMM Server runs as a *Docker image*, a *virtual appliance*, or on an *AWS instance*.
Please refer to the `official PMM documentation <https://www.percona.com/doc/percona-monitoring-and-management/2.x/setting-up/server/index.html>`_
for the installation instructions.

Installing the PMM Client
-------------------------

The following steps are needed for the PMM client installation in your
Kubernetes-based environment:

#. The PMM client installation is initiated by updating the ``pmm``
   section in the
   `deploy/cr.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/cr.yaml>`_
   file.

   -  set ``pmm.enabled=true``
   -  ensure the ``serverHost`` (the PMM service name is
      ``monitoring-service`` by default) is the same as value specified
      for the ``name`` parameter on the previous step, but with an added
      additional ``-service`` suffix.
   -  check that ``PMM_USER`` key in the
      `deploy/secrets.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/secrets.yaml>`_
      secrets file contains your PMM Server user name (``admin`` by default).
   -  make sure the ``PMM_PASSWORD`` key in the
      `deploy/secrets.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/secrets.yaml>`_
      secrets file contains password specified for the PMM Server during its
      installation
   -  you can also use ``pmm.mongodParams`` and ``pmm.mongosParams`` keys to
      specify additional parameters for the `pmm-admin add mongodb <https://www.percona.com/doc/percona-monitoring-and-management/2.x/setting-up/client/mongodb.html#adding-mongodb-service-monitoring>_ command for ``mongod`` and
      ``mongos`` Pods respectively, if needed.

   Apply changes with the ``kubectl apply -f deploy/secrets.yaml`` command.

   When done, apply the edited ``deploy/cr.yaml`` file:

   .. code:: bash

      $ kubectl apply -f deploy/cr.yaml

#. Check that correspondent Pods are
   not in a cycle of stopping and restarting. This cycle occurs if there are errors on the previous steps:

   .. code:: bash

      $ kubectl get pods
      $ kubectl logs my-cluster-name-rs0-0 -c pmm-client

#. Run the following command:

   ``kubectl get service/monitoring-service -o wide``

   In the results, locate the the ``EXTERNAL-IP`` field. The external-ip address
   can be used to access PMM via *https* in a web browser, with the
   login/password authentication, and the browser is configured to `show
   Percona Server for MongoDB
   metrics <https://www.percona.com/doc/percona-monitoring-and-management/index.metrics-monitor.dashboard.html#pmm-dashboard-mongodb-list>`__.

