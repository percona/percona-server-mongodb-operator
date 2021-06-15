# Allow downscaling replicaSets in a safe manner

* Date: 2021-06-15

## Status

* Status: proposed

## Context

Currently there are no mechanisms to stop users downscaling MongoDB replicaSets
in a manner that causes cluster crash. For example, if an user has a 7 member
replicaSet and downscales it to 3 members, the cluster is going to lose
majority (4 members) and the operator won't be able to run `replSetReconfig`
because of that.

## Considered Options

* [PR
  695](https://github.com/percona/percona-server-mongodb-operator/pull/695):
Rather than populating replicaSet members based on a set of pods that selected
by labels, we could fetch pods one by one in respect to replicaSet size and
thus removing the excess pods from replicaSet config *hopefully* before they
become inaccessible.
* Use [admission
  controllers](https://kubernetes.io/docs/reference/access-authn-authz/admission-controllers/) to validate size updates
* Document this issue and let the end users decide

## Decision

Chosen option: TBD

## Consequences

TBD

### Negative Consequences

TBD
