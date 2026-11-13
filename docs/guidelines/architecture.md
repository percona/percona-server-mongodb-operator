# Architecture

Last verified: 2026-11-03

Where code goes and how the controllers behave. The root `AGENTS.md` has the
layout and code placement basics; this file adds what a change must respect.
The review skill `review-architecture` applies it.

## Components

- `cmd/manager/main.go`: the operator binary. It registers the schemes
  (client-go, psmdb, VolumeSnapshot, cert-manager, VPA), sets up leader
  election and the `WATCH_NAMESPACE` cache, and adds the controllers listed in
  `pkg/controller/controller.go`.
- `cmd/mongodb-healthcheck/`: the liveness and readiness probe binary shipped
  in the operator image (`build/Dockerfile`).
- `pkg/apis/psmdb/v1`: the only API version. `psmdb_types.go` holds
  `PerconaServerMongoDB`; backup, restore and cluster-sync CRs have their own
  `*_types.go`. `psmdb_defaults.go` holds `CheckNSetDefaults`, which both
  validates and defaults. `zz_generated.deepcopy.go` is generated.
- `pkg/controller/<name>`: one package per CR (`perconaservermongodb`,
  `perconaservermongodbbackup`, `perconaservermongodbrestore`,
  `perconaservermongodbclustersync`), each registered from its `add_*.go`.
- `pkg/psmdb`: builders for StatefulSets, Services, mongos, PDBs and
  containers, plus the MongoDB client constructors. One subpackage per
  domain: `backup` (PBM wrapper, interface `PBM`), `mongo` (driver wrapper,
  interface `Client`), `tls` (cert-manager and manual certificates), `secret`,
  `init`, `pmm`, `logcollector`, `clustersync`, `vectorsearch`, `config`.
- `pkg/naming`: label keys, label builders (`ClusterLabels`, `MongodLabels`,
  `RSLabels`, ...), object name builders and finalizer constants. New names,
  labels and annotations go here, not in string literals.
- `pkg/k8s` (annotations, Leases, watch namespace), `pkg/util` (`Apply`,
  `ReconcileInterval`), `pkg/version` (`version.txt` is the operator
  version), `pkg/secret` (secret providers, `vault/`), `pkg/mcs`,
  `pkg/versionservice`, and `clientcmd/` (pod exec and REST client).
- `deploy/`: `crd.yaml`, `bundle.yaml` and `cw-bundle.yaml` are built by
  `make manifests` from `config/`. `rbac.yaml`, `cw-rbac.yaml`,
  `operator.yaml` and `cw-operator.yaml` are edited by hand; there are no
  `+kubebuilder:rbac` markers. `cr.yaml` is the documented example and most
  of it is commented options.

## Reconcile loop of PerconaServerMongoDB

`Reconcile` in `pkg/controller/perconaservermongodb/psmdb_controller.go`
runs these steps in order:

1. Lock the per-CR mutex, `Get` the CR, return on NotFound.
2. Defer `updateStatus`, so every exit path writes the status.
3. `setCRVersion`, then `cr.CheckNSetDefaults`. A CR that fails validation
   ends in state `error` with the message "wrong psmdb options".
4. Finalizers when `DeletionTimestamp` is set (`checkFinalizers`, in the
   order of `GetOrderedFinalizers`).
5. Pause handling, configuration checks, safe downscale.
6. Secrets, config maps, users, TLS (`reconcileSSL`), security keys.
7. Backups, VPA, replica sets (`reconcileReplsets` returns the cluster
   state), mongos, FCV upgrade, orphan PVCs, custom users, exported services,
   scheduled jobs, PBM.
8. Return `reconcile.Result{RequeueAfter: r.reconcileIn}`. The interval comes
   from `RECONCILE_INTERVAL`; the minimum and default is 5 seconds.

Rules:

- A new step is a function called from `Reconcile` at the position its
  dependencies require. It returns a wrapped error and does not write status
  itself. Status goes through `updateStatus` and `updateCondition`
  (`status.go`, `conditions.go`), which retry on conflict.
- On error return `reconcile.Result{}, err`; controller-runtime requeues with
  backoff. Use `RequeueAfter` only to wait for an external condition, as
  `reconcileSSL` does with `errTLSNotReady`.
- Steps are idempotent. Write objects through `r.createOrUpdate`,
  `util.Apply` (hash annotation `percona.com/last-config-hash`) or
  `controllerutil.CreateOrUpdate`, and set the owner reference (`cr.OwnerRef`
  or `SetControllerReference`).
