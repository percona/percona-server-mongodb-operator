Binding Percona Server for MongoDB components to Specific Kubernetes/OpenShift Nodes
==========================================================================================

The operator does a good job of automatically assigning new pods to nodes to achieve balanced distribution across the cluster.
There are situations when you must ensure that pods land
on specific nodes: for example, for the advantage of speed on an SSD-equipped machine, or reduce costs by choosing nodes in the same
availability zone.

The appropriate (sub)sections (``replsets``, ``replsets.arbiter``, ``backup``, etc.) of the
`deploy/cr.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/main/deploy/cr.yaml>`_
file contain the keys which can be used to do assign pods to nodes.

Node selector
-------------

The ``nodeSelector`` contains one or more key-value pairs. If the node is
not labeled with each key-value pair from the Pod’s ``nodeSelector``,
the Pod will not be able to land on it.

The following example binds the Pod to any node having a
self-explanatory ``disktype: ssd`` label:

.. code-block:: yaml

   nodeSelector:
     disktype: ssd

Affinity and anti-affinity
--------------------------

Affinity defines eligible pods that can be scheduled on the node which already has pods with specific labels. Anti-affinity defines pods that are not eligible. This approach is reduces costs by ensuring several pods with intensive data exchange  occupy the
same availability zone or even the same node or, on the contrary, to
spread the pods on different nodes or even different availability zones
for high availability and balancing purposes.

Percona Distribution for MongoDB Operator provides two approaches for doing
this:

-  simple way to set anti-affinity for Pods, built-in into the Operator,
-  more advanced approach based on using standard Kubernetes
   constraints.

Simple approach - use antiAffinityTopologyKey of the Percona Distribution for MongoDB Operator
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Percona Distribution for MongoDB Operator provides an
``antiAffinityTopologyKey`` option, which may have one of the following
values:

-  ``kubernetes.io/hostname`` - Pods will avoid residing within the same
   host,
-  ``failure-domain.beta.kubernetes.io/zone`` - Pods will avoid residing
   within the same zone,
-  ``failure-domain.beta.kubernetes.io/region`` - Pods will avoid
   residing within the same region,
-  ``none`` - no constraints are applied.

The following example forces Percona Server for MongoDB Pods to avoid
occupying the same node:

.. code-block:: yaml

   affinity:
     antiAffinityTopologyKey: "kubernetes.io/hostname"

Advanced approach - use standard Kubernetes constraints
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

The previous method can be used without special knowledge of the Kubernetes way
of assigning Pods to specific nodes. Still, in some cases, more complex
tuning may be needed. In this case, the ``advanced`` option placed in the
`deploy/cr.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/main/deploy/cr.yaml>`_
file turns off the effect of the ``antiAffinityTopologyKey`` and allows
the use of the standard Kubernetes affinity constraints of any complexity:

.. code-block:: yaml

   affinity:
      advanced:
        podAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
          - labelSelector:
              matchExpressions:
              - key: security
                operator: In
                values:
                - S1
            topologyKey: failure-domain.beta.kubernetes.io/zone
        podAntiAffinity:
          preferredDuringSchedulingIgnoredDuringExecution:
          - weight: 100
            podAffinityTerm:
              labelSelector:
                matchExpressions:
                - key: security
                  operator: In
                  values:
                  - S2
              topologyKey: kubernetes.io/hostname
        nodeAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
            nodeSelectorTerms:
            - matchExpressions:
              - key: kubernetes.io/e2e-az-name
                operator: In
                values:
                - e2e-az1
                - e2e-az2
          preferredDuringSchedulingIgnoredDuringExecution:
          - weight: 1
            preference:
              matchExpressions:
              - key: another-node-label-key
                operator: In
                values:
                - another-node-label-value

See explanation of the advanced affinity options `in Kubernetes
documentation <https://kubernetes.io/docs/concepts/configuration/assign-pod-node/#inter-pod-affinity-and-anti-affinity-beta-feature>`__.

Tolerations
-----------

*Tolerations* allow Pods having them to be able to land onto nodes with
matching *taints*. Toleration is expressed as a ``key`` with and
``operator``, which is either ``exists`` or ``equal`` (the equal
variant requires a corresponding ``value`` for comparison).

Toleration should have a specified ``effect``, such as the following:

  * ``NoSchedule`` -  less strict
  * ``PreferNoSchedule``
  * ``NoExecute``

When a *taint* with the ``NoExecute`` effect is assigned to a node, any pod configured to not tolerating this *taint* is removed from the node. This removal can be immediate or after the ``tolerationSeconds`` interval. The following example defines this effect and the removal interval:

.. code-block:: yaml

   tolerations:
   - key: "node.alpha.kubernetes.io/unreachable"
     operator: "Exists"
     effect: "NoExecute"
     tolerationSeconds: 6000

The `Kubernetes Taints and
Toleratins <https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/>`_
contains more examples on this topic.

Priority Classes
----------------

Pods may belong to some *priority classes*. This flexibility allows the scheduler to
distinguish more and less important Pods when needed, such as the situation when
a higher priority Pod cannot be scheduled without evicting a lower
priority one. This ability can be accomplished by adding one or more PriorityClasses in
your Kubernetes cluster, and specifying the ``PriorityClassName`` in the
`deploy/cr.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/main/deploy/cr.yaml>`_
file:

.. code-block:: yaml

   priorityClassName: high-priority

See the `Kubernetes Pods Priority and Preemption
documentation <https://kubernetes.io/docs/concepts/configuration/pod-priority-preemption>`_
to find out how to define and use priority classes in your cluster.

Pod Disruption Budgets
----------------------

Creating the `Pod Disruption
Budget <https://kubernetes.io/docs/concepts/workloads/pods/disruptions/>`_
is the Kubernetes method to limit the number of Pods of an application
that can go down simultaneously due to  *voluntary disruptions* such as the
cluster administrator’s actions during a deployment update. Distribution Budgets allow large applications
to retain their high availability during maintenance and other
administrative activities. The ``maxUnavailable`` and ``minAvailable``
options in the
`deploy/cr.yaml <https://github.com/percona/percona-server-mongodb-operator/blob/main/deploy/cr.yaml>`_
file can be used to set these limits. The recommended variant is the
following:

.. code-block:: yaml

   podDisruptionBudget:
      maxUnavailable: 1
