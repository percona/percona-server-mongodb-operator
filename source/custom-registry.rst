.. _custom-registry:

Use Docker images from a custom registry
========================================

Using images from a private Docker registry may required for
privacy, security or other reasons. In these cases, Percona Distribution for
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

      $ docker pull docker.io/perconalab/percona-server-mongodb@sha256:991d6049059e5eb1a74981290d829a5fb4ab0554993748fde1e67b2f46f26bf0
      Trying to pull repository docker.io/perconalab/percona-server-mongodb ...
      sha256:991d6049059e5eb1a74981290d829a5fb4ab0554993748fde1e67b2f46f26bf0: Pulling from docker.io/perconalab/percona-server-mongodb
      Digest: sha256:991d6049059e5eb1a74981290d829a5fb4ab0554993748fde1e67b2f46f26bf0
      Status: Image is up to date for docker.io/perconalab/percona-server-mongodb@sha256:991d6049059e5eb1a74981290d829a5fb4ab0554993748fde1e67b2f46f26bf0

   You can find correct names and SHA digests in the
   :ref:`current list of the Operator-related images officially certified by Percona<custom-registry-images>`.


5. The following method can push an image to the custom registry
   for the example OpenShift ``psmdb`` project:

   .. code:: bash

      $ docker tag \
          docker.io/perconalab/percona-server-mongodb@sha256:991d6049059e5eb1a74981290d829a5fb4ab0554993748fde1e67b2f46f26bf0 \
          172.30.162.173:5000/psmdb/percona-server-mongodb:{{{mongodb44recommended}}}
      $ docker push 172.30.162.173:5000/psmdb/percona-server-mongodb:{{{mongodb44recommended}}}

6. Verify the image is available in the OpenShift registry with the following command:

   .. code:: bash

      $ oc get is
      NAME                              DOCKER REPO                                                             TAGS             UPDATED
      percona-server-mongodb            docker-registry.default.svc:5000/psmdb/percona-server-mongodb  {{{mongodb44recommended}}}  2 hours ago

7. When the custom registry image is available, edit the the ``image:`` option in ``deploy/operator.yaml`` configuration file with a Docker Repo + Tag string (it should look like``docker-registry.default.svc:5000/psmdb/percona-server-mongodb:{{{mongodb44recommended}}}``)

   .. note::

      If the registry requires authentication, you can specify the ``imagePullSecrets`` option for all images.

8. Repeat steps 3-5 for other images, and update corresponding options
   in the ``deploy/cr.yaml`` file.

9. Now follow the standard `Percona Distribution for MongoDB Operator installation instruction <./openshift.html>`_

