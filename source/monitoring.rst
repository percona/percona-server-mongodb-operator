Monitoring
==========

The Percona Monitoring and Management (PMM) `provides an excellent
solution <https://www.percona.com/doc/percona-monitoring-and-management/index.html>`__
to monitor Percona Server for MongoDB.

Following steps are needed to install both PMM Client and PMM Server
preconfigured to monitor Percona Server for MongoDB on Kubernetes or
OpenShift.

1. The recommended installation approach is based on using
   `helm <https://github.com/helm/helm>`__ - the package manager for
   Kubernetes, which will substantially simplify further steps. So first
   thing to do is to install helm following its `official installation
   instructions <https://docs.helm.sh/using_helm/#installing-helm>`__.

2. When the helm is installed, add Percona chart repository and update
   information of available charts as follows:

   ::

      $ helm repo add percona https://percona-charts.storage.googleapis.com
      $ helm repo update

3. Now helm can be used to install PMM Server:

   ::

      $ helm install percona/pmm-server --name monitoring --set platform=openshift --set credentials.username=clusterMonitor --set "credentials.password=clusterMonitor123456"

   It is important to specify correct options in the installation
   command:

   -  ``platform`` should be either ``kubernetes`` or ``openshift``
      depending on which platform are you using.
   -  ``name`` should correspond to the ``serverHost`` key in the
      ``pmm`` section of the
      `deploy/cr.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/cr.yaml>`__
      file with a “-service” suffix, so default ``--name monitoring``
      part of the shown above command corresponds to a
      ``monitoring-service`` value of the ``serverHost`` key.
   -  ``credentials.username`` should correspond to the
      ``MONGODB_CLUSTER_MONITOR_USER`` key in the the
      `deploy/mongodb-users.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/mongodb-users.yaml>`__
      file.
   -  ``credentials.password`` should correspond to a value of the
      ``MONGODB_CLUSTER_MONITOR_PASSWORD`` key specified in
      `deploy/mongodb-users.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/mongodb-users.yaml>`__
      secrets file. Note that password specified in this example is the
      default development mode password not intended to be used on
      production systems.

4. Now the PMM is installed, and it is time to update ``pmm`` section in
   the
   `deploy/cr.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/cr.yaml>`__
   file.

   -  set ``pmm.enabled=true``
   -  make sure that ``serverHost`` (the PMM service name,
      ``monitoring-service`` by default) is the same as one specified
      for the ``name`` parameter on the previous step, but with
      additional ``-service`` suffix.
   -  make sure that ``PMM_USER`` and ``PMM_PASSWORD`` keys in the
      `deploy/mongodb-users.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/mongodb-users.yaml>`__
      secrets file are the same as ones specified for the
      ``credentials.username`` and ``credentials.password`` parameters
      on the previous step (if not, fix it and apply with the
      ``kubectl apply -f deploy/mongodb-users.yaml`` command).

   When done, apply the edited ``deploy/cr.yaml`` file:

   ::

      $ kubectl apply -f deploy/cr.yaml

5. To make sure everything gone right, check that correspondent Pods are
   not continuously restarting (which would occur in case of any errors
   on the previous two steps):

   ::

      $ kubectl get pods
      $ kubectl logs my-cluster-name-rs0-0 -c pmm-client

6. Find the external IP address (``EXTERNAL-IP`` field in the output of
   ``kubectl get service/monitoring-service -o wide``). This IP address
   can be used to access PMM via *https* in a web browser, with the
   login/password authentication, already configured and able to `show
   Percona Server for MongoDB
   metrics <https://www.percona.com/doc/percona-monitoring-and-management/index.metrics-monitor.dashboard.html#pmm-dashboard-mongodb-list>`__.
