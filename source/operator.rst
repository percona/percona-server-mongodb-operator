.. _operator.custom-resource-options:

Custom Resource options
=======================

The operator is configured via the spec section of the
`deploy/cr.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/main/deploy/cr.yaml>`_ file.

The metadata part of this file contains the following keys:

* .. _cluster-name:

  ``name`` (``my-cluster-name`` by default) sets the name of your Percona Server
  for MongoDB Cluster; it should include only `URL-compatible characters <https://datatracker.ietf.org/doc/html/rfc3986#section-2.3>`_, not exceed 22 characters, start with an alphabetic character, and end with an alphanumeric character;
* .. _finalizers:

  ``finalizers.delete-psmdb-pvc``, if present, activates the `Finalizer <https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/#finalizers>`_ which deletes appropriate `Persistent Volume Claims <https://kubernetes.io/docs/concepts/storage/persistent-volumes/>`_ after the cluster deletion event (off by default).

The spec part of the `deploy/cr.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/main/deploy/cr.yaml>`_ file contains the following sections:

.. list-table::
   :widths: 15 15 16 54
   :header-rows: 1

   * - Key
     - Value type
     - Default
     - Description

   * - platform
     - string
     - ``kubernetes``
     - Override/set the Kubernetes platform: *kubernetes* or *openshift*. Set openshift on OpenShift 3.11+

   * - pause
     - boolean
     - ``false``
     - Pause/resume: setting it to ``true`` gracefully stops the cluster, and
       setting it to ``false`` after shut down starts the cluster back.

   * - unmanaged
     - boolean
     - ``false``
     - Unmanaged site in :ref:`cross-site replication<operator-replication>`:
       setting it to ``true`` forces the Operator to run the cluster
       in unmanaged state - nodes do not form replica sets, operator does
       not control TLS certificates

   * - crVersion
     - string
     - ``{{{release}}}``
     - Version of the Operator the Custom Resource belongs to

   * - image
     - string
     - ``percona/percona``-``server``-``mongodb:{{{mongodb44recommended}}}``
     - The Docker image of `Percona Server for MongoDB <https://www.percona.com/doc/percona-server-for-mongodb/LATEST/index.html>`_ to deploy (actual image names can be found :ref:`in the list of certified images<custom-registry-images>`) 

   * - imagePullPolicy
     - string
     - ``Always``
     - The `policy used to update images <https://kubernetes.io/docs/concepts/containers/images/#updating-images>`_

   * - .. _tls-certvalidityduration:

       tls.certValidityDuration
     - string
     - ``2160h``
     - The validity duration of the external certificate for cert manager (90
       days by default). This value is used only at cluster creation time and
       can't be changed for existing clusters

   * - imagePullSecrets.name
     - string
     - ``private``-``registry``-``credentials``
     - The `Kubernetes ImagePullSecret <https://kubernetes.io/docs/concepts/configuration/secret/#using-imagepullsecrets>`_ to access the :ref:`custom registry<custom-registry>`

   * - ClusterServiceDNSSuffix
     - string
     - ``svc.cluster.local``
     - The (non-standard) cluster domain to be used as a suffix of the Service
       name

   * - .. _clusterservicednsmode:

       clusterServiceDNSMode
     - string
     - ``Internal``
     - Can be either ``internal`` (exposed MongoDB instances will use ClusterIP addresses) or ``ServiceMesh`` (turns on :abbr:`FQDN (fully qualified domain name)` for the exposed Services). Being set, ``ServiceMesh`` value suprecedes multiCluster settings, and therefore these two modes cannot be combined together.

   * - runUid
     - int
     - 1001
     - The (non-standard) user ID

   * - allowUnsafeConfigurations
     - boolean
     - ``false``
     - Prevents users from configuring a cluster with unsafe parameters: starting it with less than 3 replica set instances, with an :ref:`even number of replica set instances without additional arbiter<arbiter>`, or without TLS/SSL certificates, or running a sharded cluster with less than 3 config server Pods or less than 2 mongos Pods (if ``false``, the Operator will automatically change unsafe parameters to safe defaults)

   * - updateStrategy
     - string
     - ``SmartUpdate``
     - A strategy the Operator uses for :ref:`upgrades<operator-update>`. Possible values are :ref:`SmartUpdate<operator-update-smartupdates>`, `RollingUpdate <https://kubernetes.io/docs/concepts/workloads/controllers/statefulset/#rolling-updates>`_ and `OnDelete <https://kubernetes.io/docs/concepts/workloads/controllers/statefulset/#on-delete>`_

   * - multiCluster.enabled
     - boolean
     - ``false``
     - :ref:`Multi-cluster Services (MCS)<operator-replication-mcs>`: setting it to
       ``true`` enables `MCS cluster mode <https://cloud.google.com/kubernetes-engine/docs/concepts/multi-cluster-services>`_ 

   * - multiCluster.DNSSuffix
     - string
     - ``svc.clusterset.local``
     - The cluster domain to be used as a suffix for :ref:`multi-cluster Services<operator-replication-mcs>`
       used by Kubernetes (``svc.clusterset.local`` `by default <https://cloud.google.com/kubernetes-engine/docs/how-to/multi-cluster-services>`_)

   * - upgradeOptions
     - :ref:`subdoc<operator.upgradeoptions-section>`
     - 
     - Upgrade configuration section

   * - secrets
     - :ref:`subdoc<operator.secrets-section>`
     -
     - Operator secrets section

   * - replsets
     - :ref:`subdoc<operator.replsets-section>`
     -
     - Operator MongoDB Replica Set section

   * - pmm
     - :ref:`subdoc<operator.pmm-section>`
     - 
     - Percona Monitoring and Management section

   * - sharding
     - :ref:`subdoc<operator.sharding-section>`
     - 
     - MongoDB sharding configuration section

   * - mongod
     - :ref:`subdoc<operator.mongod-section>`
     - 
     - Operator MongoDB Mongod configuration section

   * - backup
     - :ref:`subdoc<operator.backup-section>`
     - 
     - Percona Server for MongoDB backups section

.. _operator.upgradeoptions-section:

`Upgrade Options Section <operator.html#operator-upgradeoptions-section>`_
--------------------------------------------------------------------------------

The ``upgradeOptions`` section in the `deploy/cr.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/main/deploy/cr.yaml>`_ file contains various configuration options to control Percona Server for MongoDB upgrades.

.. tabularcolumns:: |p{2cm}|p{13.6cm}|

