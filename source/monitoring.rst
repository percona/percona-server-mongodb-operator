Monitoring
==========

The Percona Monitoring and Management (PMM) `provides an excellent
solution <https://www.percona.com/doc/percona-monitoring-and-management/index.html>`__
to monitor Percona Server for MongoDB.

Installing the PMM Server
-------------------------

This first section installs the PMM Server to monitor Percona Server for MongoDB on Kubernetes or
OpenShift. The following steps are optional if
you already have installed the PMM Server. The PMM Server available on
your network does not require another installation in Kubernetes.

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

   OpenShift command:

   ::

      $ helm install monitoring percona/pmm-server --set platform=openshift --version 1.17.3 --set "credentials.password=supa|^|pazz"

   Kubernetes command:

   ::

      $ helm install monitoring percona/pmm-server --set platform=kubernetes --version 2.7.0 --set "credentials.password=supa|^|pazz"

Installing the PMM Client
-------------------------

The following steps are needed for the PMM client installation:

1. The PMM client installation is initiated by updating the ``pmm``
   section in the
   `deploy/cr.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/cr.yaml>`__
   file.

   -  set ``pmm.enabled=true``
   -  ensure the ``serverHost`` (the PMM service name is
      ``monitoring-service`` by default) is the same as value specified
      for the ``name`` parameter on the previous step, but with an added
      additional ``-service`` suffix.
   -  check that ``PMM_USER`` key in the
      `deploy/secrets.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/secrets.yaml>`_
      secrets file is a based64 encoded equivalent of the PMM Server user name (``pmm`` by default for PMM
      1.x and ``admin`` for PMM 2.x).
   -  make sure the ``PMM_PASSWORD`` key in the
      `deploy/secrets.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/secrets.yaml>`_
      secrets file is a base64 encoded equivalent of the value specified for the
      ``credentials.password`` parameter
      on the last PMM Server installation step.

   Apply changes with the ``kubectl apply -f deploy/secrets.yaml`` command.

   When done, apply the edited ``deploy/cr.yaml`` file:

   ::

      $ kubectl apply -f deploy/cr.yaml

2. Check that correspondent Pods are
   not in a cycle of stopping and restarting. This cycle occurs if there are errors on the previous steps:

   ::

      $ kubectl get pods
      $ kubectl logs my-cluster-name-rs0-0 -c pmm-client

3. Run the following command:

   ``kubectl get service/monitoring-service -o wide``

   In the results, locate the the ``EXTERNAL-IP`` field. The external-ip address
   can be used to access PMM via *https* in a web browser, with the
   login/password authentication, and the browser is configured to `show
   Percona Server for MongoDB
   metrics <https://www.percona.com/doc/percona-monitoring-and-management/index.metrics-monitor.dashboard.html#pmm-dashboard-mongodb-list>`__.
