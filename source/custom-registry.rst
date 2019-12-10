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

      $ docker pull docker.io/perconalab/percona-server-mongodb-operator@sha256:4b41c7149d6968a6b61c11e7af7cfea2d67057179716e2c08ba9f7f12459c902
      Trying to pull repository docker.io/perconalab/percona-server-mongodb-operator ...
      sha256:4b41c7149d6968a6b61c11e7af7cfea2d67057179716e2c08ba9f7f12459c902: Pulling from docker.io/perconalab/percona-server-mongodb-operator
      Digest: sha256:4b41c7149d6968a6b61c11e7af7cfea2d67057179716e2c08ba9f7f12459c902
      Status: Image is up to date for docker.io/perconalab/percona-server-mongodb-operator@sha256:4b41c7149d6968a6b61c11e7af7cfea2d67057179716e2c08ba9f7f12459c902

5. The following method can push an image to the custom registry
   for the example OpenShift PSMDB project:

   .. code:: bash

      $ docker tag \
          docker.io/perconalab/percona-server-mongodb-operator@sha256:4b41c7149d6968a6b61c11e7af7cfea2d67057179716e2c08ba9f7f12459c902 \
          172.30.162.173:5000/psmdb/percona-server-mongodb-operator:1.3.0-mongod3.6
      $ docker push 172.30.162.173:5000/psmdb/percona-server-mongodb-operator:1.3.0-mongod3.6

6. Verify the image is available in the OpenShift registry with the following command:

   .. code:: bash

      $ oc get is
      NAME                              DOCKER REPO                                                             TAGS             UPDATED
      percona-server-mongodb-operator   docker-registry.default.svc:5000/psmdb/percona-server-mongodb-operator  {{{release}}}-mongod3.6  2 hours ago

7. When the custom registry image is available, edit the the ``image:`` option in ``deploy/operator.yaml`` configuration file with a Docker Repo + Tag string (it should look like``docker-registry.default.svc:5000/psmdb/percona-server-mongodb-operator:{{{release}}}-mongod3.6``)

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
         * - percona/percona-server-mongodb-operator:1.3.0
           - d6abd625833fe3f3cae49721b7600bab5eeeaba78129df4796218a7ce170260d
         * - percona/percona-server-mongodb-operator:1.3.0-mongod3.6
           - 4b41c7149d6968a6b61c11e7af7cfea2d67057179716e2c08ba9f7f12459c902
         * - percona/percona-server-mongodb-operator:1.3.0-mongod4.0
           - cbe42483639e15b0c3f916f237664b63d552d7a15090025a3c130e62aa2f04b7
         * - percona/percona-server-mongodb-operator:1.3.0-backup
           - 1c79e370edf0391e7cba0b0d63d94a8cfc4bb699018e3508a2140a2c198c83c5
         * - percona/percona-server-mongodb-operator:1.3.0-pmm
           - 28bbb6693689a15c407c85053755334cd25d864e632ef7fed890bc85726cfb68
         * - percona/percona-server-mongodb-operator:1.2.0
           - fe8699da9ec2f5a2461ecc0e0ff70913ce4c9f053f86992e5a0236597871187b
         * - percona/percona-server-mongodb-operator:1.2.0-mongod3.6
           - eccbfe8682db0b88656a0db59df773172f232f8f65bd8a203782de625a4b32bf
         * - percona/percona-server-mongodb-operator:1.2.0-mongod4.0
           - baf07ebf9774832999238c03d3c713cca17e7e91d68aeefd93c04a90c5bf8619
         * - percona/percona-server-mongodb-operator:1.2.0-backup
           - 1c79e370edf0391e7cba0b0d63d94a8cfc4bb699018e3508a2140a2c198c83c5
         * - percona/percona-server-mongodb-operator:1.2.0-pmm
           - 28bbb6693689a15c407c85053755334cd25d864e632ef7fed890bc85726cfb68
         * - percona/percona-server-mongodb-operator:1.1.0
           - d5155898cd19bb70a4d100bb60bfb39d8c9de82c33a908d30fd7caeca1385fc3
         * - percona/percona-server-mongodb-operator:1.1.0-mongod3.6
           - b3a653b5143a7a60b624c825da8190af6e2e15dd3bc1baee24a7baaeaa455719
         * - percona/percona-server-mongodb-operator:1.1.0-mongod4.0
           - 6af85917a86a838c0ef14b923336f8b150e31a85978b537157d71fed857ae723
         * - percona/percona-server-mongodb-operator:1.1.0-backup
           - 1c79e370edf0391e7cba0b0d63d94a8cfc4bb699018e3508a2140a2c198c83c5
         * - percona/percona-server-mongodb-operator:1.0.0
           - 10a545afc94b7d0040bdbfeed5f64b332861dad190639baecc2989c94284efd1
         * - percona/percona-server-mongodb-operator:1.0.0-mongod3.6.12
           - 31a06ecdd74746d4ff7fe48ae06fd76b461f2a7730de3bd17d7ee4f9d0d2d1e5
         * - percona/percona-server-mongodb-operator:1.0.0-mongod4.0.9
           - 6743dc153c073477fc64db0ccf9a63939d2d949ca37d5bc2848bbc3e5ccd8a7a
         * - percona/percona-server-mongodb-operator:1.0.0-backup
           - c799d3efcb0b42cdf50c47aea8b726e3bbd8199547f438cffd70be6e2722feec