+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _upgradeoptions-versionserviceendpoint:							|
|                 |												|
| **Key**         | `upgradeOptions.versionServiceEndpoint							|
|                 | <operator.html#upgradeoptions-versionserviceendpoint>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value**       | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``https://check.percona.com``								|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The Version Service URL used to check versions compatibility for upgrade			|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _upgradeoptions-apply:									|
|                 |												|
| **Key**         | `upgradeOptions.apply <operator.html#upgradeoptions-apply>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value**       | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``4.4-recommended``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Specifies how :ref:`updates are processed<operator-update-smartupdates>` by the Operator.	|
|                 | ``Never`` or ``Disabled`` will completely disable automatic upgrades, otherwise it can be	|
|                 | set to ``Latest`` or ``Recommended`` or to a specific version string of Percona Server for	|
|                 | MongoDB (e.g. ``4.4.2-4``)									|
|                 | that is wished to be version-locked (so that the user can control				|
|                 | the version running, but use automatic upgrades to move between them).			|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _upgradeoptions-schedule:								|
|                 |												|
| **Key**         | `upgradeOptions.schedule <operator.html#upgradeoptions-schedule>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value**       | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``0 2 * * *``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Scheduled time to check for updates, specified in the					|
|                 | `crontab format <https://en.wikipedia.org/wiki/Cron>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _upgradeoptions-setfcv:									|
|                 |												|
| **Key**         | `upgradeOptions.setFCV <operator.html#upgradeoptions-setfcv>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value**       | boolean											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``false``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | If enabled, `FeatureCompatibilityVersion (FCV)						|
|                 | <https://docs.mongodb.com/manual/reference/command/setFeatureCompatibilityVersion/>`_	|
|                 | will be set to match the version during major version upgrade				|
+-----------------+---------------------------------------------------------------------------------------------+

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
| **Description** | The secret name for the MongoDB users **required to run the operator.**			|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
| **Key**         | .. _secrets-ssl:										|
|                 |												|
|                 | `secrets.ssl <operator.html#secrets-ssl>`_							|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``my-custom-ssl``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | A secret with TLS certificate generated for *external* communications,			|
|                 | see :ref:`tls` for details									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
| **Key**         | .. _secrets-sslinternal:									|
|                 |												|
|                 | `secrets.sslInternal <operator.html#secrets-sslinternal>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``my-custom-ssl-internal``									|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | A secret with TLS certificate generated for *internal* communications,			|
|                 | see :ref:`tls` for details									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _secrets-encryptionkey:									|
|                 |												|
| **Key**         | `secrets.encryptionKey <operator.html#secrets-encryptionkey>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``my-cluster-name-mongodb-encryption-key``							|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Specifies a secret object with the `encryption key 						|
|                 | <https://docs.mongodb.com/manual/tutorial/configure-encryption/#local-key-management>`_	|
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
|                 | .. _replsets-configuration:									|
|                 |												|
| **Key**         | `replsets.configuration <operator.html#replsets-configuration>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | .. code:: yaml										|
|                 |												|
|                 |     |											|
|                 |     operationProfiling:									|
|                 |       mode: slowOp										|
|                 |     systemLog:										|
|                 |       verbosity: 1										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Custom configuration options for mongod. Please refer to the `official manual		|
|                 | <https://docs.mongodb.com/manual/reference/configuration-options/>`_ for the full list of 	|
|                 | options, and `specific									|
|                 | <https://www.percona.com/doc/percona-server-for-mongodb/LATEST/rate-limit.html>`_		|
|                 | `Percona <https://www.percona.com/doc/percona-server-for-mongodb/LATEST/inmemory.html>`_	|
|                 | `Server <https://www.percona.com/doc/percona-server-for-mongodb/LATEST/			|
|                 | data_at_rest_encryption.html>`_ `for MongoDB <https://www.percona.com/doc/			|
|                 | percona-server-for-mongodb/LATEST/log-redaction.html>`_ `docs <https://www.percona.com/doc/	|
|                 | percona-server-for-mongodb/LATEST/audit-logging.html>`_.					|
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
|                 | .. _replsets-priorityclassname:								|
|                 |												|
| **Key**         | `replsets.priorityClassName <operator.html#replsets-priorityclassname>`_			|
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
| **Key**         | `replsets.annotations <operator.html#replsets-annotations>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``iam.amazonaws.com/role: role-arn``							|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes annotations									|
|                 | <https://kubernetes.io/docs/concepts/overview/working-with-objects/annotations/>`_		|
|                 | metadata for the Replica Set nodes								|
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
|                 | .. _replsets-nodeselector:									|
|                 |												|
| **Key**         | `replsets.nodeSelector <operator.html#replsets-nodeselector>`_				|
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
|                 | .. _replsets-storage-engine:								|
|                 |												|
| **Key**         | `replsets.storage.engine <operator.html#replsets-storage-engine>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``wiredTiger``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Sets the `storage.engine option								|
|                 | <https://docs.mongodb.com/manual/reference/configuration-options/#storage.engine>`_`	|
|                 | for the Replica Set nodes									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-storage-wiredtiger-engineconfig-cachesizeratio:				|
|                 |												|
| **Key**         | `replsets.storage.wiredTiger.engineConfig.cacheSizeRatio					|
|                 | <operator.html#replsets-storage-wiredtiger-engineconfig-cachesizeratio>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | float											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``0.5``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The ratio used to compute the `storage.wiredTiger.engineConfig.cacheSizeGB option		|
|                 | <https://www.mongodb.com/docs/manual/reference/configuration-options/			|
|                 | #mongodb-setting-storage.wiredTiger.engineConfig.cacheSizeGB>`_				|
|                 | for the Replica Set nodes									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-storage-wiredtiger-engineconfig-directoryforindexes:				|
|                 |												|
| **Key**         | `replsets.storage.wiredTiger.engineConfig.directoryForIndexes				|
|                 | <operator.html#replsets-storage-wiredtiger-engineconfig-directoryforindexes>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | bool											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``false``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Sets the `storage.wiredTiger.engineConfig.directoryForIndexes option			|
|                 | <https://www.mongodb.com/docs/manual/reference/configuration-options/			|
|                 | #mongodb-setting-storage.wiredTiger.engineConfig.directoryForIndexes>`_			|
|                 | for the Replica Set nodes									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-storage-wiredtiger-engineconfig-journalcompressor:				|
|                 |												|
| **Key**         | `replsets.storage.wiredTiger.engineConfig.journalCompressor					|
|                 | <operator.html#replsets-storage-wiredtiger-engineconfig-journalcompressor>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``snappy``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Sets the `storage.wiredTiger.engineConfig.journalCompressor option				|
|                 | <https://www.mongodb.com/docs/manual/reference/configuration-options/			|
|                 | #mongodb-setting-storage.wiredTiger.engineConfig.journalCompressor>`_			|
|                 | for the Replica Set nodes									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-storage-wiredtiger-collectionconfig-blockcompressor:				|
|                 |												|
| **Key**         | `replsets.storage.wiredTiger.collectionConfig.blockCompressor				|
|                 | <operator.html#replsets-storage-wiredtiger-collectionconfig-blockcompressor>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``snappy``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Sets the `storage.wiredTiger.collectionConfig.blockCompressor option			|
|                 | <https://www.mongodb.com/docs/manual/reference/configuration-options/			|
|                 | #mongodb-setting-storage.wiredTiger.collectionConfig.blockCompressor>`_			|
|                 | for the Replica Set nodes									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-storage-wiredtiger-indexconfig-prefixcompression:				|
|                 |												|
| **Key**         | `replsets.storage.wiredTiger.indexConfig.prefixCompression					|
|                 | <operator.html#replsets-storage-wiredtiger-indexconfig-prefixcompression>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | bool											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``true``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Sets the `storage.wiredTiger.indexConfig.prefixCompression option				|
|                 | <https://www.mongodb.com/docs/manual/reference/configuration-options/			|
|                 | #mongodb-setting-storage.wiredTiger.indexConfig.prefixCompression>`_			|
|                 | for the Replica Set nodes									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-storage-inmemory-engineconfig-inmemorysizeratio:				|
|                 |												|
| **Key**         | `replsets.storage.inMemory.engineConfig.inMemorySizeRatio					|
|                 | <operator.html#replsets-storage-inmemory-engineconfig-inmemorysizeratio>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | float											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``0.9``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The ratio used to compute the `storage.engine.inMemory.inMemorySizeGb option		|
|                 | <https://www.mongodb.com/docs/manual/reference/configuration-options/			|
|                 | #mongodb-setting-storage.inMemory.engineConfig.inMemorySizeGB>`_				|
|                 | for the Replica Set nodes									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-livenessprobe-failurethreshold:						|
|                 |												|
| **Key**         | `replsets.livenessProbe.failureThreshold							|
|                 | <operator.html#replsets-livenessprobe-failurethreshold>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``4``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Number of consecutive unsuccessful tries of the 						|
|                 | `liveness probe <https://kubernetes.io/docs/tasks/configure-pod-container/			|
|                 | configure-liveness-readiness-startup-probes/#configure-probes>`_ to be undertaken		|
|                 | before giving up.										|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-livenessprobe-initialdelayseconds:						|
|                 |												|
| **Key**         | `replsets.livenessProbe.initialDelaySeconds							|
|                 | <operator.html#replsets-livenessprobe-initialdelayseconds>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``60``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Number of seconds to wait after the container start before initiating the `liveness probe	|
|                 | <https://kubernetes.io/docs/tasks/configure-pod-container/					|
|                 | configure-liveness-readiness-startup-probes/#configure-probes>`_.				|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-livenessprobe-periodseconds:							|
|                 |												|
| **Key**         | `replsets.livenessProbe.periodSeconds							|
|                 | <operator.html#replsets-livenessprobe-periodseconds>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``30``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | How often to perform a `liveness probe 							|
|                 | <https://kubernetes.io/docs/tasks/configure-pod-container/					|
|                 | configure-liveness-readiness-startup-probes/#configure-probes>`_ (in seconds).		|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-livenessprobe-timeoutseconds:							|
|                 |												|
| **Key**         | `replsets.livenessProbe.timeoutSeconds							|
|                 | <operator.html#replsets-livenessprobe-timeoutseconds>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``10``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Number of seconds after which the `liveness probe 						|
|                 | <https://kubernetes.io/docs/tasks/configure-pod-container/					|
|                 | configure-liveness-readiness-startup-probes/#configure-probes>`_ times out.			|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-livenessprobe-startupdelayseconds:						|
|                 |												|
| **Key**         | `replsets.livenessProbe.startupDelaySeconds							|
|                 | <operator.html#replsets-livenessprobe-startupdelayseconds>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``7200``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Time after which the liveness probe is failed if the MongoDB instance didn't finish its 	|
|                 | full startup yet										|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-readinessprobe-failurethreshold:						|
|                 |												|
| **Key**         | `replsets.readinessProbe.failureThreshold							|
|                 | <operator.html#replsets-readinessprobe-failurethreshold>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``8``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Number of consecutive unsuccessful tries of the 						|
|                 | `readiness probe <https://kubernetes.io/docs/tasks/configure-pod-container/			|
|                 | configure-liveness-readiness-startup-probes/#configure-probes>`_ to be undertaken		|
|                 | before giving up.										|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-readinessprobe-initialdelayseconds:						|
|                 |												|
| **Key**         | `replsets.readinessProbe.initialDelaySeconds						|
|                 | <operator.html#replsets-readinessprobe-initialdelayseconds>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``10``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Number of seconds to wait after the container start before initiating the `readiness probe	|
|                 | <https://kubernetes.io/docs/tasks/configure-pod-container/					|
|                 | configure-liveness-readiness-startup-probes/#configure-probes>`_.				|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-readinessprobe-periodseconds:							|
|                 |												|
| **Key**         | `replsets.readinessProbe.periodSeconds							|
|                 | <operator.html#replsets-readinessprobe-periodseconds>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``3``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | How often to perform a `readiness probe 							|
|                 | <https://kubernetes.io/docs/tasks/configure-pod-container/					|
|                 | configure-liveness-readiness-startup-probes/#configure-probes>`_ (in seconds).		|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-readinessprobe-successthreshold:						|
|                 |												|
| **Key**         | `replsets.readinessProbe.successThreshold							|
|                 | <operator.html#replsets-readinessprobe-successthreshold>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``1``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Minimum consecutive successes for the `readiness probe 					|
|                 | <https://kubernetes.io/docs/tasks/configure-pod-container/					|
|                 | configure-liveness-readiness-startup-probes/#configure-probes>`_ to be considered 		|
|                 | successful after having failed.								|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-readinessprobe-timeoutseconds:							|
|                 |												|
| **Key**         | `replsets.readinessProbe.timeoutSeconds							|
|                 | <operator.html#replsets-readinessprobe-timeoutseconds>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``2``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Number of seconds after which the `readiness probe 						|
|                 | <https://kubernetes.io/docs/tasks/configure-pod-container/					|
|                 | configure-liveness-readiness-startup-probes/#configure-probes>`_ times out.			|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-runtimeclassname:								|
|                 |												|
| **Key**         | `replsets.runtimeClassName									|
|                 | <operator.html#replsets-runtimeclassname>`_							|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``image-rc``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Name of the `Kubernetes Runtime Class							|
|                 | <https://kubernetes.io/docs/concepts/containers/runtime-class/>`_				|
|                 | for Replica Set Pods									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-sidecars-image:								|
|                 |												|
| **Key**         | `replsets.sidecars.image									|
|                 | <operator.html#replsets-sidecars-image>`_							|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``busybox``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Image for the										|
|                 | :ref:`custom sidecar container<faq-sidecar>`						|
|                 | for Replica Set Pods									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-sidecars-command:								|
|                 |												|
| **Key**         | `replsets.sidecars.command									|
|                 | <operator.html#replsets-sidecars-command>`_							|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | array											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``["/bin/sh"]``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Command for the										|
|                 | :ref:`custom sidecar container<faq-sidecar>`						|
|                 | for Replica Set Pods									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-sidecars-args:									|
|                 |												|
| **Key**         | `replsets.sidecars.args									|
|                 | <operator.html#replsets-sidecars-args>`_							|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | array											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``["-c", "while true; do echo echo $(date -u) 'test' >> /dev/null; sleep 5;done"]``		|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Command arguments for the									|
|                 | :ref:`custom sidecar container<faq-sidecar>`						|
|                 | for Replica Set Pods									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-sidecars-name:									|
|                 |												|
| **Key**         | `replsets.sidecars.name									|
|                 | <operator.html#replsets-sidecars-name>`_							|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``rs-sidecar-1``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Name of the											|
|                 | :ref:`custom sidecar container<faq-sidecar>`						|
|                 | for Replica Set Pods									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-sidecars-volumemounts-mountpath:						|
|                 |												|
| **Key**         | `replsets.sidecars.volumeMounts.mountPath							|
|                 | <operator.html#replsets-sidecars-volumemounts-mountpath>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``/volume1``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Mount path of the 										|
|                 | :ref:`custom sidecar container<faq-sidecar>` volume						|
|                 | for Replica Set Pods									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-sidecars-volumemounts-name:							|
|                 |												|
| **Key**         | `replsets.sidecars.volumeMounts.name							|
|                 | <operator.html#replsets-sidecars-volumemounts-name>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``sidecar-volume-claim``									|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Name of the 										|
|                 | :ref:`custom sidecar container<faq-sidecar>` volume						|
|                 | for Replica Set Pods									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-sidecarvolumes-name:								|
|                 |												|
| **Key**         | `replsets.sidecarVolumes.name								|
|                 | <operator.html#replsets-sidecarvolumes-name>`_						|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``sidecar-config``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Name of the 										|
|                 | :ref:`custom sidecar container<faq-sidecar>` volume						|
|                 | for Replica Set Pods									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-sidecarvolumes-configmap-name:							|
|                 |												|
| **Key**         | `replsets.sidecarVolumes.configMap.name							|
|                 | <operator.html#replsets-sidecarvolumes-configmap-name>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``myconfigmap``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Name of the `ConfigMap <https://kubernetes.io/docs/concepts/storage/volumes/#configmap>`__	|
|                 | for a :ref:`custom sidecar container<faq-sidecar>` volume					|
|                 | for Replica Set Pods									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-sidecarvolumes-secret-secretname:						|
|                 |												|
| **Key**         | `replsets.sidecarVolumes.secret.secretName							|
|                 | <operator.html#replsets-sidecarvolumes-secret-secretname>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``sidecar-secret``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Name of the `Secret <https://kubernetes.io/docs/concepts/storage/volumes/#secret>`__ 	|
|                 | for a :ref:`custom sidecar container<faq-sidecar>` volume					|
|                 | for Replica Set Pods									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-sidecarpvcs:									|
|                 |												|
| **Key**         | `replsets.sidecarPVCs									|
|                 | <operator.html#replsets-sidecarpvcs>`_							|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | subdoc											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     |												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | `Persistent Volume Claim									|
|                 | <https://v1-20.docs.kubernetes.io/docs/concepts/storage/persistent-volumes/>`__ for the	|
|                 | :ref:`custom sidecar container<faq-sidecar>` volume						|
|                 | for Replica Set Pods									|
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
|                 | .. _replsets-poddisruptionbudget-minavailable:						|
|                 |												|
| **Key**         | `replsets.podDisruptionBudget.minAvailable							|
|                 | <operator.html#replsets-poddisruptionbudget-minavailable>`_					|
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
|                 | .. _replsets-expose-exposetype:								|
|                 |												|
| **Key**         | `replsets.expose.exposeType <operator.html#replsets-expose-exposetype>`_			|
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
|                 | .. _replsets-nonvoting-enabled:								|
|                 |												|
| **Key**         | `replsets.nonvoting.enabled <operator.html#replsets-nonvoting-enabled>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | boolean											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``false``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Enable or disable creation of :ref:`Replica Set non-voting instances<arbiter-nonvoting>`	|
|                 | within the cluster										|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-nonvoting-size:								|
|                 |												|
| **Key**         | `replsets.nonvoting.size <operator.html#replsets-nonvoting-size>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``1``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The number of :ref:`Replica Set non-voting instances<arbiter-nonvoting>`			|
|                 | within the cluster										|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-nonvoting-affinity-antiaffinitytopologykey:					|
|                 |												|
| **Key**         | `replsets.nonvoting.afinity.antiAffinityTopologyKey						|
|                 | <operator.html#replsets-nonvoting-affinity-antiaffinitytopologykey>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``kubernetes.io/hostname``									|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes topologyKey									|
|                 | <https://kubernetes.io/docs/concepts/configuration/assign-pod-node/				|
|                 | #inter-pod-affinity-and-anti-affinity-beta-feature>`_					|
|                 | node affinity constraint for the non-voting nodes						|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-nonvoting-affinity-advanced:							|
|                 |												|
| **Key**         | `replsets.nonvoting.affinity.advanced <operator.html#replsets-nonvoting-affinity-advanced>`_|
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
|                 | .. _replsets-nonvoting-tolerations-key:							|
|                 |												|
| **Key**         | `replsets.nonvoting.tolerations.key <operator.html#replsets-nonvoting-tolerations-key>`_	|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``node.alpha.kubernetes.io/unreachable``							|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes Pod tolerations								|
|                 | <https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts>`_	|
|                 | key for the non-voting nodes								|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-nonvoting-tolerations-operator:						|
|                 |												|
| **Key**         | `replsets.nonvoting.tolerations.operator							|
|                 | <operator.html#replsets-nonvoting-tolerations-operator>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``Exists``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes Pod tolerations								|
|                 | <https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts>`_	|
|                 | operator for the non-voting nodes								|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-nonvoting-tolerations-effect:							|
|                 |												|
| **Key**         | `replsets.nonvoting.tolerations.effect 							|
|                 | <operator.html#replsets-nonvoting-tolerations-effect>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``NoExecute``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes Pod tolerations								|
|                 | <https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts>`_	|
|                 | effect for the non-voting nodes								|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-nonvoting-tolerations-tolerationseconds:					|
|                 |												|
| **Key**         | `replsets.nonvoting.tolerations.tolerationSeconds						|
|                 | <operator.html#replsets-nonvoting-tolerations-tolerationseconds>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``6000``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes Pod tolerations								|
|                 | <https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts>`_	|
|                 | time limit for the non-voting nodes								|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-nonvoting-priorityclassname:							|
|                 |												|
| **Key**         | `replsets.nonvoting.priorityClassName <operator.html#replsets-nonvoting-priorityclassname>`_|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``high priority``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kuberentes Pod priority class								|
|                 | <https://kubernetes.io/docs/concepts/configuration/pod-priority-preemption/			|
|                 | #priorityclass>`_ for the non-voting nodes							|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-nonvoting-annotations:								|
|                 |												|
| **Key**         | `replsets.nonvoting.annotations <operator.html#replsets-nonvoting-annotations>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``iam.amazonaws.com/role: role-arn``							|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes annotations									|
|                 | <https://kubernetes.io/docs/concepts/overview/working-with-objects/annotations/>`_		|
|                 | metadata for the non-voting nodes								|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-nonvoting-labels:								|
|                 |												|
| **Key**         | `replsets.nonvoting.labels <operator.html#replsets-nonvoting-labels>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | label											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``rack: rack-22``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes affinity labels								|
|                 | <https://kubernetes.io/docs/concepts/configuration/assign-pod-node/>`_			|
|                 | for the non-voting nodes									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _replsets-nonvoting-nodeselector:							|
|                 |												|
| **Key**         | `replsets.nonvoting.nodeSelector <operator.html#replsets-nonvoting-nodeselector>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | label											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``disktype: ssd``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes nodeSelector								|
|                 | <https://kubernetes.io/docs/concepts/configuration/assign-pod-node/				|
|                 | #nodeselector>`_ affinity constraint for the non-voting nodes				|
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
|                 | <https://docs.mongodb.com/manual/core/replica-set-arbiter/>`_ instances			|
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
|                 | .. _replsets-arbiter-tolerations-tolerationseconds:						|
|                 |												|
| **Key**         | `replsets.arbiter.tolerations.tolerationSeconds						|
|                 | <operator.html#replsets-arbiter-tolerations-tolerationseconds>`_				|
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
|                 | .. _replsets-arbiter-priorityclassname:							|
|                 |												|
| **Key**         | `replsets.arbiter.priorityClassName <operator.html#replsets-arbiter-priorityclassname>`_	|
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
| **Key**         | `replsets.arbiter.annotations <operator.html#replsets-arbiter-annotations>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``iam.amazonaws.com/role: role-arn``							|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes annotations									|
|                 | <https://kubernetes.io/docs/concepts/overview/working-with-objects/annotations/>`_		|
|                 | metadata for the Arbiter nodes								|
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
|                 | .. _replsets-arbiter-nodeselector:								|
|                 |												|
| **Key**         | `replsets.arbiter.nodeSelector <operator.html#replsets-arbiter-nodeselector>`_		|
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
|                 | .. _replsets-volumespec-emptydir:								|
|                 |												|
| **Key**         | `replsets.volumeSpec.emptyDir <operator.html#replsets-volumespec-emptydir>`_		|
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
|                 | .. _replsets-volumespec-hostpath-path:							|
|                 |												|
| **Key**         | `replsets.volumeSpec.hostPath.path <operator.html#replsets-volumespec-hostpath-path>`_	|
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
|                 | .. _replsets-volumespec-hostpath-type:							|
|                 |												|
| **Key**         | `replsets.volumeSpec.hostPath.type <operator.html#replsets-volumespec-hostpath-type>`_	|
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
|                 | .. _replsets-volumespec-persistentvolumeclaim-storageclassname:				|
|                 |												|
| **Key**         | `replsets.volumeSpec.persistentVolumeClaim.storageClassName					|
|                 | <operator.html#replsets-volumespec-persistentvolumeclaim-storageclassname>`_		|
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
|                 | .. _replsets-volumespec-persistentvolumeclaim-accessmodes:					|
|                 |												|
| **Key**         | `replsets.volumeSpec.persistentVolumeClaim.accessModes					|
|                 | <operator.html#replsets-volumespec-persistentvolumeclaim-accessmodes>`_			|
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
|                 | .. _replsets-volumespec-persistentvolumeclaim-resources-requests-storage:			|
|                 |												|
| **Key**         | `replsets.volumeSpec.persistentVolumeClaim.resources.requests.storage			|
|                 | <operator.html#replsets-volumespec-persistentvolumeclaim-resources-requests-storage>`_	|
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
| **Example**     | ``percona/pmm-client:{{{pmm2recommended}}}``								|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | PMM Client docker image to use								|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _pmm-serverhost:										|
|                 |												|
| **Key**         | `pmm.serverHost <operator.html#pmm-serverhost>`_						|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``monitoring-service``									|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Address of the PMM Server to collect data from the Cluster					|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _pmm-mongodparams:									|
|                 |												|
| **Key**         | `pmm.mongodParams <operator.html#pmm-mongodparams>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``--environment=DEV-ENV --custom-labels=DEV-ENV``						|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Additional parameters which will be passed to the `pmm-admin add mongodb			|
|                 | <https://www.percona.com/doc/percona-monitoring-and-management/2.x/setting-up/client/	|
|                 | mongodb.html#adding-mongodb-service-monitoring>`_ command for ``mongod`` Pods		|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _pmm-mongosparams:									|
|                 |												|
| **Key**         | `pmm.mongosParams <operator.html#pmm-mongosparams>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``--environment=DEV-ENV --custom-labels=DEV-ENV``						|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Additional parameters which will be passed to the `pmm-admin add mongodb			|
|                 | <https://www.percona.com/doc/percona-monitoring-and-management/2.x/setting-up/client/	|
|                 | mongodb.html#adding-mongodb-service-monitoring>`_ command for ``mongos`` Pods		|
+-----------------+---------------------------------------------------------------------------------------------+

