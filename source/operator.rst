.. _operator.custom-resource-options:

Custom Resource options
=======================

The operator is configured via the spec section of the
`deploy/cr.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/cr.yaml>`__
file. This file contains the following spec sections:

.. list-table::
   :widths: 15 15 10 60
   :header-rows: 1

   * - Key
     - Value type
     - Default
     - Description

   * - platform
     - string
     - kubernetes
     - Override/set the Kubernetes platform: *kubernetes* or *openshift*. Set openshift on OpenShift 3.11+

   * - image
     - string
     - ``percona/percona-server-mongodb:4.2.8-8``
     - The Docker image of `Percona Server for MongoDB <https://www.percona.com/doc/percona-server-for-mongodb/LATEST/index.html>`_ to deploy (actual image names can be found :ref:`in the list of certified images<custom-registry-images>`) 

   * - imagePullSecrets.name
     - string
     - ``private-registry-credentials``
     - The `Kubernetes ImagePullSecret <https://kubernetes.io/docs/concepts/configuration/secret/#using-imagepullsecrets>`_ to access the :ref:`custom registry<custom-registry>`

   * - imagePullPolicy
     - string
     - ``Always``
     - The `policy used to update images <https://kubernetes.io/docs/concepts/containers/images/#updating-images>`_

   * - ClusterServiceDNSSuffix
     - string
     - ``svc.cluster.local``
     - The (non-standard) cluster domain to be used as a suffix of the Service
       name

   * - runUid
     - int
     - 1001
     - The (non-standard) user ID

   * - secrets
     - subdoc
     -
     - Operator secrets section

   * - replsets
     - array
     -
     - Operator MongoDB Replica Set section

   * - pmm
     - subdoc
     - 
     - Percona Monitoring and Management section

   * - mongod
     - subdoc
     - 
     - Operator MongoDB Mongod configuration section

   * - backup
     - subdoc
     - 
     - Percona Server for MongoDB backups section

   * - allowUnsafeConfigurations
     - boolean
     - ``false``
     - Prevents users from configuring a cluster with unsafe parameters such as starting the cluster with less than 3 nodes or starting the cluster without TLS/SSL certificates

   * - updateStrategy
     - string
     - ``SmartUpdate``
     - A strategy the Operator uses for :ref:`upgrades<operator-update>`

.. _operator.upgradeoptions-section:

`Upgrade Options Section <operator.html#operator-upgradeoptions-section>`_
--------------------------------------------------------------------------------

The ``upgradeOptions`` section in the `deploy/cr.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/cr.yaml>`_ file contains various configuration options to control Percona Server for MongoDB upgrades.

.. tabularcolumns:: |p{2cm}|p{13.6cm}|

+-----------------+-------------------------------------------------------------------------------------------+
|                 | .. _upgradeoptions-versionserviceendpoint:                                                |
|                 |                                                                                           |
| **Key**         | `upgradeOptions.versionServiceEndpoint                                                    |
|                 | <operator.html#upgradeoptions-versionserviceendpoint>`_                                   |
+-----------------+-------------------------------------------------------------------------------------------+
| **Value**       | string                                                                                    |
+-----------------+-------------------------------------------------------------------------------------------+
| **Example**     | ``https://check.percona.com/versions``                                                    |
+-----------------+-------------------------------------------------------------------------------------------+
| **Description** | The Version Service URL used to check versions compatibility for upgrade                  |
+-----------------+-------------------------------------------------------------------------------------------+
|                                                                                                             |
+-----------------+-------------------------------------------------------------------------------------------+
|                 | .. _upgradeoptions-apply:                                                                 |
|                 |                                                                                           |
| **Key**         | `upgradeOptions.apply <operator.html#upgradeoptions-apply>`_                              |
+-----------------+-------------------------------------------------------------------------------------------+
| **Value**       | string                                                                                    |
+-----------------+-------------------------------------------------------------------------------------------+
| **Example**     | ``Recommended``                                                                           |
+-----------------+-------------------------------------------------------------------------------------------+
| **Description** | Specifies how :ref:`updates are processed<operator-update-smartupdates>` by the Operator. |
|                 | ``Never`` or ``Disabled`` will completely disable automatic upgrades, otherwise it can be |
|                 | set to ``Latest`` or ``Recommended`` or to a specific version string of PSMDB (e.g.       |
|                 | ``4.2.8-8``) that is wished to be version-locked (so that the user can control the        |
|                 | version running, but use automatic upgrades to move between them).                        |
+-----------------+-------------------------------------------------------------------------------------------+
|                                                                                                             |
+-----------------+-------------------------------------------------------------------------------------------+
|                 | .. _upgradeoptions-schedule:                                                              |
|                 |                                                                                           |
| **Key**         | `upgradeOptions.schedule <operator.html#upgradeoptions-schedule>`_                        |
+-----------------+-------------------------------------------------------------------------------------------+
| **Value**       | string                                                                                    |
+-----------------+-------------------------------------------------------------------------------------------+
| **Example**     | ``0 2 * * *``                                                                             |
+-----------------+-------------------------------------------------------------------------------------------+
| **Description** | Scheduled time to check for updates, specified in the                                     |
|                 | `crontab format <https://en.wikipedia.org/wiki/Cron>`_                                    |
+-----------------+-------------------------------------------------------------------------------------------+

