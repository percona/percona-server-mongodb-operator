Custom Resource options
=======================

The operator is configured via the spec section of the
`deploy/cr.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/cr.yaml>`__
file. This file contains the following spec sections:

  .. list-table::
      :widths: 20 20 20
      :header-rows: 1

      * - Key
        - Value
        - Default
      * - platform
        - string
        - ``kubernetes``
      * - version
        - string
        - ``3.6.8``
      * - section
        - subdoc
        -

``platform`` description:
Override/set the Kubernetes platform: *kubernetes* or *openshift*. Set openshift on OpenShift 3.11+

``version`` description:
The Dockerhub tag of `percona/percona-server-mongodb <https://hub.docker.com/r/perconalab/percona-server-mongodb-operator/tags/>`__ to deploy

``section`` description:
Operator secrets section

Secrets section
---------------

Each spec in its turn may contain some key-value pairs. The secrets one
has only two of them:

  .. list-table::
      :widths: 20 20 20
      :header-rows: 1

      * - Key
        - Value Type
        - Example
      * - key
        - string
        - ``my-cluster-name-mongodb-key``
      * - users
        - string
        - ``my-cluster-name-mongodb-users``

``key`` description:
The secret name for the `MongoDB Internal Auth Key <https://docs.mongodb.com/manual/core/security-internal-authentication/>`__. *This secret is auto-created by the operator if it doesn’t exist.*

``users`` description:
The secret name for the MongoDB users required to run the operator. This secret is required to run the operator.


  Replsets section
  ----------------

  The replsets section controls the MongoDB Replica Set.

  .. list-table::
      :header-rows: 1
      :widths: 10 10 10


      * - Key
        - Value Type
        - Example
      * - name
        - string
        - ``rs 0``
      * - size
        - int
        - 3
      * - storageClass
        - string
        -
      * - arbiter.enabled
        - boolean
        - ``f``
      * - arbiter.size
        - int
        -
      * - arbiter.afinity.antiAffinityTopologyKey
        - string
        - ``kubernetes.io/hostname``
      * - arbiter.tolerations.key
        - string
        - ``node.alpha.kubernetes.io/unreachable``
      * - arbiter.tolerations.operator
        - string
        - ``Exists``
      * - arbiter.tolerations.effect
        - string
        - ``NoExecute``
      * - arbiter.tolerations.tolerationSeconds
        - int
        - ``6000``
      * - arbiter.priorityClassName
        - string
        - ``high priority``
      * - arbiter.annotations.iam.amazonaws.com/role
        - string
        - ``role-arn``
      * - arbiter.labels
        - label
        - ``rack: rack-22``
      * - arbiter.nodeSelector
        - label
        - ``disktype:ssd``
      * - expose.enabled
        - boolean
        - ``false``
      * - expose.exposeType
        - string
        - ``ClusterIP``
      * - resources.limits.cpu
        - string
        -
      * - resources.limits.memory
        - string
        -
      * - resources.limits.storage
        - string
        -
      * - resources.requests.cpu
        - string
        -
      * - resources.requests.memory
        - string
        -
      * - volumeSpec.emptyDir
        - string
        - ``{}``
      * - volumeSpec.hostPath.path
        - string
        - ``/data``
      * - volumeSpec.hostPath.type
        - string
        - ``Directory``
      * - volumeSpec.persistentVolumeClaim.storageClassName
        - string
        - ``standard``
      * - volumeSpec.persistentVolumeClaim.accessModes
        - array
        - ``[ "ReadWriteOnce" ]``
      * - volumeSpec.resources.requests.storage
        - string
        - ``3Gi``
      * - affinity.antiAffinityTopologyKey
        - string
        - ``kubernetes.io/hostname``
      * - tolerations.key
        - string
        - ``node.alpha.kubernetes.io/unreachable``
      * - tolerations.operator
        - string
        - ``Exists``
      * - tolerations.effect
        - string
        - ``NoExecute``
      * - tolerations.tolerationSeconds
        - int
        - ``6000``
      * - annotations.iam.amazonaws.com/role
        - string
        - ``role-arn``
      * - labels
        - label
        - ``rack: rack-22``
      * - nodeSelector
        - label
        - ``disktype: ssd``
      * - podDisruptionBudget.maxUnavailable
        - int
        - ``1``
      * - podDisruptionBudget.minAvailable
        - int
        - ``1``

