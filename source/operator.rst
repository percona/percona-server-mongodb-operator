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

   * - version
     - string
     - ``3.6.8``
     - The Dockerhub tag of `percona/percona-server-mongodb <https://hub.docker.com/r/perconalab/percona-server-mongodb-operator/tags/>`_ to deploy

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

Secrets section
---------------

Each spec in its turn may contain some key-value pairs. The secrets one
has only two of them:

.. list-table::
   :widths: 10 10 30 50
   :header-rows: 1

   * - Key
     - Value Type
     - Example
     - Description

   * - key
     - string
     - ``my-cluster-name-mongodb-key``
     - The secret name for the `MongoDB Internal Auth Key <https://docs.mongodb.com/manual/core/security-internal-authentication/>`_. This secret is auto-created by the operator if it doesn’t exist.

   * - users
     - string
     - ``my-cluster-name-mongodb-users``
     - The secret name for the MongoDB users required to run the operator. **This secret is required to run the operator.**

Replsets section
----------------

The replsets section controls the MongoDB Replica Set.

.. list-table::
     :header-rows: 1
     :widths: 10 10 10 60
 
     * - Key
       - Value Type
       - Example
       - Description   

     * - name
       - string
       - ``rs 0``
       - The name of the `MongoDB Replica Set <https://docs.mongodb.com/manual/replication/>`_

     * - size
       - int
       - 3
       - The size of the MongoDB Replica Set, must be >= 3 for `High-Availability <https://docs.mongodb.com/manual/replication/#redundancy-and-data-availability>`_

     * - affinity.antiAffinityTopologyKey
       - string
       - ``kubernetes.io/hostname``
       - The `Kubernetes topologyKey <https://kubernetes.io/docs/concepts/configuration/assign-pod-node/#inter-pod-affinity-and-anti-affinity-beta-feature>`_ node affinity constraint for the Replica Set nodes

     * - affinity.advanced
       - subdoc
       -
       - In cases where the pods require complex tuning the `advanced` option turns off the `topologykey` effect. This setting allows the standard Kubernetes affinity constraints of any complexity to be used

     * - tolerations.key
       - string
       - ``node.alpha.kubernetes.io/unreachable``
       - The `Kubernetes Pod tolerations <https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts>`_ key for the Replica Set nodes

     * - tolerations.operator
       - string
       - ``Exists``
       - The `Kubernetes Pod tolerations <https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts>`_ operator  for the Replica Set nodes

     * - tolerations.effect
       - string
       - ``NoExecute``
       - The `Kubernetes Pod tolerations <https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts>`_ effect  for the Replica Set nodes

     * - tolerations.tolerationSeconds
       - int
       - ``6000``
       - The `Kubernetes Pod tolerations <https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts>`_ time limit  for the Replica Set nodes

     * - annotations.iam.amazonaws.com/role
       - string
       - ``role-arn``
       - The `AWS IAM role <https://kubernetes-on-aws.readthedocs.io/en/latest/user-guide/iam-roles.html>`_  for the Replica Set nodes

     * - labels
       - label
       - ``rack: rack-22``
       - The `Kubernetes affinity labels <https://kubernetes.io/docs/concepts/configuration/assign-pod-node/>`_  for the Replica Set nodes

     * - nodeSelector
       - label
       - ``disktype: ssd``
       - The `Kubernetes nodeSelector <https://kubernetes.io/docs/concepts/configuration/assign-pod-node/#nodeselector>`_ affinity constraint  for the Replica Set nodes

     * - podDisruptionBudget.maxUnavailable
       - int
       - ``1``
       - The `Kubernetes Pod distribution budget <https://kubernetes.io/docs/concepts/workloads/pods/disruptions/>`_ limit specifying the maximum value for unavailable Pods

     * - podDisruptionBudget.minAvailable
       - int
       - ``1``
       - The `Kubernetes Pod distribution budget <https://kubernetes.io/docs/concepts/workloads/pods/disruptions/>`_ limit specifying the minimum value for available Pods

     * - expose.enabled
       - boolean
       - ``false``
       - Enable or disable exposing `MongoDB Replica Set <https://docs.mongodb.com/manual/replication/>`_ nodes with dedicated IP addresses

     * - expose.exposeType
       - string
       - ``ClusterIP``
       - the `IP address type <./expose>`_ to be exposed

     * - arbiter.enabled
       - boolean
       - ``false``
       - Enable or disable creation of `Replica Set Arbiter <https://docs.mongodb.com/manual/core/replica-set-arbiter/>`_ nodes within the cluster

     * - arbiter.size
       - int
       - 1
       - The number of `Replica Set Arbiter <https://docs.mongodb.com/manual/core/replica-set-arbiter/>`_  nodes within the cluster

     * - arbiter.afinity.antiAffinityTopologyKey
       - string
       - ``kubernetes.io/hostname``
       - The `Kubernetes topologyKey <https://kubernetes.io/docs/concepts/configuration/assign-pod-node/#inter-pod-affinity-and-anti-affinity-beta-feature>`_  node affinity constraint for the Arbiter

     * - arbiter.affinity.advanced
       - subdoc
       -
       - In cases where the pods require complex tuning the `advanced` option turns off the `topologykey` effect. This setting allows the standard Kubernetes affinity constraints of any complexity to be used

     * - arbiter.tolerations.key
       - string
       - ``node.alpha.kubernetes.io/unreachable``
       - The `Kubernetes Pod tolerations <https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts>`_ key for the Arbiter nodes

     * - arbiter.tolerations.operator
       - string
       - ``Exists``
       - The `Kubernetes Pod tolerations <https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts>`_ operator for the Arbiter nodes

     * - arbiter.tolerations.effect
       - string
       - ``NoExecute``
       - The `Kubernetes Pod tolerations <https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts>`_ effect for the Arbiter nodes

     * - arbiter.tolerations.tolerationSeconds
       - int
       - ``6000``
       - The `Kubernetes Pod tolerations <https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts>`_ time limit for the Arbiter nodes

     * - arbiter.priorityClassName
       - string
       - ``high priority``
       - The `Kuberentes Pod priority class <https://kubernetes.io/docs/concepts/configuration/pod-priority-preemption/#priorityclass>`_  for the Arbiter nodes

     * - arbiter.annotations.iam.amazonaws.com/role
       - string
       - ``role-arn``
       - The `AWS IAM role <https://kubernetes-on-aws.readthedocs.io/en/latest/user-guide/iam-roles.html>`_ for the Arbiter nodes

     * - arbiter.labels
       - label
       - ``rack: rack-22``
       - The `Kubernetes affinity labels <https://kubernetes.io/docs/concepts/configuration/assign-pod-node/>`_ for the Arbiter nodes

     * - arbiter.nodeSelector
       - label
       - ``disktype: ssd``
       - The `Kubernetes nodeSelector <https://kubernetes.io/docs/concepts/configuration/assign-pod-node/#nodeselector>`_ affinity constraint for the Arbiter nodes

     * - resources.limits.cpu
       - string
       - ``300m``
       - `Kubernetes CPU limit <https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/#resource-requests-and-limits-of-pod-and-container>`_ for MongoDB container

     * - resources.limits.memory
       - string
       - ``0.5G``
       - `Kubernetes Memory limit <https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/#resource-requests-and-limits-of-pod-and-container>`__ for MongoDB container

     * - volumeSpec.emptyDir
       - string
       - ``{}``
       - The `Kubernetes emptyDir volume <https://kubernetes.io/docs/concepts/storage/volumes/#emptydir>`_, i.e. the directory which will be created on a node, and will be accessible to the MongoDB Pod containers

     * - volumeSpec.hostPath.path
       - string
       - ``/data``
       - `Kubernetes hostPath volume <https://kubernetes.io/docs/concepts/storage/volumes/#hostpath>`_, i.e. the file or directory of a node that will be accessible to the MongoDB Pod containers

     * - volumeSpec.hostPath.type
       - string
       - ``Directory``
       - The `Kubernetes hostPath volume type <https://kubernetes.io/docs/concepts/storage/volumes/#hostpath>`_

     * - volumeSpec.persistentVolumeClaim.storageClassName
       - string
       - ``standard``
       - The `Kubernetes Storage Class <https://kubernetes.io/docs/concepts/storage/storage-classes/>`_ to use with the MongoDB container `Persistent Volume Claim <https://kubernetes.io/docs/concepts/storage/persistent-volumes/#persistentvolumeclaims>`_

     * - volumeSpec.persistentVolumeClaim.accessModes
       - array
       - ``[ "ReadWriteOnce" ]``
       - The `Kubernetes Persistent Volume <https://kubernetes.io/docs/concepts/storage/persistent-volumes/>`_ access modes for the MongoDB container

     * - volumeSpec.resources.requests.storage
       - string
       - ``3Gi``
       - The `Kubernetes Persistent Volume <https://kubernetes.io/docs/concepts/storage/persistent-volumes/>`_ size for the MongoDB container



     * - storageClass
       - string
       -
       - Set the `Kubernetes Storage Class <https://kubernetes.io/docs/concepts/storage/storage-classes/>`_ to use with the MongoDB `Persistent Volume Claim <https://kubernetes.io/docs/concepts/storage/persistent-volumes/#persistentvolumeclaims>`_

    * - resources.limits.storage
       - string
       -
       - `Kubernetes Storage limit <https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/#resource-requests-and-limits-of-pod-and-container>`_  for `Persistent Volume Claim <https://kubernetes.io/docs/concepts/storage/persistent-volumes/#persistentvolumeclaims>`_

     * - resources.requests.cpu
       - string
       -
       - The `Kubernetes CPU requests <https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/#resource-requests-and-limits-of-pod-and-container>`_ for MongoDB container

     * - resources.requests.memory
       - string
       -
       - The `Kubernetes Memory requests <https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/#resource-requests-and-limits-of-pod-and-container>`_ for MongoDB container


