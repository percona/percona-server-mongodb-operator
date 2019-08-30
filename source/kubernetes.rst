Install Percona server for MongoDB on Kubernetes
================================================

0. Clone the percona-server-mongodb-operator repository:

   .. code:: bash

      git clone -b release-1.0.0 https://github.com/percona/percona-server-mongodb-operator
      cd percona-server-mongodb-operator

   .. note::

      It is crucial to specify the right branch with ``-b``
      option while cloning the code on this step. Please be careful.

1. The Custom Resource Definition for PSMDB should be created from the
   ``deploy/crd.yaml`` file. The Custom Resource Definition extends the
   standard set of resources which Kubernetes “knows” about with the new
   items (in our case resources which are the core of the operator).

   .. code:: bash

      $ kubectl apply -f deploy/crd.yaml

   This step should be done only once; the step does not need to be repeated
   with any other Operator deployments.

2. Add the ``psmdb`` namespace to Kubernetes,
   and set the correspondent context for further steps:

   .. code:: bash

      $ kubectl create namespace psmdb
      $ kubectl config set-context $(kubectl config current-context) --namespace=psmdb

3. The role-based access control (RBAC) for PSMDB is configured with the ``deploy/rbac.yaml`` file. Role-based access is
   based on defined roles and the available actions which correspond to
   each role. The role and actions are defined for Kubernetes resources in the yaml file. Further details
   about users and roles can be found in `Kubernetes
   documentation <https://kubernetes.io/docs/reference/access-authn-authz/rbac/#default-roles-and-role-bindings>`_.

   .. code:: bash

      $ kubectl apply -f deploy/rbac.yaml

   .. note::

      Setting RBAC requires your user to have cluster-admin role
      privileges. For example, those using Google Kubernetes Engine can
      grant user needed privileges with the following command::

         $ kubectl create clusterrolebinding cluster-admin-binding --clusterrole=cluster-admin --user=$(gcloud config get-value core/account)

4. Start the operator within Kubernetes:

   .. code:: bash

      $ kubectl apply -f deploy/operator.yaml

5. Add the MongoDB Users secrets to Kubernetes. These secrets
   should be placed in the data section of the
   ``deploy/secrets.yaml`` file as login name and the base64-encoded
   passwords for the user accounts (see `Kubernetes
   documentation <https://kubernetes.io/docs/concepts/configuration/secret/>`__
   for details).

   .. note::

      The following command can be used to get base64-encoded
      password from a plain text string::

        $ echo -n 'plain-text-password' | base64

   After editing the yaml file, MongoDB Users secrets should be created
   (or updated with the new passwords) using the following command:

   .. code:: bash

      $ kubectl apply -f deploy/secrets.yaml

   More details about secrets can be found in `Users <users.html>`_.

6. Install `cert-manager <https://docs.cert-manager.io/en/release-0.8/getting-started/install/kubernetes.html>`_ if it is not up and running yet and apply ssl secrets with the following command:
   
   Pre-generated certificates are awailable in the ``deploy/ssl-secrets.yaml`` secrets file for test purposes, but we strongly recommend avoiding their usage on any production system.

   .. code:: bash

      $ kubectl apply -f <secrets file>

7. After the operator is started, Percona Server for MongoDB cluster can
   be created with the following command:

   .. code:: bash

      $ kubectl apply -f deploy/cr.yaml

   The creation process may take some time. The process is over when both
   operator and replica set pod have reached their Running status:

   .. code:: bash

      $ kubectl get pods
      NAME                                               READY   STATUS    RESTARTS   AGE
      my-cluster-name-rs0-0                              1/1     Running   0          8m
      my-cluster-name-rs0-1                              1/1     Running   0          8m
      my-cluster-name-rs0-2                              1/1     Running   0          7m
      percona-server-mongodb-operator-754846f95d-sf6h6   1/1     Running   0          9m

6. Check connectivity to newly created cluster

   .. code:: bash

      $ kubectl run -i --rm --tty percona-client --image=percona/percona-server-mongodb:3.6 --restart=Never -- bash -il
      percona-client:/$ mongo "mongodb+srv://userAdmin:userAdmin123456@my-cluster-name-rs0.psmdb.svc.cluster.local/admin?replicaSet=rs0&ssl=false"
