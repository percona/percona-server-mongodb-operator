.. _operator-replication:

Set up Percona Server for MongoDB cross-site replication
========================================================

The cross-site replication involves configuring one MongoDB cluster as *Main*, and another MongoDB cluster as *Replica* to allow replication between them:

 .. image:: ./assets/images/pxc-replication.svg
   :align: center

The Operator automates configuration of *Main* and *Replica* MongoDB Clusters, but the feature itself is not bound to Kubernetes. Either *Main* or *Replica* can run outside of Kubernetes, be regular MongoDB and be out of the Operators’ control.

This feature can be useful in several cases: for example, it can simplify migration from on-premises to the cloud with replication, and it can be really helpful in case of the disaster recovery too.

.. Describe how to stop/start replication
   Describe how to perform a failover

Configuring the cross-site replication for the cluster controlled by the Operator is explained in the following subsections.

.. contents:: :local:

.. _operator-replication-expose:

Exposing instances of the MongoDB cluster
--------------------------------------------

You need to expose ReplicaSet instances of both clusters (including Config
Servers) through a dedicated service to ensure that *Main* and *Replica*
clusters can reach each other. This is done through the
``replsets.expose`` and ``sharding.expose`` sections in the ``deploy/cr.yaml``
configuration file as follows.

.. code:: yaml

   spec:
     replsets:
     - rs0:
       expose:
         enabled: true
         exposeType: LoadBalancer
       ...
     sharding:
       configsvrReplSet:
         expose:
           enabled: true
           exposeType: LoadBalancer
         ...

The above example is using the LoadBalancer Kubernetes Service object, but there
are other options (ClusterIP, NodePort, etc.).

.. note:: The above example will create a LoadBalancer per each MongoDB Pod.
   In most cases, this Load Balancer should be internet-facing for cross-region
   replication to work.
   
To list the endpoints assigned to Pods, list the Kubernetes Service objects by 
executing ``kubectl get services -l "app.kubernetes.io/instance=CLUSTER_NAME"`` command.


.. _operator-replication-source:

Configuring cross-site replication on Main instances
------------------------------------------------------

The cluster managed by the Operator should "know" about external nodes for the
Replica Sets. You can configure this information in the
``replsets.externalNodes`` and ``sharding.configsvrReplset.externalNodes``
subsections of the ``deploy/cr.yaml`` configuration file. Following keys can
be set to specify each exernal *Replica*, both for its Replica Set and Config Server
instances:

* set ``host`` to URL or IP address of the external replset instance,
* set ``port`` to the port number of the external node (or rely on the ``27017``
  default value),

Optionaly you can set the following additional keys:

* ``priority`` key sets the `priority <https://docs.mongodb.com/manual/reference/replica-configuration/#mongodb-rsconf-rsconf.members-n-.priority>`_
  of the external node (``2`` by default for all local members of the cluster;
  external nodes should have lower priority to avoid some unmanaged node elected
  as a primary; ``0`` adds the node as a :ref:`non-voting member<nonvoting>`),
* ``votes`` key sets the number of `votes <https://docs.mongodb.com/manual/reference/replica-configuration/#mongodb-rsconf-rsconf.members-n-.votes>`_
  an external node can cast in a replica set election (``0`` by default, and
  ``0`` for non-voting members of the cluster). 

Here is an example:

.. code:: yaml

   spec:
     unmanaged: false
     replsets:
     - name: rs0
       externalNodes:
       - host: rs0-1.percona.com
         port: 27017
         priority: 0
         votes: 0   
       - host: rs0-2.percona.com
       ...
     sharding:
       configsvrReplSet:
         size: 3
         externalNodes:
           - host: cfg-1.percona.com
             port: 27017
             priority: 0
             votes: 0   
           - host: cfg-2.percona.com
           ...

The *Main* cluster will be ready for replication when you apply changes as usual:

.. code:: bash

   $ kubectl apply -f deploy/cr.yaml