PMM Section
-----------

The ``pmm`` section in the deploy/cr.yaml file contains configuration
options for Percona Monitoring and Management.

.. list-table::
      :widths: 15 15 20 60
      :header-rows: 1

      * - Key
        - Value Type
        - Example
        - Description

      * - enabled
        - boolean
        - ``false``
        - Enables or disables monitoring Percona Server for MongoDB with `PMM <https://www.percona.com/doc/percona-monitoring-and-management/index.metrics-monitor.dashboard.html>`_

      * - image
        - string
        - ``perconalab/pmm-client:1.17.1``
        - PMM Client docker image to use

      * - serverHost
        - string
        - ``monitoring-service``
        - Address of the PMM Server to collect data from the Cluster

Mongod Section
--------------

The largest section in the deploy/cr.yaml file contains the Mongod
configuration options.

.. list-table::
      :header-rows: 1

      * - Key
        - Value Type
        - Example
        - Description

      * - net.port
        - int
        - ``27017``
        - Sets the MongoDB `net.port option <https://docs.mongodb.com/manual/reference/configuration-options/#net.port>`_

      * - net.hostPort
        - int
        - ``0``
        - Sets the Kubernetes `hostPort option <https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/network-plugins/#support-hostport>`_

      * - security.redactClientLogData
        - bool
        - ``false``
        - Enables/disables `PSMDB Log Redaction <https://www.percona.com/doc/percona-server-for-mongodb/LATEST/log-redaction.html>`_

      * - security.enableEncryption
        - bool
        - ``true``
        - Enables/disables `PSMDB data at rest encryption <https://www.percona.com/doc/percona-server-for-mongodb/LATEST/data_at_rest_encryption.html>`_

      * - security.encryptionKeySecret
        - string
        - ``my-cluster-name-mongodb-encryption-key``
        - Specifies a secret object with the `encryption key <https://docs.mongodb.com/manual/tutorial/configure-encryption/#local-key-management>`_

      * - security.encryptionCipherMode
        - string
        - ``AES256-CBC``
        - Sets `PSMDB encryption cipher mode <https://docs.mongodb.com/manual/reference/program/mongod/#cmdoption-mongod-encryptionciphermode>`_

      * - setParameter.ttlMonitorSleepSecs
        - int
        - ``60``
        - Sets the PSMDB `ttlMonitorSleepSecs` option

      * - setParameter.wiredTigerConcurrentReadTransactions
        - int
        - ``128``
        - Sets the `wiredTigerConcurrentReadTransactions option <https://docs.mongodb.com/manual/reference/parameters/#param.wiredTigerConcurrentReadTransactions>`_

      * - setParameter.wiredTigerConcurrentWriteTransactions
        - int
        - ``128``
        - Sets the `wiredTigerConcurrentWriteTransactions option <https://docs.mongodb.com/manual/reference/parameters/#param.wiredTigerConcurrentWriteTransactions>`_

      * - storage.engine
        - string
        - ``wiredTiger``
        - Sets the `storage.engine option <https://docs.mongodb.com/manual/reference/configuration-options/#storage.engine>`_

      * - storage.inMemory.engineConfig.inMemorySizeRatio
        - float
        - ``0.9``
        - The ratio used to compute the `storage.engine.inMemory.inMemorySizeGb option <https://www.percona.com/doc/percona-server-for-mongodb/LATEST/inmemory.html#--inMemorySizeGB>`_

      * - storage.mmapv1.nsSize
        - int
        - ``16``
        - Sets the `storage.mmapv1.nsSize option <https://docs.mongodb.com/manual/reference/configuration-options/#storage.mmapv1.nsSize>`_

      * - storage.mmapv1.smallfiles
        - bool
        - ``false``
        - Sets the `storage.mmapv1.smallfiles option <https://docs.mongodb.com/manual/reference/configuration-options/#storage.mmapv1.smallFiles>`_

      * - storage.wiredTiger.engineConfig.cacheSizeRatio
        - float
        - ``0.5``
        - The ratio used to compute the `storage.wiredTiger.engineConfig.cacheSizeGB option <https://docs.mongodb.com/manual/reference/configuration-options/#storage.wiredTiger.engineConfig.cacheSizeGB>`_

      * - storage.wiredTiger.engineConfig.directoryForIndexes
        - bool
        - ``false``
        - Sets the `storage.wiredTiger.engineConfig.directoryForIndexes option <https://docs.mongodb.com/manual/reference/configuration-options/#storage.wiredTiger.engineConfig.directoryForIndexes>`_

      * - storage.wiredTiger.engineConfig.journalCompressor
        - string
        - ``snappy``
        - Sets the `storage.wiredTiger.engineConfig.journalCompressor option <https://docs.mongodb.com/manual/reference/configuration-options/#storage.wiredTiger.engineConfig.journalCompressor>`_

      * - storage.wiredTiger.collectionConfig.blockCompressor
        - string
        - ``snappy``
        - Sets the `storage.wiredTiger.collectionConfig.blockCompressor option <https://docs.mongodb.com/manual/reference/configuration-options/#storage.wiredTiger.collectionConfig.blockCompressor>`_

      * - storage.wiredTiger.indexConfig.prefixCompression
        - bool
        - ``true``
        - Sets the `storage.wiredTiger.indexConfig.prefixCompression option <https://docs.mongodb.com/manual/reference/configuration-options/#storage.wiredTiger.indexConfig.prefixCompression>`_

      * - operationProfiling.mode
        - string
        - ``slowOp``
        - Sets the `operationProfiling.mode option <https://docs.mongodb.com/manual/reference/configuration-options/#operationProfiling.mode>`_

      * - operationProfiling.slowOpThresholdMs
        - int
        - ``100``
        - Sets the `operationProfiling.slowOpThresholdMs <https://docs.mongodb.com/manual/reference/configuration-options/#operationProfiling.slowOpThresholdMs>`_ option

      * - operationProfiling.rateLimit
        - int
        - ``1``
        - Sets the `operationProfiling.rateLimit option <https://www.percona.com/doc/percona-server-for-mongodb/LATEST/rate-limit.html>`_

      * - auditLog.destination
        - string
        -
        - Sets the `auditLog.destination option <https://www.percona.com/doc/percona-server-for-mongodb/LATEST/audit-logging.html>`_

      * - auditLog.format
        - string
        - ``BSON``
        - Sets the `auditLog.format option <https://www.percona.com/doc/percona-server-for-mongodb/LATEST/audit-logging.html>`_

      * - auditLog.filter
        - string
        - ``{}``
        - Sets the `auditLog.filter option <https://www.percona.com/doc/percona-server-for-mongodb/LATEST/audit-logging.html>`_