``name`` description:
The name of the `MongoDB Replica Set <https://docs.mongodb.com/manual/replication/>`_

``size`` description:
The size of the MongoDB Replica Set, must be >= 3 for `High-Availability <https://docs.mongodb.com/manual/replication/#redundancy-and-data-availability>`_

``storageclass`` description:
Set the `Kubernetes Storage Class <https://kubernetes.io/docs/concepts/storage/storage-classes/>`_ to use with the MongoDB `Persistent Volume Claim <https://kubernetes.io/docs/concepts/storage/persistent-volumes/#persistentvolumeclaims>`__

``arbiter.enabled`` description:
Enable or disable creation of `Replica Set Arbiter <https://docs.mongodb.com/manual/core/replica-set-arbiter/>`__ nodes within the cluster

``arbiter.size`` description:
The number of `Replica Set Arbiter <https://docs.mongodb.com/manual/core/replica-set-arbiter/>`__  nodes within the cluster

``arbiter.afinity.antiAffinityTopologyKey`` description:
The `Kubernetes topologyKey <https://kubernetes.io/docs/concepts/configuration/assign-pod-node/#inter-pod-affinity-and-anti-affinity-beta-feature>`__  node affinity constraint for the Arbiter

``arbiter.tolerations.key`` description:
The `Kubernetes Pod tolerations <https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts>`_ key for the Arbiter nodes

``arbiter.tolerations.operator`` description:
The `Kubernetes Pod tolerations <https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts>`__ operator for the Arbiter nodes

``arbiter.tolerations.effect`` description:
The `Kubernetes Pod tolerations <https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts>`_ effect for the Arbiter nodes

``arbiter.tolerations.tolerationSeconds`` description:
The `Kubernetes Pod tolerations <https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts>`_ time limit for the Arbiter nodes

``arbiter.priorityClassName`` description:
The `Kuberentes Pod priority class <https://kubernetes.io/docs/concepts/configuration/pod-priority-preemption/#priorityclass>`_  for the Arbiter nodes

``arbiter.annotations.iam.amazonaws.com/role`` description:
The `AWS IAM role <https://kubernetes-on-aws.readthedocs.io/en/latest/user-guide/iam-roles.html>`__ for the Arbiter nodes

``arbiter.labels`` description:
The `Kubernetes affinity labels <https://kubernetes.io/docs/concepts/configuration/assign-pod-node/>`__ for the Arbiter nodes

``arbiter.nodeSelector`` description:
The `Kubernetes nodeSelector <https://kubernetes.io/docs/concepts/configuration/assign-pod-node/#nodeselector>`__ affinity constraint for the Arbiter nodes

``expose.enabled`` description:
Enable or disable exposing `MongoDB Replica Set <https://docs.mongodb.com/manual/replication/>`__ nodes with dedicated IP addresses

``expose.exposeType`` description:
the `IP address type <./expose>`__ to be exposed

``resources.limits.cpu`` description:
`Kubernetes CPU limit <https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/#resource-requests-and-limits-of-pod-and-container>`__ for MongoDB container

``resources.limits.memory`` description:
`Kubernetes Memory limit <https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/#resource-requests-and-limits-of-pod-and-container>`__ for MongoDB container

``resources.limits.storage`` description:
`Kubernetes Storage limit <https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/#resource-requests-and-limits-of-pod-and-container>`__  for `Persistent Volume Claim <https://kubernetes.io/docs/concepts/storage/persistent-volumes/#persistentvolumeclaims>`__

``resources.requests.cpu`` description:
The `Kubernetes CPU requests <https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/#resource-requests-and-limits-of-pod-and-container>`_ for MongoDB container

``resources.requests.memory`` description:
The `Kubernetes Memory requests <https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/#resource-requests-and-limits-of-pod-and-container>`_ for MongoDB container

