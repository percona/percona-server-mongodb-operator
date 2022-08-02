.. _compare:

Compare various solutions to deploy MongoDB in Kubernetes
=========================================================

There are multiple ways to deploy and manage MongoDB in Kubernetes. This article focuses on comparing the following open source solutions:

* `Bitnami Helm chart <https://github.com/bitnami/charts/tree/master/bitnami/mongodb>`_
* `KubeDB <https://github.com/kubedb>`_
* `MongoDB Community Operator <https://github.com/mongodb/mongodb-kubernetes-operator>`_
* `Percona Operator for MongoDB <https://github.com/percona/percona-server-mongodb-operator/>`_

.. list-table::
   :widths: 15 15 16 16 16
   :header-rows: 1

   * - Item / Solution
     - Bitnami Helm chart
     - KubeDB
     - MongoDB Community Operator
     - Percona Operator for MongoDB

   * - platform
     - string
     - ``kubernetes``
     - Override/set the Kubernetes platform: *kubernetes* or *openshift*. Set openshift on OpenShift 3.11+

   * - pause
     - boolean
     - ``false``
     - Pause/resume: setting it to ``true`` gracefully stops the cluster, and
       setting it to ``false`` after shut down starts the cluster back.
