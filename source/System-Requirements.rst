System Requirements
+++++++++++++++++++

The Operator was developed and tested with Percona Server for MongoDB 4.0, 4.2,
4.4, and 5.0 technical preview. Other options may also work but have not been
tested.

.. note:: The `MMAPv1 storage engine <https://docs.mongodb.com/manual/core/storage-engines/>`_
   is no longer supported for all MongoDB versions starting from the Operator
   version 1.6. MMAPv1 was already deprecated by MongoDB for a long time.
   WiredTiger is the default storage engine since MongoDB 3.2, and MMAPv1 was
   completely removed in MongoDB 4.2.

Officially supported platforms
--------------------------------

The following platforms were tested and are officially supported by the Operator
{{{release}}}: 

* OpenShift 4.6 - 4.8
* Google Kubernetes Engine (GKE) 1.17 - 1.21
* Amazon Elastic Container Service for Kubernetes (EKS) 1.16 - 1.21
* Minikube 1.22
* VMWare Tanzu

Other Kubernetes platforms may also work but have not been tested.

Resource Limits
-----------------------

A cluster running an officially supported platform contains at least 3 
Nodes and the following resources (if :ref:`sharding<operator.sharding>` is
turned off):

* 2GB of RAM,
* 2 CPU threads per Node for Pods provisioning,
* at least 60GB of available storage for Private Volumes provisioning.

Consider using 4 CPU and 6 GB of RAM if :ref:`sharding<operator.sharding>` is
turned on (the default behavior).

Also, the number of Replica Set Nodes should not be odd
if :ref:`Arbiter<arbiter>` is not enabled.

.. note:: Use Storage Class with XFS as the default filesystem if possible
   `to achieve better MongoDB performance <https://dba.stackexchange.com/questions/190578/is-xfs-still-the-best-choice-for-mongodb>`_.




