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
during hardware failure. Percona Kubernetes Operator for Percona Server for
MongoDB provides out-of-the-box functionality to automate provisioning and
management of highly available MongoDB database clusters on Kubernetes.

How can I contact the developers?
================================================================================

The best place to discuss Percona Kubernetes Operator for Percona Server for
MongoDB with developers and other community members is the `community forum <https://forums.percona.com/categories/kubernetes-operator-percona-server-mongodb>`_.

If you would like to report a bug, use the `Percona Kubernetes Operator for Percona Server for MongoDB project in JIRA <https://jira.percona.com/projects/K8SPSMDB>`_.

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
specific platform and/or you are new to the Percona Server for MongoDB Operator
as a whole.

Which versions of MongoDB the Operator supports?
================================================================================

Percona Operator for Percona Server for MongoDB provides a ready-to-use
installation of the MongoDB-based database cluster inside your Kubernetes
installation. It works with Percona Server for MongoDB 3.6, 4.0, and 4.2, and
the exact version is determined by the Docker image in use.

Percona-certified Docker images used by the Operator are listed `here <https://www.percona.com/doc/kubernetes-operator-for-psmongodb/images.html>`_.
For example, Percona Server for MongoDB 4.2 is supported with the following
recommended version: {{{mongodb42recommended}}}. More details on the exact Percona
Server for MongoDB version can be found in the release notes (`4.2 <https://www.percona.com/doc/percona-server-for-mongodb/4.2/release_notes/index.html>`_, `4.0 <https://www.percona.com/doc/percona-server-for-mongodb/4.0/release_notes/index.html>`_,
and `3.6 <https://www.percona.com/doc/percona-server-for-mongodb/3.6/release_notes/index.html>`_).

