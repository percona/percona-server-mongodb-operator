Use Docker images from a custom registry
========================================

Using images from a private Docker registry may required for
privacy, security or other reasons. In these cases, Percona Server for
MongoDB Operator allows the use of a custom registry This following example of the
Operator deployed in the OpenShift environment demonstrates the process:

1. Log into the OpenShift and create a project.

   .. code:: bash

      $ oc login
      Authentication required for https://192.168.1.100:8443 (openshift)
      Username: admin
      Password:
      Login successful.
      $ oc new-project psmdb
      Now using project "psmdb" on server "https://192.168.1.100:8443".

2. You need obtain the following objects to configure your custom registry
   access:

   -  A user token
   -  the registry IP address

   You can view the token with the following command:

   .. code:: bash

      $ oc whoami -t
      ADO8CqCDappWR4hxjfDqwijEHei31yXAvWg61Jg210s

   The following command returns the registry IP address:

   .. code:: bash

      $ kubectl get services/docker-registry -n default
      NAME              TYPE        CLUSTER-IP       EXTERNAL-IP   PORT(S)    AGE
      docker-registry   ClusterIP   172.30.162.173   <none>        5000/TCP   1d

3. Use the user token and the registry IP address to login to the
   registry:

   .. code:: bash

      $ docker login -u admin -p ADO8CqCDappWR4hxjfDqwijEHei31yXAvWg61Jg210s 172.30.162.173:5000
      Login Succeeded

4. Use the Docker commands to pull the needed image by its SHA digest:

   .. code:: bash

      $ docker pull docker.io/perconalab/percona-server-mongodb-operator@sha256:69c935ac93d448db76f257965470367683202f725f50d6054eae1c3d2e731b9a
      Trying to pull repository docker.io/perconalab/percona-server-mongodb-operator ...
      sha256:69c935ac93d448db76f257965470367683202f725f50d6054eae1c3d2e731b9a: Pulling from docker.io/perconalab/percona-server-mongodb-operator
      Digest: sha256:69c935ac93d448db76f257965470367683202f725f50d6054eae1c3d2e731b9a
      Status: Image is up to date for docker.io/perconalab/percona-server-mongodb-operator@sha256:69c935ac93d448db76f257965470367683202f725f50d6054eae1c3d2e731b9a

5. The following method can push an image to the custom registry
   for the example OpenShift PSMDB project:

   .. code:: bash

      $ docker tag \
          docker.io/perconalab/percona-server-mongodb-operator@sha256:69c935ac93d448db76f257965470367683202f725f50d6054eae1c3d2e731b9a \
          172.30.162.173:5000/psmdb/percona-server-mongodb-operator:0.2.1-mongod3.6
      $ docker push 172.30.162.173:5000/psmdb/percona-server-mongodb-operator:0.2.1-mongod3.6

6. Verify the image is available in the OpenShift registry with the following command:

   .. code:: bash

      $ oc get is
      NAME                              DOCKER REPO                                                             TAGS             UPDATED
      percona-server-mongodb-operator   docker-registry.default.svc:5000/psmdb/percona-server-mongodb-operator  0.2.1-mongod3.6  2 hours ago

7. When the custom registry image is available, edit the the ``image:`` option in ``deploy/operator.yaml`` configuration file with a Docker Repo + Tag string (it should look like``docker-registry.default.svc:5000/psmdb/percona-server-mongodb-operator:0.2.1-mongod3.6``)

   .. note::

      If the registry requires authentication, you can specify the ``imagePullSecrets`` option for all images.

8. Repeat steps 3-5 for other images, and update corresponding options
   in the ``deploy/cr.yaml`` file.

9. Now follow the standard `Percona Server for MongoDB Operator
   installation instruction <./openshift.html>`__.

Percona certified images
------------------------

Following table presents Percona’s certified images to be used with the
Percona Server for MongoDB Operator:

      .. list-table:: 
         :widths: 15 30
         :header-rows: 1

         * - Image
           - Digest
         * - percona/percona-server-mongodb-operator:0.3.0  
           - 69d2018790ed14de1a79bef1fd7afc5fb91b57374f1e4ca33e5f48996646bb3e
         * - percona/percona-server-mongodb-operator:0.3.0-mongod3.6.10
           - a02a10c9e0bc36fac2b1a7e1215832c5816abfbbe0018fca61d133835140b4e8
         * - percona/percona-server-mongodb-operator:0.3.0-mongod4.0.6
           - 0849fee6073e85414ca36d4f394046342d623292f03e9d3afd5bd5b02e6df812
         * - percona/percona-server-mongodb-operator:0.3.0-backup
           - 5a32ddf1194d862b5f6f3826fa85cc4f3c367ccd8e69e501f27b6bf94f7e3917
         * - perconalab/pmm-client:1.17.1
           - f762cda2eda9ef17bfd1242ede70ee72595611511d8d0c5c46931ecbc968e9af 

