.. _install-minikube:

Install Percona Server for MongoDB on Minikube
==============================================

Installing the Percona Distribution for MongoDB Operator on `Minikube <https://github.com/kubernetes/minikube>`_
is the easiest way to try it locally without a cloud provider. Minikube runs
Kubernetes on GNU/Linux, Windows, or macOS system using a system-wide
hypervisor, such as VirtualBox, KVM/QEMU, VMware Fusion or Hyper-V. Using it is
a popular way to test Kubernetes application locally prior to deploying it on a
cloud.

The following steps are needed to run Percona Distribution for MongoDB Operator on minikube:

#. `Install minikube <https://kubernetes.io/docs/tasks/tools/install-minikube/>`_, using a way recommended for your system. This includes the installation of the following three components:

   #. kubectl tool,
   #. a hypervisor, if it is not already installed,
   #. actual minikube package

   After the installation, run ``minikube start --memory=5120 --cpus=4 --disk-size=30g``
   (parameters increase the virtual machine limits for the CPU cores, memory, and disk,
   to ensure stable work of the Operator). Being executed, this command will
   download needed virtualized images, then initialize and run the
   cluster. After Minikube is successfully started, you can optionally run the
   Kubernetes dashboard, which visually represents the state of your cluster.
   Executing ``minikube dashboard`` will start the dashboard and open it in your
   default web browser.

#. Deploy the operator with the following command::

     kubectl apply -f https://raw.githubusercontent.com/percona/percona-server-mongodb-operator/release-1.10.0/deploy/bundle.yaml

#. Deploy MongoDB cluster with::

     kubectl apply -f https://raw.githubusercontent.com/percona/percona-server-mongodb-operator/release-1.10.0/deploy/cr-minimal.yaml
     
   This deploys a one shard MongoDB cluster with one replica set with one node,
   one mongos node and one config server node. ``deploy/cr-minimal.yaml`` is for minimal 
   non-production deployment. For more configuration options please see ``deploy/cr.yaml`` 
   and :ref:`Custom Resource Options<operator.custom-resource-options>`. The creation 
   process may take some time. The process is over when both operator and replica set pod 
   have reached their Running status. ``kubectl get pods`` output should look like this:

   .. include:: ./assets/code/kubectl-get-minimal-response.txt
   
   You can clone the repository with all manifests and source code by executing the following command::
   
      git clone -b release-1.10.0 https://github.com/percona/percona-server-mongodb-operator

#. During previous steps, the Operator has generated several `secrets <https://kubernetes.io/docs/concepts/configuration/secret/>`_,
   including the password for the admin user, which you will need to access the
   cluster. Use ``kubectl get secrets`` to see the list of Secrets objects (by
   default Secrets object you are interested in has ``minimal-cluster-name-secrets``
   name). Then ``kubectl get secret minimal-cluster-name-secrets -o yaml`` will return
   the YAML file with generated secrets, including the ``MONGODB_USER_ADMIN``
   and ``MONGODB_USER_ADMIN_PASSWORD`` strings, which should look as follows::

     ...
     data:
       ...
       MONGODB_USER_ADMIN_PASSWORD: aDAzQ0pCY3NSWEZ2ZUIzS1I=
       MONGODB_USER_ADMIN_USER: dXNlckFkbWlu

   Here the actual login name and password are base64-encoded, and
   ``echo 'aDAzQ0pCY3NSWEZ2ZUIzS1I=' | base64 --decode`` will bring it back to a
   human-readable form.

#. Check connectivity to a newly created cluster.

   First of all, run a container with a MongoDB client and connect its console
   output to your terminal. The following command will do this, naming the new
   Pod ``percona-client``:

   .. code:: bash

      kubectl run -i --rm --tty percona-client --image=percona/percona-server-mongodb:{{{mongodb44recommended}}} --restart=Never -- bash -il
   
   Executing it may require some time to deploy the correspondent Pod.  Now run
   ``mongo`` tool in the percona-client command shell using the login (which is
   ``userAdmin``) and password obtained from the secret:
   
   .. code:: bash

      mongo "mongodb://userAdmin:userAdminPassword@minimal-cluster-name-mongos.default.svc.cluster.local/admin?ssl=false"
