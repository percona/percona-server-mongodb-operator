Install Percona server for MongoDB on OpenShift
===============================================

0. Clone the percona-server-mongodb-operator repository:

   .. code:: bash

      git clone -b release-{{release}} https://github.com/percona/percona-server-mongodb-operator
      cd percona-server-mongodb-operator

   .. note::

      It is crucial to specify the right branch with ``-b``
      option while cloning the code on this step. Please be careful.

1. The Custom Resource Definition for PSMDB should be created from the
   ``deploy/crd.yaml`` file. The Custom Resource Definition extends the
   standard set of resources which Kubernetes “knows” about with the new
   items, in our case these items are the core of the operator.

   This step should be done only once; it does not need to be repeated with other deployments.

   .. code:: bash

      $ oc apply -f deploy/crd.yaml

   .. note::

      Setting Custom Resource Definition requires your user to
      have cluster-admin role privileges.

   If you want to manage PSMDB cluster with a non-privileged user, the
   necessary permissions can be granted by applying the next clusterrole:

   .. code:: bash

      $ oc create clusterrole psmdb-admin --verb="*" --resource=perconaservermongodbs.psmdb.percona.com,perconaservermongodbs.psmdb.percona.com/status,perconaservermongodbbackups.psmdb.percona.com,perconaservermongodbbackups.psmdb.percona.com/status,perconaservermongodbrestores.psmdb.percona.com,perconaservermongodbrestores.psmdb.percona.com/status
      $ oc adm policy add-cluster-role-to-user psmdb-admin <some-user>

   If you have a `cert-manager <https://docs.cert-manager.io/en/release-0.8/getting-started/install/openshift.html>`_ installed, then you have to execute two more commands to be able to manage your PSMDB cluster with a non-privileged user:

   .. code:: bash

      $ oc create clusterrole cert-admin --verb="*" --resource=iissuers.certmanager.k8s.io,certificates.certmanager.k8s.io
      $ oc adm policy add-cluster-role-to-user cert-admin <some-user>

2. Create a new ``psmdb`` project:

   .. code:: bash

      $ oc new-project psmdb

3. Add role-based access control (RBAC) for PSMDB is configured with
   the ``deploy/rbac.yaml`` file. RBAC is
   based on clearly defined roles and corresponding allowed actions. These actions are allowed on specific Kubernetes resources. The details
   about users and roles can be found in `OpenShift
   documentation <https://docs.openshift.com/enterprise/3.0/architecture/additional_concepts/authorization.html>`_.

   .. code:: bash

      $ oc apply -f deploy/rbac.yaml

4. Start the Operator within OpenShift:

   .. code:: bash

      $ oc apply -f deploy/operator.yaml

4. Add the MongoDB Users secrets to OpenShift. These secrets
   should be placed in the data section of the
   ``deploy/secrets.yaml`` file as login names and base64-encoded
   passwords (see `Kubernetes
   documentation <https://kubernetes.io/docs/concepts/configuration/secret/>`_
   for details).

   .. note::

      The following command can be used to return a base64-encoded
      password from a plain text string::

        $ echo -n 'plain-text-password' | base64

   After editing the yaml file, the secrets should be created or
   updated with the following command:

   .. code:: bash

      $ oc apply -f deploy/secrets.yaml

   More details about secrets can be found in `Users <users.html>`_.

5. Install `cert-manager <https://docs.cert-manager.io/en/release-0.8/getting-started/install/openshift.html>`_ if it is not up and running yet, then generate and apply certificates as secrets according to `TLS document <TLS.html>`_.

   Pre-generated certificates are awailable in the ``deploy/ssl-secrets.yaml`` secrets file for test purposes, but we strongly recommend avoiding their usage on any production system.

   .. code:: bash

      $ oc apply -f <secrets file>

6. Percona Server for MongoDB cluster can
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

   b (optional). In you're using minishift, please adjust antiaffinity policy to ``none``
      
       .. code:: yaml

            affinity:
              antiAffinityTopologyKey: "none"
         ...

   c. Create/apply the CR file:

      .. code:: bash

         $ oc apply -f deploy/cr.yaml

   The creation process will take time. The process is complete when both the
   operator and the replica set pod have reached their Running status:

   .. code:: bash

      $ oc get pods
      NAME                                               READY   STATUS    RESTARTS   AGE
      my-cluster-name-rs0-0                              1/1     Running   0          8m
      my-cluster-name-rs0-1                              1/1     Running   0          8m
      my-cluster-name-rs0-2                              1/1     Running   0          7m
      percona-server-mongodb-operator-754846f95d-sf6h6   1/1     Running   0          9m

7. Check connectivity to newly created cluster. Please note that mongo client command shall be executed inside the container manually.

   .. code:: bash

      $ oc run -i --rm --tty percona-client --image=percona/percona-server-mongodb:4.0 --restart=Never -- bash -il
      percona-client:/$ mongo "mongodb+srv://userAdmin:userAdmin123456@my-cluster-name-rs0.psmdb.svc.cluster.local/admin?replicaSet=rs0&ssl=false"
