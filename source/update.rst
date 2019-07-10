Update Percona Server for MongoDB Operator
===========================================

Starting from the version 1.1.0 Percona Kubernetes Operator for MongoDB allows
upgrades to newer versions. This upgrade can be done either in semi-automatic
or in manual mode.

.. note:: Manual update mode is the recomended way for a production cluster.

Semi-automatic update
---------------------

#. Edit the ``deploy/cr.yaml`` file, setting ``updateStrategy`` key to
   ``RollingUpdate``.

#. Now you should `apply a patch <https://kubernetes.io/docs/tasks/run-application/update-api-object-kubectl-patch/>`_ to your
   deployment, supplying necessary image names with a newer version tag. This
   is done with the ``kubectl patch deployment`` command. For example, updating
   to ``1.1.0`` version should look as follows::

     kubectl patch deployment percona-server-mongodb-operator \
        -p'{"spec":{"template":{"spec":{"containers":[{"name":"percona-server-mongodb-operator","image":"percona/percona-server-mongodb-operator:1.1.0"}]}}}}'

     kubectl patch psmdb my-cluster-name --type=merge --patch '{
        "spec": {
            "image": "percona/percona-server-mongodb-operator:1.1.0-mongod4.0",
            "backup": { "image": "percona/percona-server-mongodb-operator:1.1.0-backup" }
        }}'

#. The deployment rollout will be automatically triggered by the applied patch.
   You can track the rolluot process in real time with the
   ``kubectl rollout status`` command with the name of your cluster::

     kubectl rollout status sts cluster1-pxc

Manual update
-------------

#. Edit the ``deploy/cr.yaml`` file, setting ``updateStrategy`` key to
   ``OnDelete``.

#. Now you should `apply a patch <https://kubernetes.io/docs/tasks/run-application/update-api-object-kubectl-patch/>`_ to your
   deployment, supplying necessary image names with a newer version tag. This
   is done with the ``kubectl patch deployment`` command. For example, updating
   to ``1.1.0`` version should look as follows::

     kubectl patch deployment percona-server-mongodb-operator \
        -p'{"spec":{"template":{"spec":{"containers":[{"name":"percona-server-mongodb-operator","image":"percona/percona-server-mongodb-operator:1.1.0"}]}}}}'

     kubectl patch psmdb my-cluster-name --type=merge --patch '{
        "spec": {"replsets":{ "image": "percona/percona-server-mongodb-operator:1.1.0-mongod4.0" },
            "mongod": { "image": "percona/percona-server-mongodb-operator:1.1.0-mongod4.0" },
            "backup":   { "image": "percona/percona-server-mongodb-operator:1.1.0-backup" }
        }}'

#. Pod with the newer Percona Server for MongoDB image will start after you
   delete it. Delete targeted Pods manually one by one to make them restart in
   desired order:

   #. Delete the Pod using its name with the command like the following one::

         kubectl delete pod my-cluster-name-rs0-2


   #. Wait until Pod becomes ready::

         kubectl get pod my-cluster-name-rs0-2


      The output should be like this::

         NAME                    READY   STATUS    RESTARTS   AGE
         my-cluster-name-rs0-2   1/1     Running   0          3m33s

#. The update process is successfully finished when all Pods have been
   restarted.
