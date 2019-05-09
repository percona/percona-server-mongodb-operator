Custom Resource options
=======================

The operator is configured via the spec section of the
`deploy/cr.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/cr.yaml>`__
file. This file contains the following spec sections:

+---+----------+-------+----------------------------------------------+
| K | Value    | Defau | Description                                  |
| e | Type     | lt    |                                              |
| y |          |       |                                              |
+===+==========+=======+==============================================+
| p | string   | kuber | Override/set the Kubernetes platform:        |
| l |          | netes | *kubernetes* or *openshift*. Set openshift   |
| a |          |       | on OpenShift 3.11+                           |
| t |          |       |                                              |
| f |          |       |                                              |
| o |          |       |                                              |
| r |          |       |                                              |
| m |          |       |                                              |
+---+----------+-------+----------------------------------------------+
| v | string   | 3.6.8 | The Dockerhub tag of                         |
| e |          |       | `percona/percona-server-mongodb <https://hub |
| r |          |       | .docker.com/r/perconalab/percona-server-mong |
| s |          |       | odb-operator/tags/>`__                       |
| i |          |       | to deploy                                    |
| o |          |       |                                              |
| n |          |       |                                              |
+---+----------+-------+----------------------------------------------+
| s | subdoc   |       | Operator secrets section                     |
| e |          |       |                                              |
| c |          |       |                                              |
| r |          |       |                                              |
| e |          |       |                                              |
| t |          |       |                                              |
| s |          |       |                                              |
+---+----------+-------+----------------------------------------------+
| r | array    |       | Operator MongoDB Replica Set section         |
| e |          |       |                                              |
| p |          |       |                                              |
| l |          |       |                                              |
| s |          |       |                                              |
| e |          |       |                                              |
| t |          |       |                                              |
| s |          |       |                                              |
+---+----------+-------+----------------------------------------------+
| p | subdoc   |       | Percona Monitoring and Management section    |
| m |          |       |                                              |
| m |          |       |                                              |
+---+----------+-------+----------------------------------------------+
| m | subdoc   |       | Operator MongoDB Mongod configuration        |
| o |          |       | section                                      |
| n |          |       |                                              |
| g |          |       |                                              |
| o |          |       |                                              |
| d |          |       |                                              |
+---+----------+-------+----------------------------------------------+
| b | subdoc   |       | Percona Server for MongoDB backups section   |
| a |          |       |                                              |
| c |          |       |                                              |
| k |          |       |                                              |
| u |          |       |                                              |
| p |          |       |                                              |
+---+----------+-------+----------------------------------------------+

Secrets section
---------------

Each spec in its turn may contain some key-value pairs. The secrets one
has only two of them:

+--------+---------------------+---------------+-----------------------+
| Key    | Value Type          | Example       | Description           |
+========+=====================+===============+=======================+
| key    | string              | my-cluster-na | The secret name for   |
|        |                     | me-mongodb-ke | the `MongoDB Internal |
|        |                     | y             | Auth                  |
|        |                     |               | Key <https://docs.mon |
|        |                     |               | godb.com/manual/core/ |
|        |                     |               | security-internal-aut |
|        |                     |               | hentication/>`__.     |
|        |                     |               | This secret is        |
|        |                     |               | auto-created by the   |
|        |                     |               | operator if it        |
|        |                     |               | doesn’t exist         |
+--------+---------------------+---------------+-----------------------+
| users  | string              | my-cluster-na | The secret name for   |
|        |                     | me-mongodb-us | the MongoDB users     |
|        |                     | ers           | required to run the   |
|        |                     |               | operator. **This      |
|        |                     |               | secret is required to |
|        |                     |               | run the operator!**   |
+--------+---------------------+---------------+-----------------------+

Replsets section
----------------

The replsets section controls the MongoDB Replica Set.

