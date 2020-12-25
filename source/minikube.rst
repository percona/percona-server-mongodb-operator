.. _install-minikube:

Install Percona Server for MongoDB on Minikube
==============================================

Installing the Percona Server for MongoDB Operator on `Minikube <https://github.com/kubernetes/minikube>`_
is the easiest way to try it locally without a cloud provider. Minikube runs
Kubernetes on GNU/Linux, Windows, or macOS system using a system-wide
hypervisor, such as VirtualBox, KVM/QEMU, VMware Fusion or Hyper-V. Using it is
a popular way to test Kubernetes application locally prior to deploying it on a
cloud.

The following steps are needed to run Percona Server for MongoDB Operator on minikube:

0. `Install minikube <https://kubernetes.io/docs/tasks/tools/install-minikube/>`_, using a way recommended for your system. This includes the installation of the following three components:
   #. kubectl tool,
   #. a hypervisor, if it is not already installed,
   #. actual minikube package

   After the installation, run ``minikube start --memory=4096 --cpus=3``
   (parameters increase the virtual machine limits for the CPU cores and memory,
   to ensure stable work of the Operator). Being executed, this command will
   download needed virtualized images, then initialize and run the
   cluster. After Minikube is successfully started, you can optionally run the
   Kubernetes dashboard, which visually represents the state of your cluster.
   Executing ``minikube dashboard`` will start the dashboard and open it in your
   default web browser.

1. Clone the percona-server-mongodb-operator repository::

     git clone -b v{{{release}}} https://github.com/percona/percona-server-mongodb-operator
     cd percona-server-mongodb-operator

2. Deploy the operator with the following command::

     kubectl apply -f deploy/bundle.yaml

3. Because minikube runs locally, the default ``deploy/cr.yaml`` file should
   be edited to adapt the Operator for the the local installation with limited
   resources. Change the following keys in the ``replsets`` section:

   #. comment ``resources.requests.memory`` and ``resources.requests.cpu`` keys
      (this will fit the Operator in minikube default limitations)
   #. set ``affinity.antiAffinityTopologyKey`` key to ``"none"`` (the Operator
      will be unable to spread the cluster on several nodes)

   Also, switch ``allowUnsafeConfigurations`` key to ``true`` (this option turns
   off the Operator’s control over the cluster configuration, making it possible to
   deploy Percona Server for MongoDB as a one-node cluster).

4. Now apply the ``deploy/cr.yaml`` file with the following command::

     kubectl apply -f deploy/cr.yaml

5. During previous steps, the Operator has generated several `secrets <https://kubernetes.io/docs/concepts/configuration/secret/>`_,
   including the password for the admin user, which you will need to access the
   cluster. Use ``kubectl get secrets`` to see the list of Secrets objects (by
   default Secrets object you are interested in has ``my-cluster-name-secrets``
   name). Then ``kubectl get secret my-cluster-name-secrets -o yaml`` will return
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

6. Check connectivity to a newly created cluster.

   First of all, run percona-client and connect its console output to your
   terminal (running it may require some time to deploy the correspondent Pod): 
   
   .. code:: bash

      kubectl run -i --rm --tty percona-client --image=percona/percona-server-mongodb:{{{mongodb42recommended}}} --restart=Never -- bash -il
   
   Now run ``mongo`` tool in the percona-client command shell using the login
   (which is ``userAdmin``) and password obtained from the secret:
   
   .. code:: bash

      mongo "mongodb://userAdmin:userAdminPassword@my-cluster-name-mongos.default.svc.cluster.local/admin?ssl=false"