.. _operator.secrets-section:

`Secrets section <operator.html#operator-secrets-section>`_
------------------------------------------------------------

Each spec in its turn may contain some key-value pairs. The secrets one
has only two of them:

.. tabularcolumns:: |p{2cm}|p{13.6cm}|

+-----------------+---------------------------------------------------------------------------------------------+
| **Key**         | .. _secrets-key:										|
|                 |												|
|                 | `secrets.key <operator.html#secrets-key>`_							|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``my-cluster-name-mongodb-key``								|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The secret name for the `MongoDB Internal Auth Key						|
|                 | <https://docs.mongodb.com/manual/core/security-internal-authentication/>`_. This secret is	|
|                 | auto-created by the operator if it doesn’t exist.						|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
| **Key**         | .. _secrets-users:										|
|                 |												|
|                 | `secrets.users <operator.html#secrets-users>`_						|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``my-cluster-name-mongodb-users``								|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The secret name for the MongoDB users required to run the operator.				|
|                 | **This secret is required to run the operator.**						|
+-----------------+---------------------------------------------------------------------------------------------+

.. _operator.replsets-section:

`Replsets Section <operator.html#operator-replsets-section>`_
--------------------------------------------------------------

The replsets section controls the MongoDB Replica Set.

.. tabularcolumns:: |p{2cm}|p{13.6cm}|