``volumeSpec.emptyDir`` description:
The `Kubernetes emptyDir volume <https://kubernetes.io/docs/concepts/storage/volumes/#emptydir>`_, i.e. the directory which will be created on a node, and will be accessible to the MongoDB Pod containers

``volumeSpec.hostPath.path`` description:
`Kubernetes hostPath volume <https://kubernetes.io/docs/concepts/storage/volumes/#hostpath>`_, i.e. the file or directory of a node that will be accessible to the MongoDB Pod containers

``volumeSpec.hostPath.type`` description:
The `Kubernetes hostPath volume type <https://kubernetes.io/docs/concepts/storage/volumes/#hostpath>`_

``volumeSpec.persistentVolumeClaim.storageClassName`` description:
The `Kubernetes Storage Class <https://kubernetes.io/docs/concepts/storage/storage-classes/>`_ to use with the MongoDB container `Persistent Volume Claim <https://kubernetes.io/docs/concepts/storage/persistent-volumes/#persistentvolumeclaims>`__

``volumeSpec.persistentVolumeClaim.accessModes`` description:
The `Kubernetes Persistent Volume <https://kubernetes.io/docs/concepts/storage/persistent-volumes/>`_ access modes for the MongoDB container

``volumeSpec.resources.requests.storage`` description:
The `Kubernetes Persistent Volume <https://kubernetes.io/docs/concepts/storage/persistent-volumes/>`_ size for the MongoDB container

``affinity.antiAffinityTopologyKey`` description:
The `Kubernetes topologyKey <https://kubernetes.io/docs/concepts/configuration/assign-pod-node/#inter-pod-affinity-and-anti-affinity-beta-feature>`_ node affinity constraint for the Replica Set nodes

``tolerations.key`` description:
The `Kubernetes Pod tolerations <https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts>`_ key for the Replica Set nodes

``tolerations.operator`` description:
The `Kubernetes Pod tolerations <https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts>`_ operator  for the Replica Set nodes

``tolerations.effect`` description:
The `Kubernetes Pod tolerations <https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts>`_ effect  for the Replica Set nodes

``tolerations.tolerationSeconds`` description:
The `Kubernetes Pod tolerations <https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts>`_ time limit  for the Replica Set nodes

``annotations.iam.amazonaws.com/role`` description:
The `AWS IAM role <https://kubernetes-on-aws.readthedocs.io/en/latest/user-guide/iam-roles.html>`_  for the Replica Set nodes

``labels`` description:
The `Kubernetes affinity labels <https://kubernetes.io/docs/concepts/configuration/assign-pod-node/>`_  for the Replica Set nodes

``nodeSelector`` description:
The `Kubernetes nodeSelector <https://kubernetes.io/docs/concepts/configuration/assign-pod-node/#nodeselector>`_ affinity constraint  for the Replica Set nodes

``podDisruptionBudget.maxUnavailable`` description:
The `Kubernetes Pod distribution budget <https://kubernetes.io/docs/concepts/workloads/pods/disruptions/>`_ limit specifying the maximum value for unavailable Pods

``podDisruptionBudget.minAvailable``` description:
The `Kubernetes Pod distribution budget <https://kubernetes.io/docs/concepts/workloads/pods/disruptions/>`_ limit specifying the minimum value for available Pods


PMM Section
-----------

The ``pmm`` section in the deploy/cr.yaml file contains configuration
options for Percona Monitoring and Management.

  .. list-table::
      :widths: 15 15 20
      :header-rows: 1

      * - Key
        - Value Type
        - Example
      * - enabled
        - boolean
        - ``false``
      * - image
        - string
        - ``perconalab/pmm-client:1.17.1``
      * - serverHost
        - string
        - ``monitoring-service``

``enabled`` description:
Enables or disables monitoring Percona Server for MongoDB with `PMM <https://www.percona.com/doc/percona-monitoring-and-management/index.metrics-monitor.dashboard.html>`_

``image`` description:
PMM Client docker image to use

``serverhost`` description:
Address of the PMM Server to collect data from the Cluster



Mongod Section
--------------

