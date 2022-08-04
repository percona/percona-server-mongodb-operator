.. _compare:

Compare various solutions to deploy MongoDB in Kubernetes
=========================================================

There are multiple ways to deploy and manage MongoDB in Kubernetes. Here we will focus on comparing the following open source solutions:

* `Bitnami Helm chart <https://github.com/bitnami/charts/tree/master/bitnami/mongodb>`_
* `KubeDB <https://github.com/kubedb>`_
* `MongoDB Community Operator <https://github.com/mongodb/mongodb-kubernetes-operator>`_
* `Percona Operator for MongoDB <https://github.com/percona/percona-server-mongodb-operator/>`_

Generic
*******

Here is the review of generic features, such as supported MongoDB versions, open source models and more.

+-------------------------+-------------------------------+---------------------+-----------------------------------+-------------------------------------+
| Feature/Product         | Percona Operator for MongoDB  | Bitnami Helm Chart  | KubeDB for MongoDB                | MongoDB Community Edtion Operator   |
+=========================+===============================+=====================+===================================+=====================================+
| Open source model       | Apache 2.0                    | Apache 2.0          | Open core                         | Open core                           |
+-------------------------+-------------------------------+---------------------+-----------------------------------+-------------------------------------+
| MongoDB versions        | PSMDB 4.2, 4.4, 5.0           | MongoDB 5.0         | MongoDB 3.4, 3.6. 4.0, 4.1, 4.2   | MongoDB 4.2, 4.4, 5.0               |
+-------------------------+-------------------------------+---------------------+-----------------------------------+-------------------------------------+
| Kubernetes conformance  | Various versions are tested   | No guarantee        | No guarantee                      | No guarantee                        |
+-------------------------+-------------------------------+---------------------+-----------------------------------+-------------------------------------+
| Cluster-wide mode       | No                            | Not an operator     | Enterprise only                   | Yes                                 |
+-------------------------+-------------------------------+---------------------+-----------------------------------+-------------------------------------+
| Network exposure        | Yes                           | Yes                 | No, only through manual config    | Enterprise only                     |
+-------------------------+-------------------------------+---------------------+-----------------------------------+-------------------------------------+

Maintenance
***********

Upgrade and scaling are the two most common maintenance tasks that are executed by database administrators and developers.

+------------------+-------------------------------+--------------------------+---------------------+-----------------------------+
| Feature/Product  | Percona Operator for MongoDB  | Bitnami Helm Chart       | KubeDB for MongoDB  | MongoDB Community Operator  |
+==================+===============================+==========================+=====================+=============================+
| Operator upgrade | Yes                           | Helm upgrade             | Image change        | Yes                         |
+------------------+-------------------------------+--------------------------+---------------------+-----------------------------+
| Database upgrade | Automated minor, manual major | No                       | Manual minor        | Manual mintor and major     |
+------------------+-------------------------------+--------------------------+---------------------+-----------------------------+
| Compute scaling  | Horizontal and vertical       | Horizontal and vertical  | Enterprise only     | Horizontal only             |
+------------------+-------------------------------+--------------------------+---------------------+-----------------------------+
| Storage scaling  | Manual                        | Manual                   | Enterprise only     | Enterprise only             |
+------------------+-------------------------------+--------------------------+---------------------+-----------------------------+

MongoDB topologies
******************

The next comparison is focused on replica sets, arbiters, sharding and other node types.

+---------------------------+-------------------------------+---------------------+---------------------+-----------------------------+
| Feature/Product           | Percona Operator for MongoDB  | Bitnami Helm Chart  | KubeDB for MongoDB  | MongoDB Community Operator  |
+===========================+===============================+=====================+=====================+=============================+
| Multi-cluster deployment  | Yes                           | No                  | No                  | No                          |
+---------------------------+-------------------------------+---------------------+---------------------+-----------------------------+
| Sharding                  | Yes                           | Yes, another chart  | Yes                 | No                          |
+---------------------------+-------------------------------+---------------------+---------------------+-----------------------------+
| Arbiter                   | Yes                           | Yes                 | Yes                 | Yes                         |
+---------------------------+-------------------------------+---------------------+---------------------+-----------------------------+
| Non-voting nodes          | Yes                           | No                  | No                  | No                          |
+---------------------------+-------------------------------+---------------------+---------------------+-----------------------------+
| Hidden nodes              | No                            | Yes                 | Yes                 | Yes                         |
+---------------------------+-------------------------------+---------------------+---------------------+-----------------------------+
| Network exposure          | Yes                           | Yes                 | Manual              | Enterprise only             |
+---------------------------+-------------------------------+---------------------+---------------------+-----------------------------+

Backups
*******

Here are the backup and restore capabilities of each solution.

+-------------------------+-------------------------------+---------------------+---------------------+-----------------------------+
| Feature/Product         | Percona Operator for MongoDB  | Bitnami Helm Chart  | KubeDB for MongoDB  | MongoDB Community Operator  |
+=========================+===============================+=====================+=====================+=============================+
| Scheduled backups       | Yes                           | No                  | Enterprise only     | Enterprise only             |
+-------------------------+-------------------------------+---------------------+---------------------+-----------------------------+
| Incremental backups     | No                            | No                  | Enterprise only     | No                          |
+-------------------------+-------------------------------+---------------------+---------------------+-----------------------------+
| Point-in-time recovery  | Yes                           | No                  | No                  | Enterprise only             |
+-------------------------+-------------------------------+---------------------+---------------------+-----------------------------+

Monitoring
**********

Monitoring is crucial for any operations team.

+------------------------------------------+-------------------------------+--------------------------------+--------------------------------+--------------------------------------+
| Feature/Product                          | Percona Operator for MongoDB  | Bitnami Helm Chart             | KubeDB for MongoDB             | MongoDB Community Operator           |
+==========================================+===============================+================================+================================+======================================+
| Custom exporters                         | Yes, through sidecars         | mongodb-exporter as a sidecar  | mongodb-exporter as a sidecar  | Integrate with prometheus operator   |
+------------------------------------------+-------------------------------+--------------------------------+--------------------------------+--------------------------------------+
| Percona Monitoring and Management (PMM)  | Yes                           | No                             | No                             | No                                   |
+------------------------------------------+-------------------------------+--------------------------------+--------------------------------+--------------------------------------+

Miscellaneous
*************

Finally, let's compare various features that are not a good fit for other categories.

+----------------------------------+-------------------------------+---------------------+-------------------------+-----------------------------+
| Feature/Product                  | Percona Operator for MongoDB  | Bitnami Helm Chart  | KubeDB for MongoDB      | MongoDB Community Operator  |
+==================================+===============================+=====================+=========================+=============================+
| Customize MongoDB configuration  | Yes                           | Yes                 | Yes                     | No, only some params        |
+----------------------------------+-------------------------------+---------------------+-------------------------+-----------------------------+
| Helm                             | Yes                           | Yes                 | Yes, for operator only  | Yes, for operator only      |
+----------------------------------+-------------------------------+---------------------+-------------------------+-----------------------------+
| SSL/TLS                          | Yes                           | Yes                 | Enterprise only         | Yes                         |
+----------------------------------+-------------------------------+---------------------+-------------------------+-----------------------------+
| Create users/roles               | No, only some params          | Yes                 | No                      | Yes                         |
+----------------------------------+-------------------------------+---------------------+-------------------------+-----------------------------+
