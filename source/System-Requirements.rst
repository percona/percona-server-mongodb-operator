System Requirements
+++++++++++++++++++

The Operator was developed and tested with Percona Server for MongoDB 3.6, 4.0,
and 4.2. Other options may or may not work.

.. note:: The already deprecated `MMAPv1 storage engine <https://docs.mongodb.com/manual/core/storage-engines/>`_
   for Percona Server for MongoDB 3.6 and 4.0 is not supported by the Operator
   starting from version 1.6.

Also, Operator 1.6 supports Percona Server for MongoDB sharding with only one
Replica Set.

Officially supported platforms
--------------------------------

The following platforms were tested and are officially supported by the Operator
{{{release}}}: 

* OpenShift 3.11
* OpenShift 4.5
* Google Kubernetes Engine (GKE) 1.15 - 1.17
* Amazon Elastic Container Service for Kubernetes (EKS) 1.15
* Minikube 1.10
* VMWare Tanzu

Other Kubernetes platforms may also work but have not been tested.

Resource Limits
-----------------------

A cluster running an officially supported platform contains at least 3 
Nodes, with the following resources:

* 2GB of RAM,
* 2 CPU threads per Node for Pods provisioning,
* at least 60GB of available storage for Private Volumes provisioning.

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