+------------+-----+---+---------------------------------------------+
| Key        | Val | E | Description                                 |
|            | ue  | x |                                             |
|            | Typ | a |                                             |
|            | e   | m |                                             |
|            |     | p |                                             |
|            |     | l |                                             |
|            |     | e |                                             |
+============+=====+===+=============================================+
| name       | str | r | The name of the `MongoDB Replica            |
|            | ing | s | Set <https://docs.mongodb.com/manual/replic |
|            |     | 0 | ation/>`__                                  |
+------------+-----+---+---------------------------------------------+
| size       | int | 3 | The size of the MongoDB Replica Set, must   |
|            |     |   | be >= 3 for                                 |
|            |     |   | `High-Availability <https://docs.mongodb.co |
|            |     |   | m/manual/replication/#redundancy-and-data-a |
|            |     |   | vailability>`__                             |
+------------+-----+---+---------------------------------------------+
| storageCla | str |   | Set the `Kubernetes Storage                 |
| ss         | ing |   | Class <https://kubernetes.io/docs/concepts/ |
|            |     |   | storage/storage-classes/>`__                |
|            |     |   | to use with the MongoDB `Persistent Volume  |
|            |     |   | Claim <https://kubernetes.io/docs/concepts/ |
|            |     |   | storage/persistent-volumes/#persistentvolum |
|            |     |   | eclaims>`__                                 |
+------------+-----+---+---------------------------------------------+
| arbiter.en | boo | f | Enable or disable creation of `Replica Set  |
| abled      | lea | a | Arbiter <https://docs.mongodb.com/manual/co |
|            | n   | l | re/replica-set-arbiter/>`__                 |
|            |     | s | nodes within the cluster                    |
|            |     | e |                                             |
+------------+-----+---+---------------------------------------------+
| arbiter.si | int |   | The number of `Replica Set                  |
| ze         |     |   | Arbiter <https://docs.mongodb.com/manual/co |
|            |     |   | re/replica-set-arbiter/>`__                 |
|            |     |   | nodes within the cluster                    |
+------------+-----+---+---------------------------------------------+
| arbiter.af | str | ` | The `Kubernetes                             |
| finity.ant | ing | ` | topologyKey <https://kubernetes.io/docs/con |
| iAffinityT |     | k | cepts/configuration/assign-pod-node/#inter- |
| opologyKey |     | u | pod-affinity-and-anti-affinity-beta-feature |
|            |     | b | >`__                                        |
|            |     | e | node affinity constraint for the Arbiter    |
|            |     | r |                                             |
|            |     | n |                                             |
|            |     | e |                                             |
|            |     | t |                                             |
|            |     | e |                                             |
|            |     | s |                                             |
|            |     | . |                                             |
|            |     | i |                                             |
|            |     | o |                                             |
|            |     | / |                                             |
|            |     | h |                                             |
|            |     | o |                                             |
|            |     | s |                                             |
|            |     | t |                                             |
|            |     | n |                                             |
|            |     | a |                                             |
|            |     | m |                                             |
|            |     | e |                                             |
|            |     | ` |                                             |
|            |     | ` |                                             |
+------------+-----+---+---------------------------------------------+
| arbiter.to | str | ` | The [Kubernetes Pod tolerations]            |
| lerations. | ing | ` | (https://kubernetes.io/docs/concepts/config |
| key        |     | n | uration/taint-and-toleration/#concepts)     |
|            |     | o | key for the Arbiter nodes                   |
|            |     | d |                                             |
|            |     | e |                                             |
|            |     | . |                                             |
|            |     | a |                                             |
|            |     | l |                                             |
|            |     | p |                                             |
|            |     | h |                                             |
|            |     | a |                                             |
|            |     | . |                                             |
|            |     | k |                                             |
|            |     | u |                                             |
|            |     | b |                                             |
|            |     | e |                                             |
|            |     | r |                                             |
|            |     | n |                                             |
|            |     | e |                                             |
|            |     | t |                                             |
|            |     | e |                                             |
|            |     | s |                                             |
|            |     | . |                                             |
|            |     | i |                                             |
|            |     | o |                                             |
|            |     | / |                                             |
|            |     | u |                                             |
|            |     | n |                                             |
|            |     | r |                                             |
|            |     | e |                                             |
|            |     | a |                                             |
|            |     | c |                                             |
|            |     | h |                                             |
|            |     | a |                                             |
|            |     | b |                                             |
|            |     | l |                                             |
|            |     | e |                                             |
|            |     | ` |                                             |
|            |     | ` |                                             |
+------------+-----+---+---------------------------------------------+
| arbiter.to | str | ` | The [Kubernetes Pod tolerations]            |
| lerations. | ing | ` | (https://kubernetes.io/docs/concepts/config |
| operator   |     | E | uration/taint-and-toleration/#concepts)     |
|            |     | x | operator for the Arbiter nodes              |
|            |     | i |                                             |
|            |     | s |                                             |
|            |     | t |                                             |
|            |     | s |                                             |
|            |     | ` |                                             |
|            |     | ` |                                             |
+------------+-----+---+---------------------------------------------+
| arbiter.to | str | ` | The [Kubernetes Pod tolerations]            |
| lerations. | ing | ` | (https://kubernetes.io/docs/concepts/config |
| effect     |     | N | uration/taint-and-toleration/#concepts)     |
|            |     | o | effect for the Arbiter nodes                |
|            |     | E |                                             |
|            |     | x |                                             |
|            |     | e |                                             |
|            |     | c |                                             |
|            |     | u |                                             |
|            |     | t |                                             |
|            |     | e |                                             |
|            |     | ` |                                             |
|            |     | ` |                                             |
+------------+-----+---+---------------------------------------------+
| arbiter.to | int | ` | The [Kubernetes Pod tolerations]            |
| lerations. |     | ` | (https://kubernetes.io/docs/concepts/config |
| toleration |     | 6 | uration/taint-and-toleration/#concepts)     |
| Seconds    |     | 0 | time limit for the Arbiter nodes            |
|            |     | 0 |                                             |
|            |     | 0 |                                             |
|            |     | ` |                                             |
|            |     | ` |                                             |
+------------+-----+---+---------------------------------------------+
| arbiter.pr | str | ` | The `Kuberentes Pod priority                |
| iorityClas | ing | ` | class <https://kubernetes.io/docs/concepts/ |
| sName      |     | h | configuration/pod-priority-preemption/#prio |
|            |     | i | rityclass>`__                               |
|            |     | g | for the Arbiter nodes                       |
|            |     | h |                                             |
|            |     | - |                                             |
|            |     | p |                                             |
|            |     | r |                                             |
|            |     | i |                                             |
|            |     | o |                                             |
|            |     | r |                                             |
|            |     | i |                                             |
|            |     | t |                                             |
|            |     | y |                                             |
|            |     | ` |                                             |
|            |     | ` |                                             |
+------------+-----+---+---------------------------------------------+
| arbiter.an | str | ` | The `AWS IAM                                |
| notations. | ing | ` | role <https://kubernetes-on-aws.readthedocs |
| iam.amazon |     | r | .io/en/latest/user-guide/iam-roles.html>`__ |
| aws.com/ro |     | o | for the Arbiter nodes                       |
| le         |     | l |                                             |
|            |     | e |                                             |
|            |     | - |                                             |
|            |     | a |                                             |
|            |     | r |                                             |
|            |     | n |                                             |
|            |     | ` |                                             |
|            |     | ` |                                             |
+------------+-----+---+---------------------------------------------+
| arbiter.la | lab | ` | The `Kubernetes affinity                    |
| bels       | el  | ` | labels <https://kubernetes.io/docs/concepts |
|            |     | r | /configuration/assign-pod-node/>`__         |
|            |     | a | for the Arbiter nodes                       |
|            |     | c |                                             |
|            |     | k |                                             |
|            |     | : |                                             |
|            |     |   |                                             |
|            |     | r |                                             |
|            |     | a |                                             |
|            |     | c |                                             |
|            |     | k |                                             |
|            |     | - |                                             |
|            |     | 2 |                                             |
|            |     | 2 |                                             |
|            |     | ` |                                             |
|            |     | ` |                                             |
+------------+-----+---+---------------------------------------------+
| arbiter.no | lab | ` | The `Kubernetes                             |
| deSelector | el  | ` | nodeSelector <https://kubernetes.io/docs/co |
|            |     | d | ncepts/configuration/assign-pod-node/#nodes |
|            |     | i | elector>`__                                 |
|            |     | s | affinity constraint for the Arbiter nodes   |
|            |     | k |                                             |
|            |     | t |                                             |
|            |     | y |                                             |
|            |     | p |                                             |
|            |     | e |                                             |
|            |     | : |                                             |
|            |     |   |                                             |
|            |     | s |                                             |
|            |     | s |                                             |
|            |     | d |                                             |
|            |     | ` |                                             |
|            |     | ` |                                             |
+------------+-----+---+---------------------------------------------+
| expose.ena | boo | f | Enable or disable exposing `MongoDB Replica |
| bled       | lea | a | Set <https://docs.mongodb.com/manual/replic |
|            | n   | l | ation/>`__                                  |
|            |     | s | nodes with dedicated IP addresses           |
|            |     | e |                                             |
+------------+-----+---+---------------------------------------------+
| expose.exp | str | C | the `IP address type <./expose>`__ to be    |
| oseType    | ing | l | exposed                                     |
|            |     | u |                                             |
|            |     | s |                                             |
|            |     | t |                                             |
|            |     | e |                                             |
|            |     | r |                                             |
|            |     | I |                                             |
|            |     | P |                                             |
+------------+-----+---+---------------------------------------------+
| resources. | str |   | `Kubernetes CPU                             |
| limits.cpu | ing |   | limit <https://kubernetes.io/docs/concepts/ |
|            |     |   | configuration/manage-compute-resources-cont |
|            |     |   | ainer/#resource-requests-and-limits-of-pod- |
|            |     |   | and-container>`__                           |
|            |     |   | for MongoDB container                       |
+------------+-----+---+---------------------------------------------+
| resources. | str |   | `Kubernetes Memory                          |
| limits.mem | ing |   | limit <https://kubernetes.io/docs/concepts/ |
| ory        |     |   | configuration/manage-compute-resources-cont |
|            |     |   | ainer/#resource-requests-and-limits-of-pod- |
|            |     |   | and-container>`__                           |
|            |     |   | for MongoDB container                       |
+------------+-----+---+---------------------------------------------+
| resources. | str |   | `Kubernetes Storage                         |
| limits.sto | ing |   | limit <https://kubernetes.io/docs/concepts/ |
| rage       |     |   | configuration/manage-compute-resources-cont |
|            |     |   | ainer/#resource-requests-and-limits-of-pod- |
|            |     |   | and-container>`__                           |
|            |     |   | for `Persistent Volume                      |
|            |     |   | Claim <https://kubernetes.io/docs/concepts/ |
|            |     |   | storage/persistent-volumes/#persistentvolum |
|            |     |   | eclaims>`__                                 |
+------------+-----+---+---------------------------------------------+
| resources. | str |   | `Kubernetes CPU                             |
| requests.c | ing |   | requests <https://kubernetes.io/docs/concep |
| pu         |     |   | ts/configuration/manage-compute-resources-c |
|            |     |   | ontainer/#resource-requests-and-limits-of-p |
|            |     |   | od-and-container>`__                        |
|            |     |   | for MongoDB container                       |
+------------+-----+---+---------------------------------------------+
| resources. | str |   | `Kubernetes Memory                          |
| requests.m | ing |   | requests <https://kubernetes.io/docs/concep |
| emory      |     |   | ts/configuration/manage-compute-resources-c |
|            |     |   | ontainer/#resource-requests-and-limits-of-p |
|            |     |   | od-and-container>`__                        |
|            |     |   | for MongoDB container                       |
+------------+-----+---+---------------------------------------------+
| volumeSpec | str | ` | `Kubernetes emptyDir                        |
| .emptyDir  | ing | ` | volume <https://kubernetes.io/docs/concepts |
|            |     | { | /storage/volumes/#emptydir>`__,             |
|            |     | } | i.e. the directory which will be created on |
|            |     | ` | a node, and will be accessible to the       |
|            |     | ` | MongoDB Pod containers                      |
+------------+-----+---+---------------------------------------------+
| volumeSpec | str | ` | `Kubernetes hostPath                        |
| .hostPath. | ing | ` | volume <https://kubernetes.io/docs/concepts |
| path       |     | / | /storage/volumes/#hostpath>`__,             |
|            |     | d | i.e. the file or directory of a node that   |
|            |     | a | will be accessible to the MongoDB Pod       |
|            |     | t | containers                                  |
|            |     | a |                                             |
|            |     | ` |                                             |
|            |     | ` |                                             |
+------------+-----+---+---------------------------------------------+
| volumeSpec | str | ` | The `Kubernetes hostPath volume             |
| .hostPath. | ing | ` | type <https://kubernetes.io/docs/concepts/s |
| type       |     | D | torage/volumes/#hostpath>`__                |
|            |     | i |                                             |
|            |     | r |                                             |
|            |     | e |                                             |
|            |     | c |                                             |
|            |     | t |                                             |
|            |     | o |                                             |
|            |     | r |                                             |
|            |     | y |                                             |
|            |     | ` |                                             |
|            |     | ` |                                             |
+------------+-----+---+---------------------------------------------+
| volumeSpec | str | ` | The `Kubernetes Storage                     |
| .persisten | ing | ` | Class <https://kubernetes.io/docs/concepts/ |
| tVolumeCla |     | s | storage/storage-classes/>`__                |
| im.storage |     | t | to use with the MongoDB container           |
| ClassName  |     | a | `Persistent Volume                          |
|            |     | n | Claim <https://kubernetes.io/docs/concepts/ |
|            |     | d | storage/persistent-volumes/#persistentvolum |
|            |     | a | eclaims>`__                                 |
|            |     | r |                                             |
|            |     | d |                                             |
|            |     | ` |                                             |
|            |     | ` |                                             |
+------------+-----+---+---------------------------------------------+
| volumeSpec | arr | ` | `Kubernetes Persistent                      |
| .persisten | ay  | ` | Volume <https://kubernetes.io/docs/concepts |
| tVolumeCla |     | [ | /storage/persistent-volumes/>`__            |
| im.accessM |     |   | access modes for the MongoDB container      |
| odes       |     | " |                                             |
|            |     | R |                                             |
|            |     | e |                                             |
|            |     | a |                                             |
|            |     | d |                                             |
|            |     | W |                                             |
|            |     | r |                                             |
|            |     | i |                                             |
|            |     | t |                                             |
|            |     | e |                                             |
|            |     | O |                                             |
|            |     | n |                                             |
|            |     | c |                                             |
|            |     | e |                                             |
|            |     | " |                                             |
|            |     |   |                                             |
|            |     | ] |                                             |
|            |     | ` |                                             |
|            |     | ` |                                             |
+------------+-----+---+---------------------------------------------+
| volumeSpec | str | ` | The `Kubernetes Persistent                  |
| .resources | ing | ` | Volume <https://kubernetes.io/docs/concepts |
| .requests. |     | 3 | /storage/persistent-volumes/>`__            |
| storage    |     | G | size for the MongoDB container              |
|            |     | i |                                             |
|            |     | ` |                                             |
|            |     | ` |                                             |
+------------+-----+---+---------------------------------------------+
| affinity.a | str | ` | The `Kubernetes                             |
| ntiAffinit | ing | ` | topologyKey <https://kubernetes.io/docs/con |
| yTopologyK |     | k | cepts/configuration/assign-pod-node/#inter- |
| ey         |     | u | pod-affinity-and-anti-affinity-beta-feature |
|            |     | b | >`__                                        |
|            |     | e | node affinity constraint for the Replica    |
|            |     | r | Set nodes                                   |
|            |     | n |                                             |
|            |     | e |                                             |
|            |     | t |                                             |
|            |     | e |                                             |
|            |     | s |                                             |
|            |     | . |                                             |
|            |     | i |                                             |
|            |     | o |                                             |
|            |     | / |                                             |
|            |     | h |                                             |
|            |     | o |                                             |
|            |     | s |                                             |
|            |     | t |                                             |
|            |     | n |                                             |
|            |     | a |                                             |
|            |     | m |                                             |
|            |     | e |                                             |
|            |     | ` |                                             |
|            |     | ` |                                             |
+------------+-----+---+---------------------------------------------+
| toleration | str | ` | The [Kubernetes Pod tolerations]            |
| s.key      | ing | ` | (https://kubernetes.io/docs/concepts/config |
|            |     | n | uration/taint-and-toleration/#concepts)     |
|            |     | o | key for the Replica Set nodes               |
|            |     | d |                                             |
|            |     | e |                                             |
|            |     | . |                                             |
|            |     | a |                                             |
|            |     | l |                                             |
|            |     | p |                                             |
|            |     | h |                                             |
|            |     | a |                                             |
|            |     | . |                                             |
|            |     | k |                                             |
|            |     | u |                                             |
|            |     | b |                                             |
|            |     | e |                                             |
|            |     | r |                                             |
|            |     | n |                                             |
|            |     | e |                                             |
|            |     | t |                                             |
|            |     | e |                                             |
|            |     | s |                                             |
|            |     | . |                                             |
|            |     | i |                                             |
|            |     | o |                                             |
|            |     | / |                                             |
|            |     | u |                                             |
|            |     | n |                                             |
|            |     | r |                                             |
|            |     | e |                                             |
|            |     | a |                                             |
|            |     | c |                                             |
|            |     | h |                                             |
|            |     | a |                                             |
|            |     | b |                                             |
|            |     | l |                                             |
|            |     | e |                                             |
|            |     | ` |                                             |
|            |     | ` |                                             |
+------------+-----+---+---------------------------------------------+
| toleration | str | ` | The [Kubernetes Pod tolerations]            |
| s.operator | ing | ` | (https://kubernetes.io/docs/concepts/config |
|            |     | E | uration/taint-and-toleration/#concepts)     |
|            |     | x | operator for the Replica Set nodes          |
|            |     | i |                                             |
|            |     | s |                                             |
|            |     | t |                                             |
|            |     | s |                                             |
|            |     | ` |                                             |
|            |     | ` |                                             |
+------------+-----+---+---------------------------------------------+
| toleration | str | ` | The [Kubernetes Pod tolerations]            |
| s.effect   | ing | ` | (https://kubernetes.io/docs/concepts/config |
|            |     | N | uration/taint-and-toleration/#concepts)     |
|            |     | o | effect for the Replica Set nodes            |
|            |     | E |                                             |
|            |     | x |                                             |
|            |     | e |                                             |
|            |     | c |                                             |
|            |     | u |                                             |
|            |     | t |                                             |
|            |     | e |                                             |
|            |     | ` |                                             |
|            |     | ` |                                             |
+------------+-----+---+---------------------------------------------+
| toleration | int | ` | The [Kubernetes Pod tolerations]            |
| s.tolerati |     | ` | (https://kubernetes.io/docs/concepts/config |
| onSeconds  |     | 6 | uration/taint-and-toleration/#concepts)     |
|            |     | 0 | time limit for the Replica Set nodes        |
|            |     | 0 |                                             |
|            |     | 0 |                                             |
|            |     | ` |                                             |
|            |     | ` |                                             |
+------------+-----+---+---------------------------------------------+
| priorityCl | str | ` | The `Kuberentes Pod priority                |
| assName    | ing | ` | class <https://kubernetes.io/docs/concepts/ |
|            |     | h | configuration/pod-priority-preemption/#prio |
|            |     | i | rityclass>`__                               |
|            |     | g | for the Replica Set nodes                   |
|            |     | h |                                             |
|            |     | - |                                             |
|            |     | p |                                             |
|            |     | r |                                             |
|            |     | i |                                             |
|            |     | o |                                             |
|            |     | r |                                             |
|            |     | i |                                             |
|            |     | t |                                             |
|            |     | y |                                             |
|            |     | ` |                                             |
|            |     | ` |                                             |
+------------+-----+---+---------------------------------------------+
| annotation | str | ` | The `AWS IAM                                |
| s.iam.amaz | ing | ` | role <https://kubernetes-on-aws.readthedocs |
| onaws.com/ |     | r | .io/en/latest/user-guide/iam-roles.html>`__ |
| role       |     | o | for the Replica Set nodes                   |
|            |     | l |                                             |
|            |     | e |                                             |
|            |     | - |                                             |
|            |     | a |                                             |
|            |     | r |                                             |
|            |     | n |                                             |
|            |     | ` |                                             |
|            |     | ` |                                             |
+------------+-----+---+---------------------------------------------+
| labels     | lab | ` | The `Kubernetes affinity                    |
|            | el  | ` | labels <https://kubernetes.io/docs/concepts |
|            |     | r | /configuration/assign-pod-node/>`__         |
|            |     | a | for the Replica Set nodes                   |
|            |     | c |                                             |
|            |     | k |                                             |
|            |     | : |                                             |
|            |     |   |                                             |
|            |     | r |                                             |
|            |     | a |                                             |
|            |     | c |                                             |
|            |     | k |                                             |
|            |     | - |                                             |
|            |     | 2 |                                             |
|            |     | 2 |                                             |
|            |     | ` |                                             |
|            |     | ` |                                             |
+------------+-----+---+---------------------------------------------+
| nodeSelect | lab | ` | The `Kubernetes                             |
| or         | el  | ` | nodeSelector <https://kubernetes.io/docs/co |
|            |     | d | ncepts/configuration/assign-pod-node/#nodes |
|            |     | i | elector>`__                                 |
|            |     | s | affinity constraint for the Replica Set     |
|            |     | k | nodes                                       |
|            |     | t |                                             |
|            |     | y |                                             |
|            |     | p |                                             |
|            |     | e |                                             |
|            |     | : |                                             |
|            |     |   |                                             |
|            |     | s |                                             |
|            |     | s |                                             |
|            |     | d |                                             |
|            |     | ` |                                             |
|            |     | ` |                                             |
+------------+-----+---+---------------------------------------------+
| podDisrupt | int | 1 | The `Kubernetes Pod distribution            |
| ionBudget. |     |   | budget <https://kubernetes.io/docs/concepts |
| maxUnavail |     |   | /workloads/pods/disruptions/>`__            |
| able       |     |   | limit specifying the maximum value for      |
|            |     |   | unavailable Pods                            |
+------------+-----+---+---------------------------------------------+
| podDisrupt | int | 1 | The `Kubernetes Pod distribution            |
| ionBudget. |     |   | budget <https://kubernetes.io/docs/concepts |
| minAvailab |     |   | /workloads/pods/disruptions/>`__            |
| le         |     |   | limit specifying the minimum value for      |
|            |     |   | available Pods                              |
+------------+-----+---+---------------------------------------------+

PMM Section
-----------

The ``pmm`` section in the deploy/cr.yaml file contains configuration
options for Percona Monitoring and Management.

+---------+----------+--------------------+----------------------------+
| Key     | Value    | Example            | Description                |
|         | Type     |                    |                            |
+=========+==========+====================+============================+
| enabled | boolean  | ``false``          | Enables or disables        |
|         |          |                    | monitoring Percona Server  |
|         |          |                    | for MongoDB with           |
|         |          |                    | `PMM <https://www.percona. |
|         |          |                    | com/doc/percona-monitoring |
|         |          |                    | -and-management/index.metr |
|         |          |                    | ics-monitor.dashboard.html |
|         |          |                    | >`__                       |
+---------+----------+--------------------+----------------------------+
| image   | string   | ``perconalab/pmm-c | PMM Client docker image to |
|         |          | lient:1.17.1``     | use                        |
+---------+----------+--------------------+----------------------------+
| serverH | string   | ``monitoring-servi | Address of the PMM Server  |
| ost     |          | ce``               | to collect data from the   |
|         |          |                    | Cluster                    |
+---------+----------+--------------------+----------------------------+

Mongod Section
--------------

The largest section in the deploy/cr.yaml file contains the Mongod
configuration options.

+--------+---------------------+---------------+-----------------------+
| Key    | Value Type          | Example       | Description           |
+========+=====================+===============+=======================+
| net.po | int                 | 27017         | Sets the MongoDB      |
| rt     |                     |               | `‘net.port’           |
|        |                     |               | option <https://docs. |
|        |                     |               | mongodb.com/manual/re |
|        |                     |               | ference/configuration |
|        |                     |               | -options/#net.port>`_ |
|        |                     |               | _                     |
+--------+---------------------+---------------+-----------------------+
| net.ho | int                 | 0             | Sets the Kubernetes   |
| stPort |                     |               | `‘hostPort’           |
|        |                     |               | option <https://kuber |
|        |                     |               | netes.io/docs/concept |
|        |                     |               | s/extend-kubernetes/c |
|        |                     |               | ompute-storage-net/ne |
|        |                     |               | twork-plugins/#suppor |
|        |                     |               | t-hostport>`__        |
+--------+---------------------+---------------+-----------------------+
| securi | bool                | false         | Enables/disables      |
| ty.red |                     |               | `PSMDB Log            |
| actCli |                     |               | Redaction <https://ww |
| entLog |                     |               | w.percona.com/doc/per |
| Data   |                     |               | cona-server-for-mongo |
|        |                     |               | db/LATEST/log-redacti |
|        |                     |               | on.html>`__           |
+--------+---------------------+---------------+-----------------------+
| setPar | int                 | 60            | Sets the PSMDB        |
| ameter |                     |               | ‘ttlMonitorSleepSecs’ |
| .ttlMo |                     |               | option                |
| nitorS |                     |               |                       |
| leepSe |                     |               |                       |
| cs     |                     |               |                       |
+--------+---------------------+---------------+-----------------------+
| setPar | int                 | 128           | Sets the              |
| ameter |                     |               | `‘wiredTigerConcurren |
| .wired |                     |               | tReadTransactions’    |
| TigerC |                     |               | option <https://docs. |
| oncurr |                     |               | mongodb.com/manual/re |
| entRea |                     |               | ference/parameters/#p |
| dTrans |                     |               | aram.wiredTigerConcur |
| action |                     |               | rentReadTransactions> |
| s      |                     |               | `__                   |
+--------+---------------------+---------------+-----------------------+
| setPar | int                 | 128           | Sets the              |
| ameter |                     |               | `‘wiredTigerConcurren |
| .wired |                     |               | tWriteTransactions’   |
| TigerC |                     |               | option <https://docs. |
| oncurr |                     |               | mongodb.com/manual/re |
| entWri |                     |               | ference/parameters/#p |
| teTran |                     |               | aram.wiredTigerConcur |
| sactio |                     |               | rentWriteTransactions |
| ns     |                     |               | >`__                  |
+--------+---------------------+---------------+-----------------------+
| storag | string              | wiredTiger    | Sets the              |
| e.engi |                     |               | `‘storage.engine’     |
| ne     |                     |               | option <https://docs. |
|        |                     |               | mongodb.com/manual/re |
|        |                     |               | ference/configuration |
|        |                     |               | -options/#storage.eng |
|        |                     |               | ine>`__               |
+--------+---------------------+---------------+-----------------------+
| storag | float               | 0.9           | Ratio used to compute |
| e.inMe |                     |               | the                   |
| mory.i |                     |               | `‘storage.engine.inMe |
| nMemor |                     |               | mory.inMemorySizeGb’  |
| ySizeR |                     |               | option <https://www.p |
| atio   |                     |               | ercona.com/doc/percon |
|        |                     |               | a-server-for-mongodb/ |
|        |                     |               | LATEST/inmemory.html# |
|        |                     |               | --inMemorySizeGB>`__  |
+--------+---------------------+---------------+-----------------------+
| storag | int                 | 16            | Sets the              |
| e.mmap |                     |               | `‘storage.mmapv1.nsSi |
| v1.nsS |                     |               | ze’                   |
| ize    |                     |               | option <https://docs. |
|        |                     |               | mongodb.com/manual/re |
|        |                     |               | ference/configuration |
|        |                     |               | -options/#storage.mma |
|        |                     |               | pv1.nsSize>`__        |
+--------+---------------------+---------------+-----------------------+
| storag | bool                | false         | Sets the              |
| e.mmap |                     |               | `‘storage.mmapv1.smal |
| v1.sma |                     |               | lfiles’               |
| llfile |                     |               | option <https://docs. |
| s      |                     |               | mongodb.com/manual/re |
|        |                     |               | ference/configuration |
|        |                     |               | -options/#storage.mma |
|        |                     |               | pv1.smallFiles>`__    |
+--------+---------------------+---------------+-----------------------+
| storag | float               | 0.5           | Ratio used to compute |
| e.wire |                     |               | the                   |
| dTiger |                     |               | `‘storage.wiredTiger. |
| .engin |                     |               | engineConfig.cacheSiz |
| eConfi |                     |               | eGB’                  |
| g.cach |                     |               | option <https://docs. |
| eSizeR |                     |               | mongodb.com/manual/re |
| atio   |                     |               | ference/configuration |
|        |                     |               | -options/#storage.wir |
|        |                     |               | edTiger.engineConfig. |
|        |                     |               | cacheSizeGB>`__       |
+--------+---------------------+---------------+-----------------------+
| storag | bool                | false         | Sets the              |
| e.wire |                     |               | `‘storage.wiredTiger. |
| dTiger |                     |               | engineConfig.director |
| .engin |                     |               | yForIndexes’          |
| eConfi |                     |               | option <https://docs. |
| g.dire |                     |               | mongodb.com/manual/re |
| ctoryF |                     |               | ference/configuration |
| orInde |                     |               | -options/#storage.wir |
| xes    |                     |               | edTiger.engineConfig. |
|        |                     |               | directoryForIndexes>` |
|        |                     |               | __                    |
+--------+---------------------+---------------+-----------------------+
| storag | string              | snappy        | Sets the              |
| e.wire |                     |               | `‘storage.wiredTiger. |
| dTiger |                     |               | engineConfig.journalC |
| .engin |                     |               | ompressor’            |
| eConfi |                     |               | option <https://docs. |
| g.jour |                     |               | mongodb.com/manual/re |
| nalCom |                     |               | ference/configuration |
| presso |                     |               | -options/#storage.wir |
| r      |                     |               | edTiger.engineConfig. |
|        |                     |               | journalCompressor>`__ |
+--------+---------------------+---------------+-----------------------+
| storag | string              | snappy        | Sets the              |
| e.wire |                     |               | `‘storage.wiredTiger. |
| dTiger |                     |               | collectionConfig.bloc |
| .colle |                     |               | kCompressor’          |
| ctionC |                     |               | option <https://docs. |
| onfig. |                     |               | mongodb.com/manual/re |
| blockC |                     |               | ference/configuration |
| ompres |                     |               | -options/#storage.wir |
| sor    |                     |               | edTiger.collectionCon |
|        |                     |               | fig.blockCompressor>` |
|        |                     |               | __                    |
+--------+---------------------+---------------+-----------------------+
| storag | bool                | true          | Sets the              |
| e.wire |                     |               | `‘storage.wiredTiger. |
| dTiger |                     |               | indexConfig.prefixCom |
| .index |                     |               | pression’             |
| Config |                     |               | option <https://docs. |
| .prefi |                     |               | mongodb.com/manual/re |
| xCompr |                     |               | ference/configuration |
| ession |                     |               | -options/#storage.wir |
|        |                     |               | edTiger.indexConfig.p |
|        |                     |               | refixCompression>`__  |
+--------+---------------------+---------------+-----------------------+
| operat | string              | slowOp        | Sets the              |
| ionPro |                     |               | `‘operationProfiling. |
| filing |                     |               | mode’                 |
| .mode  |                     |               | option <https://docs. |
|        |                     |               | mongodb.com/manual/re |
|        |                     |               | ference/configuration |
|        |                     |               | -options/#operationPr |
|        |                     |               | ofiling.mode>`__      |
+--------+---------------------+---------------+-----------------------+
| operat | int                 | 100           | Sets the              |
| ionPro |                     |               | `‘operationProfiling. |
| filing |                     |               | slowOpThresholdMs’ <h |
| .slowO |                     |               | ttps://docs.mongodb.c |
| pThres |                     |               | om/manual/reference/c |
| holdMs |                     |               | onfiguration-options/ |
|        |                     |               | #operationProfiling.s |
|        |                     |               | lowOpThresholdMs>`__  |
|        |                     |               | option                |
+--------+---------------------+---------------+-----------------------+
| operat | int                 | 1             | Sets the              |
| ionPro |                     |               | `‘operationProfiling. |
| filing |                     |               | rateLimit’            |
| .rateL |                     |               | option <https://www.p |
| imit   |                     |               | ercona.com/doc/percon |
|        |                     |               | a-server-for-mongodb/ |
|        |                     |               | LATEST/rate-limit.htm |
|        |                     |               | l>`__                 |
+--------+---------------------+---------------+-----------------------+
| auditL | string              |               | Sets the              |
| og.des |                     |               | `‘auditLog.destinatio |
| tinati |                     |               | n’                    |
| on     |                     |               | option <https://www.p |
|        |                     |               | ercona.com/doc/percon |
|        |                     |               | a-server-for-mongodb/ |
|        |                     |               | LATEST/audit-logging. |
|        |                     |               | html>`__              |
+--------+---------------------+---------------+-----------------------+
| auditL | string              | BSON          | Sets the              |
| og.for |                     |               | `‘auditLog.format’    |
| mat    |                     |               | option <https://www.p |
|        |                     |               | ercona.com/doc/percon |
|        |                     |               | a-server-for-mongodb/ |
|        |                     |               | LATEST/audit-logging. |
|        |                     |               | html>`__              |
+--------+---------------------+---------------+-----------------------+
| auditL | string              | {}            | Sets the              |
| og.fil |                     |               | `‘auditLog.filter’    |
| ter    |                     |               | option <https://www.p |
|        |                     |               | ercona.com/doc/percon |
|        |                     |               | a-server-for-mongodb/ |
|        |                     |               | LATEST/audit-logging. |
|        |                     |               | html>`__              |
+--------+---------------------+---------------+-----------------------+

backup section
--------------

The ``backup`` section in the
`deploy/cr.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/master/deploy/cr.yaml>`__
file contains the following configuration options for the regular
Percona Server for MongoDB backups.

+---------------------+-------+------+--------------------------------+
| Key                 | Value | Exam | Description                    |
|                     | Type  | ple  |                                |
+=====================+=======+======+================================+
| enabled             | boole | ``fa | Enables or disables the        |
|                     | an    | lse` | backups functionality          |
|                     |       | `    |                                |
+---------------------+-------+------+--------------------------------+
| version             | strin | ``0. |                                |
|                     | g     | 3.0` |                                |
|                     |       | `    |                                |
+---------------------+-------+------+--------------------------------+
| restartOnFailure    | boole | ``tr |                                |
|                     | an    | ue`` |                                |
+---------------------+-------+------+--------------------------------+
| storages.type       | strin | ``s3 | Type of the cloud storage to   |
|                     | g     | ``   | be used for backups. Currently |
|                     |       |      | only ``s3`` type is supported  |
+---------------------+-------+------+--------------------------------+
| storages.s3.credent | strin | ``my | `Kubernetes                    |
| ialsSecret          | g     | -clu | secret <https://kubernetes.io/ |
|                     |       | ster | docs/concepts/configuration/se |
|                     |       | -nam | cret/>`__                      |
|                     |       | e-ba | for backups. It should contain |
|                     |       | ckup | ``AWS_ACCESS_KEY_ID`` and      |
|                     |       | -s3` | ``AWS_SECRET_ACCESS_KEY``      |
|                     |       | `    | keys.                          |
+---------------------+-------+------+--------------------------------+
| storages.s3.bucket  | strin |      | The `Amazon S3                 |
|                     | g     |      | bucket <https://docs.aws.amazo |
|                     |       |      | n.com/en_us/AmazonS3/latest/de |
|                     |       |      | v/UsingBucket.html>`__         |
|                     |       |      | name for backups               |
+---------------------+-------+------+--------------------------------+
| storages.s3.region  | strin | ``us | The `AWS                       |
|                     | g     | -eas | region <https://docs.aws.amazo |
|                     |       | t-1` | n.com/en_us/general/latest/gr/ |
|                     |       | `    | rande.html>`__                 |
|                     |       |      | to use. Please note **this     |
|                     |       |      | option is mandatory** not only |
|                     |       |      | for Amazon S3, but for all     |
|                     |       |      | S3-compatible storages.        |
+---------------------+-------+------+--------------------------------+
| storages.s3.endpoin | strin |      | The endpoint URL of the        |
| tUrl                | g     |      | S3-compatible storage to be    |
|                     |       |      | used (not needed for the       |
|                     |       |      | original Amazon S3 cloud)      |
+---------------------+-------+------+--------------------------------+
| coordinator.resourc | strin | ``10 | Kubernetes CPU                 |
| es.limits.cpu       | g     | 0m`` | limit](https://kubernetes.io/d |
|                     |       |      | ocs/concepts/configuration/man |
|                     |       |      | age-compute-resources-containe |
|                     |       |      | r/#resource-requests-and-limit |
|                     |       |      | s-of-pod-and-container)        |
|                     |       |      | for the MongoDB Coordinator    |
|                     |       |      | container                      |
+---------------------+-------+------+--------------------------------+
| coordinator.resourc | strin | ``0. | `Kubernetes Memory             |
| es.limits.memory    | g     | 2G`` | limit <https://kubernetes.io/d |
|                     |       |      | ocs/concepts/configuration/man |
|                     |       |      | age-compute-resources-containe |
|                     |       |      | r/#resource-requests-and-limit |
|                     |       |      | s-of-pod-and-container>`__     |
|                     |       |      | for the MongoDB Coordinator    |
|                     |       |      | container                      |
+---------------------+-------+------+--------------------------------+
| coordinator.resourc | strin | ``1G | `Kubernetes Storage            |
| es.limits.storage   | g     | i``  | limit <https://kubernetes.io/d |
|                     |       |      | ocs/concepts/configuration/man |
|                     |       |      | age-compute-resources-containe |
|                     |       |      | r/#resource-requests-and-limit |
|                     |       |      | s-of-pod-and-container>`__     |
|                     |       |      | for the MongoDB Coordinator    |
|                     |       |      | container                      |
+---------------------+-------+------+--------------------------------+
| affinity.antiAffini | strin | ``ku | The `Kubernetes                |
| tyTopologyKey       | g     | bern | topologyKey <https://kubernete |
|                     |       | etes | s.io/docs/concepts/configurati |
|                     |       | .io/ | on/assign-pod-node/#inter-pod- |
|                     |       | host | affinity-and-anti-affinity-bet |
|                     |       | name | a-feature>`__                  |
|                     |       | ``   | node affinity constraint for   |
|                     |       |      | the backup storage             |
+---------------------+-------+------+--------------------------------+
| tolerations.key     | strin | ``no | The [Kubernetes Pod            |
|                     | g     | de.a | tolerations]                   |
|                     |       | lpha | (https://kubernetes.io/docs/co |
|                     |       | .kub | ncepts/configuration/taint-and |
|                     |       | erne | -toleration/#concepts)         |
|                     |       | tes. | key for the backup storage     |
|                     |       | io/u | nodes                          |
|                     |       | nrea |                                |
|                     |       | chab |                                |
|                     |       | le`` |                                |
+---------------------+-------+------+--------------------------------+
| tolerations.operato | strin | ``Ex | The [Kubernetes Pod            |
| r                   | g     | ists | tolerations]                   |
|                     |       | ``   | (https://kubernetes.io/docs/co |
|                     |       |      | ncepts/configuration/taint-and |
|                     |       |      | -toleration/#concepts)         |
|                     |       |      | operator for the backup        |
|                     |       |      | storage nodes                  |
+---------------------+-------+------+--------------------------------+
| tolerations.effect  | strin | ``No | The [Kubernetes Pod            |
|                     | g     | Exec | tolerations]                   |
|                     |       | ute` | (https://kubernetes.io/docs/co |
|                     |       | `    | ncepts/configuration/taint-and |
|                     |       |      | -toleration/#concepts)         |
|                     |       |      | effect for the backup storage  |
|                     |       |      | nodes                          |
+---------------------+-------+------+--------------------------------+
| tolerations.tolerat | int   | ``60 | The [Kubernetes Pod            |
| ionSeconds          |       | 00`` | tolerations]                   |
|                     |       |      | (https://kubernetes.io/docs/co |
|                     |       |      | ncepts/configuration/taint-and |
|                     |       |      | -toleration/#concepts)         |
|                     |       |      | time limit for the backup      |
|                     |       |      | storage nodes                  |
+---------------------+-------+------+--------------------------------+
| priorityClassName   | strin | ``hi | The `Kuberentes Pod priority   |
|                     | g     | gh-p | class <https://kubernetes.io/d |
|                     |       | rior | ocs/concepts/configuration/pod |
|                     |       | ity` | -priority-preemption/#priority |
|                     |       | `    | class>`__                      |
|                     |       |      | for the backup storage nodes   |
+---------------------+-------+------+--------------------------------+
| annotations.iam.ama | strin | ``ro | The `AWS IAM                   |
| zonaws.com/role     | g     | le-a | role <https://kubernetes-on-aw |
|                     |       | rn`` | s.readthedocs.io/en/latest/use |
|                     |       |      | r-guide/iam-roles.html>`__     |
|                     |       |      | for the backup storage nodes   |
+---------------------+-------+------+--------------------------------+
| labels              | label | ``ra | The `Kubernetes affinity       |
|                     |       | ck:  | labels <https://kubernetes.io/ |
|                     |       | rack | docs/concepts/configuration/as |
|                     |       | -22` | sign-pod-node/>`__             |
|                     |       | `    | for the backup storage nodes   |
+---------------------+-------+------+--------------------------------+
| nodeSelector        | label | ``di | The `Kubernetes                |
|                     |       | skty | nodeSelector <https://kubernet |
|                     |       | pe:  | es.io/docs/concepts/configurat |
|                     |       | ssd` | ion/assign-pod-node/#nodeselec |
|                     |       | `    | tor>`__                        |
|                     |       |      | affinity constraint for the    |
|                     |       |      | backup storage nodes           |
+---------------------+-------+------+--------------------------------+
| coordinator.request | strin | ``1G | The `Kubernetes Persistent     |
| s.storage           | g     | i``  | Volume <https://kubernetes.io/ |
|                     |       |      | docs/concepts/storage/persiste |
|                     |       |      | nt-volumes/>`__                |
|                     |       |      | size for the MongoDB           |
|                     |       |      | Coordinator container          |
+---------------------+-------+------+--------------------------------+
| coordinator.request | strin | ``aw | Set the `Kubernetes Storage    |
| s.storageClass      | g     | s-gp | Class <https://kubernetes.io/d |
|                     |       | 2``  | ocs/concepts/storage/storage-c |
|                     |       |      | lasses/>`__                    |
|                     |       |      | to use with the MongoDB        |
|                     |       |      | Coordinator container          |
+---------------------+-------+------+--------------------------------+
| coordinator.debug   | strin | ``fa | Enables or disables debug mode |
|                     | g     | lse` | for the MongoDB Coordinator    |
|                     |       | `    | operation                      |
+---------------------+-------+------+--------------------------------+
| tasks.name          | strin | ``sa | The backup name                |
|                     | g     | t-ni |                                |
|                     |       | ght- |                                |
|                     |       | back |                                |
|                     |       | up`` |                                |
+---------------------+-------+------+--------------------------------+
| tasks.enabled       | boole | ``tr | Enables or disables this exact |
|                     | an    | ue`` | backup                         |
+---------------------+-------+------+--------------------------------+
| tasks.schedule      | strin | ``0  | Scheduled time to make a       |
|                     | g     | 0 *  | backup, specified in the       |
|                     |       | * 6` | `crontab                       |
|                     |       | `    | format <https://en.wikipedia.o |
|                     |       |      | rg/wiki/Cron>`__               |
+---------------------+-------+------+--------------------------------+
| tasks.storageName   | strin | ``st | Name of the S3-compatible      |
|                     | g     | -us- | storage for backups,           |
|                     |       | west | configured in the ``storages`` |
|                     |       | ``   | subsection                     |
+---------------------+-------+------+--------------------------------+
| tasks.compressionTy | strin | ``gz | The compression format to      |
| pe                  | g     | ip`` | store backups in               |
+---------------------+-------+------+--------------------------------+
