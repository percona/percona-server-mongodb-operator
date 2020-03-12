Crash Recovery
=================

Percona Server for MongoDB Operator provides two ways of recovery in situations
when there was a cluster crash. 

* The automated :ref:`recovery-bootstrap` is the simplest one, but it
  may cause loss of several recent transactions.
* The manual :ref:`recovery-object-surgery` includes a lot of operations, but
  it allows to restore all the data.

.. _recovery-object-surgery:

Object Surgery Crash Recovery method
------------------------------------

.. warning:: This method is intended for advanced users only!

This method involves swapping PV/PVC associations from some Pod to Pod ``0``, so
that the cluster can be restarted in a safe manner with no negative impacts to
the cluster consistency.

Let's assume that a full crash have occurred for the cluster named ``cluster1``,
which is based on three PXC Pods. The Operator tries to start the Pod ``0``.

.. note:: The following instruction is written for PXC 8.0. The steps are also
   valid for PXC 5.7 unless specifically indicated otherwise.

1. Change the PXC image inside the cluster object (for several steps we will use
   the debug image instead of the original one):

   .. code-block:: bash

      $ kubectl patch pxc cluster1 --type="merge" -p '{"spec":{"pxc":{"image":"perconalab/percona-xtradb-cluster-operator:master-pxc8.0-debug"}}}'

   .. note:: For PXC 5.7 this command should be as follows:

      .. code-block:: bash

         $ kubectl patch pxc cluster1 --type="merge" -p '{"spec":{"pxc":{"image":"perconalab/percona-xtradb-cluster-operator:master-pxc5.7-debug"}}}'

2.  Restart the Pod ``0``:

   .. code-block:: bash

      $ kubectl delete pod cluster1-pxc-0 --force --grace-period=0 

3. Wait for the Pod ``0`` appearance, and execute the following code (required
   for the Pod liveness check):

   .. code-block:: bash

      $ for i in $(seq 0 $(($(kubectl get pxc cluster1 -o jsonpath='{.spec.pxc.size}')-1))); do until [[ $(kubectl get pod cluster1-pxc-$i -o jsonpath='{.status.phase}') == 'Running' ]]; do sleep 10; done; kubectl exec cluster1-pxc-$i -- touch /var/lib/mysql/sst_in_progress; done

4. Wait for all PXC Pods to start, then run

   .. code-block:: bash

      $ for i in $(seq 0 $(($(kubectl get pxc cluster1 -o jsonpath='{.spec.pxc.size}')-1))); do echo "###############cluster1-pxc-$i##############"; kubectl exec cluster1-pxc-$i -- cat /var/lib/mysql/grastate.dat; done

   Now find the Pod with the largest ``seqno`` in the output. This Pod will be
   the recovery donor. Let's assume it is ``cluster1-pxc-2``.

5. Now execute the following commands *in a separate shell*:

   .. code-block:: bash

      $ kubectl exec cluster1-pxc-2 -- sed -i 's/safe_to_bootstrap: 0/safe_to_bootstrap: 1/g' /var/lib/mysql/grastate.dat
      $ kubectl exec cluster1-pxc-2 -- sed -i 's/wsrep_cluster_address=.*/wsrep_cluster_address=gcomm:\/\//g' /etc/mysql/node.cnf
      $ kubectl exec cluster1-pxc-2 -- mysqld

   The ``mysqld`` process will start and initialize the database once again,
   and it will be available for the incoming connections.

6. Now go back to the original PXC image because the debug image is no longer
   needed:

   .. code-block:: bash

      $ kubectl patch pxc cluster1 --type="merge" -p '{"spec":{"pxc":{"image":"perconalab/percona-xtradb-cluster-operator:master-pxc8.0"}}}'

   .. note:: For PXC 5.7 this command should be as follows:

      .. code-block:: bash

         $ kubectl patch pxc cluster1 --type="merge" -p '{"spec":{"pxc":{"image":"perconalab/percona-xtradb-cluster-operator:master-pxc5.7"}}}'

7. Go back *to the previous shell* and restart all Pods except the
   ``cluster1-pxc-2`` Pod (the recovery donor).

   .. code-block:: bash

      $ for i in $(seq 0 $(($(kubectl get pxc cluster1 -o jsonpath='{.spec.pxc.size}')-1))); do until [[ $(kubectl get pod cluster1-pxc-$i -o jsonpath='{.status.phase}') == 'Running' ]]; do sleep 10; done; kubectl exec cluster1-pxc-$i -- rm /var/lib/mysql/sst_in_progress; done
      $ kubectl delete pods --force --grace-period=0 cluster1-pxc-0 cluster1-pxc-1

8. Wait for successful startup of the Pods which were deleted during the
   previous step, and finally remove the ``cluster1-pxc-2`` Pod:

   .. code-block:: bash

      $ kubectl delete pods --force --grace-period=0 cluster1-pxc-2

9. After the Pod startup the cluster is fully recovered.