+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-name:										|
|                 |												|
| **Key**         | `replsets.name <operator.html#replsets-name>`_						|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``rs 0``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The name of the `MongoDB Replica Set <https://docs.mongodb.com/manual/replication/>`_ 	|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-size:										|
|                 |												|
| **Key**         | `replsets.size <operator.html#replsets-size>`_						|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | 3												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The size of the MongoDB Replica Set, must be >= 3 for `High-Availability			|
|                 | <https://docs.mongodb.com/manual/replication/#redundancy-and-data-availability>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-affinity-antiaffinitytopologykey:						|
|                 |												|
| **Key**         | `replsets.affinity.antiAffinityTopologyKey							|
|                 | <operator.html#replsets-affinity-antiaffinitytopologykey>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``kubernetes.io/hostname``									|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes topologyKey 								|
|                 | <https://kubernetes.io/docs/concepts/configuration/assign-pod-node/				|
|                 | #inter-pod-affinity-and-anti-affinity-beta-feature>`_ node affinity constraint for the	|
|                 | Replica Set nodes										|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-affinity-advanced:								|
|                 |												|
| **Key**         | `replsets.affinity.advanced <operator.html#replsets-affinity-advanced>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | subdoc											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     |												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | In cases where the pods require complex tuning the `advanced` option turns off the		|
|                 | ``topologykey`` effect. This setting allows the standard Kubernetes affinity constraints of	|
|                 | any complexity to be used									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-tolerations-key:								|
|                 |												|
| **Key**         | `replsets.tolerations.key <operator.html#replsets-tolerations-key>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``node.alpha.kubernetes.io/unreachable``							|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes Pod tolerations								|
|                 | <https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts>`_ key	|
|                 | for the Replica Set nodes									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-tolerations-operator:								|
|                 |												|
| **Key**         | `replsets.tolerations.operator <operator.html#replsets-tolerations-operator>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``Exists``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes Pod tolerations								|
|                 | <https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts>`_	|
|                 | operator for the Replica Set nodes								|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-tolerations-effect:								|
|                 |												|
| **Key**         | `replsets.tolerations.effect <operator.html#replsets-tolerations-effect>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``NoExecute``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes Pod tolerations 								|
|                 | <https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts>`_ effect	|
|                 | for the Replica Set nodes									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-tolerations-tolerationSeconds:							|
|                 |												|
| **Key**         | `replsets.tolerations.tolerationSeconds							|
|                 | <operator.html#replsets-tolerations-tolerationSeconds>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int	 											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``6000``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes Pod tolerations 								|
|                 | <https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts>`_ time	|
|                 | limit  for the Replica Set nodes								|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-priorityClassName:								|
|                 |												|
| **Key**         | `replsets.priorityClassName <operator.html#replsets-priorityClassName>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``high priority``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kuberentes Pod priority class								|
|                 | <https://kubernetes.io/docs/concepts/configuration/pod-priority-preemption/			|
|                 | #priorityclass>`_  for the Replica Set nodes						|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-annotations:									|
|                 |												|
| **Key**         | `replsets.annotations.iam.amazonaws.com/role <operator.html#replsets-annotations>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``role-arn``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `AWS IAM role 										|
|                 | <https://kubernetes-on-aws.readthedocs.io/en/latest/user-guide/iam-roles.html>`_  for the	|
|                 | Replica Set nodes										|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-labels:									|
|                 |												|
| **Key**         | `replsets.labels <operator.html#replsets-labels>`_						|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | label											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``rack: rack-22``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes affinity labels								|
|                 | <https://kubernetes.io/docs/concepts/configuration/assign-pod-node/>`_			|
|                 | for the Replica Set nodes									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-nodeSelector:									|
|                 |												|
| **Key**         | `replsets.nodeSelector <operator.html#replsets-nodeSelector>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | label											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``disktype: ssd``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes nodeSelector								|
|                 | <https://kubernetes.io/docs/concepts/configuration/assign-pod-node/#nodeselector>`_		|
|                 | affinity constraint  for the Replica Set nodes						|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-poddisruptionbudget-maxunavailable:						|
|                 |												|
| **Key**         | `replsets.podDisruptionBudget.maxUnavailable						|
|                 | <operator.html#replsets-poddisruptionbudget-maxunavailable>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``1``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes Pod distribution budget							|
|                 | <https://kubernetes.io/docs/concepts/workloads/pods/disruptions/>`_				|
|                 | limit specifying the maximum value for unavailable Pods					|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-podDisruptionBudget-minAvailable:						|
|                 |												|
| **Key**         | `replsets.podDisruptionBudget.minAvailable							|
|                 | <operator.html#replsets-podDisruptionBudget-minAvailable>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``1``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes Pod distribution budget							|
|                 | <https://kubernetes.io/docs/concepts/workloads/pods/disruptions/>`_				|
|                 | limit specifying the minimum value for available Pods					|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-expose-enabled:								|
|                 |												|
| **Key**         | `replsets.expose.enabled <operator.html#replsets-expose-enabled>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | boolean											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``false``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Enable or disable exposing `MongoDB Replica Set						|
|                 | <https://docs.mongodb.com/manual/replication/>`_ nodes with dedicated IP addresses		|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-expose-exposeType:								|
|                 |												|
| **Key**         | `replsets.expose.exposeType <operator.html#replsets-expose-exposeType>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``ClusterIP``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `IP address type <https://kubernetes.io/docs/concepts/services-networking/service/	|
|                 | #publishing-services-service-types>`_ to be exposed						|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-arbiter-enabled:								|
|                 |												|
| **Key**         | `replsets.arbiter.enabled <operator.html#replsets-arbiter-enabled>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | boolean											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``false``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Enable or disable creation of `Replica Set Arbiter						|
|                 | <https://docs.mongodb.com/manual/core/replica-set-arbiter/>`_ nodes within the cluster	|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-arbiter-size:									|
|                 |												|
| **Key**         | `replsets.arbiter.size <operator.html#replsets-arbiter-size>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``1``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The number of `Replica Set Arbiter								|
|                 | <https://docs.mongodb.com/manual/core/replica-set-arbiter/>`_  nodes			|
|                 | within the cluster										|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-arbiter-affinity-antiaffinitytopologykey:					|
|                 |												|
| **Key**         | `replsets.arbiter.afinity.antiAffinityTopologyKey						|
|                 | <operator.html#replsets-arbiter-affinity-antiaffinitytopologykey>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``kubernetes.io/hostname``									|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes topologyKey									|
|                 | <https://kubernetes.io/docs/concepts/configuration/assign-pod-node/				|
|                 | #inter-pod-affinity-and-anti-affinity-beta-feature>`_					|
|                 | node affinity constraint for the Arbiter							|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-arbiter-affinity-advanced:							|
|                 |												|
| **Key**         | `replsets.arbiter.affinity.advanced <operator.html#replsets-arbiter-affinity-advanced>`_	|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | subdoc											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     |												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | In cases where the pods require complex tuning the `advanced` option turns off		|
|                 | the ``topologykey`` effect. This setting allows the standard Kubernetes affinity		|
|                 | constraints of any complexity to be used							|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-arbiter-tolerations-key:							|
|                 |												|
| **Key**         | `replsets.arbiter.tolerations.key <operator.html#replsets-arbiter-tolerations-key>`_	|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``node.alpha.kubernetes.io/unreachable``							|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes Pod tolerations								|
|                 | <https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts>`_	|
|                 | key for the Arbiter nodes									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-arbiter-tolerations-operator:							|
|                 |												|
| **Key**         | `replsets.arbiter.tolerations.operator							|
|                 | <operator.html#replsets-arbiter-tolerations-operator>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``Exists``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes Pod tolerations								|
|                 | <https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts>`_	|
|                 | operator for the Arbiter nodes								|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-arbiter-tolerations-effect:							|
|                 |												|
| **Key**         | `replsets.arbiter.tolerations.effect <operator.html#replsets-arbiter-tolerations-effect>`_	|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``NoExecute``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes Pod tolerations								|
|                 | <https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts>`_	|
|                 | effect for the Arbiter nodes								|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-arbiter-tolerations-tolerationSeconds:						|
|                 |												|
| **Key**         | `replsets.arbiter.tolerations.tolerationSeconds						|
|                 | <operator.html#replsets-arbiter-tolerations-tolerationSeconds>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``6000``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes Pod tolerations								|
|                 | <https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts>`_	|
|                 | time limit for the Arbiter nodes								|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-arbiter-priorityClassName:							|
|                 |												|
| **Key**         | `replsets.arbiter.priorityClassName <operator.html#replsets-arbiter-priorityClassName>`_	|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``high priority``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kuberentes Pod priority class								|
|                 | <https://kubernetes.io/docs/concepts/configuration/pod-priority-preemption/			|
|                 | #priorityclass>`_ for the Arbiter nodes							|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-arbiter-annotations:								|
|                 |												|
| **Key**         | `replsets.arbiter.annotations.iam.amazonaws.com/role					|
|                 | <operator.html#replsets-arbiter-annotations>`_						|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``role-arn``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `AWS IAM role										|
|                 | <https://kubernetes-on-aws.readthedocs.io/en/latest/user-guide/iam-roles.html>`_		|
|                 | for the Arbiter nodes									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-arbiter-labels:								|
|                 |												|
| **Key**         | `replsets.arbiter.labels <operator.html#replsets-arbiter-labels>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | label											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``rack: rack-22``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes affinity labels								|
|                 | <https://kubernetes.io/docs/concepts/configuration/assign-pod-node/>`_			|
|                 | for the Arbiter nodes									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-arbiter-nodeSelector:								|
|                 |												|
| **Key**         | `replsets.arbiter.nodeSelector <operator.html#replsets-arbiter-nodeSelector>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | label											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``disktype: ssd``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes nodeSelector								|
|                 | <https://kubernetes.io/docs/concepts/configuration/assign-pod-node/				|
|                 | #nodeselector>`_ affinity constraint for the Arbiter nodes					|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-resources-limits-cpu:								|
|                 |												|
| **Key**         | `replsets.resources.limits.cpu <operator.html#replsets-resources-limits-cpu>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``300m``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | `Kubernetes CPU limit									|
|                 | <https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/	|
|                 | #resource-requests-and-limits-of-pod-and-container>`_ for MongoDB container			|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-resources-limits-memory:							|
|                 |												|
| **Key**         | `replsets.resources.limits.memory <operator.html#replsets-resources-limits-memory>`_	|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``0.5G``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | `Kubernetes Memory limit 									|
|                 | <https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/	|
|                 | #resource-requests-and-limits-of-pod-and-container>`__ for MongoDB container		|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-resources-requests-cpu:							|
|                 |												|
| **Key**         | `replsets.resources.requests.cpu <operator.html#replsets-resources-requests-cpu>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     |												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes CPU requests								|
|                 | <https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/	|
|                 | #resource-requests-and-limits-of-pod-and-container>`_ for MongoDB container			|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-resources-requests-memory:							|
|                 |												|
| **Key**         | `replsets.resources.requests.memory <operator.html#replsets-resources-requests-memory>`_	|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     |												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes Memory requests								|
|                 | <https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/	|
|                 | #resource-requests-and-limits-of-pod-and-container>`_ for MongoDB container			|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-volumeSpec-emptyDir:								|
|                 |												|
| **Key**         | `replsets.volumeSpec.emptyDir <operator.html#replsets-volumeSpec-emptyDir>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``{}``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes emptyDir volume <https://kubernetes.io/docs/concepts/storage/volumes/	|
|                 | #emptydir>`_, i.e. the directory which will be created on a node, and will be accessible to	|
|                 | the MongoDB Pod containers									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-volumeSpec-hostPath-path:							|
|                 |												|
| **Key**         | `replsets.volumeSpec.hostPath.path <operator.html#replsets-volumeSpec-hostPath-path>`_	|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``/data``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | `Kubernetes hostPath volume <https://kubernetes.io/docs/concepts/storage/volumes/		|
|                 | #hostpath>`_, i.e. the file or directory of a node that will be accessible to the MongoDB	|
|                 | Pod containers										|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-volumeSpec-hostPath-type:							|
|                 |												|
| **Key**         | `replsets.volumeSpec.hostPath.type <operator.html#replsets-volumeSpec-hostPath-type>`_	|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``Directory``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes hostPath volume type							|
|                 | <https://kubernetes.io/docs/concepts/storage/volumes/#hostpath>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-volumeSpec-persistentVolumeClaim-storageClassName:				|
|                 |												|
| **Key**         | `replsets.volumeSpec.persistentVolumeClaim.storageClassName					|
|                 | <operator.html#replsets-volumeSpec-persistentVolumeClaim-storageClassName>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``standard``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes Storage Class								|
|                 | <https://kubernetes.io/docs/concepts/storage/storage-classes/>`_				|
|                 | to use with the MongoDB container `Persistent Volume Claim 					|
|                 | <https://kubernetes.io/docs/concepts/storage/persistent-volumes/#persistentvolumeclaims>`_.	|
|                 | Use Storage Class with XFS as the default filesystem if possible, `for better MongoDB 	|
|                 | performance 										|
|                 | <https://dba.stackexchange.com/questions/190578/is-xfs-still-the-best-choice-for-mongodb>`_	|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-volumeSpec-persistentVolumeClaim-accessModes:					|
|                 |												|
| **Key**         | `replsets.volumeSpec.persistentVolumeClaim.accessModes					|
|                 | <operator.html#replsets-volumeSpec-persistentVolumeClaim-accessModes>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | array											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``[ "ReadWriteOnce" ]``									|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes Persistent Volume								|
|                 | <https://kubernetes.io/docs/concepts/storage/persistent-volumes/>`_				|
|                 | access modes for the MongoDB container							|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-volumeSpec-persistentVolumeClaim-resources-requests-storage:			|
|                 |												|
| **Key**         | `replsets.volumeSpec.persistentVolumeClaim.resources.requests.storage			|
|                 | <operator.html#replsets-volumeSpec-persistentVolumeClaim-resources-requests-storage>`_	|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``3Gi``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes Persistent Volume								|
|                 | <https://kubernetes.io/docs/concepts/storage/persistent-volumes/>`_				|
|                 | size for the MongoDB container								|
+-----------------+---------------------------------------------------------------------------------------------+