.. _operator.sharding-section:

`Sharding Section <operator.html#operator-sharding-section>`_
--------------------------------------------------------------

The ``sharding`` section in the deploy/cr.yaml file contains configuration
options for Percona Server for MondoDB :ref:`sharding<operator.sharding>`.

.. tabularcolumns:: |p{2cm}|p{13.6cm}|

+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-enabled:									|
|                 |												|
| **Key**         | `sharding.enabled <operator.html#sharding-enabled>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | boolean											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``true``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Enables or disables `Percona Server for MondoDB 	 					|
|                 | sharding <https://docs.mongodb.com/manual/sharding/>`_ 					|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-configsvrreplset-size:								|
|                 |												|
| **Key**         | `sharding.configsvrReplSet.size <operator.html#sharding-configsvrreplset-size>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``3``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The number of `Config Server instances							|
|                 | <https://docs.mongodb.com/manual/core/sharded-cluster-config-servers/>`_ within the cluster |
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-configsvrreplset-configuration:						|
|                 |												|
| **Key**         | `sharding.configsvrReplSet.configuration							|
|                 | <operator.html#sharding-configsvrreplset-configuration>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | .. code:: yaml										|
|                 |												|
|                 |     |											|
|                 |     operationProfiling:									|
|                 |       mode: slowOp										|
|                 |     systemLog:										|
|                 |       verbosity: 1										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Custom configuration options for Config Servers. Please refer to the `official manual	|
|                 | <https://docs.mongodb.com/manual/reference/configuration-options/>`_ for the full list of 	|
|                 | options											|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-configsvrreplset-livenessprobe-failurethreshold:				|
|                 |												|
| **Key**         | `sharding.configsvrReplSet.livenessProbe.failureThreshold					|
|                 | <operator.html#sharding-configsvrreplset-livenessprobe-failurethreshold>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``4``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Number of consecutive unsuccessful tries of the 						|
|                 | `liveness probe <https://kubernetes.io/docs/tasks/configure-pod-container/			|
|                 | configure-liveness-readiness-startup-probes/#configure-probes>`_ to be undertaken		|
|                 | before giving up.										|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-configsvrreplset-livenessprobe-initialdelayseconds:				|
|                 |												|
| **Key**         | `sharding.configsvrReplSet.livenessProbe.initialDelaySeconds				|
|                 | <operator.html#sharding-configsvrreplset-livenessprobe-initialdelayseconds>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``60``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Number of seconds to wait after the container start before initiating the `liveness probe	|
|                 | <https://kubernetes.io/docs/tasks/configure-pod-container/					|
|                 | configure-liveness-readiness-startup-probes/#configure-probes>`_.				|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-configsvrreplset-livenessprobe-periodseconds:					|
|                 |												|
| **Key**         | `sharding.configsvrReplSet.livenessProbe.periodSeconds					|
|                 | <operator.html#sharding-configsvrreplset-livenessprobe-periodseconds>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``30``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | How often to perform a `liveness probe 							|
|                 | <https://kubernetes.io/docs/tasks/configure-pod-container/					|
|                 | configure-liveness-readiness-startup-probes/#configure-probes>`_ (in seconds).		|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-configsvrreplset-livenessprobe-timeoutseconds:					|
|                 |												|
| **Key**         | `sharding.configsvrReplSet.livenessProbe.timeoutSeconds					|
|                 | <operator.html#sharding-configsvrreplset-livenessprobe-timeoutseconds>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``10``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Number of seconds after which the `liveness probe 						|
|                 | <https://kubernetes.io/docs/tasks/configure-pod-container/					|
|                 | configure-liveness-readiness-startup-probes/#configure-probes>`_ times out.			|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-configsvrreplset-livenessprobe-startupdelayseconds:				|
|                 |												|
| **Key**         | `sharding.configsvrReplSet.livenessProbe.startupDelaySeconds				|
|                 | <operator.html#sharding-configsvrreplset-livenessprobe-startupdelayseconds>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``7200``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Time after which the liveness probe is failed if the MongoDB instance didn't finish its 	|
|                 | full startup yet										|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-configsvrreplset-readinessprobe-failurethreshold:				|
|                 |												|
| **Key**         | `sharding.configsvrReplSet.readinessProbe.failureThreshold					|
|                 | <operator.html#sharding-configsvrreplset-readinessprobe-failurethreshold>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``3``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Number of consecutive unsuccessful tries of the 						|
|                 | `readiness probe <https://kubernetes.io/docs/tasks/configure-pod-container/			|
|                 | configure-liveness-readiness-startup-probes/#configure-probes>`_ to be undertaken		|
|                 | before giving up.										|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-configsvrreplset-readinessprobe-initialdelayseconds:				|
|                 |												|
| **Key**         | `sharding.configsvrReplSet.readinessProbe.initialDelaySeconds				|
|                 | <operator.html#sharding-configsvrreplset-readinessprobe-initialdelayseconds>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``10``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Number of seconds to wait after the container start before initiating the `readiness probe	|
|                 | <https://kubernetes.io/docs/tasks/configure-pod-container/					|
|                 | configure-liveness-readiness-startup-probes/#configure-probes>`_.				|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-configsvrreplset-readinessprobe-periodseconds:					|
|                 |												|
| **Key**         | `sharding.configsvrReplSet.readinessProbe.periodSeconds					|
|                 | <operator.html#sharding-configsvrreplset-readinessprobe-periodseconds>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``3``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | How often to perform a `readiness probe 							|
|                 | <https://kubernetes.io/docs/tasks/configure-pod-container/					|
|                 | configure-liveness-readiness-startup-probes/#configure-probes>`_ (in seconds).		|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-configsvrreplset-readinessprobe-successthreshold:				|
|                 |												|
| **Key**         | `sharding.configsvrReplSet.readinessProbe.successThreshold					|
|                 | <operator.html#sharding-configsvrreplset-readinessprobe-successthreshold>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``1``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Minimum consecutive successes for the `readiness probe 					|
|                 | <https://kubernetes.io/docs/tasks/configure-pod-container/					|
|                 | configure-liveness-readiness-startup-probes/#configure-probes>`_ to be considered 		|
|                 | successful after having failed.								|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-configsvrreplset-readinessprobe-timeoutseconds:				|
|                 |												|
| **Key**         | `sharding.configsvrReplSet.readinessProbe.timeoutSeconds					|
|                 | <operator.html#sharding-configsvrreplset-readinessprobe-timeoutseconds>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``2``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Number of seconds after which the `readiness probe 						|
|                 | <https://kubernetes.io/docs/tasks/configure-pod-container/					|
|                 | configure-liveness-readiness-startup-probes/#configure-probes>`_ times out.			|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-configsvrreplset-runtimeclassname:						|
|                 |												|
| **Key**         | `sharding.configsvrReplSet.runtimeClassName							|
|                 | <operator.html#sharding-configsvrreplset-runtimeclassname>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``image-rc``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Name of the `Kubernetes Runtime Class							|
|                 | <https://kubernetes.io/docs/concepts/containers/runtime-class/>`_				|
|                 | for Config Server Pods									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-configsvrreplset-sidecars-image:						|
|                 |												|
| **Key**         | `sharding.configsvrReplSet.sidecars.image							|
|                 | <operator.html#sharding-configsvrreplset-sidecars-image>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``busybox``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Image for the										|
|                 | :ref:`custom sidecar container<faq-sidecar>`						|
|                 | for Config Server Pods									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-configsvrreplset-sidecars-command:						|
|                 |												|
| **Key**         | `sharding.configsvrReplSet.sidecars.command							|
|                 | <operator.html#sharding-configsvrreplset-sidecars-command>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | array											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``["/bin/sh"]``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Command for the										|
|                 | :ref:`custom sidecar container<faq-sidecar>`						|
|                 | for Config Server Pods									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-configsvrreplset-sidecars-args:						|
|                 |												|
| **Key**         | `sharding.configsvrReplSet.sidecars.args							|
|                 | <operator.html#sharding-configsvrreplset-sidecars-args>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | array											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``["-c", "while true; do echo echo $(date -u) 'test' >> /dev/null; sleep 5;done"]``		|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Command arguments for the									|
|                 | :ref:`custom sidecar container<faq-sidecar>`						|
|                 | for Config Server Pods									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-configsvrreplset-sidecars-name:						|
|                 |												|
| **Key**         | `sharding.configsvrReplSet.sidecars.name							|
|                 | <operator.html#sharding-configsvrreplset-sidecars-name>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``rs-sidecar-1``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Name of the											|
|                 | :ref:`custom sidecar container<faq-sidecar>`						|
|                 | for Config Server Pods									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-configsvrreplset-volumespec-emptydir:						|
|                 |												|
| **Key**         | `sharding.configsvrReplSet.volumeSpec.emptyDir						|
|                 | <operator.html#sharding-configsvrreplset-volumespec-emptydir>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``{}``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes emptyDir volume <https://kubernetes.io/docs/concepts/storage/volumes/	|
|                 | #emptydir>`_, i.e. the directory which will be created on a node, and will be accessible to	|
|                 | the Config Server Pod containers								|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-configsvrreplset-volumespec-hostpath-path:					|
|                 |												|
| **Key**         | `sharding.configsvrReplSet.volumeSpec.hostPath.path						|
|                 | <operator.html#sharding-configsvrreplset-volumespec-hostpath-path>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``/data``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | `Kubernetes hostPath volume <https://kubernetes.io/docs/concepts/storage/volumes/		|
|                 | #hostpath>`_, i.e. the file or directory of a node that will be accessible to the Config	|
|                 | Server Pod containers									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-configsvrreplset-volumespec-hostpath-type:					|
|                 |												|
| **Key**         | `sharding.configsvrReplSet.volumeSpec.hostPath.type						|
|                 | <operator.html#sharding-configsvrreplset-volumespec-hostpath-type>`_			|
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
|                 | .. _sharding-configsvrreplset-volumespec-persistentvolumeclaim-storageclassname:		|
|                 |												|
| **Key**         | `sharding.configsvrReplSet.volumeSpec.persistentVolumeClaim.storageClassName <operator.html#|
|                 | sharding-configsvrreplset-volumespec-persistentvolumeclaim-storageclassname>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``standard``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes Storage Class								|
|                 | <https://kubernetes.io/docs/concepts/storage/storage-classes/>`_				|
|                 | to use with the Config Server container `Persistent Volume Claim 				|
|                 | <https://kubernetes.io/docs/concepts/storage/persistent-volumes/#persistentvolumeclaims>`_.	|
|                 | Use Storage Class with XFS as the default filesystem if possible, `for better MongoDB 	|
|                 | performance 										|
|                 | <https://dba.stackexchange.com/questions/190578/is-xfs-still-the-best-choice-for-mongodb>`_	|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-configsvrreplset-volumespec-persistentvolumeclaim-accessmodes:			|
|                 |												|
| **Key**         | `sharding.configsvrReplSet.volumeSpec.persistentVolumeClaim.accessModes			|
|                 | <operator.html#sharding-configsvrreplset-volumespec-persistentvolumeclaim-accessmodes>`_	|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | array											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``[ "ReadWriteOnce" ]``									|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes Persistent Volume								|
|                 | <https://kubernetes.io/docs/concepts/storage/persistent-volumes/>`_				|
|                 | access modes for the Config Server container						|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-configsvrreplset-volumespec-persistentvolumeclaim-resources-requests-storage:	|
|                 |												|
| **Key**         | `sharding.configsvrReplSet.volumeSpec.persistentVolumeClaim.resources.requests.storage	|
|                 | <operator.html#										|
|                 | sharding-configsvrreplset-volumespec-persistentvolumeclaim-resources-requests-storage>`_	|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``3Gi``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes Persistent Volume								|
|                 | <https://kubernetes.io/docs/concepts/storage/persistent-volumes/>`_				|
|                 | size for the Config Server container							|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-mongos-size:									|
|                 |												|
| **Key**         | `sharding.mongos.size <operator.html#sharding-mongos-size>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``3``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The number of `mongos									|
|                 | <https://docs.mongodb.com/manual/core/sharded-cluster-query-router/>`_ instances		|
|                 | within the cluster										|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-mongos-configuration:								|
|                 |												|
| **Key**         | `sharding.mongos.configuration <operator.html#sharding-mongos-configuration>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | .. code:: yaml										|
|                 |												|
|                 |     |											|
|                 |     systemLog:										|
|                 |       verbosity: 1										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Custom configuration options for mongos. Please refer to the `official manual		|
|                 | <https://docs.mongodb.com/manual/reference/configuration-options/>`_ for the full list of 	|
|                 | options											|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-mongos-affinity-antiaffinitytopologykey:					|
|                 |												|
| **Key**         | `sharding.mongos.afinity.antiAffinityTopologyKey						|
|                 | <operator.html#sharding-mongos-affinity-antiaffinitytopologykey>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``kubernetes.io/hostname``									|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes topologyKey									|
|                 | <https://kubernetes.io/docs/concepts/configuration/assign-pod-node/				|
|                 | #inter-pod-affinity-and-anti-affinity-beta-feature>`_					|
|                 | node affinity constraint for mongos								|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-mongos-affinity-advanced:							|
|                 |												|
| **Key**         | `sharding.mongos.affinity.advanced <operator.html#sharding-mongos-affinity-advanced>`_	|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | subdoc											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     |												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | In cases where the Pods require complex tuning the `advanced` option turns off		|
|                 | the ``topologykey`` effect. This setting allows the standard Kubernetes affinity		|
|                 | constraints of any complexity to be used							|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-mongos-tolerations-key:							|
|                 |												|
| **Key**         | `sharding.mongos.tolerations.key <operator.html#sharding-mongos-tolerations-key>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``node.alpha.kubernetes.io/unreachable``							|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes Pod tolerations								|
|                 | <https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts>`_	|
|                 | key for mongos instances									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-mongos-tolerations-operator:							|
|                 |												|
| **Key**         | `sharding.mongos.tolerations.operator							|
|                 | <operator.html#sharding-mongos-tolerations-operator>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``Exists``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes Pod tolerations								|
|                 | <https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts>`_	|
|                 | operator for mongos instances								|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-mongos-tolerations-effect:							|
|                 |												|
| **Key**         | `sharding.mongos.tolerations.effect <operator.html#sharding-mongos-tolerations-effect>`_	|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``NoExecute``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes Pod tolerations								|
|                 | <https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts>`_	|
|                 | effect for mongos instances									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-mongos-tolerations-tolerationseconds:						|
|                 |												|
| **Key**         | `sharding.mongos.tolerations.tolerationSeconds						|
|                 | <operator.html#sharding-mongos-tolerations-tolerationseconds>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``6000``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes Pod tolerations								|
|                 | <https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts>`_	|
|                 | time limit for mongos instances								|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-mongos-priorityclassname:							|
|                 |												|
| **Key**         | `sharding.mongos.priorityClassName <operator.html#sharding-mongos-priorityclassname>`_	|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``high priority``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kuberentes Pod priority class								|
|                 | <https://kubernetes.io/docs/concepts/configuration/pod-priority-preemption/			|
|                 | #priorityclass>`_ for mongos instances							|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-mongos-annotations:								|
|                 |												|
| **Key**         | `sharding.mongos.annotations <operator.html#sharding-mongos-annotations>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``iam.amazonaws.com/role: role-arn``							|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes annotations									|
|                 | <https://kubernetes.io/docs/concepts/overview/working-with-objects/annotations/>`_		|
|                 | metadata for the mongos instances								|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-mongos-labels:									|
|                 |												|
| **Key**         | `sharding.mongos.labels <operator.html#sharding-mongos-labels>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | label											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``rack: rack-22``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes affinity labels								|
|                 | <https://kubernetes.io/docs/concepts/configuration/assign-pod-node/>`_			|
|                 | for mongos instances									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-mongos-nodeselector:								|
|                 |												|
| **Key**         | `sharding.mongos.nodeSelector <operator.html#sharding-mongos-nodeselector>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | label											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``disktype: ssd``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes nodeSelector								|
|                 | <https://kubernetes.io/docs/concepts/configuration/assign-pod-node/				|
|                 | #nodeselector>`_ affinity constraint for mongos instances					|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-mongos-livenessprobe-failurethreshold:						|
|                 |												|
| **Key**         | `sharding.mongos.livenessProbe.failureThreshold						|
|                 | <operator.html#sharding-mongos-livenessprobe-failurethreshold>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``4``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Number of consecutive unsuccessful tries of the 						|
|                 | `liveness probe <https://kubernetes.io/docs/tasks/configure-pod-container/			|
|                 | configure-liveness-readiness-startup-probes/#configure-probes>`_ to be undertaken		|
|                 | before giving up.										|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-mongos-livenessprobe-initialdelayseconds:					|
|                 |												|
| **Key**         | `sharding.mongos.livenessProbe.initialDelaySeconds						|
|                 | <operator.html#sharding-mongos-livenessprobe-initialdelayseconds>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``60``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Number of seconds to wait after the container start before initiating the `liveness probe	|
|                 | <https://kubernetes.io/docs/tasks/configure-pod-container/					|
|                 | configure-liveness-readiness-startup-probes/#configure-probes>`_.				|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-mongos-livenessprobe-periodseconds:						|
|                 |												|
| **Key**         | `sharding.mongos.livenessProbe.periodSeconds						|
|                 | <operator.html#sharding-mongos-livenessprobe-periodseconds>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``30``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | How often to perform a `liveness probe 							|
|                 | <https://kubernetes.io/docs/tasks/configure-pod-container/					|
|                 | configure-liveness-readiness-startup-probes/#configure-probes>`_ (in seconds).		|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-mongos-livenessprobe-timeoutseconds:						|
|                 |												|
| **Key**         | `sharding.mongos.livenessProbe.timeoutSeconds						|
|                 | <operator.html#sharding-mongos-livenessprobe-timeoutseconds>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``10``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Number of seconds after which the `liveness probe 						|
|                 | <https://kubernetes.io/docs/tasks/configure-pod-container/					|
|                 | configure-liveness-readiness-startup-probes/#configure-probes>`_ times out.			|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-mongos-livenessprobe-startupdelayseconds:					|
|                 |												|
| **Key**         | `sharding.mongos.livenessProbe.startupDelaySeconds						|
|                 | <operator.html#sharding-mongos-livenessprobe-startupdelayseconds>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``7200``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Time after which the liveness probe is failed if the MongoDB instance didn't finish its 	|
|                 | full startup yet										|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-mongos-readinessprobe-failurethreshold:					|
|                 |												|
| **Key**         | `sharding.mongos.readinessProbe.failureThreshold						|
|                 | <operator.html#sharding-mongos-readinessprobe-failurethreshold>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``3``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Number of consecutive unsuccessful tries of the 						|
|                 | `readiness probe <https://kubernetes.io/docs/tasks/configure-pod-container/			|
|                 | configure-liveness-readiness-startup-probes/#configure-probes>`_ to be undertaken		|
|                 | before giving up.										|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-mongos-readinessprobe-initialdelayseconds:					|
|                 |												|
| **Key**         | `sharding.mongos.readinessProbe.initialDelaySeconds						|
|                 | <operator.html#sharding-mongos-readinessprobe-initialdelayseconds>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``10``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Number of seconds to wait after the container start before initiating the `readiness probe	|
|                 | <https://kubernetes.io/docs/tasks/configure-pod-container/					|
|                 | configure-liveness-readiness-startup-probes/#configure-probes>`_.				|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-mongos-readinessprobe-periodseconds:						|
|                 |												|
| **Key**         | `sharding.mongos.readinessProbe.periodSeconds						|
|                 | <operator.html#sharding-mongos-readinessprobe-periodseconds>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``3``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | How often to perform a `readiness probe 							|
|                 | <https://kubernetes.io/docs/tasks/configure-pod-container/					|
|                 | configure-liveness-readiness-startup-probes/#configure-probes>`_ (in seconds).		|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-mongos-readinessprobe-successthreshold:					|
|                 |												|
| **Key**         | `sharding.mongos.readinessProbe.successThreshold						|
|                 | <operator.html#sharding-mongos-readinessprobe-successthreshold>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``1``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Minimum consecutive successes for the `readiness probe 					|
|                 | <https://kubernetes.io/docs/tasks/configure-pod-container/					|
|                 | configure-liveness-readiness-startup-probes/#configure-probes>`_ to be considered 		|
|                 | successful after having failed.								|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-mongos-readinessprobe-timeoutseconds:						|
|                 |												|
| **Key**         | `sharding.mongos.readinessProbe.timeoutSeconds						|
|                 | <operator.html#sharding-mongos-readinessprobe-timeoutseconds>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``2``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Number of seconds after which the `readiness probe 						|
|                 | <https://kubernetes.io/docs/tasks/configure-pod-container/					|
|                 | configure-liveness-readiness-startup-probes/#configure-probes>`_ times out.			|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-mongos-runtimeclassname:							|
|                 |												|
| **Key**         | `sharding.mongos.runtimeClassName								|
|                 | <operator.html#sharding-mongos-runtimeclassname>`_						|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``image-rc``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Name of the `Kubernetes Runtime Class							|
|                 | <https://kubernetes.io/docs/concepts/containers/runtime-class/>`_				|
|                 | for mongos Pods										|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-mongos-sidecars-image:								|
|                 |												|
| **Key**         | `sharding.mongos.sidecars.image								|
|                 | <operator.html#sharding-mongos-sidecars-image>`_						|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``busybox``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Image for the										|
|                 | :ref:`custom sidecar container<faq-sidecar>`						|
|                 | for mongos Pods										|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-mongos-sidecars-command:							|
|                 |												|
| **Key**         | `sharding.mongos.sidecars.command								|
|                 | <operator.html#sharding-mongos-sidecars-command>`_						|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | array											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``["/bin/sh"]``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Command for the										|
|                 | :ref:`custom sidecar container<faq-sidecar>`						|
|                 | for mongos Pods										|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-mongos-sidecars-args:								|
|                 |												|
| **Key**         | `sharding.mongos.sidecars.args								|
|                 | <operator.html#sharding-mongos-sidecars-args>`_						|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | array											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``["-c", "while true; do echo echo $(date -u) 'test' >> /dev/null; sleep 5;done"]``		|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Command arguments for the									|
|                 | :ref:`custom sidecar container<faq-sidecar>`						|
|                 | for mongos Pods										|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-mongos-sidecars-name:								|
|                 |												|
| **Key**         | `sharding.mongos.sidecars.name								|
|                 | <operator.html#sharding-mongos-sidecars-name>`_						|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``rs-sidecar-1``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Name of the											|
|                 | :ref:`custom sidecar container<faq-sidecar>`						|
|                 | for mongos Pods										|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-mongos-resources-limits-cpu:							|
|                 |												|
| **Key**         | `sharding.mongos.limits.cpu <operator.html#sharding-mongos-resources-limits-cpu>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``300m``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | `Kubernetes CPU limit									|
|                 | <https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/	|
|                 | #resource-requests-and-limits-of-pod-and-container>`_ for mongos container			|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-mongos-resources-limits-memory:						|
|                 |												|
| **Key**         | `sharding.mongos.limits.memory <operator.html#sharding-mongos-resources-limits-memory>`_	|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``0.5G``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | `Kubernetes Memory limit 									|
|                 | <https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/	|
|                 | #resource-requests-and-limits-of-pod-and-container>`_ for mongos container			|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-mongos-resources-requests-cpu:							|
|                 |												|
| **Key**         | `sharding.mongos.resources.requests.cpu							|
|                 | <operator.html#sharding-mongos-resources-requests-cpu>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``300m``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes CPU requests								|
|                 | <https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/	|
|                 | #resource-requests-and-limits-of-pod-and-container>`_ for mongos container			|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-mongos-resources-requests-memory:						|
|                 |												|
| **Key**         | `sharding.mongos.requests.memory <operator.html#sharding-mongos-resources-requests-memory>`_|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``0.5G``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes Memory requests								|
|                 | <https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/	|
|                 | #resource-requests-and-limits-of-pod-and-container>`_ for mongos container			|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-mongos-expose-exposetype:							|
|                 |												|
| **Key**         | `sharding.mongos.expose.exposeType <operator.html#sharding-mongos-expose-exposetype>`_	|
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
|                 | .. _sharding-mongos-expose-serviceperpod:							|
|                 |												|
| **Key**         | `sharding.mongos.expose.servicePerPod <operator.html#sharding-mongos-expose-serviceperpod>`_|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | boolean											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``false``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | If set to ``true``, a separate ClusterIP Service is created for each mongos instance	|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-mongos-expose-loadbalancersourceranges:					|
|                 |												|
| **Key**         | `sharding.mongos.expose.loadBalancerSourceRanges						|
|                 | <operator.html#sharding-mongos-expose-loadbalancersourceranges>`_				|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value**       | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``10.0.0.0/8``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The range of client IP addresses from which the load balancer should be reachable		|
|                 | (if not set, there is no limitations)							|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-mongos-expose-serviceannotations:						|
|                 |												|
| **Key**         | `sharding.mongos.expose.serviceAnnotations 							|
|                 | <operator.html#sharding-mongos-expose-serviceannotations>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value**       | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``service.beta.kubernetes.io/aws-load-balancer-backend-protocol: http``			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes annotations									|
|                 | <https://kubernetes.io/docs/concepts/overview/working-with-objects/annotations/>`_		|
|                 | metadata for the MongoDB mongos daemon							|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-mongos-auditLog-destination:							|
|                 |												|
| **Key**         | `sharding.mongos.auditLog.destination <operator.html#sharding-mongos-auditLog-destination>`_|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     |												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Sets the `auditLog.destination option							|
|                 | <https://www.percona.com/doc/percona-server-for-mongodb/LATEST/audit-logging.html>`_	|
|                 | for the MongoDB mongos daemon.								|
|                 | **Deprecated in the Operator version 1.9.0+, unavailable in**				|
|                 | **v1.13.0+; use** :ref:`sharding.mongos.configuration<sharding-mongos-configuration>`	|
|                 | **instead**											|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-mongos-auditLog-format:							|
|                 |												|
| **Key**         | `sharding.mongos.auditLog.format <operator.html#sharding-mongos-auditLog-format>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``BSON``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Sets the `auditLog.format option								|
|                 | <https://www.percona.com/doc/percona-server-for-mongodb/LATEST/audit-logging.html>`_	|
|                 | for the MongoDB mongos daemon.								|
|                 | **Deprecated in the Operator version 1.9.0+, unavailable in**				|
|                 | **v1.13.0+; use** :ref:`sharding.mongos.configuration<sharding-mongos-configuration>`	|
|                 | **instead**											|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _sharding-mongos-auditLog-filter:							|
|                 |												|
| **Key**         | `sharding.mongos.auditLog.filter <operator.html#sharding-mongos-auditLog-filter>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``{}``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Sets the `auditLog.filter option								|
|                 | <https://www.percona.com/doc/percona-server-for-mongodb/LATEST/audit-logging.html>`_	|
|                 | for the MongoDB mongos daemon.								|
|                 | **Deprecated in the Operator version 1.9.0+, unavailable in**				|
|                 | **v1.13.0+; use** :ref:`sharding.mongos.configuration<sharding-mongos-configuration>`	|
|                 | **instead**											|
+-----------------+---------------------------------------------------------------------------------------------+

