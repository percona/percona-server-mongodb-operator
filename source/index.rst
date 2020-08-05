.. K8s-PSMDB-docs documentation master file, created by
   sphinx-quickstart on Thu May  9 09:17:34 2019.
   You can adapt this file completely to your liking, but it should at least
   contain the root `toctree` directive.

Percona Kubernetes Operator for Percona Server for MongoDB
==========================================================

The `Percona Kubernetes Operator for Percona Server for MongoDB <https://github.com/percona/percona-server-mongodb-operator>`_ automates the creation, modification, or deletion of items in your Percona Server for MongoDB environment. The Operator contains the necessary Kubernetes settings to maintain a consistent Percona Server for MongoDB instance.

The Percona Kubernetes Operators are based on best practices for the configuration of a Percona Server for MongoDB replica set. The Operator provides many benefits but saving time, a consistent environment are the most important.

Overview
========

.. toctree::
   :maxdepth: 1

   architecture
   System-Requirements


Installation
============

.. toctree::
   :maxdepth: 1

   kubernetes
   openshift
   minikube
   scaling
   update
   custom-registry
   broker

Configuration
=============

.. toctree::
   :maxdepth: 1

   users
   operator
   backups
   private
   arbiter
   expose
   constraints
   storage
   TLS
   encryption
   debug

Reference
=========

.. toctree::
  :maxdepth: 1

  api
  Release Notes <RN/index.rst>