.. _operator.pmm-section:

`PMM Section <operator.html#operator-pmm-section>`_
----------------------------------------------------

The ``pmm`` section in the deploy/cr.yaml file contains configuration
options for Percona Monitoring and Management.

.. tabularcolumns:: |p{2cm}|p{13.6cm}|

+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _pmm-enabled:										|
|                 |												|
| **Key**         | `pmm.enabled <operator.html#pmm-enabled>`_							|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | boolean											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``false``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Enables or disables monitoring Percona Server for MongoDB with 				|
|                 | `PMM <https://www.percona.com/doc/percona-monitoring-and-management 			|
|                 | index.metrics-monitor.dashboard.html>`_							|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _pmm-image:										|
|                 |												|
| **Key**         | `pmm.image <operator.html#pmm-image>`_							|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``perconalab/pmm-client:1.17.1``								|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | PMM Client docker image to use								|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _pmm-serverHost:										|
|                 |												|
| **Key**         | `pmm.serverHost <operator.html#pmm-serverHost>`_						|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``monitoring-service``									|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Address of the PMM Server to collect data from the Cluster					|
+-----------------+---------------------------------------------------------------------------------------------+

.. _operator.mongod-section:

`Mongod Section <operator.html#operator-mongod-section>`_
----------------------------------------------------------