backup section
--------------

The ``backup`` section in the
`deploy/cr.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/cr.yaml>`__
file contains the following configuration options for the regular
Percona Server for MongoDB backups.

.. list-table::
      :header-rows: 1

      * - Key
        - Value Type
        - Example
        - Description

      * - enabled
        - boolean
        - ``true``
        - Enables or disables making backups

      * - debug
        - boolean
        - ``true``
        - Enables or disables debug mode for bakups

      * - restartOnFailure
        - boolean
        - ``true``
        - Enables or disables restarting the previously failed backup process

      * - image
        - string
        - ``percona/percona-server-mongodb-operator:1.2.0-backup``
        - The Percona Server for MongoDB Docker image to use for the backup
  
      * - serviceAccountName
        - string
        - ``percona-server-mongodb-operator``
        - name of the separate privileged service account used by the Operator

      * - coordinator.enableClientsLogging
        - boolean
        - ``true``
        - Enables or disables backups-related client logging

      * - coordinator.resources.limits.cpu
        - string
        - ``100m``
        - `Kubernetes CPU limit <https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/#resource-requests-and-limits-of-pod-and-container>`_ for the MongoDB Coordinator container

      * - coordinator.resources.limits.memory
        - string
        - ``0.2G``
        - `Kubernetes Memory limit <https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/#resource-requests-and-limits-of-pod-and-container>`__ for the MongoDB Coordinator container

     * - coordinator.resources.requests.cpu
       - string
       - ``100m``
       - The `Kubernetes CPU requests <https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/#resource-requests-and-limits-of-pod-and-container>`_ for the MongoDB Coordinator container

     * - coordinator.resources.requests.memory
       - string
       - ``0.1G``
       - The `Kubernetes Memory requests <https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/#resource-requests-and-limits-of-pod-and-container>`_ for the MongoDB Coordinator container

      * - coordinator.resources.requests.storage
        - string
        - ``1Gi``
        - The `Kubernetes Persistent Volume <https://kubernetes.io/docs/concepts/storage/persistent-volumes/>`_ size for the MongoDB Coordinator container

      * - coordinator.storageClass
        - string
        - ``aws-gp2``
        - Sets the `Kubernetes Storage Class <https://kubernetes.io/docs/concepts/storage/storage-classes/>`_ to use with the MongoDB Coordinator container

      * - coordinator.afinity.antiAffinityTopologyKey
        - string
        - ``kubernetes.io/hostname``
        - The `Kubernetes topologyKey <https://kubernetes.io/docs/concepts/configuration/assign-pod-node/#inter-pod-affinity-and-anti-affinity-beta-feature>`_  node affinity constraint for the backups

      * - coordinator.affinity.advanced
        - subdoc
        -
        - In cases where the pods require complex tuning the `advanced` option turns off the `topologykey` effect. This setting allows the standard Kubernetes affinity constraints of any complexity to be used

      * - coordinator.tolerations.key
        - string
        - ``node.alpha.kubernetes.io/unreachable``
        - The `Kubernetes Pod tolerations <https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts>`_ key for the backups nodes

      * - coordinator.tolerations.operator
        - string
        - ``Exists``
        - The `Kubernetes Pod tolerations <https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts>`_ operator for the backups nodes

      * - coordinator.tolerations.effect
        - string
        - ``NoExecute``
        - The `Kubernetes Pod tolerations <https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts>`_ effect for the backups nodes

      * - coordinator.tolerations.tolerationSeconds
        - int
        - ``6000``
        - The `Kubernetes Pod tolerations <https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts>`_ time limit for the backups nodes

      * - coordinator.priorityClassName
        - string
        - ``high priority``
        - The `Kuberentes Pod priority class <https://kubernetes.io/docs/concepts/configuration/pod-priority-preemption/#priorityclass>`_  for the backups nodes

      * - coordinator.annotations.iam.amazonaws.com/role
        - string
        - ``role-arn``
        - The `AWS IAM role <https://kubernetes-on-aws.readthedocs.io/en/latest/user-guide/iam-roles.html>`_  for the backup storage nodes

      * - coordinator.labels
        - label
        - ``rack: rack-22``
        - The `Kubernetes affinity labels <https://kubernetes.io/docs/concepts/configuration/assign-pod-node/>`_  for the backup storage nodes

      * - coordinator.nodeSelector
        - label
        - ``disktype: ssd``
        - The `Kubernetes nodeSelector <https://kubernetes.io/docs/concepts/configuration/assign-pod-node/#nodeselector>`_ affinity constraint  for the backup storage nodes

      * - tasks.name
        - string
        - ``sat-night-backup``
        - The backup name

      * - tasks.enabled
        - boolean
        - ``true``
        - Enables or disables this exact backup

      * - tasks.schedule
        - string
        - ``0 0 * * 6``
        - The scheduled time to make a backup, specified in the `crontab format <https://en.wikipedia.org/wiki/Cron>`_

      * - tasks.storageName
        - string
        - ``st-us-west``
        - The name of the S3-compatible storage for backups, configured in the `storages` subsection

      * - tasks.compressionType
        - string
        - ``gzip``
        - The backup compression format




