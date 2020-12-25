.. _operator.monitoring:

Monitoring
==========

Percona Monitoring and Management (PMM) `provides an excellent
solution <https://www.percona.com/doc/percona-monitoring-and-management/2.x/index.html>`_
of monitoring Percona Server for MongoDB.

.. note:: Only PMM 2.x versions are supported by the Operator.

PMM is a client/server application. *PMM Client* runs on each node with the
database you wish to monitor: it collects needed metrics and sends gathered data
to *PMM Server*. As a user, you connect to PMM Server to see database metrics on
a number of dashboards.

That's why PMM Server and PMM Client need to be installed separately.

Installing PMM Server
-------------------------

PMM Server runs as a *Docker image*, a *virtual appliance*, or on an *AWS instance*.
Please refer to the `official PMM documentation <https://www.percona.com/doc/percona-monitoring-and-management/2.x/setting-up/server/index.html>`_
for the installation instructions.

Installing PMM Client
-------------------------

The following steps are needed for the PMM client installation in your
Kubernetes-based environment:

#. The PMM client installation is initiated by updating the ``pmm``
   section in the
   `deploy/cr.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/cr.yaml>`_
   file.

   -  set ``pmm.enabled=true``
   -  set the ``pmm.serverHost`` key to your PMM Server hostname.
   -  check that  the``PMM_SERVER_USER`` key in the
      `deploy/secrets.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/secrets.yaml>`_
      secrets file contains your PMM Server user name (``admin`` by default).
   -  make sure the ``PMM_SERVER_PASSWORD`` key in the
      `deploy/secrets.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/secrets.yaml>`_
      secrets file contains the password specified for the PMM Server during its
      installation.
      
      .. note:: You use ``deploy/secrets.yaml`` file to *create* Secrets Object.
         The file contains all values for each key/value pair in a convenient
         plain text format. But the resulting Secrets contain passwords stored
         as base64-encoded strings. If you want to *update* password field,
         you'll need to encode the value into base64 format. To do this, you can
         run ``echo -n "password" | base64`` in your local shell to get valid
         values. For example, setting the PMM Server user's password to 
         `new_password`` in the ``my-cluster-name-secrets`` object can be done
         with the following command:

         .. code:: bash

            kubectl patch secret/my-cluster-name-secrets -p '{"data":{"PMM_SERVER_PASSWORD": '$(echo -n new_password | base64)'}}'
      
   -  you can also use ``pmm.mongodParams`` and ``pmm.mongosParams`` keys to
      specify additional parameters for the `pmm-admin add mongodb <https://www.percona.com/doc/percona-monitoring-and-management/2.x/setting-up/client/mongodb.html#adding-mongodb-service-monitoring>`_ command for ``mongod`` and
      ``mongos`` Pods respectively, if needed.
      
      .. note:: Please take into account that Operator automatically manages
         common MongoDB Service Monitoring parameters mentioned in the officiall ``pmm-admin add mongodb`` `documentation <https://www.percona.com/doc/percona-monitoring-and-management/2.x/setting-up/client/mongodb.html#adding-mongodb-service-monitoring>`_,
         such like username, password, service-name, host, etc. Assigning values
         to these parameters is not recommended and can negatively affect the
         functionality of the PMM setup carried out by the Operator.

   Apply changes with the ``kubectl apply -f deploy/secrets.yaml`` command.

   When done, apply the edited ``deploy/cr.yaml`` file:

   .. code:: bash

      $ kubectl apply -f deploy/cr.yaml

#. Check that corresponding Pods are
   not in a cycle of stopping and restarting. This cycle occurs if there are errors on the previous steps:

   .. code:: bash

      $ kubectl get pods
      $ kubectl logs my-cluster-name-rs0-0 -c pmm-client

#. Run the following command:

   ``kubectl get service/monitoring-service -o wide``

   In the results, locate the the ``EXTERNAL-IP`` field. The external-ip address
   can be used to access PMM via *https* in a web browser, with the
   login/password authentication, and the browser is configured to show
   Percona Server for MongoDB metrics.

