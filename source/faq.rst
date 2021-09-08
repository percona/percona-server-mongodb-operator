.. _faq:

================================================================================
Frequently Asked Questions
================================================================================

.. contents::
   :local:
   :depth: 1

Why do we need to follow "the Kubernetes way" when Kubernetes was never intended to run databases?
=====================================================================================================

As it is well known, the Kubernetes approach is targeted at stateless
applications but provides ways to store state (in Persistent Volumes, etc.) if
the application needs it. Generally, a stateless mode of operation is supposed
to provide better safety, sustainability, and scalability, it makes the
already-deployed components interchangeable. You can find more about substantial
benefits brought by Kubernetes to databases in `this blog post <https://www.percona.com/blog/2020/10/08/the-criticality-of-a-kubernetes-operator-for-databases/>`_.

The architecture of state-centric applications (like databases) should be
composed in a right way to avoid crashes, data loss, or data inconsistencies
during hardware failure. Percona Distribution for MongoDB Operator provides
out-of-the-box functionality to automate provisioning and
management of highly available MongoDB database clusters on Kubernetes.

How can I contact the developers?
================================================================================

The best place to discuss Percona Distribution for
MongoDB Operator with developers and other community members is the `community forum <https://forums.percona.com/categories/kubernetes-operator-percona-server-mongodb>`_.

If you would like to report a bug, use the `Percona Distribution for MongoDB Operator project in JIRA <https://jira.percona.com/projects/K8SPSMDB>`_.

What is the difference between the Operator quickstart and advanced installation ways?
=======================================================================================

As you have noticed, the installation section of docs contains both quickstart
and advanced installation guides.

The quickstart guide is simpler. It has fewer installation steps in favor of
predefined default choices. Particularly, in advanced installation guides, you
separately apply the Custom Resource Definition and Role-based Access Control
configuration files with possible edits in them. At the same time, quickstart
guides rely on the all-inclusive bundle configuration.

At another point, quickstart guides are related to specific platforms you are
going to use (Minikube, Google Kubernetes Engine, etc.) and therefore include
some additional steps needed for these platforms.

Generally, rely on the quickstart guide if you are a beginner user of the
specific platform and/or you are new to the Percona Distribution for MongoDB
Operator as a whole.

Which versions of MongoDB the Operator supports?
================================================================================

Percona Distribution for MongoDB Operator provides a ready-to-use
installation of the MongoDB-based database cluster inside your Kubernetes
installation. It works with Percona Server for MongoDB 4.0, 4.2, and 4.4,
and the exact version is determined by the Docker image in use.

Percona-certified Docker images used by the Operator are listed `here <https://www.percona.com/doc/kubernetes-operator-for-psmongodb/images.html>`_.
For example, Percona Server for MongoDB 4.4 is supported with the following
recommended version: {{{mongodb44recommended}}}. More details on the exact Percona
Server for MongoDB version can be found in the release notes (`4.4 <https://www.percona.com/doc/percona-server-for-mongodb/4.4/release_notes/index.html>`_, `4.2 <https://www.percona.com/doc/percona-server-for-mongodb/4.2/release_notes/index.html>`_, and `4.0 <https://www.percona.com/doc/percona-server-for-mongodb/4.0/release_notes/index.html>`_.

.. _faq-sidecar:

How can I add custom sidecar containers to my cluster?
================================================================================

The Operator allows you to deploy additional (so-called *sidecar*) containers to
the Pod. You can use this feature to run debugging tools, some specific
monitoring solutions, etc. Add such sidecar container to the ``deploy/cr.yaml``
configuration file, specifying its name and image, and possibly a command to
run:

.. code:: yaml

   spec:
     replsets:
     - name: rs0
       ....
       sidecars:
       - image: busybox
         command: ["/bin/sh"]
         args: ["-c", "while true; do echo echo $(date -u) 'test' >> /dev/null; sleep 5; done"]
         name: rs-sidecar-1
       ....

You can add ``sidecars`` subsection to ``replsets``,
``sharding.configsvrReplSet``, and ``sharding.mongos`` sections.

.. note::  Custom sidecar containers `can easily access other components of your cluster <https://kubernetes.io/docs/concepts/workloads/pods/#resource-sharing-and-communication>`_. Therefore
   they should be used carefully and by experienced users only.

How to provoke the initial sync of a Pod
========================================

There are certain situations where it might be necessary to delete all MongoDB
instance data to force the resync. For example, there may be the following
reasons:

* rebuilding the node to defragment the database,
* recreating the member failing to sync due to some bug.

In the case of a "regular" MongoDB, wiping the dbpath would trigger such resync.
In the case of a MongoDB cluster controlled by the Operator, you will need to do
the following steps:

#. Find out the names of the Persistent Volume Claim and Pod you are going to
   delete (use ``kubectl get pvc`` command for PVC and ``kubectl get pod`` one
   for Pods).
#. Delete the appropriate PVC and Pod. For example, wiping out the
   ``my-cluster-name-rs0-2`` Pod should look as follows:

   .. code:: bash

      kubectl delete pod/my-cluster-name-rs0-2 pvc/mongod-data-my-cluster-name-rs0-2

The Operator will automatically recreate the needed Pod and PVC after deletion.

How to carry on migration and/or disaster Recovery for a MongoDB Cluster
========================================================================

Disaster recovery allows you to restore MongoDB Cluster as a replica from an
existing one. It uses same technologies as migration of an existing cluster (but
in case of migration, the cluster you would like to "restore" doesn't exist).

You can use :ref:`cross-site replication<operator-replication>` for both tasks.
Also, see `this blogpost <...>`_ with detailed explanation and example of this
disaster recovery techniques.
