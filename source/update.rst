Update Percona Server for MongoDB Operator
===========================================

Starting from the version 1.1.0 the Percona Kubernetes Operator for MongoDB allows
upgrades to newer versions. The upgrade can be either semi-automatic or manual.

.. note:: Manual update mode is the recommended way for a production cluster.

.. note:: Only the incremental update to a nearest minor version is supported
   (for example, update from 1.3.0 to 1.4.0).
   To update to a newer version, which differs from the current version by more
   than one, make several incremental updates sequentially.

Semi-automatic update
---------------------

#. Edit the ``deploy/cr.yaml`` file, setting ``updateStrategy`` key to
   ``RollingUpdate``.

#. Now you should `apply a patch <https://kubernetes.io/docs/tasks/run-application/update-api-object-kubectl-patch/>`_ to your
   deployment, supplying necessary image names with a newer version tag. This
   is done with the ``kubectl patch deployment`` command. For example, updating
   to the ``{{{release}}}`` version should look as follows::

     kubectl patch deployment percona-server-mongodb-operator \
        -p'{"spec":{"template":{"spec":{"containers":[{"name":"percona-server-mongodb-operator","image":"percona/percona-server-mongodb-operator:{{{release}}}"}]}}}}'

     kubectl patch psmdb my-cluster-name --type=merge --patch '{
        "metadata": {"annotations":{ "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"pxc.percona.com/v{{{apiversion}}}\"}" }},
        "spec": {
            "image": "percona/percona-server-mongodb-operator:{{{release}}}-mongod4.0",
            "backup": { "image": "percona/percona-server-mongodb-operator:{{{release}}}-backup" },
            "pmm": { "image": "percona/percona-server-mongodb-operator:{{{release}}}-pmm" }
        }}'

#. The deployment rollout will be automatically triggered by the applied patch.
   You can track the rollout process in real time using the
   ``kubectl rollout status`` command with the name of your cluster::

     kubectl rollout status sts cluster1-pxc

Manual update
-------------

#. Edit the ``deploy/cr.yaml`` file, setting ``updateStrategy`` key to
   ``OnDelete``.

#. Now you should `apply a patch <https://kubernetes.io/docs/tasks/run-application/update-api-object-kubectl-patch/>`_ to your
   deployment, supplying necessary image names with a newer version tag. This
   is done with the ``kubectl patch deployment`` command. For example, updating
   to the ``{{{release}}}`` version should look as follows::

     kubectl patch deployment percona-server-mongodb-operator \
        -p'{"spec":{"template":{"spec":{"containers":[{"name":"percona-server-mongodb-operator","image":"percona/percona-server-mongodb-operator:{{{release}}}"}]}}}}'

     kubectl patch psmdb my-cluster-name --type=merge --patch '{
        "metadata": {"annotations":{ "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"pxc.percona.com/v{{{apiversion}}}\"}" }},
        "spec": {"replsets":{ "image": "percona/percona-server-mongodb-operator:{{{release}}}-mongod4.0" },
            "mongod": { "image": "percona/percona-server-mongodb-operator:{{{release}}}-mongod4.0" },
            "backup":   { "image": "percona/percona-server-mongodb-operator:{{{release}}}-backup" },
            "pmm": { "image": "percona/percona-server-mongodb-operator:{{{release}}}-pmm" }
        }}'

#. Pod with the newer Percona Server for MongoDB image will start after you
   delete it. Delete targeted Pods manually one by one to make them restart in
   the desired order:

   #. Delete the Pod using its name with the command like the following one::

         kubectl delete pod my-cluster-name-rs0-2


   #. Wait until Pod becomes ready::

         kubectl get pod my-cluster-name-rs0-2


      The output should be like this::

         NAME                    READY   STATUS    RESTARTS   AGE
         my-cluster-name-rs0-2   1/1     Running   0          3m33s

#. The update process is successfully finished when all Pods have been
   restarted.
