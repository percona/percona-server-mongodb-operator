System Requirements
+++++++++++++++++++

The Operator was developed and tested with Percona Server for MongoDB 3.6, 4.0,
4.2, and 4.4. Other options may also work but have not been tested.

.. note:: The `MMAPv1 storage engine <https://docs.mongodb.com/manual/core/storage-engines/>`_
   is no longer supported for all MongoDB versions starting from the Operator
   version 1.6. MMAPv1 was already deprecated by MongoDB for a long time.
   WiredTiger is the default storage engine since MongoDB 3.2, and MMAPv1 was
   completely removed in MongoDB 4.2.

Officially supported platforms
--------------------------------

The following platforms were tested and are officially supported by the Operator
{{{release}}}: 

* OpenShift 3.11
* OpenShift 4.7
* Google Kubernetes Engine (GKE) 1.16 - {{{gkerecommended}}}
* Amazon Elastic Container Service for Kubernetes (EKS) 1.19
* Minikube 1.19
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

Platform-specific limitations
------------------------------

The Operator is subsequent to specific platform limitations.

* Minikube doesn't support multi-node cluster configurations because of its
  local nature, which is in collision with the default affinity requirements
  of the Operator. To arrange this, the :ref:`install-minikube` instruction
  includes an additional step which turns off the requirement of having not
  less than three Nodes.




