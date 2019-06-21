Scale Percona Server for MongoDB on Kubernetes and OpenShift
============================================================

One of the great advantages brought by Kubernetes and the OpenShift
platform is the ease of an application scaling. Scaling a Deployment up
or down ensures new Pods are created and set to available Kubernetes
nodes.

Size of the cluster is controlled by a ``size`` key in the Custom
Resource options configuration, as specified in the `Operator Options
section <operator.html>`__. That’s why scaling the cluster needs
nothing more but changing this option and applying the updated
configuration file. This may be done in a specifically saved config, or
on the fly, using the following command, which saves the current
configuration, updates it and applies the changed version:

.. code:: bash

   $ kubectl get psmdb/my-cluster-name -o yaml | sed -e 's/size: 3/size: 5/' | kubectl apply -f -

In this example we have changed the size of the Percona Server for
MongoDB from ``3``, which is a minimum recommended value, to ``5``
nodes.

**Note:** *Using ``kubectl scale StatefulSet_name`` command to rescale
Percona Server for MongoDB is not recommended, as it makes ``size``
configuration option out of sync, and the next config change may result
in reverting the previous number of nodes.*
