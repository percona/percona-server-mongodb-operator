# Allow downscaling replicaSets in a safe manner

* Date: 2021-06-15

## Status

* Status: proposed

## Context

Currently there are no mechanisms to stop users downscaling MongoDB replicaSets
in a manner that causes cluster failure. For example, if an user has a 7 member
replicaSet and downscales it to 3 members, the cluster is going to lose
majority (4 members) and the operator won't be able to run `replSetReconfig`
because of that.

## Considered Options

* Compare current statefulset size with replicaSet size in CR and downscale one
  by one on each reconciliation until reaching the target size.
* Compare current statefulset size with replicaSet size in CR and if the target
  size breaks the majority throw an error.
* Rather than populating replicaSet members based on a set of pods that selected
  by labels, we could fetch pods one by one in respect to replicaSet size and
  thus removing the excess pods from replicaSet config *hopefully* before they
  become inaccessible.
* ~Use [admission controllers](https://kubernetes.io/docs/reference/access-authn-authz/admission-controllers/) to validate size updates.~ For every CRD, there can be only one webhook in endpoint in a cluster. PSMDB Operator doesn't work cluster wide
* Document this issue and let the end users decide

## Decision

Chosen option: We will compare the current statefulset size with the replicaSet size in
the CR and downscale one by one on each reconciliation until reaching the target
size.

## Consequences

Users can downscale their clusters to any size they like in a single step.

### Negative Consequences

We will be mutating the replicaSet size field in CR to downscale one by one. If
CR is updated by operator before the replicaSet reachs the target size, we'll
overwrite the user's changes on size field. For instance, in the `writeStatus`
method, we're trying to update the status subresource but if it fails we update
the whole CR.
