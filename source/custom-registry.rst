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

      $ docker pull docker.io/perconalab/percona-server-mongodb@sha256:a66e889d3e986413e41083a9c887f33173da05a41c8bd107cf50eede4588a505
      Trying to pull repository docker.io/perconalab/percona-server-mongodb ...
      sha256:a66e889d3e986413e41083a9c887f33173da05a41c8bd107cf50eede4588a505: Pulling from docker.io/perconalab/percona-server-mongodb
      Digest: sha256:a66e889d3e986413e41083a9c887f33173da05a41c8bd107cf50eede4588a505
      Status: Image is up to date for docker.io/perconalab/percona-server-mongodb@sha256:a66e889d3e986413e41083a9c887f33173da05a41c8bd107cf50eede4588a505

5. The following method can push an image to the custom registry
   for the example OpenShift PSMDB project:

   .. code:: bash

      $ docker tag \
          docker.io/perconalab/percona-server-mongodb@sha256:a66e889d3e986413e41083a9c887f33173da05a41c8bd107cf50eede4588a505 \
          172.30.162.173:5000/psmdb/percona-server-mongodb:{{{mongodb42recommended}}}
      $ docker push 172.30.162.173:5000/psmdb/percona-server-mongodb:{{{mongodb42recommended}}}

6. Verify the image is available in the OpenShift registry with the following command:

   .. code:: bash

      $ oc get is
      NAME                              DOCKER REPO                                                             TAGS             UPDATED
      percona-server-mongodb            docker-registry.default.svc:5000/psmdb/percona-server-mongodb  {{{mongodb42recommended}}}  2 hours ago

7. When the custom registry image is available, edit the the ``image:`` option in ``deploy/operator.yaml`` configuration file with a Docker Repo + Tag string (it should look like``docker-registry.default.svc:5000/psmdb/percona-server-mongodb:{{{mongodb42recommended}}}``)

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
         :widths: 35 65
         :header-rows: 1

         * - Image
           - Digest
         * - percona/percona-server-mongodb-operator:1.5.0
           - f6cc982d7c71e0aeb794099c2ef611f190825154dde7952d565f88a6342c704d
         * - percona/percona-server-mongodb:4.2.8-8
           - a66e889d3e986413e41083a9c887f33173da05a41c8bd107cf50eede4588a505
         * - percona/percona-server-mongodb:4.2.8-8-debug
           - 402aa22a8c1899d49f70933558bb3de4a9a9d094a33f1d8bdc19c6ac434cbb64
         * - percona/percona-server-mongodb:4.2.7-7
           - 1d8a0859b48a3e9cadf9ad7308ec5aa4b278a64ca32ff5d887156b1b46146b13
         * - percona/percona-server-mongodb:4.0.20-13
           - badef1eb2807b0b27a2298f697388f1dffa5398d5caa306a65fc41b98f7a72e3
         * - percona/percona-server-mongodb:4.0.20-13-debug
           - 7ecc6fa0b935f553509a94745200866b5ec81170e4b90dffb5288956d433228f
         * - percona/percona-server-mongodb:4.0.19-12
           - 24a8214d84c3a9a4147c12c4c159d4a1aa3dae831859f77b3db1a563a268e2bf
         * - percona/percona-server-mongodb:4.0.18
           - bf9e69712868f7e93daef22c14c083bbb2a74d3028d78d8597b2aeacda340c69
         * - percona/percona-server-mongodb:3.6.19-7.0
           - fbc2a312446b393a0221797c93acb8fc4df84a1f725eb78e04f5111c63dbec62
         * - percona/percona-server-mongodb:3.6.19-7.0-debug
           - c932343b43aa0190a296c4a63634bf73efe67755567341acf5e9041c2877121a
         * - percona/percona-server-mongodb:3.6.18-6.0
           - d559d75611d7bc0254a6d049dd95eacbb9b32cd7c4f7eee854d02e81e26d03f7
         * - percona/percona-server-mongodb:3.6.18-5.0
           - 0dc8bf7f135c5c7fdf15e1b9a02b0a6f08bc3de4c96f79b4f532ff682d2aff4b
         * - percona/percona-server-mongodb-operator:1.5.0-pmm
           - bf0cdfd9f9971964cb720a92e99da1a75367cf6a07deec9367ca6b80e78b0f89
         * - percona/percona-server-mongodb-operator:1.5.0-backup
           - b60c2e7a4135b9b4ece9937bae9a1ccf258ea3366389f39cecebd5ba0e1d8867
         * - percona/percona-server-mongodb-operator:1.4.0
           - fcae74acdc26a065e3d25f272a6be088daa6dd6f254207368e048ce492bcc1c0
         * - percona/percona-server-mongodb-operator:1.4.0-mongod3.6
           - 1532e1930a6aa89c56821f2a4248a0902971357fcfff3ebe336ad44e217c1aa6
         * - percona/percona-server-mongodb-operator:1.4.0-mongod4.0
           - d37a2b8c29707e521ad838939571f9587f7863e0927ac513096fbdf20e4728b7
         * - percona/percona-server-mongodb-operator:1.4.0-mongod4.2
           - d79a68524efb48d06e79e84b50870d1673cdfecc92b043d811e3a76cb0ae05ab
         * - percona/percona-server-mongodb-operator:1.4.0-backup
           - a7c661789afa45b5ccbab5ec288557b0863fd0e3b2697d113ced639b98183905
         * - percona/percona-server-mongodb-operator:1.4.0-pmm
           - bf0cdfd9f9971964cb720a92e99da1a75367cf6a07deec9367ca6b80e78b0f89
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