- The controller watches only its own CR (`For()` without `Owns` or
  `Watches`), so changes to child objects are seen on the next interval.
  Adding watches changes the reconcile load of every cluster; it needs a
  proposal.
- Cluster states are the `AppState` constants (`initializing`, `stopping`,
  `paused`, `ready`, `error`, `sharding`). Conditions use `AddCondition`,
  `FindCondition` and `RemoveCondition`.

## Backups and restores

- The backup controller runs a `backupExecutor` (`Start`, `Status`, `PBM`):
  `managedBackups` for PBM backups, `snapshotBackups` for external
  VolumeSnapshot backups. States: new (empty), `waiting`, `requested`,
  `running`, `error`, `ready`; `ready` and `error` are terminal.
- The restore controller dispatches on the backup type: logical, physical or
  incremental (PBM), or external (snapshot, split into one function per
  state). States: empty, `waiting`, `requested`, `rejected`, `running`,
  `error`, `ready`.
- PBM is reached only through the `backup.PBM` interface, injected as
  `newPBM`. A new PBM call is added to the interface, to the fake in
  `pkg/psmdb/backup/fake`, and to the generated `mock_pbm.go` (run
  `go generate`).
- Backups and cluster sync coordinate through Leases (`pkg/k8s/lease.go`)
  and the release-lock finalizer.

## Kubernetes and MongoDB access

- Use the reconciler's controller-runtime client (`r.client`; `r.apiReader`
  bypasses the cache). The code base has no typed clientset.
- Commands inside pods run through `clientcmd.Client.Exec`.
- MongoDB connections come from `MongoClientProvider` (`connections.go`),
  which reads credentials from the users Secret (`api.UserSecretName(cr)`)
  and dials through `pkg/psmdb/mongo`. Inject the provider in tests; do not
  build a client inline.
- cert-manager is reached through `pkg/psmdb/tls` (`NewCertManagerController`,
  fake in `tls/fake`).

## Changing the CR

- Add the field to `pkg/apis/psmdb/v1/*_types.go` with the kubebuilder
  markers in use (`default`, `validation:Enum`, `validation:Minimum`,
  `validation:XValidation`, `printcolumn`). Run `make generate manifests` and
  commit `zz_generated.deepcopy.go`, `config/crd/bases/*.yaml`,
  `deploy/crd.yaml`, `deploy/bundle.yaml`, `deploy/cw-bundle.yaml` and
  `e2e-tests/version-service/conf/crd.yaml`. CI fails on drift (job
  `manifests` in `.github/workflows/reviewdog.yml`).
- Defaults and cross-field validation go into `CheckNSetDefaults` or a
  `validate*` function it calls, not into the reconciler.
- Gate new behavior with `cr.CompareVersion("<version>") >= 0`, using the
  version that ships it (`pkg/version/version.txt`). Clusters keep their old
  behavior until the user raises `crVersion`.
- Document the field as a commented example in `deploy/cr.yaml`. The Helm
  chart (`percona-helm-charts`) and the user docs (`k8spsmdb-docs`) live in
  their own repos; the PR checklist asks for both.
- Finalizer names are constants in `pkg/naming` with the `percona.com/`
  prefix; CRs older than 1.17.0 use the legacy unprefixed names.
- New API groups or verbs go into `deploy/rbac.yaml` and `deploy/cw-rbac.yaml`
  by hand, followed by `make manifests` to rebuild the bundles.

## Compatibility

- An operator upgrade must not change a running cluster: new behavior is
  gated by `crVersion`, new fields have defaults, status changes are
  additive.
- New validation is new behavior. A check that rejects a spec the previous
  operator version accepted is gated by `crVersion`, even when the spec was
  already wrong: a CR that fails `CheckNSetDefaults` also stops its backups,
  restores and cluster sync. The 1.16.0 safety checks (`checkSafeDefaults` in
  `pkg/apis/psmdb/v1/psmdb_defaults.go`) are the pattern.
- `e2e-tests/release_versions` lists the platform and image versions CI
  uses; the upgrade e2e tests (`upgrade`, `upgrade-sharded`,
  `upgrade-consistency*`) cover the previous operator version.
- The PR checklist asks about the oldest and newest supported MongoDB and
  Kubernetes versions; say in the PR how the change behaves on each.

## Proposals

A change that adds or changes CR fields, changes backup, restore, upgrade or
TLS behavior, or touches more than one controller gets a proposal in
`docs/enhancements/<TICKET>-<slug>/proposal.md`, from
`docs/enhancements/TEMPLATE.md`. The template asks for decisions, constraints
and rationale, not file paths.