The largest section in the deploy/cr.yaml file contains the Mongod
configuration options.

.. tabularcolumns:: |p{2cm}|p{13.6cm}|

+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _mongod-net-port:									|
|                 |												|
| **Key**         | `mongod.net.port <operator.html#mongod-net-port>`_						|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``27017``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Sets the MongoDB `net.port option 					 			|
|                 | <https://docs.mongodb.com/manual/reference/configuration-options/#net.port>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _mongod-net-hostPort:									|
|                 |												|
| **Key**         | `mongod.net.hostPort <operator.html#mongod-net-hostPort>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``0``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Sets the Kubernetes `hostPort option							|
|                 | <https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/network-plugins/ |
|                 | #support-hostport>`_									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _mongod-security-redactClientLogData:							|
|                 |												|
| **Key**         | `mongod.security.redactClientLogData <operator.html#mongod-security-redactClientLogData>`_	|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | bool											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``false``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Enables/disables `PSMDB Log Redaction							|
|                 | <https://www.percona.com/doc/percona-server-for-mongodb/LATEST/log-redaction.html>`_	|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _mongod-security-enableEncryption:							|
|                 |												|
| **Key**         | `mongod.security.enableEncryption <operator.html#mongod-security-enableEncryption>`_	|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | bool											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``true``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Enables/disables `PSMDB data at rest encryption						|
|                 | <https://www.percona.com/doc/percona-server-for-mongodb/LATEST/				|
|                 | data_at_rest_encryption.html>`_								|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _mongod-security-encryptionKeySecret:							|
|                 |												|
| **Key**         | `mongod.security.encryptionKeySecret <operator.html#mongod-security-encryptionKeySecret>`_	|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``my-cluster-name-mongodb-encryption-key``							|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Specifies a secret object with the `encryption key 						|
|                 | <https://docs.mongodb.com/manual/tutorial/configure-encryption/#local-key-management>`_	|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _mongod-security-encryptionCipherMode:							|
|                 |												|
| **Key**         | `mongod.security.encryptionCipherMode <operator.html#mongod-security-encryptionCipherMode>`_|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``AES256-CBC``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Sets											|
|                 | `PSMDB encryption cipher mode <https://docs.mongodb.com/manual/reference/program/mongod/	|
|                 | #cmdoption-mongod-encryptionciphermode>`_							|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _mongod-setParameter-ttlMonitorSleepSecs:						|
|                 |												|
| **Key**         | `mongod.setParameter.ttlMonitorSleepSecs							|
|                 | <operator.html#mongod-setParameter-ttlMonitorSleepSecs>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``60``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Sets the PSMDB `ttlMonitorSleepSecs` option							|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _mongod-setParameter-wiredTigerConcurrentReadTransactions:				|
|                 |												|
| **Key**         | `mongod.setParameter.wiredTigerConcurrentReadTransactions					|
|                 | <operator.html#mongod-setParameter-wiredTigerConcurrentReadTransactions>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``128``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Sets the `wiredTigerConcurrentReadTransactions option					|
|                 | <https://docs.mongodb.com/manual/reference/parameters/					|
|                 | #param.wiredTigerConcurrentReadTransactions>`_						|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _mongod-setParameter-wiredTigerConcurrentWriteTransactions:				|
|                 |												|
| **Key**         | `mongod.setParameter.wiredTigerConcurrentWriteTransactions					|
|                 | <operator.html#mongod-setParameter-wiredTigerConcurrentWriteTransactions>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``128``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Sets the `wiredTigerConcurrentWriteTransactions option					|
|                 | <https://docs.mongodb.com/manual/reference/parameters/					|
|                 | #param.wiredTigerConcurrentWriteTransactions>`_						|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _mongod-storage-engine:									|
|                 |												|
| **Key**         | `mongod.storage.engine <operator.html#mongod-storage-engine>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``wiredTiger``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Sets the `storage.engine option								|
|                 | <https://docs.mongodb.com/manual/reference/configuration-options/#storage.engine>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _mongod-storage-inMemory-engineConfig-inMemorySizeRatio:					|
|                 |												|
| **Key**         | `mongod.storage.inMemory.engineConfig.inMemorySizeRatio					|
|                 | <operator.html#mongod-storage-inMemory-engineConfig-inMemorySizeRatio>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | float											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``0.9``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The ratio used to compute the `storage.engine.inMemory.inMemorySizeGb option		|
|                 | <https://www.percona.com/doc/percona-server-for-mongodb/LATEST/inmemory.html		|
|                 | #--inMemorySizeGB>`_									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _mongod-storage-mmapv1-nsSize:								|
|                 |												|
| **Key**         | `mongod.storage.mmapv1.nsSize <operator.html#mongod-storage-mmapv1-nsSize>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``16``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Sets the `storage.mmapv1.nsSize option							|
|                 | <https://docs.mongodb.com/manual/reference/configuration-options/#storage.mmapv1.nsSize>`_	|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _mongod-storage-mmapv1-smallfiles:							|
|                 |												|
| **Key**         | `mongod.storage.mmapv1.smallfiles <operator.html#mongod-storage-mmapv1-smallfiles>`_	|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | bool											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``false``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Sets the `storage.mmapv1.smallfiles option							|
|                 | <https://docs.mongodb.com/manual/reference/configuration-options/				|
|                 | #storage.mmapv1.smallFiles>`_								|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _mongod-storage-wiredTiger-engineConfig-cacheSizeRatio:					|
|                 |												|
| **Key**         | `mongod.storage.wiredTiger.engineConfig.cacheSizeRatio					|
|                 | <operator.html#mongod-storage-wiredTiger-engineConfig-cacheSizeRatio>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | float											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``0.5``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The ratio used to compute the `storage.wiredTiger.engineConfig.cacheSizeGB option		|
|                 | <https://docs.mongodb.com/manual/reference/configuration-options/				|
|                 | #storage.wiredTiger.engineConfig.cacheSizeGB>`_						|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _mongod-storage-wiredTiger-engineConfig-directoryForIndexes:				|
|                 |												|
| **Key**         | `mongod.storage.wiredTiger.engineConfig.directoryForIndexes					|
|                 | <operator.html#mongod-storage-wiredTiger-engineConfig-directoryForIndexes>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | bool											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``false``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Sets the `storage.wiredTiger.engineConfig.directoryForIndexes option			|
|                 | <https://docs.mongodb.com/manual/reference/configuration-options/				|
|                 | #storage.wiredTiger.engineConfig.directoryForIndexes>`_                                     |
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _mongod-storage-wiredTiger-engineConfig-journalCompressor:				|
|                 |												|
| **Key**         | `mongod.storage.wiredTiger.engineConfig.journalCompressor					|
|                 | <operator.html#mongod-storage-wiredTiger-engineConfig-journalCompressor>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``snappy``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Sets the `storage.wiredTiger.engineConfig.journalCompressor option				|
|                 | <https://docs.mongodb.com/manual/reference/configuration-options/				|
|                 | #storage.wiredTiger.engineConfig.journalCompressor>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _mongod-storage-wiredTiger-collectionConfig-blockCompressor:				|
|                 |												|
| **Key**         | `mongod.storage.wiredTiger.collectionConfig.blockCompressor					|
|                 | <operator.html#mongod-storage-wiredTiger-collectionConfig-blockCompressor>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``snappy``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Sets the `storage.wiredTiger.collectionConfig.blockCompressor option			|
|                 | <https://docs.mongodb.com/manual/reference/configuration-options/				|
|                 | #storage.wiredTiger.collectionConfig.blockCompressor>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _mongod-storage-wiredTiger-indexConfig-prefixCompression:				|
|                 |												|
| **Key**         | `mongod.storage.wiredTiger.indexConfig.prefixCompression					|
|                 | <operator.html#mongod-storage-wiredTiger-indexConfig-prefixCompression>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | bool											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``true``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Sets the `storage.wiredTiger.indexConfig.prefixCompression option				|
|                 | <https://docs.mongodb.com/manual/reference/configuration-options/				|
|                 | #storage.wiredTiger.indexConfig.prefixCompression>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _mongod-operationProfiling-mode:								|
|                 |												|
| **Key**         | `mongod.operationProfiling.mode <operator.html#mongod-operationProfiling-mode>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``slowOp``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Sets the `operationProfiling.mode option 							|
|                 | <https://docs.mongodb.com/manual/reference/configuration-options/				|
|                 | #operationProfiling.mode>`_									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _mongod-operationProfiling-slowOpThresholdMs:						|
|                 |												|
| **Key**         | `mongod.operationProfiling.slowOpThresholdMs						|
|                 | <operator.html#mongod-operationProfiling-slowOpThresholdMs>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``100``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Sets the `operationProfiling.slowOpThresholdMs						|
|                 | <https://docs.mongodb.com/manual/reference/configuration-options/				|
|                 | #operationProfiling.slowOpThresholdMs>`_ option						|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _mongod-operationProfiling-rateLimit:							|
|                 |												|
| **Key**         | `mongod.operationProfiling.rateLimit <operator.html#mongod-operationProfiling-rateLimit>`_	|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``1``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Sets the `operationProfiling.rateLimit option						|
|                 | <https://www.percona.com/doc/percona-server-for-mongodb/LATEST/rate-limit.html>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _mongod-auditLog-destination:								|
|                 |												|
| **Key**         | `mongod.auditLog.destination <operator.html#mongod.auditLog.destination>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     |												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Sets the `auditLog.destination option							|
|                 | <https://www.percona.com/doc/percona-server-for-mongodb/LATEST/audit-logging.html>`_	|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _mongod-auditLog-format:									|
|                 |												|
| **Key**         | `mongod.auditLog.format <operator.html#mongod-auditLog-format>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``BSON``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Sets the `auditLog.format option								|
|                 | <https://www.percona.com/doc/percona-server-for-mongodb/LATEST/audit-logging.html>`_	|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _mongod-auditLog-filter:									|
|                 |												|
| **Key**         | `mongod.auditLog.filter <operator.html#mongod-auditLog-filter>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``{}``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Sets the `auditLog.filter option								|
|                 | <https://www.percona.com/doc/percona-server-for-mongodb/LATEST/audit-logging.html>`_	|
+-----------------+---------------------------------------------------------------------------------------------+

