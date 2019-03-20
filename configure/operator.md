Custom Resource options
==============================================================

The operator is configured via the spec section of the [deploy/cr.yaml](https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/cr.yaml) file. This file contains the following spec sections: 

| Key | Value Type | Default | Description                                      |
|-----|------------|---------|--------------------------------------------------|
|platform | string | kubernetes | Override/set the Kubernetes platform: *kubernetes* or *openshift*. Set openshift on OpenShift 3.11+ |
| version | string | 3.6.8      | The Dockerhub tag of [percona/percona-server-mongodb](https://hub.docker.com/r/perconalab/percona-server-mongodb-operator/tags/) to deploy |
| secrets | subdoc |            | Operator secrets section                      |
|replsets | array  |            | Operator MongoDB Replica Set section          |
| pmm     | subdoc |            | Percona Monitoring and Management section     |
| mongod  | subdoc |            | Operator MongoDB Mongod configuration section |
| backup  | subdoc |            | Percona Server for MongoDB backups section    |


### Secrets section

Each spec in its turn may contain some key-value pairs. The secrets one has only two of them:

| Key | Value Type | Example | Description |
|-----|------------|---------|-------------|
|key  | string     | my-cluster-name-mongodb-key   | The secret name for the [MongoDB Internal Auth Key](https://docs.mongodb.com/manual/core/security-internal-authentication/). This secret is auto-created by the operator if it doesn't exist |
|users| string     | my-cluster-name-mongodb-users | The secret name for the MongoDB users required to run the operator. **This secret is required to run the operator!** |

### Replsets section

The replsets section controls the MongoDB Replica Set. 

| Key                     | Value Type | Example | Description                                                                         |
|-------------------------|------------|---------|-------------------------------------------------------------------------------------|
|name                     | string     | rs0     | The name of the [MongoDB Replica Set](https://docs.mongodb.com/manual/replication/) |
|size                     | int        | 3       | The size of the MongoDB Replica Set, must be >= 3 for [High-Availability](https://docs.mongodb.com/manual/replication/#redundancy-and-data-availability) |
|storageClass             | string     |         | Set the [Kubernetes Storage Class](https://kubernetes.io/docs/concepts/storage/storage-classes/) to use with the MongoDB [Persistent Volume Claim](https://kubernetes.io/docs/concepts/storage/persistent-volumes/#persistentvolumeclaims) |
|arbiter.enabled          | boolean    | false   | Enable or disable creation of [Replica Set Arbiter](https://docs.mongodb.com/manual/core/replica-set-arbiter/) nodes within the cluster         |
|arbiter.size             | int        |         | The number of [Replica Set Arbiter](https://docs.mongodb.com/manual/core/replica-set-arbiter/) nodes within the cluster          |
|arbiter.affinity.antiAffinityTopologyKey|string|`kubernetes.io/hostname`| The [Kubernetes topologyKey](https://kubernetes.io/docs/concepts/configuration/assign-pod-node/#inter-pod-affinity-and-anti-affinity-beta-feature) node affinity constraint for the Arbiter |
|arbiter.tolerations.key                 | string     | `node.alpha.kubernetes.io/unreachable` | The [Kubernetes Pod tolerations] (https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts) key  for the Arbiter nodes|
|arbiter.tolerations.operator            | string     | `Exists`  | The [Kubernetes Pod tolerations] (https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts) operator  for the Arbiter nodes|
|arbiter.tolerations.effect              | string     |`NoExecute`| The [Kubernetes Pod tolerations] (https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts) effect for the Arbiter nodes|
|arbiter.tolerations.tolerationSeconds   | int | `6000`    | The [Kubernetes Pod tolerations] (https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts) time limit  for the Arbiter nodes|
|arbiter.priorityClassName               | string     | `high-priority`  | The [Kuberentes Pod priority class](https://kubernetes.io/docs/concepts/configuration/pod-priority-preemption/#priorityclass)  for the Arbiter nodes|
|arbiter.annotations.iam.amazonaws.com/role | string |`role-arn`| The [AWS IAM role](https://kubernetes-on-aws.readthedocs.io/en/latest/user-guide/iam-roles.html)  for the Arbiter nodes                             |
|arbiter.labels                          | label      | `rack: rack-22` | The [Kubernetes affinity labels](https://kubernetes.io/docs/concepts/configuration/assign-pod-node/)  for the Arbiter nodes                      |
|arbiter.nodeSelector                    | label      | `disktype: ssd`        | The [Kubernetes nodeSelector](https://kubernetes.io/docs/concepts/configuration/assign-pod-node/#nodeselector) affinity constraint  for the Arbiter nodes|
|expose.enabled           | boolean    | false   | Enable or disable exposing [MongoDB Replica Set](https://docs.mongodb.com/manual/replication/) nodes with dedicated IP addresses         |
|expose.exposeType        | string     |ClusterIP| the [IP address type](./expose) to be exposed         |
|resources.limits.cpu     | string     |         |[Kubernetes CPU limit](https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/#resource-requests-and-limits-of-pod-and-container) for MongoDB container|
|resources.limits.memory  | string     |         | [Kubernetes Memory limit](https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/#resource-requests-and-limits-of-pod-and-container) for MongoDB container|
|resources.limits.storage | string     |         | [Kubernetes Storage limit](https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/#resource-requests-and-limits-of-pod-and-container) for [Persistent Volume Claim](https://kubernetes.io/docs/concepts/storage/persistent-volumes/#persistentvolumeclaims) |
|resources.requests.cpu   | string     |         | [Kubernetes CPU requests](https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/#resource-requests-and-limits-of-pod-and-container) for MongoDB container|
|resources.requests.memory| string     |         | [Kubernetes Memory requests](https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/#resource-requests-and-limits-of-pod-and-container) for MongoDB container|
|volumeSpec.emptyDir      | string     | `{}`    | [Kubernetes emptyDir volume](https://kubernetes.io/docs/concepts/storage/volumes/#emptydir), i.e. the directory which will be created on a node, and will be accessible to the MongoDB Pod containers|
|volumeSpec.hostPath.path | string     | `/data` | [Kubernetes hostPath volume](https://kubernetes.io/docs/concepts/storage/volumes/#hostpath), i.e. the file or directory of a node that will be accessible to the MongoDB Pod containers|
|volumeSpec.hostPath.type | string     |`Directory`| The [Kubernetes hostPath volume type](https://kubernetes.io/docs/concepts/storage/volumes/#hostpath) |
|affinity.antiAffinityTopologyKey|string|`kubernetes.io/hostname`| The [Kubernetes topologyKey](https://kubernetes.io/docs/concepts/configuration/assign-pod-node/#inter-pod-affinity-and-anti-affinity-beta-feature) node affinity constraint for the Replica Set nodes|
|tolerations.key                 | string     | `node.alpha.kubernetes.io/unreachable` | The [Kubernetes Pod tolerations] (https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts) key for the Replica Set nodes |
|tolerations.operator            | string     | `Exists`  | The [Kubernetes Pod tolerations] (https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts) operator  for the Replica Set nodes          |
|tolerations.effect              | string     |`NoExecute`| The [Kubernetes Pod tolerations] (https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts) effect  for the Replica Set nodes            |
|tolerations.tolerationSeconds   | int | `6000`    | The [Kubernetes Pod tolerations] (https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts) time limit  for the Replica Set nodes        |
|priorityClassName               | string     | `high-priority`  | The [Kuberentes Pod priority class](https://kubernetes.io/docs/concepts/configuration/pod-priority-preemption/#priorityclass)  for the Replica Set nodes|
|annotations.iam.amazonaws.com/role | string |`role-arn`| The [AWS IAM role](https://kubernetes-on-aws.readthedocs.io/en/latest/user-guide/iam-roles.html)  for the Replica Set nodes                             |
|labels                          | label      | `rack: rack-22` | The [Kubernetes affinity labels](https://kubernetes.io/docs/concepts/configuration/assign-pod-node/)  for the Replica Set nodes                      |
|nodeSelector                    | label      | `disktype: ssd`        | The [Kubernetes nodeSelector](https://kubernetes.io/docs/concepts/configuration/assign-pod-node/#nodeselector) affinity constraint  for the Replica Set nodes|
|podDisruptionBudget.maxUnavailable | int     | 1 | The [Kubernetes Pod distribution budget](https://kubernetes.io/docs/concepts/workloads/pods/disruptions/) limit specifying the maximum value for unavailable Pods              |
|podDisruptionBudget.minAvailable | int     | 1 | The [Kubernetes Pod distribution budget](https://kubernetes.io/docs/concepts/workloads/pods/disruptions/) limit specifying the minimum value for available Pods           |

### PMM Section

The ``pmm`` section in the deploy/cr.yaml file contains configuration options for Percona Monitoring and Management.

| Key       | Value Type | Example               | Description                    |
|-----------|------------|-----------------------|--------------------------------|
|enabled    | boolean    | `false`               | Enables or disables monitoring Percona Server for MongoDB with [PMM](https://www.percona.com/doc/percona-monitoring-and-management/index.metrics-monitor.dashboard.html) |
|image      | string     |`perconalab/pmm-client:1.17.1`| PMM Client docker image to use |
|serverHost | string     | `monitoring-service`  | Address of the PMM Server to collect data from the Cluster |

### Mongod Section

The largest section in the deploy/cr.yaml file contains the Mongod configuration options.

| Key | Value Type | Example | Description |
|-----|------------|---------|-------------|
|net.port |       int | 27017    | Sets the MongoDB ['net.port' option](https://docs.mongodb.com/manual/reference/configuration-options/#net.port)    |
|net.hostPort|    int | 0        | Sets the Kubernetes ['hostPort' option](https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/network-plugins/#support-hostport) |
security.redactClientLogData|bool|false|Enables/disables [PSMDB Log Redaction](https://www.percona.com/doc/percona-server-for-mongodb/LATEST/log-redaction.html)|
|setParameter.ttlMonitorSleepSecs|int|60|Sets the PSMDB 'ttlMonitorSleepSecs' option|
|setParameter.wiredTigerConcurrentReadTransactions| int|128|Sets the ['wiredTigerConcurrentReadTransactions' option](https://docs.mongodb.com/manual/reference/parameters/#param.wiredTigerConcurrentReadTransactions) |
|setParameter.wiredTigerConcurrentWriteTransactions|int|128|Sets the ['wiredTigerConcurrentWriteTransactions' option](https://docs.mongodb.com/manual/reference/parameters/#param.wiredTigerConcurrentWriteTransactions)|
|storage.engine|string|wiredTiger| Sets the ['storage.engine' option](https://docs.mongodb.com/manual/reference/configuration-options/#storage.engine)|
|storage.inMemory.inMemorySizeRatio|float|0.9|Ratio used to compute the ['storage.engine.inMemory.inMemorySizeGb' option](https://www.percona.com/doc/percona-server-for-mongodb/LATEST/inmemory.html#--inMemorySizeGB)|
|storage.mmapv1.nsSize|int|16    | Sets the ['storage.mmapv1.nsSize' option](https://docs.mongodb.com/manual/reference/configuration-options/#storage.mmapv1.nsSize)|
|storage.mmapv1.smallfiles|bool|false| Sets the ['storage.mmapv1.smallfiles' option](https://docs.mongodb.com/manual/reference/configuration-options/#storage.mmapv1.smallFiles) |
|storage.wiredTiger.engineConfig.cacheSizeRatio|float|0.5|Ratio used to compute the ['storage.wiredTiger.engineConfig.cacheSizeGB' option](https://docs.mongodb.com/manual/reference/configuration-options/#storage.wiredTiger.engineConfig.cacheSizeGB) |
|storage.wiredTiger.engineConfig.directoryForIndexes|bool|false|Sets the ['storage.wiredTiger.engineConfig.directoryForIndexes' option](https://docs.mongodb.com/manual/reference/configuration-options/#storage.wiredTiger.engineConfig.directoryForIndexes)|
|storage.wiredTiger.engineConfig.journalCompressor|string|snappy|Sets the ['storage.wiredTiger.engineConfig.journalCompressor' option](https://docs.mongodb.com/manual/reference/configuration-options/#storage.wiredTiger.engineConfig.journalCompressor)|
|storage.wiredTiger.collectionConfig.blockCompressor|string|snappy|Sets the ['storage.wiredTiger.collectionConfig.blockCompressor' option](https://docs.mongodb.com/manual/reference/configuration-options/#storage.wiredTiger.collectionConfig.blockCompressor)|
|storage.wiredTiger.indexConfig.prefixCompression|bool|true|Sets the ['storage.wiredTiger.indexConfig.prefixCompression' option](https://docs.mongodb.com/manual/reference/configuration-options/#storage.wiredTiger.indexConfig.prefixCompression)|
|operationProfiling.mode|string|slowOp|Sets the ['operationProfiling.mode' option](https://docs.mongodb.com/manual/reference/configuration-options/#operationProfiling.mode)|
|operationProfiling.slowOpThresholdMs|int|100| Sets the ['operationProfiling.slowOpThresholdMs'](https://docs.mongodb.com/manual/reference/configuration-options/#operationProfiling.slowOpThresholdMs) option |
|operationProfiling.rateLimit|int|1|Sets the ['operationProfiling.rateLimit' option](https://www.percona.com/doc/percona-server-for-mongodb/LATEST/rate-limit.html)|
|auditLog.destination|string| | Sets the ['auditLog.destination' option](https://www.percona.com/doc/percona-server-for-mongodb/LATEST/audit-logging.html)|
|auditLog.format |string|BSON|Sets the ['auditLog.format' option](https://www.percona.com/doc/percona-server-for-mongodb/LATEST/audit-logging.html)|
|auditLog.filter |string|{}  | Sets the ['auditLog.filter' option](https://www.percona.com/doc/percona-server-for-mongodb/LATEST/audit-logging.html)|

## backup section

The ``backup`` section in the [deploy/cr.yaml](https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/cr.yaml) file contains the following configuration options for the regular Percona Server for MongoDB backups.

| Key                            | Value Type | Example   | Description                                   |
|--------------------------------|------------|-----------|-----------------------------------------------|
|enabled                         | boolean    | `false`   | Enables or disables the backups functionality |
|version                         | string     | `0.2.1`   |                                               |
|restartOnFailure                | boolean    | `true`    |                                               |
|storages.type                   | string     | `s3`      | Type of the cloud storage to be used for backups. Currently only `s3` type is supported                                                          |
|storages.s3.credentialsSecret   | string     | `my-cluster-name-backup-s3`| [Kubernetes secret](https://kubernetes.io/docs/concepts/configuration/secret/) for backups. It should contain `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` keys. |
|storages.s3.bucket              | string     |           | The [Amazon S3 bucket](https://docs.aws.amazon.com/en_us/AmazonS3/latest/dev/UsingBucket.html) name for backups                    |
|storages.s3.region              | string     |`us-east-1`| The [AWS region](https://docs.aws.amazon.com/en_us/general/latest/gr/rande.html) to use. Please note **this option is mandatory** not only for Amazon S3, but for all S3-compatible storages.|
|storages.s3.endpointUrl         | string     |           | The endpoint URL of the S3-compatible storage to be used (not needed for the original Amazon S3 cloud)                             |
|coordinator.resources.limits.cpu| string     |`100m`     | Kubernetes CPU limit](https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/#resource-requests-and-limits-of-pod-and-container) for the MongoDB Coordinator container  |
|coordinator.resources.limits.memory | string |`0.2G`     | [Kubernetes Memory limit](https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/#resource-requests-and-limits-of-pod-and-container) for the MongoDB Coordinator container  |
|coordinator.resources.limits.storage| string |`1Gi`      | [Kubernetes Storage limit](https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/#resource-requests-and-limits-of-pod-and-container) for the MongoDB Coordinator container  |
|affinity.antiAffinityTopologyKey|string|`kubernetes.io/hostname`| The [Kubernetes topologyKey](https://kubernetes.io/docs/concepts/configuration/assign-pod-node/#inter-pod-affinity-and-anti-affinity-beta-feature) node affinity constraint for the backup storage|
|tolerations.key                 | string     | `node.alpha.kubernetes.io/unreachable` | The [Kubernetes Pod tolerations] (https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts) key  for the backup storage nodes|
|tolerations.operator            | string     | `Exists`  | The [Kubernetes Pod tolerations] (https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts) operator  for the backup storage nodes|
|tolerations.effect              | string     |`NoExecute`| The [Kubernetes Pod tolerations] (https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts) effect for the backup storage nodes|
|tolerations.tolerationSeconds   | int | `6000`    | The [Kubernetes Pod tolerations] (https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/#concepts) time limit  for the backup storage nodes|
|priorityClassName               | string     | `high-priority`  | The [Kuberentes Pod priority class](https://kubernetes.io/docs/concepts/configuration/pod-priority-preemption/#priorityclass)  for the backup storage nodes|
|annotations.iam.amazonaws.com/role | string |`role-arn`| The [AWS IAM role](https://kubernetes-on-aws.readthedocs.io/en/latest/user-guide/iam-roles.html)  for the backup storage nodes                             |
|labels                          | label      | `rack: rack-22` | The [Kubernetes affinity labels](https://kubernetes.io/docs/concepts/configuration/assign-pod-node/)  for the backup storage nodes                      |
|nodeSelector                    | label      | `disktype: ssd`        | The [Kubernetes nodeSelector](https://kubernetes.io/docs/concepts/configuration/assign-pod-node/#nodeselector) affinity constraint  for the backup storage nodes|
|coordinator.requests.storage    | string     | `1Gi`     | The [Kubernetes Persistent Volume](https://kubernetes.io/docs/concepts/storage/persistent-volumes/) size for the MongoDB Coordinator container                           |
|coordinator.requests.storageClass | string   | `aws-gp2` | Set the [Kubernetes Storage Class](https://kubernetes.io/docs/concepts/storage/storage-classes/) to use with the MongoDB Coordinator container                      |
|coordinator.debug               | string     | `false`   | Enables or disables debug mode for the MongoDB Coordinator operation     |
|tasks.name                      | string     | `sat-night-backup` | The backup name    |
|tasks.enabled                   | boolean    | `true`             | Enables or disables this exact backup |
|tasks.schedule                  | string     | `0 0 * * 6`        | Scheduled time to make a backup, specified in the [crontab format](https://en.wikipedia.org/wiki/Cron)                                                        |
|tasks.storageName               | string     | `st-us-west`       | Name of the S3-compatible storage for backups, configured in the `storages` subsection                                                                       |
|tasks.compressionType           | string     | `gzip`             | The compression format to store backups in |

