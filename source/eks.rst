==========================================================================================
Install Percona Server for MongoDB on Amazon Elastic Kubernetes Service (EKS)
==========================================================================================

This quickstart shows you how to deploy |operator| on Amazon Elastic Kubernetes Service (EKS). The document assumes some experience with Amazon EKS. For more information on the EKS, see the `Amazon EKS official documentation <https://aws.amazon.com/eks/>`_.

Prerequisites
=============

The following tools are used in this guide and therefore should be preinstalled:

1. **AWS Command Line Interface (AWS CLI)** for interacting with the different
   parts of AWS. You can install it following the `official installation instructions for your system <https://docs.aws.amazon.com/cli/latest/userguide/cli-chap-install.html>`_.

2. **eksctl** to simplify cluster creation on EKS. It can be installed
   along its `installation notes on GitHub <https://github.com/weaveworks/eksctl#installation>`_.

3. **kubectl**  to manage and deploy applications on Kubernetes. Install
   it `following the official installation instructions <https://kubernetes.io/docs/tasks/tools/install-kubectl/>`_.

Also, you need to configure AWS CLI with your credentials according to the `official guide <https://docs.aws.amazon.com/cli/latest/userguide/cli-chap-configure.html>`_.

Create the EKS cluster
======================

To create your cluster, you will need the following data:

* name of your EKS cluster,
* AWS region in which you wish to deploy your cluster,
* the amount of nodes you would like tho have,
* the desired ratio between `on-demand <https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/ec2-on-demand-instances.html>`_ and `spot <https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/using-spot-instances.html>`_ instances in the total number of nodes.

.. note:: `spot <https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/using-spot-instances.html>`_ instances 
   are not recommended for production environment, but may be useful e.g. for testing purposes.

The most easy and visually clear way is to describe the desired cluster in YAML
and to pass this configuration to the ``eksctl`` command. 

The following example configures a EKS cluster with one `managed node group <https://docs.aws.amazon.com/eks/latest/userguide/managed-node-groups.html>`_:

.. code:: yaml

   apiVersion: eksctl.io/v1alpha5
   kind: ClusterConfig

   metadata:
       name: test-cluster
       region: eu-west-2

   nodeGroups:
       - name: ng-1
         minSize: 3
         maxSize: 5
         instancesDistribution:
           maxPrice: 0.15
           instanceTypes: ["m5.xlarge", "m5.2xlarge"] # At least two instance types should be specified
           onDemandBaseCapacity: 0
           onDemandPercentageAboveBaseCapacity: 50
           spotInstancePools: 2
         tags:
           'iit-billing-tag': 'cloud'
         preBootstrapCommands:
             - "echo 'OPTIONS=\"--default-ulimit nofile=1048576:1048576\"' >> /etc/sysconfig/docker"
             - "systemctl restart docker"

.. note:: ``preBootstrapCommands`` section is used in the
          above example to increase the limits for the amount of opened files:
          this is important and shouldn't be omitted, taking into account the
          default EKS soft limit of 65536 files.

When the cluster configuration file is ready, you can actually create your cluster
by the following command:

.. code:: bash

   $ eksctl create cluster -f ~/cluster.yaml


Install the Operator
=======================

1. Create a namespace and set the context for the namespace. The resource names
   must be unique within the namespace and provide a way to divide cluster
   resources between users spread across multiple projects.

   So, create the namespace and save it in the namespace context for subsequent
   commands as follows (replace the ``<namespace name>`` placeholder with some
   descriptive name):

   .. code:: bash

      $ kubectl create namespace <namespace name>
      $ kubectl config set-context $(kubectl config current-context) --namespace=<namespace name>

   At success, you will see the message that namespace/<namespace name> was created, and the context was modified.

2. Use the following ``git clone`` command to download the correct branch of the percona-server-mongodb-operator repository:

   .. code:: bash

      $ git clone -b v{{{release}}} https://github.com/percona/percona-server-mongodb-operator

   After the repository is downloaded, change the directory to run the rest of the commands in this document:

   .. code:: bash

      $ cd percona-server-mongodb-operator

3. Deploy the Operator `using <https://kubernetes.io/docs/reference/using-api/server-side-apply/>`_ the following command:

   .. code:: bash

      $ kubectl apply -f deploy/bundle.yaml --server-side

   The following confirmation is returned:

   .. code:: text

      customresourcedefinition.apiextensions.k8s.io/perconaservermongodbs.psmdb.percona.com created
      customresourcedefinition.apiextensions.k8s.io/perconaservermongodbbackups.psmdb.percona.com created
      customresourcedefinition.apiextensions.k8s.io/perconaservermongodbrestores.psmdb.percona.com created
      role.rbac.authorization.k8s.io/percona-server-mongodb-operator created
      serviceaccount/percona-server-mongodb-operator created
      rolebinding.rbac.authorization.k8s.io/service-account-percona-server-mongodb-operator created
      deployment.apps/percona-server-mongodb-operator created

4. The Operator has been started, and you can create the Percona Server for MongoDB:

   .. code:: bash

      $ kubectl apply -f deploy/cr.yaml

   The creation process may take some time. The process is over when all Pods
   have reached their Running status. You can check it with the following command:

   .. code:: bash

      $ kubectl get pods

   The result should look as follows:

   .. include:: ./assets/code/kubectl-get-pods-response.txt

5. During previous steps, the Operator has generated several `secrets <https://kubernetes.io/docs/concepts/configuration/secret/>`_, including the password for the ``root`` user, which you will need to access the cluster.

   Use ``kubectl get secrets`` command to see the list of Secrets objects (by default Secrets object you are interested in has ``my-cluster-secrets`` name). Then ``kubectl get secret my-cluster-secrets -o yaml`` will return the YAML file with generated secrets, including the ``MONGODB_USER_ADMIN``
   and ``MONGODB_USER_ADMIN_PASSWORD`` strings, which should look as follows:

   .. code:: yaml

      ...
      data:
        ...
        MONGODB_USER_ADMIN_PASSWORD: aDAzQ0pCY3NSWEZ2ZUIzS1I=
        MONGODB_USER_ADMIN_USER: dXNlckFkbWlu

   Here the actual password is base64-encoded, and ``echo 'aDAzQ0pCY3NSWEZ2ZUIzS1I=' | base64 --decode`` will bring it back to a human-readable form.

6. Check connectivity to a newly created cluster.

   First of all, run a container with a MongoDB client and connect its console
   output to your terminal. The following command will do this, naming the new
   Pod ``percona-client``:
   
   .. code:: bash

      $ kubectl run -i --rm --tty percona-client --image=percona/percona-server-mongodb:{{{mongodb44recommended}}} --restart=Never -- bash -il
   
   Executing it may require some time to deploy the correspondent Pod. Now run
   ``mongo`` tool in the percona-client command shell using the login (which is
   ``userAdmin``) and password obtained from the secret:
   
   .. code:: bash

      $ mongo "mongodb://userAdmin:userAdminPassword@my-cluster-name-mongos.<namespace name>.svc.cluster.local/admin?ssl=false"