.. _operator.backup-section:

`Backup Section <operator.html#operator-backup-section>`_
----------------------------------------------------------

The ``backup`` section in the
`deploy/cr.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/cr.yaml>`__
file contains the following configuration options for the regular
Percona Server for MongoDB backups.

.. tabularcolumns:: |p{2cm}|p{13.6cm}|

+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _backup-enabled:										|
|                 |												|
| **Key**         | `backup.enabled <operator.html#backup-enabled>`_						|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | boolean											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``true``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Enables or disables making backups					 			|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _backup-debug:										|
|                 |												|
| **Key**         | `backup.debug <operator.html#backup-debug>`_						|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | boolean											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``true``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Enables or disables debug mode for bakups							|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _backup-restartOnFailure:								|
|                 |												|
| **Key**         | `backup.restartOnFailure <operator.html#backup-restartOnFailure>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | boolean											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``true``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Enables or disables restarting the previously failed backup process				|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _backup-image:										|
|                 |												|
| **Key**         | `backup.image <operator.html#backup-image>`_						|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``percona/percona-server-mongodb-operator:{{{release}}}-backup``					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The Percona Server for MongoDB Docker image to use for the backup				|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _backup-serviceAccountName:								|
|                 |												|
| **Key**         | `backup.serviceAccountName <operator.html#backup-serviceAccountName?>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``percona-server-mongodb-operator``								|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Nname of the separate privileged service account used by the Operator			|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _backup-resources-limits-cpu:								|
|                 |												|
| **Key**         | `backup.resources.limits.cpu <operator.html#backup-resources-limits-cpu>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``100m``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | `Kubernetes CPU limit									|
|                 | <https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/	|
|                 | #resource-requests-and-limits-of-pod-and-container>`_ for backups				|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _backup-resources-limits-memory:								|
|                 |												|
| **Key**         | `backup.resources.limits.memory <operator.html#backup-resources-limits-memory>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``0.2G``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | `Kubernetes Memory limit									|
|                 | <https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/	|
|                 | #resource-requests-and-limits-of-pod-and-container>`_ for backups				|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _backup-resources-requests-cpu:								|
|                 |												|
| **Key**         | `backup.resources.requests.cpu <operator.html#backup-resources-requests-cpu>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``100m``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes CPU requests 								|
|                 | <https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/	|
|                 | #resource-requests-and-limits-of-pod-and-container>`_ for backups				|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _backup-resources-requests-memory:							|
|                 |												|
| **Key**         | `backup.resources.requests.memory <operator.html#backup-resources-requests-memory>`_	|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``0.1G``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes Memory requests								|
|                 | <https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/	|
|                 | #resource-requests-and-limits-of-pod-and-container>`_ for backups				|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _backup-storages-type:									|
|                 |												|
| **Key**         | `backup.storages.<storage-name>.type <operator.html#backup-storages-type>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value**       | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``s3``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The cloud storage type used for backups. Only ``s3`` type is currently			|
|                 | supported											|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _backup-storages-s3-credentialssecret:							|
|                 |												|
| **Key**         | `backup.storages.<storage-name>.s3.credentialsSecret					| 	
|                 | <operator.html#backup-storages-s3-credentialssecret>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value**       | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``my-cluster-name-backup-s3``								|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes secret <https://kubernetes.io/docs/concepts/configuration/secret/>`_ for	|
|                 | backups. It should contain ``AWS_ACCESS_KEY_ID`` and ``AWS_SECRET_ACCESS_KEY`` keys.	|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _backup-storages-s3-bucket:								|
|                 |												|
| **Key**         | `backup.storages.<storage-name>.s3.bucket <operator.html#backup-storages-s3-bucket?>`_	|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value**       | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     |												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Amazon S3 bucket <https://docs.aws.amazon.com/AmazonS3/latest/dev/UsingBucket.html>`_	|
|                 | name for backups										|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _backup-storages-s3-region:								|
|                 |												|
| **Key**         | `backup.storages.s3.<storage-name>.region <operator.html#backup-storages-s3-region>`_	|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value**       | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``us-east-1``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `AWS region <https://docs.aws.amazon.com/general/latest/gr/rande.html>`_ to use.	|
|                 | Please note **this option is mandatory** for Amazon and all S3-compatible storages		|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _backup-storages-s3-endpointUrl:								|
|                 |												|
| **Key**         | `backup.storages.s3.<storage-name>.endpointUrl						|
|                 | <operator.html#backup-storages-s3-endpointUrl>`_						|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value**       | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     |												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The endpoint URL of the S3-compatible storage to be used (not needed for the original	|
|                 | Amazon S3 cloud)										|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _backup-tasks-enabled:									|
|                 |												|
| **Key**         | `backup.tasks.enabled <operator.html#backup-tasks-enabled>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | boolean											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``true``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Enables or disables this exact backup							|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _backup-tasks-schedule:									|
|                 |												|
| **Key**         | `backup.tasks.schedule <operator.html#backup-tasks-schedule>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``0 0 * * 6``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The scheduled time to make a backup, specified in the 					|
|                 | `crontab format <https://en.wikipedia.org/wiki/Cron>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _backup-tasks-storageName:								|
|                 |												|
| **Key**         | `backup.tasks.storageName <operator.html#backup-tasks-storageName>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``st-us-west``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The name of the S3-compatible storage for backups, configured in the `storages` subsection	|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _backup-tasks-compressionType:								|
|                 |												|
| **Key**         | `backup.tasks.compressionType <operator.html#backup-tasks-compressionType>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``gzip``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The backup compression format								|
+-----------------+---------------------------------------------------------------------------------------------+