.. _operator.mongod-section:

`Mongod Section <operator.html#operator-mongod-section>`_
----------------------------------------------------------

This section contains the Mongod configuration options.
This section is **deprecated** in |operator|
v1.12.0+, **and will be unavailable** in v1.14.0+. Options were moved to
:ref:`replsets.configuration<replsets-configuration>`.

.. tabularcolumns:: |p{2cm}|p{13.6cm}|

+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _mongod-security-encryptionkeysecret:							|
|                 |												|
| **Key**         | `mongod.security.encryptionKeySecret <operator.html#mongod-security-encryptionkeysecret>`_	|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``my-cluster-name-mongodb-encryption-key``							|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Specifies a secret object with the `encryption key 						|
|                 | <https://docs.mongodb.com/manual/tutorial/configure-encryption/#local-key-management>`_	|
|                 | **Please note that this option is deprecated;**						|
|                 | **use** :ref:`spec.secrets.encryptionKey<secrets-encryptionkey>` **instead**		|
+-----------------+---------------------------------------------------------------------------------------------+

.. _operator.backup-section:

`Backup Section <operator.html#operator-backup-section>`_
----------------------------------------------------------

The ``backup`` section in the
`deploy/cr.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/main/deploy/cr.yaml>`_
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
|                 | .. _backup-serviceaccountname:								|
|                 |												|
| **Key**         | `backup.serviceAccountName <operator.html#backup-serviceaccountname>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``percona-server-mongodb-operator``								|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Name of the separate privileged service account used by the Operator			|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _backup-annotations:									|
|                 |												|
| **Key**         | `backup.annotations <operator.html#backup-annotations>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``sidecar.istio.io/inject: "false"``							|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes annotations									|
|                 | <https://kubernetes.io/docs/concepts/overview/working-with-objects/annotations/>`_		|
|                 | metadata for the backup job									|
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
|                 | .. _backup-storages-verifytls:								|
|                 |												|
| **Key**         | `backup.storages.<storage-name>.s3.insecureSkipTLSVerify <operator.html#			|
|                 | backup-storages-s3-insecureskiptlsverify>`_							|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value**       | boolean											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``true``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Enable or disable verification of the storage server TLS certificate. Disabling it may be	|
|                 | useful e.g. to skip TLS verification for private S3-compatible storage with a self-issued	|
|                 | certificate.										|
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
| **Key**         | `backup.storages.<storage-name>.s3.bucket <operator.html#backup-storages-s3-bucket>`_	|
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
|                 | .. _backup-storages-s3-prefix:								|
|                 |												|
| **Key**         | `backup.storages.<storage-name>.s3.prefix <operator.html#backup-storages-s3-prefix>`_	|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value**       | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``""``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The path (sub-folder) to the backups inside the `bucket					|
|                 | <https://docs.aws.amazon.com/AmazonS3/latest/dev/UsingBucket.html>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _backup-storages-s3-region:								|
|                 |												|
| **Key**         | `backup.storages.<storage-name>.s3.region <operator.html#backup-storages-s3-region>`_	|
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
|                 | .. _backup-storages-s3-endpointurl:								|
|                 |												|
| **Key**         | `backup.storages.<storage-name>.s3.endpointUrl						|
|                 | <operator.html#backup-storages-s3-endpointurl>`_						|
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
|                 | .. _backup-storages-azure-credentialssecret:						|
|                 |												|
| **Key**         | `backup.storages.<storage-name>.azure.credentialsSecret					|
|                 | <operator.html#backup-storages-azure-credentialssecret>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value**       | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``my-cluster-azure-secret``									|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The `Kubernetes secret <https://kubernetes.io/docs/concepts/configuration/secret/>`_ for	|
|                 | backups. It should contain ``AZURE_STORAGE_ACCOUNT_NAME`` and ``AZURE_STORAGE_ACCOUNT_KEY``.|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _backup-storages-azure-container:							|
|                 |												|
| **Key**         | `backup.storages.<storage-name>.azure.container <operator.html#				|
|                 | backup-storages-azure-container>`_								|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value**       | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``my-container``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Name of the `container									|
|                 | <https://docs.microsoft.com/en-us/azure/storage/blobs/storage-blobs-introduction#		|
|                 | containers>`_ for backups									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _backup-storages-azure-prefix:								|
|                 |												|
| **Key**         | `backup.storages.<storage-name>.azure.prefix <operator.html#backup-storages-azure-prefix>`_	|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value**       | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``""``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The path (sub-folder) to the backups inside the `container					|
|                 | <https://docs.microsoft.com/en-us/azure/storage/blobs/storage-blobs-introduction#		|
|                 | containers>`_										|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _backup-pitr-enabled:									|
|                 |												|
| **Key**         | `backup.pitr.enabled <operator.html#backup-pitr-enabled>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value**       | boolean											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``false``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Enables or disables :ref:`point-in-time-recovery functionality<backups-pitr-oplog>`		|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _backup-pitr-oplogspanmin:								|
|                 |												|
| **Key**         | `backup.pitr.oplogSpanMin <operator.html#backup-pitr-oplogspanmin>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value**       | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``10``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | Number of minutes between the uploads of oplogs						|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _backup-pitr-compressiontype:								|
|                 |												|
| **Key**         | `backup.pitr.compressionType <operator.html#backup-pitr-compressiontype>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``gzip``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The point-in-time-recovery chunks compression format,					|
|                 | `can be gzip, snappy, lz4, pgzip, zstd, s2, or none						|
|                 | <https://docs.percona.com/percona-backup-mongodb/point-in-time-recovery.html#		|
|                 | incremental-backups>`_									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _backup-pitr-compressionlevel:								|
|                 |												|
| **Key**         | `backup.pitr.compressionLevel <operator.html#pitr-tasks-compressionlevel>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``6``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The point-in-time-recovery chunks compression level						|
|                 | (`higher values result in better but slower compression 					|
|                 | <https://docs.percona.com/percona-backup-mongodb/point-in-time-recovery.html#		|
|                 | incremental-backups>`__)									|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _backup-tasks-name:									|
|                 |												|
| **Key**         | `backup.tasks.name <operator.html#backup-tasks-name>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | 												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The name of the backup									|
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
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``0 0 * * 6``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The scheduled time to make a backup, specified in the 					|
|                 | `crontab format <https://en.wikipedia.org/wiki/Cron>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _backup-tasks-keep:									|
|                 |												|
| **Key**         | `backup.tasks.keep <operator.html#backup-tasks-keep>`_					|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``3``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The amount of most recent backups to store. Older backups are automatically deleted.	|
|                 | Set ``keep`` to zero or completely remove it to disable automatic deletion of backups	|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _backup-tasks-storagename:								|
|                 |												|
| **Key**         | `backup.tasks.storageName <operator.html#backup-tasks-storagename>`_			|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``st-us-west``										|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The name of the S3-compatible storage for backups, configured in the `storages` subsection	|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _backup-tasks-compressiontype:								|
|                 |												|
| **Key**         | `backup.tasks.compressionType <operator.html#backup-tasks-compressiontype>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | string											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``gzip``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The backup compression format, `can be gzip, snappy, lz4, pgzip, zstd, s2, or none		|
|                 | <https://docs.percona.com/percona-backup-mongodb/running.html#starting-a-backup>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
|														|
+-----------------+---------------------------------------------------------------------------------------------+
|                 | .. _backup-tasks-compressionlevel:								|
|                 |												|
| **Key**         | `backup.tasks.compressionLevel <operator.html#backup-tasks-compressionlevel>`_		|
+-----------------+---------------------------------------------------------------------------------------------+
| **Value Type**  | int												|
+-----------------+---------------------------------------------------------------------------------------------+
| **Example**     | ``6``											|
+-----------------+---------------------------------------------------------------------------------------------+
| **Description** | The backup compression level (`higher values result in better but slower compression	|
|                 | <https://docs.percona.com/percona-backup-mongodb/running.html#starting-a-backup>`__)	|
+-----------------+---------------------------------------------------------------------------------------------+

