.. _install-helm:

Install Percona Server for MongoDB using Helm
==============================================

`Helm <https://github.com/helm/helm>`_ is the package manager for Kubernetes.

Pre-requisites
--------------

Install Helm following its `official installation instructions <https://docs.helm.sh/using_helm/#installing-helm>`_.

.. note:: At least ``2.4.0`` version of Helm is needed to run the following steps.


Installation
-------------

#. Add the Percona's Helm charts repository:

   .. code:: bash

      helm repo add percona https://percona-lab.github.io/percona-helm-charts/

#. Install Percona Operator for Percona Server for MongoDB:

   .. code:: bash

      helm install percona/psmdb-operator
  
   .. note:: If nothing explicitly specified, ``helm install`` command will work
      with ``default`` namespace. To use different namespace, provide it with
      the following additional parameter: ``--namespace my-namespace``.

#. Install Percona Server for MongoDB:

   .. code:: bash

      helm install percona/psmdb-db

Installing Percona Server for MongoDB with customized parameters
----------------------------------------------------------------

The command above installs Percona Server for MongoDB with :ref:`default parameters<operator.custom-resource-options>`.
Custom options can be passed to a ``helm install`` command as a
``--set key=value[,key=value]`` argument. The options passed with a chart can be
any of the Operator's :ref:`operator.custom-resource-options`.
Additionally, there is a number of options specific to the Helm chart:

+--------------------+------------------------------------------------+--------+
| Parameter          | Description                                    | Default|
+--------------------+------------------------------------------------+--------+
| ``nameOverride``   | Set if the chart name is not desired to be the | ``""`` |
|                    | operators name                                 |        |
+--------------------+------------------------------------------------+--------+
|``fullnameOverride``| By default operator name will contain chart    |        |
|                    | name concatenated with operators name.         |        |
|                    |                                                | ``""`` |
|                    | Set this variable if you want to set the       |        |
|                    | whole bunch of names                           |        |
+--------------------+------------------------------------------------+--------+
| ``runUid``         | Set UserID                                     | ``""`` |
+--------------------+------------------------------------------------+--------+
| ``users``          | PSMDB essential users                          | ``{}`` |
+--------------------+------------------------------------------------+--------+


The following example will deploy a Percona Server for MongoDB Cluster in the
``psmdb`` namespace, with disabled backups and 20 Gi storage:

.. code:: bash

   helm install dev  --namespace psmdb . \
     --set runUid=1001 --set replset.volumeSpec.pvc.resources.requests.storage=20Gi \
     --set backup.enabled=false