The largest section in the deploy/cr.yaml file contains the Mongod
configuration options.

  .. list-table::
      :header-rows: 1

      * - Key
        - Value Type
        - Example
      * - net.port
        - int
        - ``27017``
      * - net.hostPort
        - int
        - ``0``
      * - security.redactClientLogData
        - bool
        - ``false``
      * - setParameter.ttlMonitorSleepSecs
        - int
        - ``60``
      * - setParameter.wiredTigerConcurrentReadTransactions
        - int
        - ``128``
      * - setParameter.wiredTigerConcurrentWriteTransactions
        - int
        - ``128``
      * - storage.engine
        - string
        - ``wiredTiger``
      * - storage.inMemory.inMemorySizeRatio
        - float
        - ``0.9``
      * - storage.mmapv1.nsSize
        - int
        - ``16``
      * - storage.mmapv1.smallfiles
        - bool
        - ``false``
      * - storage.wiredTiger.engineConfig.cacheSizeRatio
        - float
        - ``0.5``
      * - storage.wiredTiger.engineConfig.directoryForIndexes
        - bool
        - ``false``
      * - storage.wiredTiger.engineConfig.journalCompressor
        - string
        - ``snappy``
      * - storage.wiredTiger.collectionConfig.blockCompressor
        - string
        - ``snappy``
      * - storage.wiredTiger.indexConfig.prefixCompression
        - bool
        - ``true``
      * - operationProfiling.mode
        - string
        - ``slowOp``
      * - operationProfiling.slowOpThresholdMs
        - int
        - ``100``
      * - operationProfiling.rateLimit
        - int
        - ``1``
      * - auditLog.destination
        - string
        -
      * - auditLog.format
        - string
        - ``BSON``

``net.port`` description:
Sets the MongoDB `net.port option <https://docs.mongodb.com/manual/reference/configuration-options/#net.port>`_

``net.hostPort`` description:
Sets the Kubernetes `hostPort option <https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/network-plugins/#support-hostport>`__

``security.redactClientLogData`` description:
Enables/disables `PSMDB Log Redaction <https://www.percona.com/doc/percona-server-for-mongodb/LATEST/log-redaction.html>`_

``setParameter.ttlMonitorSleepSecs`` description:
Sets the PSMDB `ttlMonitorSleepSecs` option

``setParameter.wiredTigerConcurrentReadTransactions`` description:
Sets the `wiredTigerConcurrentReadTransactions option <https://docs.mongodb.com/manual/reference/parameters/#param.wiredTigerConcurrentReadTransactions>`_

``setParameter.wiredTigerConcurrentWriteTransactions`` description:
Sets the `wiredTigerConcurrentWriteTransactions option <https://docs.mongodb.com/manual/reference/parameters/#param.wiredTigerConcurrentWriteTransactions>`_

``storage.engine`` description:
Sets the `storage.engine option <https://docs.mongodb.com/manual/reference/configuration-options/#storage.engine>`_

``storage.inMemory.inMemorySizeRatio`` description:
The ratio used to compute the `storage.engine.inMemory.inMemorySizeGb option <https://www.percona.com/doc/percona-server-for-mongodb/LATEST/inmemory.html#--inMemorySizeGB>`__

``storage.mmapv1.nsSize`` description:
Sets the `storage.mmapv1.nsSize option <https://docs.mongodb.com/manual/reference/configuration-options/#storage.mmapv1.nsSize>`__

``storage.mmapv1.smallfiles`` description:
Sets the `storage.mmapv1.smallfiles option <https://docs.mongodb.com/manual/reference/configuration-options/#storage.mmapv1.smallFiles>`_

``storage.wiredTiger.engineConfig.cacheSizeRatio`` description:
The ratio used to compute the `storage.wiredTiger.engineConfig.cacheSizeGB option <https://docs.mongodb.com/manual/reference/configuration-options/#storage.wiredTiger.engineConfig.cacheSizeGB>`__

``storage.wiredTiger.engineConfig.directoryForIndexes`` description:
Sets the `storage.wiredTiger.engineConfig.directoryForIndexes option <https://docs.mongodb.com/manual/reference/configuration-options/#storage.wiredTiger.engineConfig.directoryForIndexes>`_

