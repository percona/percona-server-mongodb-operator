Monitoring
==========

The Percona Monitoring and Management (PMM) `provides an excellent
solution <https://www.percona.com/doc/percona-monitoring-and-management/index.html>`__
to monitor Percona Server for MongoDB.

The following steps are needed to install both PMM Client and PMM Server. The PMM Client and PMM Server are
preconfigured to monitor Percona Server for MongoDB on Kubernetes or
OpenShift.

1. The recommended installation approach is based on using
   `helm <https://github.com/helm/helm>`__ - the package manager for
   Kubernetes, which will substantially simplify further steps. Install helm following its `official installation
   instructions <https://docs.helm.sh/using_helm/#installing-helm>`__.

2. Using helm, add the Percona chart repository and update the
   information for the available charts as follows:

   ::

      $ helm repo add percona https://percona-charts.storage.googleapis.com
      $ helm repo update

3. Use helm to install PMM Server:

   ::

      $ helm install percona/pmm-server --name monitoring --set platform=openshift --set credentials.username=clusterMonitor --set "credentials.password=clusterMonitor123456"

   You must specify the correct options in the installation
   command:

   -  ``platform`` should be either ``kubernetes`` or ``openshift``
      depending on which platform are you using.
   -  ``name`` should correspond to the ``serverHost`` key in the
      ``pmm`` section of the
      `deploy/cr.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/cr.yaml>`__
      file with a “-service” suffix, the default ``--name monitoring``
      part of the command corresponds to a
      ``monitoring-service`` value of the ``serverHost`` key.
   -  ``credentials.username`` should correspond to the
      ``MONGODB_CLUSTER_MONITOR_USER`` base64 decoded value of key in the the
      `deploy/secrets.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/secrets.yaml>`__
      file.
   -  ``credentials.password`` should correspond to a value of the
      ``MONGODB_CLUSTER_MONITOR_PASSWORD`` base64 decoded value of key specified in
      `deploy/secrets.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/secrets.yaml>`__
      secrets file. Note - the password specified in this example is the
      default development mode password and is not intended to be used on
      production systems.

4. You must edit and update the ``pmm`` section in
   the
   `deploy/cr.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/cr.yaml>`__
   file.

   -  set ``pmm.enabled=true``
   -  ensure the ``serverHost`` (the PMM service name is
      ``monitoring-service`` by default) is the same as value specified
      for the ``name`` parameter on the previous step, but with an added
      additional ``-service`` suffix.
   -  make sure the ``PMM_USER`` and ``PMM_PASSWORD`` keys in the
      `deploy/secrets.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/secrets.yaml>`__
      secrets file are the same as base64 decoded equivalent values specified for the
      ``credentials.username`` and ``credentials.password`` parameters
      on the previous step (if not, fix the value and apply with the
      ``kubectl apply -f deploy/secrets.yaml`` command).

   When done, apply the edited ``deploy/cr.yaml`` file:

   ::

      $ kubectl apply -f deploy/cr.yaml

5. Check that correspondent Pods are
   not in a cycle of stopping and restarting. This cycle occurs if there are errors on the previous steps:

   ::

      $ kubectl get pods
      $ kubectl logs my-cluster-name-rs0-0 -c pmm-client

6. Run the following command:

   ``kubectl get service/monitoring-service -o wide``

   In the results, locate the the ``EXTERNAL-IP`` field. The external-ip address
   can be used to access PMM via *https* in a web browser, with the
   login/password authentication, and the browser is configured to `show
   Percona Server for MongoDB
   metrics <https://www.percona.com/doc/percona-monitoring-and-management/index.metrics-monitor.dashboard.html#pmm-dashboard-mongodb-list>`__.
