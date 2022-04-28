|operator|
================================================================================

The `Percona Operator for MongoDB <https://github.com/percona/percona-server-mongodb-operator>`_ automates the creation, modification, or deletion of items in your Percona Server for MongoDB environment. The Operator contains the necessary Kubernetes settings to maintain a consistent Percona Server for MongoDB instance.

The Percona Kubernetes Operators are based on best practices for the configuration of a Percona Server for MongoDB replica set. The Operator provides many benefits but saving time, a consistent environment are the most important.

Requirements
============

.. toctree::
   :maxdepth: 1

   System Requirements <System-Requirements.rst>
   Design and architecture <architecture.rst>

Quickstart guides
=================

.. toctree::
   :maxdepth: 1

   Install with Helm <helm.rst>
   Install on Minikube <minikube.rst>
   Install on Google Kubernetes Engine (GKE) <gke.rst>
   Install on Amazon Elastic Kubernetes Service (AWS EKS) <eks.rst>

Advanced Installation Guides
============================

.. toctree::
   :maxdepth: 1

   Generic Kubernetes installation <kubernetes.rst>
   Install on OpenShift <openshift.rst>
   Use private registry <custom-registry.rst>
   Deploy with Service Broker <broker.rst>

Configuration
=============

.. toctree::
   :maxdepth: 1

   Local Storage support <storage.rst>
   Anti-affinity and tolerations <constraints.rst>
   Changing MongoDB Options <options.rst>
   Exposing the cluster <expose.rst>
   Arbiter and non-voting nodes <arbiter.rst>
   MongoDB Sharding <sharding.rst>
   Transport Encryption (TLS/SSL) <TLS.rst>
   Data at rest encryption <encryption.rst>
   Application and system users <users.rst>

Management
==========

.. toctree::
   :maxdepth: 1

   Backup and restore <backups.rst>
   Upgrade MongoDB and the Operator <update.rst>
   Horizontal and vertical scaling <scaling.rst>
   Multi-cluster and multi-region deployment <replication.rst>
   Monitor with Percona Monitoring and Management (PMM) <monitoring.rst>
   Add sidecar containers <sidecar.rst>
   Restart or pause the cluster <pause.rst>
   Debug and troubleshoot <debug.rst>

HOWTOs
======

.. toctree::
   :maxdepth: 1

   OpenLDAP integration <ldap.rst>
   Creating a private S3-compatible cloud for backups <private.rst>

Reference
=========

.. toctree::
   :maxdepth: 1

   Custom Resource options <operator.rst>
   Percona certified images <images.rst>
   Operator API <api.rst>
   Frequently Asked Questions <faq.rst>
   Release Notes <RN/index.rst>