``storage.wiredTiger.engineConfig.journalCompressor`` description:
Sets the `storage.wiredTiger.engineConfig.journalCompressor option <https://docs.mongodb.com/manual/reference/configuration-options/#storage.wiredTiger.engineConfig.journalCompressor>`_

``storage.wiredTiger.collectionConfig.blockCompressor`` description:
Sets the `storage.wiredTiger.collectionConfig.blockCompressor option <https://docs.mongodb.com/manual/reference/configuration-options/#storage.wiredTiger.collectionConfig.blockCompressor>`_

``storage.wiredTiger.indexConfig.prefixCompression`` description:
Sets the `storage.wiredTiger.indexConfig.prefixCompression option <https://docs.mongodb.com/manual/reference/configuration-options/#storage.wiredTiger.indexConfig.prefixCompression>`_

``operationProfiling.mode`` description:
Sets the `operationProfiling.mode option <https://docs.mongodb.com/manual/reference/configuration-options/#operationProfiling.mode>`_

``operationProfiling.slowOpThresholdMs`` description:
Sets the `operationProfiling.slowOpThresholdMs <https://docs.mongodb.com/manual/reference/configuration-options/#operationProfiling.slowOpThresholdMs>`_ option

``operationProfiling.rateLimit`` description:
Sets the `operationProfiling.rateLimit option <https://www.percona.com/doc/percona-server-for-mongodb/LATEST/rate-limit.html>`_

``auditLog.destination`` description:
Sets the `auditLog.destination option <https://www.percona.com/doc/percona-server-for-mongodb/LATEST/audit-logging.html>`_

``auditLog.format`` description:
Sets the `auditLog.format option <https://www.percona.com/doc/percona-server-for-mongodb/LATEST/audit-logging.html>`_

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
      * - annotations.iam.amazonaws.com/role
        - string
        - ``role-arn``
      * - labels
        - label
        - ``rack: rack-22``
      * - nodeSelector
        - label
        - ``disktype: ssd``
      * - coordinator.requests.storage
        - string
        - ``1Gi``
      * - coordinator.requests.storageClass
        - string
        - ``aws-gp2``
      * - coordinator.debug
        - string
        - ``false``
      * - tasks.name
        - string
        - ``sat-night-backup``
      * - tasks.enabled
        - boolean
        - ``true``
      * - tasks.schedule
        - string
        - ``0 0 * * 6``
      * - tasks.storageName
        - string
        - ``st-us-west``
      * - tasks.compressionType
        - string
        - ``gzip``

``annotations.iam.amazonaws.com/role`` description:
The `AWS IAM role <https://kubernetes-on-aws.readthedocs.io/en/latest/user-guide/iam-roles.html>`_  for the backup storage nodes

``labels`` description:
The `Kubernetes affinity labels <https://kubernetes.io/docs/concepts/configuration/assign-pod-node/>`_  for the backup storage nodes

``nodeSelector`` description:
The `Kubernetes nodeSelector <https://kubernetes.io/docs/concepts/configuration/assign-pod-node/#nodeselector>`_ affinity constraint  for the backup storage nodes

``coordinator.requests.storage`` description:
The `Kubernetes Persistent Volume <https://kubernetes.io/docs/concepts/storage/persistent-volumes/>`_ size for the MongoDB Coordinator container

``coordinator.requests.storageClass`` description:
Sets the `Kubernetes Storage Class <https://kubernetes.io/docs/concepts/storage/storage-classes/>`_ to use with the MongoDB Coordinator container

``coordinator.debug`` description:
Enables or disables debug mode for the MongoDB Coordinator operation

``tasks.name`` description:
The backup name

``tasks.enabled`` description:
Enables or disables this exact backup

``tasks.schedule`` description:
The scheduled time to make a backup, specified in the `crontab format <https://en.wikipedia.org/wiki/Cron>`_

``tasks.storageName`` description:
The name of the S3-compatible storage for backups, configured in the `storages` subsection

``tasks.compressionType`` description: 
The backup compression format
