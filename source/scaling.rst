Scale Percona Server for MongoDB on Kubernetes and OpenShift
============================================================

One of the great advantages brought by Kubernetes and the OpenShift
platform is the ease of an application scaling. Scaling a Deployment up
or down ensures new Pods are created and set to available Kubernetes
nodes.

The size of the cluster is controlled by the ``size`` key in the
:ref:`operator.custom-resource-options` configuration.

.. note:: Using ``kubectl scale StatefulSet_name`` command to rescale
   Percona Server for MongoDB is not recommended, as it makes ``size``
   configuration options out of sync, and the next config change may result
   in reverting the previous number of nodes.

You can change size separately for different components of your cluster by
setting this option in the appropriate subsections:

* :ref:`replsets.size<replsets-size>` allows to set the size of the MongoDB
  Replica Set,
* :ref:`replsets.arbiter.size<replsets-arbiter-size> allows to set the number
  of :ref:`Replica Set Arbiter instances<arbiter>`,
* :ref:`sharding.configsvrReplSet.size<sharding-configsvrreplset-size>` allows
  to set the number of `Config Server instances <https://docs.mongodb.com/manual/core/sharded-cluster-config-servers/>`_,
* :ref:`sharding.mongos.size<sharding-mongos-size>` allows to set the number of `mongos <https://docs.mongodb.com/manual/core/sharded-cluster-query-router/>`_ instances.

For example, the following update in ``deploy/cr.yaml`` will set the size of the
MongoDB Replica Set to ``5`` nodes:

.. code:: yaml

   ....
   replsets:
     ....
     size: 5
     ....

Don't forget to apply changes as usual, running the
``kubectl apply -f deploy/cr.yaml`` command.
