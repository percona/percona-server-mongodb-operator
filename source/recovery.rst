Crash Recovery
=================

Percona Server for MongoDB Operator provides two ways of recovery in situations
when there was a cluster crash. 

* The automated :ref:`recovery-bootstrap` is the simplest one, but it
  may cause loss of several recent transactions.
* The manual :ref:`recovery-object-surgery` includes a lot of operations, but
  it allows to restore all the data.

.. _recovery-bootstrap:

Bootstrap Crash Recovery method
-------------------------------

In this case recovery is done automatically. The recovey is triggered by the
``forceBootstrap`` option set to ``true`` in the ``deploy/cr.yaml`` file.

Doing this makes cluster to start, however there may exist data inconsistency
in the cluster, and several last transactions may be lost. For situations when
such data loss is undesirable, there is a more advanced manual method described
in the next chapter.