.. _operator-replication-source-secrets:

Getting the Main cluster secrets and certificates to be copied to Replica
*************************************************************************

*Main* and *Replica* cluster should have same Secrets objects (to have same
users credentials) and certificates. So you may need to copy them from your
*Main* cluster. Names of the corresponding objects are set in the ``users``,
``ssl``, and ``sslInternal`` keys of the Custom Resource ``secrets`` subsection
(``my-cluster-name-secrets``, ``my-cluster-name-ssl``, and
``my-cluster-name-ssl-internal`` by default).

If you can get Secrets from an existing cluster by executing the
``kubectl get secret`` command for *each* Secrets object you want to acquire:

.. code:: bash

   $ kubectl get secret my-cluster-name-secrets -o yaml > my-cluster-secrets.yaml

Next remove the ``annotations``, ``creationTimestamp``, ``resourceVersion``,
``selfLink``, and ``uid`` metadata fields from the resulting file to make it
ready for the *Replica* cluster.

.. _operator-replication-replica:

Configuring cross-site replication on Replica instances
-------------------------------------------------------

When the Operator creates a new cluster, a lot of things are happening, such as
electing the Primary, generating certificates, and picking specific names. This
should not happen if we want Operator to run the cluster as *Replica*, so first
of all the cluster should be put into unmanaged state by setting the
``unmanaged`` key in the ``deploy/cr.yaml`` configuration file to true.

.. note:: Setting ``unmanaged`` to true will not only prevent the Operator from
   controlling the Replica Set configuration, but it will also result in not
   generating certificates and users credentials for new clusters.

The cluster should also "know" about external nodes for the Replica Sets. You
can configure this information in the ``replsets.externalNodes`` and
``sharding.configsvrReplset.externalNodes`` subsections of the
``deploy/cr.yaml`` configuration file. Following keys can be set to specify each
exernal instance of the *Main* cluster, (both Replica Set and Config Server
instances):

* set ``host`` to URL or IP address of the external replset instance,
* set ``port`` to the port number of the external node (or rely on the ``27017``
  default value),

Optionaly you can set the following additional keys:

* ``priority`` key sets the `priority <https://docs.mongodb.com/manual/reference/replica-configuration/#mongodb-rsconf-rsconf.members-n-.priority>`_
  of the external node (``0`` by default, which adds the node as a :ref:`non-voting member<nonvoting>`),
* ``votes`` key sets the number of `votes <https://docs.mongodb.com/manual/reference/replica-configuration/#mongodb-rsconf-rsconf.members-n-.votes>`_
  an external node can cast in a replica set election (``0`` by default, and
  ``0`` for non-voting members of the cluster).

Here is an example:

.. code:: yaml

   spec:
     unmanaged: true
     replsets:
     - name: rs0
       size: 3
       externalNodes:
       - host: rs0-repl0.percona.com
         port: 27017
         priority: 0
         votes: 0
       - host: rs0-repl1.percona.com
       ...
     sharding:
       configsvrReplSet:
       size: 3
       externalNodes:
         - host: rs0-repl0.percona.com
           port: 27017
           priority: 0
           votes: 0   
         - host: rs0-repl1.percona.com
         ...

*Main* and *Replica* cluster should have same Secrets objects, so don't forget
to apply Secrets from your *Main* cluster. Names of the corresponding objects
are set in the ``users``, ``ssl``, and ``sslInternal`` keys of the Custom
Resource ``secrets`` subsection (``my-cluster-name-secrets``,
``my-cluster-name-ssl``, and ``my-cluster-name-ssl-internal`` by default).

:ref:`Copy your secrets from an existing cluster<operator-replication-source-secrets>`
and apply each of them on your *Replica* cluster as follows:

.. code:: bash

   $  kubectl apply -f my-cluster-secrets.yaml

The *Replica* cluster will be ready for replication when you apply changes as usual:

.. code:: bash

   $ kubectl apply -f deploy/cr.yaml

