Install Percona Server for MongoDB on OpenShift
===============================================

Installing Percona Server for MongoDB on OpenShift includes two steps:

* Installing the Percona Operator for Percona Server for MongoDB,
* Using the Operator to make actual Percona Server for MongoDB installation.

Install the Operator
--------------------

You can install Percona Operator for Percona Server for MongoDB on OpenShift using the `Red Hat Marketplace <https://marketplace.redhat.com>`_ web interface or using the command line interface.

Install the Operator via the Red Hat Marketplace
************************************************

1. login to the Red Hat Marketplace and register your cluster `following the official instructions <https://marketplace.redhat.com/en-us/workspace/clusters/add/register>`_.

2. Go to the `Kubernetes Operator for Percona Server for MongoDB <https://marketplace.redhat.com/en-us/products/percona-server-for-mongodb>`_ page and click the `Free trial` button:

   .. image:: img/marketplace-operator-page.png
      :align: center
      :alt: Percona Operator for Percona Server for MongoDB on Red Hat Marketplace

   Here you can "purchase" the Operator for 0.0 USD.

3. When finished, chose ``Workspace->Software`` in the system menu on the top and choose the Operator:

   .. image:: img/marketplace-operator-install.png
      :align: center
      :alt: Broker in the OpenShift Console

   Click the ``Install Operator`` button.

Install the Operator via the command-line interface
***************************************************

1. Clone the percona-server-mongodb-operator repository:

   .. code:: bash

      git clone -b v{{{release}}} https://github.com/percona/percona-server-mongodb-operator
      cd percona-server-mongodb-operator

   .. note::

      It is crucial to specify the right branch with ``-b``
      option while cloning the code on this step. Please be careful.

2. The Custom Resource Definition for Percona Server for MongoDB should be
   created from the ``deploy/crd.yaml`` file. The Custom Resource Definition
   extends the standard set of resources which Kubernetes “knows” about with the
   new items, in our case these items are the core of the operator.

   This step should be done only once; it does not need to be repeated with other deployments.

   .. code:: bash

      $ oc apply -f deploy/crd.yaml

   .. note::

      Setting Custom Resource Definition requires your user to
      have cluster-admin role privileges.

   If you want to manage Percona Server for MongoDB cluster with a
   non-privileged user, the necessary permissions can be granted by applying the
   next clusterrole:

   .. code:: bash

      $ oc create clusterrole psmdb-admin --verb="*" --resource=perconaservermongodbs.psmdb.percona.com,perconaservermongodbs.psmdb.percona.com/status,perconaservermongodbbackups.psmdb.percona.com,perconaservermongodbbackups.psmdb.percona.com/status,perconaservermongodbrestores.psmdb.percona.com,perconaservermongodbrestores.psmdb.percona.com/status
      $ oc adm policy add-cluster-role-to-user psmdb-admin <some-user>

   If you have a `cert-manager <https://docs.cert-manager.io/en/release-0.8/getting-started/install/openshift.html>`_ installed, then you have to execute two more commands to be able to manage certificates with a non-privileged user:

   .. code:: bash

      $ oc create clusterrole cert-admin --verb="*" --resource=iissuers.certmanager.k8s.io,certificates.certmanager.k8s.io
      $ oc adm policy add-cluster-role-to-user cert-admin <some-user>

3. Create a new ``psmdb`` project:

   .. code:: bash

      $ oc new-project psmdb

4. Add role-based access control (RBAC) for Percona Server for MongoDB is
   configured with the ``deploy/rbac.yaml`` file. RBAC is
   based on clearly defined roles and corresponding allowed actions. These
   actions are allowed on specific Kubernetes resources. The details about users
   and roles can be found in `OpenShift documentation <https://docs.openshift.com/enterprise/3.0/architecture/additional_concepts/authorization.html>`_.

   .. code:: bash

      $ oc apply -f deploy/rbac.yaml

5. Start the Operator within OpenShift:

   .. code:: bash

      $ oc apply -f deploy/operator.yaml

Install Percona Server for MongoDB
----------------------------------

1. Add the MongoDB Users secrets to OpenShift. These secrets
   should be placed as plain text in the stringData section of the
   ``deploy/secrets.yaml`` file as login name and
   passwords for the user accounts (see `Kubernetes
   documentation <https://kubernetes.io/docs/concepts/configuration/secret/>`_
   for details).

   After editing the yaml file, the secrets should be created
   with the following command:

   .. code:: bash

      $ oc create -f deploy/secrets.yaml

   More details about secrets can be found in :ref:`users`.

2. Now certificates should be generated. By default, the Operator generates
   certificates automatically, and no actions are required at this step. Still,
   you can generate and apply your own certificates as secrets according
   to the :ref:`TLS instructions <tls>`.

3. Percona Server for MongoDB cluster can
   be created at any time with the following two steps:

   a. Uncomment the ``deploy/cr.yaml`` field ``#platform:`` and edit the field
      to ``platform: openshift``. The result should be like this:

      .. code:: yaml

         apiVersion: psmdb.percona.com/v1alpha1
         kind: PerconaServerMongoDB
         metadata:
           name: my-cluster-name
         spec:
           platform: openshift
         ...

   b. (optional) In you're using minishift, please adjust antiaffinity policy to ``none``

       .. code:: yaml

            affinity:
              antiAffinityTopologyKey: "none"
         ...

   c. Create/apply the CR file:

      .. code:: bash

         $ oc apply -f deploy/cr.yaml

   The creation process will take time. The process is complete when all Pods
   have reached their Running status. You can check it with the following command:

   .. code:: bash

      $ oc get pods

   The result should look as follows:

   .. include:: ./assets/code/kubectl-get-pods-response.txt

4. Check connectivity to newly created cluster. Please note that mongo client command shall be executed inside the container manually.

   .. code:: bash

      $ oc run -i --rm --tty percona-client --image=percona/percona-server-mongodb:{{{mongodb44recommended}}} --restart=Never -- bash -il
      percona-client:/$ mongo "mongodb://userAdmin:userAdmin123456@my-cluster-name-mongos.psmdb.svc.cluster.local/admin?ssl=false"
