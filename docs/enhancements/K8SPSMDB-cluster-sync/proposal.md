# K8SPSMDB-1610: Integrate Percona ClusterSync for MongoDB

| Field        | Value                                    |
|--------------|------------------------------------------|
| Author       | George Kechagias                         |
| Status       | Draft                                    |
| Created      | 2026-05-12                               |
| Last Updated | 2026-07-02                               |
| Reviewers    |                                          |

---

## 1. Overview

The operator will deploy and manage Percona ClusterSync for MongoDB (PCSM) declaratively through a new `PerconaServerMongoDBClusterSync` CRD, enabling users to replicate data between MongoDB deployments in real time or perform one-time migrations with near-zero downtime. Today users must manage PCSM manually outside the operator. This benefits any team needing cross-cluster replication or migration without manual process orchestration.

### 1.1 Goals

- Provide a fully declarative way to set up cross-cluster replication and migrations through a dedicated CRD
- Manage PCSM lifecycle (start, pause, resume, reset, finalize) within the operator reconciliation loop, driven exclusively by explicit user intent expressed on the CR — the controller never auto-recovers from failed states and never auto-restarts after a reset
- Expose replication status and lag in the CR status for observability
- Keep PCSM lifecycle independent of the target `PerconaServerMongoDB` CR — adding or removing replication does not require modifying the cluster CR

### 1.2 Non-Goals (Out of Scope)

- Reverse synchronization: PCSM does not support this upstream. Revisit when upstream adds support.
- Multi-source or multi-target replication: PCSM only supports single source/target pairs.
- Persistent Query Settings migration: PCSM does not replicate Persistent Query Settings (MongoDB 8+). Migration requires manual export/import via `$querySettings` aggregation and `setQuerySettings` admin command after finalization. The operator will not automate this.

### 1.3 Deferred (Future Iterations)

- Source cluster reference by CR name (`sourceCluster`): the operator manages both source and target clusters, so it could resolve connection details and create the source user automatically from a CR name reference instead of requiring manual `source.uri` and `source.credentialsSecret`. Deferred to a future iteration; first iteration uses a raw URI + Secret.
    - *Design constraint to remember when this lands:* the source URI built by the operator must honor the source PSMDB CR's `clusterServiceDNSMode`, the same way the target URI already does for the target. Today the source URI is user-supplied, so `clusterServiceDNSMode` does not apply — once the operator starts constructing it from a CR reference, the source side picks up the same DNS-mode responsibility as the target side.
- Version service integration for PCSM image: the PSMDB CR's version service flow auto-fills `spec.image`, `spec.backup.image`, and `spec.pmm.image`. The version service response does not yet expose a PCSM image, so the first iteration requires `spec.image` on the ClusterSync CR. Revisit once PCSM is added to the version service.
- PMM integration: may be added in a future iteration if monitoring of PCSM through PMM is needed.
- Exposing PCSM outside the cluster (Service, `spec.expose`): the first iteration does not create a Service for the PCSM Deployment. Operator-to-PCSM communication happens via `kubectl exec` into the pod (see §3.3), and PCSM 0.9.0 binds its own HTTP server to `127.0.0.1` only, so there is no port worth exposing externally today. Revisit alongside the upstream fix that lets PCSM bind to `0.0.0.0` (PCSM-345).
- Cluster-pause coordination with the PSMDB reconciler: the ADR originally proposed the PSMDB reconciler signalling the ClusterSync controller to `pcsm pause` before scaling MongoDB down for `spec.pause=true`. That coordination is **not** implemented in the first iteration — pausing the target cluster while PCSM is running just leaves PCSM to observe the disconnect and either report `failed` or self-recover. Revisit once operational feedback shows the manual "pause PCSM first" workflow is a real pain point.
- Admission webhook for mode transitions and source immutability: the ADR originally described admission-time rejection of invalid transitions and source changes. Not implemented in the first iteration — invalid transitions are no-ops in the controller (see §5.4), and source-field edits take effect only when the PCSM pod restarts. Revisit if the no-op behavior surprises users in practice.
- Post-finalize resource cleanup (proactive Deployment/Secret deletion) and the `percona.com/delete-clustersync-after-finalize` opt-in finalizer: not implemented in the first iteration. Finalized CRs keep their Deployment and Secrets around until the user deletes the CR (child resources GC via ownerReferences). See §5.5 for the current behavior.
- High availability / multiple PCSM instances per ClusterSync CR: the first iteration runs a single PCSM instance (`replicas: 1`, `Recreate` strategy). Constraint 6 forbids two PCSM processes writing to the same target simultaneously, so any future HA must be active/passive. Today's failover is pod restart + checkpoint recovery (Observation 2).

---

## 2. Background

### 2.1 Core Concepts

- **Percona ClusterSync for MongoDB (PCSM):** A standalone binary that replicates data between two MongoDB deployments. It performs an initial sync followed by real-time replication.
- **Initial sync:** The first phase of replication. PCSM clones all data from the source to the target, then applies all changes that occurred since the clone started. For sharded clusters, PCSM first retrieves shard key information from the source and creates collections on the target with the same shard keys before copying data. Cannot resume after failure; must restart from scratch.
- **Real-time replication:** After initial sync completes, PCSM captures change stream events from the source and applies them to the target, ensuring real-time synchronization. Resumes from the last stored checkpoint after restart.
- **Checkpoint:** A persisted position in the source's change stream that allows PCSM to resume real-time replication after a restart without re-running initial sync.
- **Finalization:** A one-time operation that completes the migration. PCSM finalizes replication, creates required indexes on the target, and stops. After finalization, starting PCSM again begins a new initial sync from scratch.
- **PCSM workflow:** `start` (begin replication) → initial sync → real-time replication → `pause`/`resume` (control replication) → `finalize` (complete migration) → cutover (switch clients to target).
- **PCSM control channel (CLI via `kubectl exec`):** PCSM ships a `pcsm` CLI binary in the container image. The operator runs `pcsm status`, `pcsm start`, `pcsm pause`, `pcsm resume [--from-failure]`, and `pcsm finalize` via `kubectl exec` into the running PCSM container to control the lifecycle. PCSM also exposes an HTTP API on port 2242, but as of PCSM 0.9.0 that server binds to `127.0.0.1` only (upstream tracked as PCSM-345), so the operator does not use it and no Service is created for the Deployment; container-local `curl http://127.0.0.1:2242/status` is used only by the pod's readiness/liveness probes. Every CLI verb is driven by an explicit user-set value of `spec.mode` (see §4.2), with a single exception (`state=failed` + `spec.mode=running` auto-fires `pcsm resume --from-failure`, see §3.4). The controller only issues a call when `spec.mode != status.mode`; after the call succeeds it mirrors the value into `status.mode` via the status subresource. The controller **never** writes back to spec, so re-applying the same manifest is idempotent and GitOps tools like Argo CD never see drift.

### 2.2 Key Constraints

1. **Same major version required:** PCSM only supports replication between the same major MongoDB version (e.g., 7.x to 7.x). Cross-major replication is not supported.
2. **Single source/target pair:** PCSM supports only one source and one target cluster per instance.
3. **Sharded clusters have limitations:** PCSM can replicate sharded cluster data but does not replicate sharding metadata. Certain admin commands (`movePrimary`, `reshardCollection`, `unshardCollection`, `refineCollectionShardKey`) break replication and force a full initial sync restart.
4. **Initial sync is not resumable:** If PCSM crashes or restarts during initial sync, it restarts from scratch. For large datasets this can mean hours or days of work lost.
5. **Primary-only connection:** PCSM connects only to the primary node; the `directConnection` option to force secondary connections is ignored.
6. **Recreate deployment strategy required:** Running two PCSM instances against the same target simultaneously can corrupt data, so RollingUpdate is not safe.
7. **Unsupported data types:** Queryable encryption, timeseries collections, capped collections via `cloneCollectionAsCapped`/`convertToCapped`, `system.*` collections, clustered collections with TTL indexes, Percona Memory Engine, Persistent Query Settings (MongoDB 8+), and documents with field names containing periods/dollar signs are all unsupported.

---

## 3. Architecture

### 3.1 Architecture Before This Change

PCSM is managed entirely outside the operator. Users must:
1. Manually deploy PCSM as a standalone process or container
2. Manually construct connection strings with credentials
3. Manually run `pcsm start`, monitor status, handle failures
4. Manually integrate with monitoring

```
User
  → Manual PCSM deployment
    → Source MongoDB cluster
    → Target MongoDB cluster (operator-managed)
  ← Manual status monitoring
```

### 3.2 Architecture After This Change

The operator introduces a new CRD, `PerconaServerMongoDBClusterSync`, with its own controller. The controller deploys PCSM as a separate Deployment and manages its full lifecycle declaratively. The PSMDB CR is not modified; a ClusterSync CR references the target cluster by name.

```
PerconaServerMongoDBClusterSync (CR)
  → ClusterSync Controller
    → Resolve target PSMDB CR (spec.clusterName, same namespace)
    → Acquire ClusterSync Lease (psmdb-<clusterName>-clustersync-lock)
    │   → Also serves as the "cluster busy" signal for backup/restore admission (§8.4)
    → Reconcile Secrets
    │   → Read source.credentialsSecret (user-provided, source credentials)
    │   → Read/create <cr>-pcsm-target-user Secret (operator-managed, target credentials)
    │   → Create <cr>-pcsm-secret with `source-uri` and `target-uri` keys
    │       (source URI = spec.source.uri with credentials percent-encoded in;
    │        target URI = auto-constructed from target PSMDB CR + syncTargetUser)
    → Reconcile PCSM Deployment (replicas: 1, Recreate strategy)
    │   → PCSM container
    │       envFrom: <cr>-pcsm-secret → PCSM_SOURCE_URI, PCSM_TARGET_URI
    │       args:   --log-level, --log-json (from spec.pcsmConfig)
    │       probes: exec `curl http://127.0.0.1:2242/status`
    → Control PCSM via `kubectl exec` into the pod (no Service, no HTTP calls)
    │   → `pcsm status`                    → observed state, lag, error
    │   → `pcsm start [--exclude-namespaces=...]` (paused → running, first time)
    │   → `pcsm resume [--from-failure]`   (paused → running, or state=failed self-heal)
    │   → `pcsm pause`                     (running → paused)
    │   → `pcsm finalize`                  (→ finalized)
    → Update ClusterSync CR status (state, lagTimeSeconds, error, conditions, mode)
    → On finalize: release the ClusterSync Lease so backups/restores can proceed
  ← Status update on ClusterSync CR

PerconaServerMongoDB (target CR, unchanged)
  → PSMDB Reconciler (existing) — no direct coordination with ClusterSync in the first iteration
  → Backup controller — checks the ClusterSync Lease; waits while it is held (§8.4)
  → Restore controller — checks the ClusterSync Lease; waits while it is held (§8.4)
```

**Resources managed by the operator for PCSM (owned by the ClusterSync CR):**

| Resource | Purpose |
|----------|---------|
| Deployment (`<cr>-pcsm`) | Runs the PCSM container. `replicas: 1`, `Recreate` strategy. No Service; the operator talks to PCSM through `kubectl exec`. Kept until CR deletion (GC via ownerReferences). |
| Secret (`<cr>-pcsm-secret`) | Holds `source-uri` and `target-uri` with credentials percent-encoded in. The PCSM container reads both via `envFrom.SecretKeyRef` (`PCSM_SOURCE_URI`, `PCSM_TARGET_URI`), so URIs and credentials never appear in the pod spec or the CR spec. Kept until CR deletion. |
| Secret (`<cr>-pcsm-target-user`) | Target cluster credentials for the operator-provisioned MongoDB user (`clustersync-<cr-name>`). Kept until CR deletion (GC via ownerReferences). The underlying MongoDB user on the target cluster is **not** dropped in either case (see §5.3). |
| Lease (`psmdb-<clusterName>-clustersync-lock`) | Cluster-ownership lock in the same namespace as the target PSMDB CR. Guarantees at-most-one active ClusterSync CR per (namespace, clusterName) and doubles as the backup/restore admission signal (§8.4). Released when `status.state=finalized` or on CR deletion. |
| Secret (`source.credentialsSecret`) | Source cluster credentials, created by the user. Read-only for the operator; not owned by the CR. |

The controller also registers an internal finalizer (`internal.percona.com/release-lock`) on the CR to guarantee the ClusterSync Lease is released even when the CR is force-deleted; no user-facing finalizer strings exist in the first iteration.

**Reconciliation flow:**

1. **Cluster-ownership Lease:** Before touching the target cluster or provisioning the
   PCSM Deployment, the controller acquires the ClusterSync Lease
   (`psmdb-<clusterName>-clustersync-lock`) in the ClusterSync CR's namespace. A second CR
   pointing at the same (namespace, `clusterName`) sees a foreign holder and its reconcile
   surfaces the collision through `status.error` until the incumbent releases the Lease.
   The Lease doubles as the backup/restore admission signal (§8.4). The controller also
   registers an internal finalizer (`internal.percona.com/release-lock`) on the CR so the
   Lease is released even if the CR is force-deleted.

2. **Deployment and secret reconciliation:** On each reconcile the controller ensures the
   URI Secret (`<cr>-pcsm-secret`, keys `source-uri` and `target-uri`) and the
   `<cr>-pcsm-target-user` Secret exist, then applies the PCSM Deployment
   (`replicas: 1`, `Recreate` strategy — see §5.1, §5.2, and §1.3). No Service is created;
   the operator reaches the pod through `kubectl exec` instead. On CR deletion the
   internal finalizer releases the Lease and all owned children GC via ownerReferences;
   the `syncTargetUser` MongoDB user on the target is intentionally left in place (see
   §5.3). Post-finalize cleanup is limited to releasing the Lease so backups/restores
   can proceed (see §5.5) — Deployment and Secrets stay until the CR is deleted.

3. **Lifecycle control via `kubectl exec`:** After the Deployment is ready, the controller
   runs `pcsm status` inside the container to read observed state. It then compares
   `spec.mode` against `status.mode` (the last user intent the controller has already
   acted on). When the two are equal, the controller only refreshes observed status. When
   they differ, the controller runs exactly one transition command and, on success,
   mirrors `spec.mode` into `status.mode` via the status subresource:

   | Transition (`status.mode` → `spec.mode`) | CLI command | Notes |
   |------|------|------|
   | (empty / first reconcile) → `running` | `pcsm start [--exclude-namespaces=...]` | Default on new CRs — `spec.mode` defaults to `running`. |
   | (empty / first reconcile) → `paused` | (no command; mirror only) | User opted into a two-step create-then-start. |
   | `paused` → `running` | `pcsm start` if PCSM has never started; otherwise `pcsm resume` | Post-start transitions always use `resume` regardless of how many pauses have passed. |
   | `running` → `paused` | `pcsm pause` | |
   | `running` or `paused` → `finalized` | `pcsm finalize` once PCSM reports caught up | Terminal. `paused → finalized` requires that PCSM had previously started (`status.startedAt != nil`). |

   Any other transition (e.g., `finalized → *`, `paused → finalized` before any start,
   or the value collapsing to itself) is a no-op in the controller — `nextAction`
   returns `actionNone` and `status.mode` is left untouched. There is no admission
   webhook in the first iteration (see §5.4). Idempotency is preserved either way:
   re-applying the same manifest fires no CLI command and mutates no spec.

   In addition to the transition matrix above, the controller applies one deliberate
   self-heal: if `spec.mode=running`, `status.mode=running`, PCSM has started at least
   once, **and** observed `state=failed`, the controller runs `pcsm resume --from-failure`
   without waiting for a spec change. This is documented in §3.4 as the single exception
   to "no automatic actions."

   Before firing any command that would cause PCSM to write to the target cluster
   (`start` or `resume`), the controller consults the backup Lease and the list of
   in-flight `PerconaServerMongoDBRestore` CRs for the same cluster. If either is
   active, the transition is held, `status.error` explains why, and a `ClusterBusy`
   event is emitted; the transition retries on the next reconcile.

4. **Status propagation:** The controller maps the `pcsm status` JSON payload to CR
   status fields:
   - `state` → `status.state` (PCSM-reported: `idle`, `running`, `paused`, `finalizing`, `finalized`, `failed`). This reflects what PCSM is actually doing, independently of user intent in `spec.mode`. PCSM 0.9.0 does not distinguish initial-sync from real-time replication in this field — both are `running`.
   - `lagTimeSeconds` → `status.lagTimeSeconds`
   - `error` → `status.error` (also populated with local errors, e.g., "cluster busy" or an unreachable PCSM pod).
   - `status.startedAt` is set once, the first time PCSM reports `state=running`. It is
     not updated on subsequent restarts.
   - `status.mode` is written by the controller only after the corresponding CLI command
     succeeds (item 3 above); it represents the last user intent the controller has
     applied. It is not derived from `pcsm status`.
   - `status.conditions` carries two condition types: `Running` (`True` while `state=running`,
     otherwise `False` with reason `PCSMNotRunning`) and `Finalized` (`True` once
     `state=finalized`). Their `LastTransitionTime` captures transition history without
     needing dedicated timestamp fields.

5. **Interaction with the PSMDB reconciler:** No direct coordination in the first
   iteration. The PSMDB reconciler does **not** consult ClusterSync CRs, and cluster
   pause (`spec.pause=true` on the PSMDB CR) does not signal PCSM. The two controllers
   share state only through the ClusterSync Lease (§4.1), which the backup and restore
   controllers on the target cluster consult before starting new operations:
   - The backup controller waits (Backup state `Waiting`) if the ClusterSync Lease is
     held for its target cluster. PBM holds the backup cursor open for the duration of
     the backup; with PCSM continuously applying source writes onto the target, the
     cursor pins WiredTiger history and disk usage can grow unbounded.
   - The restore controller waits under the same signal. A restore overwrites data PCSM
     is actively replicating, so it must wait until PCSM finalizes (Lease released) or
     the ClusterSync CR is deleted (Lease released via the internal finalizer).

### 3.3 Key Observations

1. **PCSM is a standalone process:** It does not need to run on every replset member, making a separate Deployment the natural fit rather than a sidecar.
2. **PCSM auto-recovers on pod restart (PCSM-internal):** When the PCSM process restarts (pod crash + Kubernetes restart), PCSM itself recovers — it restarts initial sync from scratch or resumes real-time replication from its checkpoint. This is PCSM's internal behavior, not an operator action. The operator does **not** trigger restarts to recover from failures, and does **not** issue any CLI commands just because PCSM came back up.
3. **Connection strings require credential injection:** The operator reads credentials from `source.credentialsSecret` (source) and the operator-managed `<cr>-pcsm-target-user` Secret (target), builds `PCSM_SOURCE_URI` and `PCSM_TARGET_URI` with percent-encoded credentials, and stores both in the `<cr>-pcsm-secret` Secret. The PCSM container consumes them via `envFrom.SecretKeyRef`, so credentials are never in the CR spec and never in the pod spec.
4. **Interaction with cluster pause:** If `spec.pause=true` scales down MongoDB, PCSM loses its connection. In the first iteration the operator does **not** coordinate this — PCSM observes the disconnect and reports `failed` or self-recovers when the target comes back. Cross-controller coordination is deferred (§1.3, §8.3, §11 Q3).
5. **Interaction with backups and restores:** Both must be blocked for the entire ClusterSync lifecycle. PBM holds the backup cursor open while a backup runs; with PCSM continuously applying source writes onto the target, the cursor pins WiredTiger history and disk usage can grow unbounded, on top of the oplog contention between PBM and PCSM. Restores are similarly incompatible: they overwrite data while PCSM is still applying change-stream events from the source, with no safe interleave. Coordination happens through the ClusterSync Lease (§4.1, §8.4), not through CR-list lookups.
6. **CR existence is the deployment signal; `spec.mode` is the lifecycle signal.** A ClusterSync CR exists for the duration of a replication relationship. Creating the CR provisions PCSM resources (Deployment, URI Secret, `syncTargetUser`) and starts replication immediately — `spec.mode` defaults to `running`. Users who want a create-then-start workflow set `spec.mode: paused` at creation and flip to `running` later. Deleting the CR tears everything down via ownerReferences (Lease released via the internal finalizer).

### 3.4 No Automatic Actions Principle

The controller takes lifecycle actions on PCSM (`pcsm start`, `pcsm pause`, `pcsm resume`, `pcsm finalize`) **only** in response to an explicit `spec.mode` change made by the user, with a single well-scoped self-heal exception described below.

Concrete consequences:

- **Optional two-step start on CR creation.** `spec.mode` defaults to `running`, so a fresh CR starts replication as soon as the Deployment is ready — the recommended workflow keeps this default. Users who want to review the setup before any writes happen create the CR with `spec.mode: paused`, verify the Deployment, then flip to `running`.
- **Failure self-heal (the one exception).** When `spec.mode=running`, `status.mode=running`, PCSM has started at least once, and observed `state=failed`, the controller runs `pcsm resume --from-failure` without waiting for a spec change. The user's standing intent (`spec.mode=running`) is already recorded; PCSM's `--from-failure` recovery is checkpoint-preserving and idempotent, so the controller resumes rather than making the user re-flip the mode by hand. If the same failure keeps recurring the controller keeps retrying — the user must either pause (`spec.mode: paused`) or fix the underlying cause. The controller does **not** call any destructive verb (no `/reset` exists in the first iteration; see §11 Q15), and does **not** scale the Deployment or delete the pod.
- **No auto-transition into finalize, pause, or start beyond the matrix above.** The state machine in `nextAction` (§3.3 item 3) is the only source of lifecycle CLI calls apart from the failure self-heal.
- **K8s-level pod restarts are still allowed.** `restartPolicy: Always` on the Deployment means Kubernetes restarts the PCSM container if it crashes. That is a Kubernetes guarantee, not an operator action, and combined with Observation 2 it gives PCSM a chance to self-recover from transient process-level failures without operator involvement. The operator's "no automatic actions" rule is about lifecycle CLI commands and reconciler-driven workload mutations, not pod-level liveness.

This is a deliberate posture for the first iteration: cluster sync has many failure modes the operator cannot diagnose or fix automatically (source-side configuration, version mismatches, MongoDB admin commands run outside the operator's view), so the safe default is to surface state and let the user drive recovery. The failure self-heal is scoped tight (only when the user has already asked for `running`, only via checkpoint-preserving `resume --from-failure`) precisely so it does not become a catch-22 loop against unresolved source-side issues.

---

## 4. CRD and Interface Changes

### 4.1 New CRD: `PerconaServerMongoDBClusterSync`

A new CRD in the `psmdb.percona.com/v1` group, modeled after the existing
`PerconaServerMongoDBBackup` and `PerconaServerMongoDBRestore` CRDs.

| Field        | Value                                    |
|--------------|------------------------------------------|
| Group        | `psmdb.percona.com`                      |
| Version      | `v1`                                     |
| Kind         | `PerconaServerMongoDBClusterSync`        |
| Short name   | `psmdb-clustersync`                      |
| Scope        | Namespaced                               |
| Source file  | `pkg/apis/psmdb/v1/perconaservermongodbclustersync_types.go` |

CR existence provisions PCSM resources (Deployment, URI Secret,
`syncTargetUser`) and — with the default `spec.mode=running` — begins
replication as soon as the Deployment is ready. Users who want to review the
setup first create the CR with `spec.mode: paused` and flip to `running`
later. Deleting the CR tears down all managed resources via ownerReferences
(the ClusterSync Lease is released by an internal finalizer, see §4.2.1).
There is no `enabled` field — `spec.mode` is the lifecycle control surface
(see §4.2 and §3.4).

**Cardinality:** At most one non-finalized ClusterSync CR may target a given
target cluster (namespace + `spec.clusterName`) at a time (PCSM Constraint 2 —
single source/target pair). Enforced by a `coordination.k8s.io/v1` Lease named
`psmdb-<clusterName>-clustersync-lock` **in the same namespace as the
ClusterSync CR and the target PSMDB CR** (not in the operator's own namespace).
The controller acquires the Lease before provisioning the Deployment and
releases it when `status.state=finalized` or on CR deletion. Putting the Lease
next to the cluster it guards means cluster-wide-mode deployments are
collision-free for free — two PSMDB clusters in different namespaces sharing a
`clusterName` each get their own Lease in their own namespace — and the
operator only needs `coordination.k8s.io/leases` RBAC in the namespaces it
watches, not a privileged Lease scope in its own namespace. A second CR
targeting the same namespace+`clusterName` fails to acquire the Lease and
surfaces the collision in `status.error` (`clustersync lease ... held by "<other-cr>-<uid>", not by this CR`); it does not transition to a dedicated `failed` state — it simply keeps retrying so the reconcile picks up automatically once the incumbent goes away.

The same Lease is the signal the backup and restore controllers consult before
starting new operations (§8.4). Releasing it at finalize therefore has two
consequences: it lets a follow-up ClusterSync CR (for a fresh migration)
acquire the Lease, **and** it unblocks backups and restores on the target
cluster without requiring the user to delete the finalized CR.

**OwnerReferences:** Following the backup/restore precedent, the ClusterSync CR is
NOT owned by the target PSMDB CR — deleting the cluster does not auto-delete
ClusterSync history. Child resources (Deployment, URI Secret, syncTargetUser
Secret) ARE owned by the ClusterSync CR.

### 4.2 Spec

```go
type PerconaServerMongoDBClusterSyncSpec struct {
    ClusterName string `json:"clusterName"`
    Image       string `json:"image"`

    ImagePullPolicy  corev1.PullPolicy             `json:"imagePullPolicy,omitempty"`
    ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

    Resources                corev1.ResourceRequirements `json:"resources,omitempty"`
    NodeSelector             map[string]string           `json:"nodeSelector,omitempty"`
    Tolerations              []corev1.Toleration         `json:"tolerations,omitempty"`
    Affinity                 *PodAffinity                `json:"affinity,omitempty"`
    Annotations              map[string]string           `json:"annotations,omitempty"`
    Labels                   map[string]string           `json:"labels,omitempty"`
    RuntimeClassName         *string                     `json:"runtimeClassName,omitempty"`
    ContainerSecurityContext *corev1.SecurityContext     `json:"containerSecurityContext,omitempty"`
    PodSecurityContext       *corev1.PodSecurityContext  `json:"podSecurityContext,omitempty"`

    LivenessProbe  *corev1.Probe `json:"livenessProbe,omitempty"`
    ReadinessProbe *corev1.Probe `json:"readinessProbe,omitempty"`

    Source ClusterSyncSource `json:"source"`

    // ExcludeNamespaces lists MongoDB namespaces to skip (e.g. "db1",
    // "db2.collection"). Passed through to `pcsm start` as
    // --exclude-namespaces.
    ExcludeNamespaces []string `json:"excludeNamespaces,omitempty"`

    // Mode is the user intent for the PCSM lifecycle and the sole
    // driver of the pcsm start/pause/resume/finalize CLI calls the
    // controller issues, apart from the failure self-heal in §3.4.
    // +kubebuilder:default=running
    Mode ClusterSyncMode `json:"mode,omitempty"`

    // PCSMConfig holds the small set of PCSM tuning knobs exposed as
    // typed fields. Anything not listed here goes through Env.
    PCSMConfig ClusterSyncPCSMConfig `json:"pcsmConfig,omitempty"`

    // Env is a passthrough for extra environment variables on the PCSM
    // container. Used to expose PCSM knobs that only have PCSM_* env
    // equivalents (e.g. source TLS tuning) without minting typed fields
    // for each one.
    Env []corev1.EnvVar `json:"env,omitempty"`
}

type ClusterSyncSource struct {
    // URI is the source connection string WITHOUT credentials;
    // credentials from CredentialsSecret are injected at runtime,
    // percent-encoded per RFC 3986. TLS parameters, replicaSet, and
    // other options are passed through query-string parameters on the
    // URI (see §4.6). There is no typed `tls` sub-struct in the first
    // iteration.
    URI string `json:"uri"`

    // CredentialsSecret is a same-namespace Secret with `username` and
    // `password` keys.
    CredentialsSecret string `json:"credentialsSecret"`
}

// ClusterSyncPCSMConfig holds PCSM knobs that have no PCSM_* env-var
// equivalent and must be passed as CLI args on the container.
type ClusterSyncPCSMConfig struct {
    // LogLevel maps to --log-level.
    // +kubebuilder:validation:Enum={debug,info,warn,error}
    LogLevel string `json:"logLevel,omitempty"`

    // LogJSON maps to --log-json. Pointer so the user can explicitly
    // disable structured logging even if a future default flips.
    LogJSON *bool `json:"logJSON,omitempty"`
}

// ClusterSyncMode is the user-controlled lifecycle intent for PCSM.
// The controller never writes back to spec; it mirrors spec.mode into
// status.mode after the corresponding CLI command succeeds.
// +kubebuilder:validation:Enum={paused,running,finalized}
type ClusterSyncMode string

const (
    // ClusterSyncModePaused asks the controller to run `pcsm pause`
    // (from running) or hold PCSM idle (from a fresh CR that never
    // started).
    ClusterSyncModePaused ClusterSyncMode = "paused"

    // ClusterSyncModeRunning is the default on new CRs. The
    // controller runs `pcsm start` the first time or `pcsm resume`
    // from paused. It also triggers `pcsm resume --from-failure` as
    // the single failure self-heal described in §3.4.
    ClusterSyncModeRunning ClusterSyncMode = "running"

    // ClusterSyncModeFinalized asks the controller to run
    // `pcsm finalize` once PCSM is caught up. Terminal for that CR;
    // the controller ignores any subsequent spec.mode change and
    // releases the ClusterSync Lease.
    ClusterSyncModeFinalized ClusterSyncMode = "finalized"
)
```

A `reset` mode was proposed in earlier ADR revisions but is not part of the
first iteration — see §11 Q15 for the rationale (PCSM 0.9.0 has no `pcsm reset`
verb and the failure self-heal + `--from-failure` covers the recoverable
subset). Fresh initial sync from scratch is achieved by deleting and
recreating the ClusterSync CR (the finalized/deleted CR releases the Lease so
the replacement acquires it cleanly).

**Field notes:**

- `clusterName`: references the target `PerconaServerMongoDB` CR in the same namespace. The controller resolves it to build `PCSM_TARGET_URI`. Multiple PSMDB clusters in one namespace are supported — each ClusterSync CR picks exactly one by name; the cardinality Lease (§4.1) is keyed by namespace + `clusterName` so co-located clusters cannot collide.
- `image`: required in the first iteration. See §1.3 (Deferred) for the version service plan.
- `source.uri`: connection string for the source cluster without credentials. Format: `mongodb://h1:p1,h2:p2/admin?replicaSet=rs0`. TLS parameters live on the URI itself (e.g. `?tls=true&tlsCAFile=/etc/pcsm/tls/ca.crt`) rather than in a typed sub-struct. Credentials from `source.credentialsSecret` are percent-encoded and merged in by the controller at reconcile time.
- `source.credentialsSecret`: name of a Kubernetes Secret with `username` and `password` keys. The operator percent-encodes both per RFC 3986 before writing them into the `<cr>-pcsm-secret` Secret alongside `PCSM_TARGET_URI`.
- `excludeNamespaces`: list of **MongoDB namespaces** (NOT Kubernetes namespaces) to exclude from replication. A MongoDB namespace is `<database>.<collection>` (e.g., `db3.collection3`); a database-wide exclude is `<database>`. Passed through to PCSM verbatim as its `--exclude-namespaces` argument on `pcsm start`. The name is inherited from PCSM's CLI surface.
- `mode`: single enum (`paused` | `running` | `finalized`) that expresses the user's lifecycle intent. Defaults to `running` on new CRs via CRD validation. Every PCSM CLI command (`pcsm start`, `pcsm pause`, `pcsm resume`, `pcsm finalize`) is driven by a transition of this field, plus the deliberate failure self-heal in §3.4. The controller acts on a transition when `spec.mode != status.mode` and, after a successful CLI command, mirrors `spec.mode` into `status.mode` via the status subresource. Re-applying the same manifest fires no additional command, mutates no spec, and produces no GitOps drift. The valid transition matrix (matches `nextAction` in the controller):

  | From (`status.mode`) | To (`spec.mode`) | Controller action |
  |------|------|------|
  | (empty) | `running` | `pcsm start` — default first-reconcile path |
  | (empty) | `paused` | none (mirror only) — opt-in create-then-start |
  | `paused` | `running` | `pcsm start` if PCSM has never started; otherwise `pcsm resume` |
  | `running` | `paused` | `pcsm pause` |
  | `running` | `finalized` | `pcsm finalize` (waits for PCSM to report caught up) |
  | `paused`  | `finalized` | `pcsm finalize` **only** if PCSM had previously started; otherwise no-op |
  | `finalized` | (anything) | no-op — controller stops issuing commands and skips further transitions |

  In addition to the matrix above, `state=failed` + `spec.mode=running` + `status.startedAt != nil` triggers `pcsm resume --from-failure` without waiting for a spec change (§3.4). No admission webhook exists in the first iteration: invalid transitions do not fail admission — they are quietly ignored by the controller (`actionNone`). Cluster-wide "cluster busy with backup or restore" holds any transition that would cause a target write (`start`, `resume`) until the backup Lease clears and no restore CR is in-flight (§8.4).

### 4.2.1 Finalizer Strings

The first iteration exposes **no user-facing finalizer strings**. There is no `percona.com/delete-clustersync-after-finalize` opt-in (proposed in earlier ADR revisions but dropped — see §1.3 and §5.5); finalized CRs stay in the namespace as historical records until the user removes them with `kubectl delete`.

The controller does register one internal finalizer of its own:

| Finalizer string | Set by | Purpose |
|------------------|--------|---------|
| `internal.percona.com/release-lock` | Controller (automatic, on every reconcile) | Guarantees the ClusterSync Lease is released even when the CR is force-deleted. On CR deletion the controller enters `handleDeletion`, releases the Lease, then removes this finalizer so Kubernetes can complete GC of the CR and its owned children. |

Users should not add, remove, or edit this finalizer; it is bookkeeping between the controller and the Lease. GitOps workflows that want finalized CRs to disappear once replication ends should delete the CR explicitly after inspecting `status.state=finalized`.

### 4.3 Status

```go
type PerconaServerMongoDBClusterSyncStatus struct {
    // Mode mirrors spec.mode once the corresponding CLI command has
    // succeeded. The controller compares spec.mode against this value
    // to decide whether a transition is required. Empty on a
    // freshly-created CR; the first successful command mirrors
    // spec.mode into this field.
    Mode ClusterSyncMode `json:"mode,omitempty"`

    // State is the PCSM-reported runtime state from `pcsm status`.
    // Distinct from spec.mode/status.mode: spec.mode is user intent,
    // state is what PCSM is actually doing right now.
    State ClusterSyncState `json:"state,omitempty"`

    LagTimeSeconds int64        `json:"lagTimeSeconds,omitempty"`
    Error          string       `json:"error,omitempty"`
    StartedAt      *metav1.Time `json:"startedAt,omitempty"`

    Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// ClusterSyncState mirrors PCSM's `pcsm status` "state" field. PCSM
// 0.9.0 does not distinguish initial sync from real-time replication;
// both are reported as "running".
type ClusterSyncState string

const (
    ClusterSyncStateIdle       ClusterSyncState = "idle"
    ClusterSyncStateRunning    ClusterSyncState = "running"
    ClusterSyncStatePaused     ClusterSyncState = "paused"
    ClusterSyncStateFinalizing ClusterSyncState = "finalizing"
    ClusterSyncStateFinalized  ClusterSyncState = "finalized"
    ClusterSyncStateFailed     ClusterSyncState = "failed"
)

const (
    // Set True while status.state=running, False otherwise (with
    // reason PCSMNotRunning).
    ConditionClusterSyncRunning = "Running"

    // Set True once status.state=finalized. Not removed afterwards.
    ConditionClusterSyncFinalized = "Finalized"
)
```

`status.error` carries both PCSM-reported errors from `pcsm status` and local-side errors (source secret missing, target cluster not found, cluster-busy holds, unreachable PCSM pod). It is a free-form string, not a coded error.

**Why two fields (`status.mode` and `status.state`)?** They answer different questions:

- `status.mode` answers "what user intent has the controller already acted on?" — drives idempotency for spec changes.
- `status.state` answers "what is PCSM actually doing right now?" — read from `pcsm status`, drives the failure self-heal in §3.4 and the operational observability the printer columns surface.

In a healthy run, the two evolve together (e.g., `spec.mode=running`, `status.mode=running`, `status.state=running`). On failure they diverge (`spec.mode=running`, `status.mode=running`, `status.state=failed`) — a divergence that the controller acts on via the `--from-failure` self-heal, and that surfaces to the user through printer columns and the `Running` condition flipping to `False`.

The backup/restore admission policy in §8.4 does **not** read `status.state` directly; it reads the ClusterSync Lease, which the controller releases only when `status.state=finalized`. That indirection keeps the admission check as one Lease get instead of a CR list, at the cost of a small eventual-consistency window between "PCSM stopped" and "Lease released."

### 4.4 Printer Columns

```
NAME | CLUSTER | MODE | STATE | LAG(S) | AGE
```

Mapped from: `metadata.name`, `spec.clusterName`, `spec.mode`, `status.state`, `status.lagTimeSeconds`, `metadata.creationTimestamp`.

Because PCSM 0.9.0 reports both initial sync and real-time replication as `state=running`, there is no distinct "initial-sync" column — the operator does not synthesize one. Users who need to know whether initial sync has completed watch for `LAG(S)` dropping below their tolerance; steady-state "caught up" is `LAG(S)=0`. The `MODE` column is the user-set intent (`spec.mode`), making it easy to spot when intent and reality diverge (e.g., `MODE=running`, `STATE=failed`).

### 4.5 Internal Contracts

- **`<cr>-pcsm-secret`** Secret (owned by the ClusterSync CR): stores the two URIs the container consumes.
  - `source-uri`: `spec.source.uri` with `username`/`password` from `spec.source.credentialsSecret` percent-encoded in per RFC 3986. TLS/replicaSet/other options remain on the query string as the user wrote them.
  - `target-uri`: auto-built by the controller from the target PSMDB CR. For sharded targets: `mongodb://<user>:<pass>@<cluster>-mongos.<ns>.<dnsSuffix>:<port>`. For replset targets: `mongodb://<user>:<pass>@<cluster>-<rs>.<ns>.<dnsSuffix>:<port>?replicaSet=<rs>`. The DNS suffix honors `target.Spec.ClusterServiceDNSSuffix` (defaults to the operator's `DefaultDNSSuffix`).
- **`PCSM_SOURCE_URI` / `PCSM_TARGET_URI`** env vars on the PCSM container: sourced via `envFrom.SecretKeyRef` from the Secret above. Neither the URI nor the credentials appear in the pod spec.
- **PCSM CLI**: the operator runs `pcsm` inside the running container via `kubectl exec` (`clientcmd.Exec`). The `pkg/psmdb/clustersync/client.Client` type wraps `status`, `start`, `pause`, `resume`, `finalize`; `pcsm status` returns JSON that the controller unmarshals into `client.Status{State, Info, Error, LagTimeSeconds}`.
- **Target user**: MongoDB user `clustersync-<cr-name>` on the target cluster, created with roles `restore`, `clusterMonitor`, `clusterManager`, `readWriteAnyDatabase` on the `admin` DB. Password lives in the `<cr>-pcsm-target-user` Secret; on reconcile the controller re-applies password + roles to heal drift (see §5.3).

### 4.6 User-Facing Behavior Changes

**New resources visible in the namespace:**
- A `PerconaServerMongoDBClusterSync` CR (visible via `kubectl get psmdb-clustersync`).
- A PCSM Deployment (visible via `kubectl get deployments`), named `<clustersync-cr-name>-pcsm`.
- Two Secrets: `<clustersync-cr-name>-pcsm-secret` (URI Secret) and `<clustersync-cr-name>-pcsm-target-user` (target user credentials).
- One Lease: `psmdb-<clusterName>-clustersync-lock` (cluster-ownership + backup/restore admission signal).

**No PCSM Service is created.** The operator uses `kubectl exec` for lifecycle control and PCSM's own liveness/readiness probes use container-local curl. External monitoring of PCSM is not a first-iteration feature.

**PSMDB CR:** No fields added or modified. The PSMDB CR remains unchanged.

**Kubernetes Events emitted by the controller (on the ClusterSync CR):** the first iteration emits a single event type — `ClusterBusy` (Normal) — whenever the controller holds a `pcsm start` or `pcsm resume` transition because the backup Lease is held or a restore CR is in-flight against the target. Full lifecycle events (`Started`, `Paused`, `Resumed`, `Finalized`, `Failed`) proposed in earlier ADR revisions were not implemented; lifecycle history is available through `status.conditions` (transition times) and controller logs instead.

**Operator log messages:**
- Lifecycle transitions (start, pause, resume, finalize) are logged at info level with the from/to modes and the chosen action.
- `pcsm` CLI failures are logged at error level with the trimmed stderr from the container. Unreachable-pod errors are logged at V(1) info level because the reconciler retries them on the next requeue.

---

## 5. Design Decisions and Alternatives

### 5.1 PCSM Workload Type

**Chosen approach:** Deploy PCSM as a Kubernetes Deployment managed by the operator.

**Why:** PCSM is a stateless, standalone process that does not need to run on every replset member (Observation 1). A Deployment provides a clear lifecycle boundary and simpler failure handling.

**Note:** The operator currently uses only StatefulSets for all workloads. Adding a Deployment introduces a new resource type to the operator's management scope. This requires new builder code for Deployment creation and reconciliation, since no existing pattern can be reused directly.

**Alternatives considered:**

| Alternative | Why Rejected |
|------------|--------------|
| Single-replica StatefulSet | Would be consistent with existing operator patterns, but StatefulSets provide guarantees (stable network identity, ordered deployment) that PCSM does not need. Adds unnecessary complexity. |

### 5.2 Deployment Strategy

**Chosen approach:** Use `Recreate` strategy unconditionally for the PCSM Deployment.

**Why:** Due to Constraint 6, two PCSM instances writing to the same target simultaneously can corrupt data. `RollingUpdate` briefly overlaps old and new pods.

**Alternatives considered:**

| Alternative | Why Rejected |
|------------|--------------|
| RollingUpdate | Overlapping pods risk data corruption on the target |

### 5.3 User Creation and Deletion Strategy

PCSM requires users on both the source and target clusters. The operator only manages the **target** cluster, so it can only create the target user locally. The source user must be created by the user (or by another operator instance if the source is also operator-managed).

| User | Created by | Cluster | Roles |
|------|-----------|---------|-------|
| Source user | User's responsibility (not managed by the operator) | Source cluster | `backup`, `clusterMonitor`, `readAnyDatabase` |
| `clustersync-<cr-name>` | Operator | Target cluster (local) | `restore`, `clusterMonitor`, `clusterManager`, `readWriteAnyDatabase` |

**Chosen approach (decided 2026-06-04, refined at implementation time):** The ClusterSync controller creates the target user on the first reconcile and re-applies password and roles on every reconcile to heal drift (e.g., the CR and its `<cr>-pcsm-target-user` Secret were deleted and recreated while the MongoDB user, which has no owner ref, kept its old password). The user is scoped per CR — the username is `clustersync-<cr-name>` — so a fresh CR (with a new name) after a finalized one gets its own user. The operator does **not** drop the user — it persists on the target across `status.state` transitions to `finalized` and across ClusterSync CR deletion. The K8s `<cr>-pcsm-target-user` Secret is owned by the ClusterSync CR and GCs via ownerReferences when the CR is deleted; the MongoDB user itself stays on the target.

Password is generated once at Secret-creation time and stored in the Secret; the reconcile loop reads it from the Secret and applies it back to the user, so if a cluster admin changes the password out of band the next reconcile heals it. There is still no scheduled rotation — password rotation is a separate design if it becomes needed later.

**Why:** Treating user creation as one-way matches the operator's broader posture on user management — once a user is created on a cluster, removing it is a cluster-admin decision rather than an operator action. It also avoids requiring the operator to hold `dropUser` privileges and removes a class of failure modes where a stuck `dropUser` call blocks CR deletion (no `dropUser` call → no Kubernetes finalizer needed for that; child resources GC via ownerReferences; the internal `release-lock` finalizer only exists to guarantee Lease release, not to gate user cleanup). To suspend replication without removing PCSM, set `spec.mode=paused` instead of deleting the CR.

The source cluster credentials are provided by the user via `spec.source.credentialsSecret`, a Kubernetes Secret with `username` and `password` keys. The operator reads the Secret and writes the fully-formed `PCSM_SOURCE_URI` (with percent-encoded credentials) into the `<cr>-pcsm-secret` Secret. Credentials are never stored in the CR spec.

**Alternatives considered:**

| Alternative | Why Rejected |
|------------|--------------|
| Drop the target user at finalize and on CR delete | Earlier design choice (until 2026-06-04). Required a Kubernetes finalizer to keep MongoDB-user drop ahead of K8s-Secret GC, gave the operator a class of stuck-deletion failure modes (`dropUser` against an unreachable target blocks `kubectl delete`), and required broader privileges on the target. Reversed in favor of "operator never drops users it created." |
| Operator creates source user on the source cluster | The source cluster may be external and not managed by this operator instance |

### 5.4 Configuration Change Handling

**Chosen approach (first iteration):** No admission webhook. `spec.source.uri` and `spec.source.credentialsSecret` are treated as *effectively immutable* — the URI stored in `<cr>-pcsm-secret` is refreshed on every reconcile, so the pod only picks up a change if it is restarted, and restarting PCSM against a different source invalidates the checkpoint and forces a fresh initial sync. The controller does not fail admission on an edit and does not proactively restart the pod. Image, pod knobs (resources, tolerations, affinity, probes, env, pcsmConfig), and `spec.mode` transitions are all mutable and reflected on the next Deployment reconcile. `spec.excludeNamespaces` is passed only on `pcsm start`, so a change after PCSM has started does not re-evaluate filters retroactively — recreate the CR to change the filter set. Once `status.mode=finalized`, spec changes are accepted at the API level but the controller runs no further CLI commands (`nextAction` returns `actionNone`), so they have no effect on PCSM.

**Why the ADR's original admission webhook is not in the first iteration:** shipping the CRD + controller + Lease + backup/restore integration was already large; admission was scoped out for a follow-up. The `nextAction` state machine already returns `actionNone` for invalid transitions, so the runtime consequence of a bad edit is a no-op rather than a broken state. Source changes do carry a real risk of quiet reinitialization if the pod restarts, so operational guidance is still "delete and recreate the CR to change source" (see §7 and §8.5).

**Mode transition rules (enforced in `nextAction`, not admission):**

- Any transition once `status.mode=finalized` is silently ignored. The controller does not emit an error, does not run any CLI command, and leaves the CR alone.
- A transition to `mode=finalized` is only executed from `running`, or from `paused` when PCSM has previously started (`status.startedAt != nil`). Otherwise the controller returns `actionNone`.
- A transition from `mode=paused` to `mode=running` runs `pcsm start` on the very first activation and `pcsm resume` on every subsequent activation, based on `status.startedAt`.

**Alternatives considered:**

| Alternative | Why Rejected (or deferred) |
|------------|--------------|
| Allow source updates freely | Silently restarting a multi-day initial sync against a different source is too expensive and surprising. Documented as "delete and recreate" in §7/§8.5. |
| Admission webhook rejecting invalid mode transitions and source edits | Deferred to a later iteration to keep the first cut lean; the state machine's `actionNone` behavior covers correctness at runtime, at the cost of the user not getting an admission-time error. |
| Separate `spec.paused`, `spec.finalize`, `spec.resetGeneration` fields (earlier ADR shape, dropped 2026-06-08) | Three independent fields meant the controller had to reason about combinations; a single enum makes intent mutually exclusive by construction and matches how the shipped code models the state machine. |
| Include a `reset` enum value + `pcsm reset` CLI | Dropped — PCSM 0.9.0 has no `pcsm reset` verb. Fresh initial sync is achieved by deleting and recreating the CR; failure-preserving retry is `pcsm resume --from-failure` (§3.4). |

### 5.5 Post-Finalize Resource Cleanup

**Chosen approach (first iteration):** Once the controller runs `pcsm finalize` and PCSM reports `state=finalized`, the controller mirrors `status.mode=finalized`, updates the `Finalized` condition, and **releases the ClusterSync Lease** so backups and restores can proceed against the target cluster again. It does **not** delete the PCSM Deployment, the `<cr>-pcsm-secret` URI Secret, or the `<cr>-pcsm-target-user` Secret at finalize-time. Those resources are kept until the user deletes the ClusterSync CR, at which point they GC via ownerReferences. The `syncTargetUser` MongoDB user on the target is **not** dropped (see §5.3 — the operator never removes users it created).

**Why the resources are kept until CR deletion:** the finalized CR is treated as an auditable historical record; keeping the Deployment, Secrets, and CR side-by-side means `kubectl describe psmdb-clustersync` still shows the full picture of a completed migration. Freeing the target cluster for backups and restores does not require deleting anything — it requires releasing the Lease — so cleanup is folded into that single action. The user removes the record with a plain `kubectl delete psmdb-clustersync`; ownerReferences GC the rest.

**Why no user-facing opt-in "auto-delete after finalize":** the ADR's original `percona.com/delete-clustersync-after-finalize` finalizer is not in the first iteration. Users who want GitOps-style disappearance can wrap `kubectl delete` in a follow-up reconcile in their own tooling. Revisiting the opt-in is deferred (§1.3).

**Alternatives considered:**

| Alternative | Why Rejected (or deferred) |
|------------|--------------|
| Delete Deployment/Secrets proactively at finalize | Original ADR design; not implemented in the first iteration. Trade-off: freeing pod resources vs. keeping the finalized CR as a self-contained audit trail. Deferred until operational feedback shows the idle pod is a real cost. |
| Drop the `syncTargetUser` MongoDB user on the target at finalize | See §5.3 — the operator never drops users it created. |
| Auto-delete the CR itself | Erases the historical record and would need a Kubernetes finalizer to sequence Lease release before CR delete. Deferred with the opt-in finalizer above. |
| `spec.deleteAfterFinalize` bool on the spec | Same reasons the opt-in finalizer string would exist; the finalizer-string pattern is a better match with the PSMDB CR convention if we come back to this. |

---

## 6. Sharding Impact

### 6.1 Sharded Cluster Behavior

> **Upstream status:** Sharded-cluster replication is labeled **Technical Preview** in PCSM 0.7.0+. The operator inherits this label for sharded ClusterSync CRs and surfaces it in the CR docs; the first iteration's sharding behavior is exactly what PCSM 0.7.0+ provides, with no operator-side workarounds or extra knobs.

**How replication works between sharded clusters (no shard-to-shard mapping involved):**

The target cluster is an already-deployed sharded MongoDB cluster — PCSM does **not** provision shards. The number of shards on the target is whatever the operator created when it reconciled the target's `PerconaServerMongoDB` CR, independently of the source. What PCSM creates on the target are the **sharded collections**, not shards:

1. PCSM reads which collections are sharded on the source and what their shard keys are.
2. For each one, PCSM calls `sh.shardCollection(...)` on the target's mongos with the same shard key.
3. PCSM clones documents through the target's mongos. The target's mongos routes each document to whichever target shard owns the relevant key range; the target's balancer manages chunk distribution across all target shards on its own.

That mechanism is why a 3-shard source can replicate onto an 8-shard target (or vice versa) with no extra configuration: there is nothing to map. PCSM only preserves the shard key; the target cluster decides everything else (chunk layout, primary shard, balancer behavior) independently from the source. There is no "X-to-Y shard mapping" surface in PCSM and the operator does not introduce one.

**Other sharding behaviors for a sharded ClusterSync CR (`sharding.enabled=true` on the target PSMDB CR):**

- PCSM connects via mongos on both source and target clusters.
- The balancer does not need to be disabled on either cluster. The target cluster's balancer continues to operate normally and manages chunk distribution according to its own configuration.
- PCSM replicates data, not metadata. Chunk distribution, primary shard names, and zone configuration are NOT preserved from the source. The target cluster manages these internally.
- Running `movePrimary`, `reshardCollection`, `unshardCollection`, or `refineCollectionShardKey` on the source during replication causes a failure requiring a full initial sync restart.

### 6.2 Single Replset Behavior

When sharding is disabled:
- PCSM connects directly to the replset.
- Full feature support.
- No additional flags required beyond creating a `PerconaServerMongoDBClusterSync` CR.

---

## 7. User Experience

### 7.1 Existing PSMDB CR (Unchanged)

The PSMDB CR is not modified by this work. Existing clusters continue to behave identically after operator upgrade.

```yaml
apiVersion: psmdb.percona.com/v1
kind: PerconaServerMongoDB
metadata:
  name: cluster1
spec:
  replsets:
    - name: rs0
      size: 3
```

### 7.2 Enable Cross-Cluster Replication

The default workflow is single-shot: create the ClusterSync CR and let `spec.mode: running` (the CRD default) start replication as soon as the PCSM Deployment is ready.

```yaml
apiVersion: psmdb.percona.com/v1
kind: PerconaServerMongoDBClusterSync
metadata:
  name: cluster1-sync
spec:
  clusterName: cluster1
  image: percona/percona-clustersync-mongodb:0.9.0
  # mode: running  (default — controller runs `pcsm start` once the pod is ready)
  source:
    uri: mongodb://host1:27017,host2:27017,host3:27017/admin?replicaSet=rs0&tls=true
    credentialsSecret: pcsm-source-credentials   # Secret with username/password keys
  excludeNamespaces:
    - db3.collection3
  pcsmConfig:
    logLevel: info
```

Source TLS is configured via query-string parameters on `source.uri` (e.g. `tls=true&tlsCAFile=/etc/pcsm/tls/ca.crt`); if CA/client-certificate files need to be mounted onto the container, add them via `spec.env` referencing a user-managed Secret and mount them through pod-level fields.

For an opt-in "review first, then start" workflow, create with `mode: paused` and flip to `running` when ready:

```yaml
# Step 1 — CR exists, Deployment is up, but PCSM stays idle
spec:
  mode: paused

# Step 2 — flip to running to trigger `pcsm start`
spec:
  mode: running
```

### 7.3 Pause and Resume Replication

```yaml
# Pause replication (running -> paused; controller runs `pcsm pause`)
spec:
  mode: paused

# Resume replication (paused -> running; controller runs `pcsm resume`)
spec:
  mode: running
```

### 7.4 Sharded Cluster Replication

```yaml
apiVersion: psmdb.percona.com/v1
kind: PerconaServerMongoDBClusterSync
metadata:
  name: cluster1-sync
spec:
  clusterName: cluster1   # PSMDB CR with sharding.enabled=true
  image: percona/percona-clustersync-mongodb:0.9.0
  source:
    uri: mongodb://mongos1:27017,mongos2:27017/admin
    credentialsSecret: pcsm-source-credentials
```

### 7.5 Finalize Migration

After verifying lag is acceptable, set `spec.mode: finalized` to complete the migration. The controller runs `pcsm finalize` once PCSM reports caught up, mirrors `status.mode=finalized`, updates the `Finalized` condition, and releases the ClusterSync Lease so backups and restores can proceed against the target again (see §5.5). The Deployment and Secrets stay in place until the user deletes the CR.

```yaml
apiVersion: psmdb.percona.com/v1
kind: PerconaServerMongoDBClusterSync
metadata:
  name: cluster1-sync
spec:
  clusterName: cluster1
  image: percona/percona-clustersync-mongodb:0.9.0
  source:
    uri: mongodb://host1:27017/admin?replicaSet=rs0
    credentialsSecret: pcsm-source-credentials
  mode: finalized   # controller runs `pcsm finalize` once caught up; terminal
```

Allowed only from `mode=running`, or from `mode=paused` when PCSM has previously started (see §5.4). Once `status.mode=finalized`, any further spec.mode changes are silently ignored by the controller (`actionNone`). To run a new migration create a new ClusterSync CR — the finalized one holds no Lease, so a fresh CR can acquire it immediately.

GitOps workflows that want the finalized CR to disappear automatically must do so from their own tooling (e.g. a follow-up `kubectl delete` step). The first iteration does not offer a controller-side opt-in for this (§1.3, §5.5).

### 7.6 Recover from a Failure

When `status.state=failed`, the controller auto-runs `pcsm resume --from-failure` on subsequent reconciles as long as `spec.mode=running` and PCSM has started at least once (§3.4). No user action is required for checkpoint-preserving recovery — for transient failures (source briefly unreachable, etc.) the failure clears on its own the next time PCSM manages to reconnect.

If the failure keeps recurring, the retry loop is a signal that the root cause is not transient (e.g. source oplog rolled over past the checkpoint, source and target major versions differ, a disallowed sharding admin command ran on the source — see Constraint 3). In that case the recovery path is:

**Path A — pause while you diagnose.** Flip to `paused` so the controller stops issuing `resume` commands, investigate/fix the source-side issue, then flip back to `running`:

```yaml
# 1. Stop the retry loop
spec:
  mode: paused

# 2. After fixing the root cause on the source side
spec:
  mode: running   # controller runs `pcsm resume` — from-failure if PCSM is still failed
```

**Path B — start over from scratch.** If the checkpoint is unrecoverable (e.g. oplog rolled over), the fresh-initial-sync path in the first iteration is *delete the CR and recreate it*:

```bash
kubectl delete psmdb-clustersync cluster1-sync
# fix source oplog retention or whatever caused the original failure
kubectl apply -f cluster1-sync.yaml   # a new CR runs `pcsm start`, i.e. fresh initial sync
```

Deleting the CR releases the ClusterSync Lease and GCs all owned children; the replacement CR provisions everything again and starts a full initial sync. There is no `spec.mode: reset` in the first iteration (see §11 Q15 for the rationale).

### 7.7 Disable Replication

To stop replication entirely, delete the ClusterSync CR. The internal `release-lock` finalizer releases the Lease first, and the controller's ownerReferences GC the PCSM Deployment, `<cr>-pcsm-secret`, and `<cr>-pcsm-target-user` Secret. The user-provided `source.credentialsSecret` is not deleted (not owned by the CR). The MongoDB `clustersync-<cr-name>` user on the target cluster is not dropped (§5.3).

```bash
kubectl delete psmdb-clustersync cluster1-sync
```

---

## 8. Error Handling and Edge Cases

> **Guiding principle (§3.4):** apart from the one deliberate failure self-heal (`resume --from-failure`), the operator only issues PCSM CLI commands in response to an explicit `spec.mode` change. It never auto-starts fresh, never auto-finalizes, never scales the Deployment on its own. The scenarios below describe what the controller observes and what the user must do.

### 8.1 PCSM Process Crash or Pod Restart

**Scenario:** The PCSM pod crashes or is evicted while `spec.mode=running`.

**Expected behavior:**
- Kubernetes restarts the pod automatically (`restartPolicy: Always`). This is a pod-level guarantee, not an operator action.
- PCSM's internal recovery (Observation 2) takes over: a crash during initial sync restarts initial sync from scratch; a crash during real-time replication resumes from the last stored checkpoint.
- The controller does **not** issue a fresh `pcsm start`. `spec.mode` is still `running`, `status.mode` is still `running`, so no transition is required — the controller just refreshes `status.state` from `pcsm status`.
- If PCSM ends up in `state=failed` after a crash loop, the controller applies the self-heal in §8.2.

### 8.2 PCSM Reports `failed` (Source Unreachable, Source Misconfiguration, etc.)

**Scenario:** `pcsm status` returns `state=failed` — for example because the source cluster is unreachable, the source oplog has rolled over past the checkpoint, the source/target major versions differ, or one of the disallowed sharding admin commands ran on the source (Constraint 3).

**Expected behavior:**
- Controller writes `status.state=failed` and populates `status.error` with PCSM's error message; the `Running` condition flips to `False` with reason `PCSMNotRunning`.
- If `spec.mode=running` and PCSM has started at least once, the controller runs `pcsm resume --from-failure` on the next reconcile as the self-heal path (§3.4). For transient failures (source briefly unreachable, network blip) this clears the failure without user action.
- If the failure keeps recurring, the reconcile loop keeps re-issuing `resume --from-failure`. The controller does **not** back off automatically. The user is expected to notice the persistent failure via `status.error` / the `Running` condition and either pause the retry loop or re-create the CR.
- Controller does **not** scale the Deployment down or delete the pod. The pod continues to run; PCSM may continue to report `failed` between retry attempts.
- User-driven recovery (§7.6):
  - **Path A — pause while you diagnose:** `spec.mode: paused` stops the retry loop. Fix the source-side cause, then `spec.mode: running` resumes.
  - **Path B — start over from scratch:** delete the CR and recreate it (there is no `spec.mode: reset` in the first iteration; the CR delete/recreate is the fresh-initial-sync path).

**Common source-side root causes the user is responsible for fixing:**

| Symptom in `status.error` | Likely cause | User remediation (on source cluster) |
|---------------------------|--------------|---------------------------------------|
| Checkpoint position no longer available in oplog | Oplog retention window is shorter than the replication gap | Increase oplog size (`replSetResizeOplog`) or retention hours on the source replset; then delete + recreate the ClusterSync CR (full initial sync). |
| `movePrimary` / `reshardCollection` / `unshardCollection` / `refineCollectionShardKey` ran on the source | Constraint 3: these break replication and force a full initial sync | Avoid running these on the source during replication; recover by deleting + recreating the ClusterSync CR. |
| Source MongoDB major version differs from target | Constraint 1: cross-major replication unsupported | Match major versions on both sides before retrying |
| Network partition between PCSM and source | Source cluster unreachable | Restore network connectivity; transient failures clear via the self-heal path; persistent ones require §7.6 Path A. |

The operator does not attempt to diagnose or auto-remediate any of these — they all require visibility into and changes on the source cluster, which is out of the operator's reach.

### 8.3 Cluster Pause While PCSM Is Running

**Scenario:** User sets `spec.pause=true` on the PSMDB CR, which scales down MongoDB StatefulSets, causing PCSM to lose its target connection.

**Expected behavior in the first iteration (no cross-controller coordination):**
- The PSMDB reconciler proceeds with the scale-down without consulting or notifying the ClusterSync controller.
- PCSM observes the disconnected target and typically transitions to `state=failed`. The ClusterSync controller then applies the failure self-heal from §8.2, which keeps retrying `pcsm resume --from-failure` and — once the target comes back — reconnects on its own.
- To avoid the failure/retry churn during a planned pause, the user should manually flip `spec.mode: paused` on the ClusterSync CR *before* pausing the target cluster, and back to `running` after unpause.

Cross-controller coordination (the PSMDB reconciler auto-signalling the ClusterSync CR to pause first) was in the ADR's original design but is deferred (§1.3, §11 Q3). Revisit once operational feedback shows the manual sequencing is a real burden.

### 8.4 Backup or Restore While ClusterSync Is Active

**Scenario:** A backup or restore is requested against a cluster that has a non-terminal ClusterSync CR targeting it.

**Expected behavior (both):**
- The backup controller (`checkClusterSyncLease`) checks the ClusterSync Lease before taking a fresh backup out of the `New`/`Waiting` state. If the Lease is held, the backup stays in `Waiting`, a `ClusterSyncActive` event is emitted, and it retries on the next reconcile.
- The restore controller (`checkClusterSyncLease`) applies the same rule for restores in `New`/`Waiting`.
- The Lease is held from the moment the ClusterSync controller starts reconciling a CR against the target cluster (before the Deployment exists) until either `status.state=finalized` or CR deletion. Backups and restores are therefore blocked for the entire non-terminal lifecycle.
- Conversely, the ClusterSync controller consults the backup Lease and lists in-flight `PerconaServerMongoDBRestore` CRs before firing any command that writes to the target (`start`, `resume`). If a backup or restore is already in-flight, PCSM transitions are held with a `ClusterBusy` event and retried later. Pause and finalize are always allowed because they stop or drain PCSM.

**Why block backups for the full lifecycle (not just initial sync):** PBM holds the backup cursor open while a backup runs. With PCSM continuously applying source writes onto the target, that cursor pins WiredTiger history aggressively and on-disk usage can grow unbounded for the duration of the backup, on top of PBM and PCSM contending for the oplog. The risk is the same in `running` and `paused` states.

**Why block restores for the full lifecycle:** A restore writes the full dataset onto the target, which conflicts with PCSM continuously applying source change-stream events. There is no safe interleave.

**Recommended workflow if a backup or restore is needed during an active migration:** finalize (`spec.mode: finalized`) if the migration is done, or delete the ClusterSync CR if not. Both release the Lease; the backup or restore then leaves `Waiting` on its next reconcile.

### 8.5 Source URI Changed During Active Replication

**Scenario:** User modifies `spec.source.uri` (or `spec.source.credentialsSecret`) on an existing ClusterSync CR.

**Expected behavior:**
- There is no admission webhook in the first iteration; the API accepts the edit.
- The controller re-builds `PCSM_SOURCE_URI` and re-writes `<cr>-pcsm-secret` on the next reconcile, but the running PCSM pod does not pick up the change until it restarts.
- If the pod then restarts (image update, deployment strategy change, node failure) it comes up talking to the new source and its stored checkpoint no longer refers to the same oplog — replication typically goes to `state=failed` and the self-heal cannot recover from an incompatible checkpoint. The safe workflow is: delete the CR, wait for GC, create a new CR with the new source.

**Constraint:** The checkpoint is tied to the original source's oplog. Changing the source invalidates the checkpoint (Constraint 4 makes restarting initial sync expensive).

### 8.6 MongoDB Version Mismatch

**Scenario:** Source and target clusters have different major MongoDB versions.

**Expected behavior:**
- If versions differ, PCSM will fail with its own error. The controller propagates this via `status.state=failed` and the `error` field.
- The operator does NOT perform a proactive version check against the source cluster. The operator currently only connects to its own local cluster; creating a MongoDB client connection to an external source using user-provided credentials adds complexity and may not always be possible (network access, firewalls). PCSM itself validates version compatibility on `pcsm start`.

**Constraint:** Due to Constraint 1, cross-major replication is unsupported.

---

## 9. Migration and Backward Compatibility

### 9.1 Existing Clusters

- The PSMDB CR schema is not modified by this change. Existing CRs behave identically after operator upgrade.
- No PCSM resources are created until the user creates a `PerconaServerMongoDBClusterSync` CR.
- The `clustersync-<cr-name>` MongoDB user is only created on clusters that have a ClusterSync CR targeting them.

### 9.2 CRD Compatibility

- The change introduces a new CRD (`PerconaServerMongoDBClusterSync`). Existing CRDs are not modified.
- The new CRD must be installed alongside the operator upgrade (added to `deploy/crd.yaml`, `deploy/bundle.yaml`, `deploy/cw-bundle.yaml`).
- CRD manifests need generation via `make generate`.
- Operator RBAC needs additional rules for the new resource (get/list/watch/create/update/patch/delete on `perconaservermongodbclustersyncs`).
- The backup and restore controllers get one additional read permission: `coordination.k8s.io/leases` (get) so they can consult the ClusterSync Lease before starting new operations (§8.4).
- The new controller registers in `pkg/apis/psmdb/v1/register.go` and is wired up in `cmd/manager/main.go` via `pkg/controller/add_perconaservermongodbclustersync.go` (following the backup/restore controller pattern).

---

## 10. Testing Strategy

### 10.1 E2E Test Scenarios

| Scenario | Cluster Type | What It Validates |
|----------|-------------|-------------------|
| Create ClusterSync CR with default `mode=running` | Single replset | Deployment, `<cr>-pcsm-secret`, `<cr>-pcsm-target-user` Secret, and ClusterSync Lease created; controller runs `pcsm start` on first reconcile; `status.state` advances to `running`; data appears on the target. |
| Create ClusterSync CR with `mode: paused` (two-step start) | Single replset | Resources created but PCSM stays idle; no `pcsm start` until user flips to `running`. |
| Pause and resume replication via `spec.mode` | Single replset | `running → paused` runs `pcsm pause` and sets `status.mode=paused`; `paused → running` runs `pcsm resume`; data continues flowing. |
| Re-apply same `mode: running` manifest | Single replset | `nextAction` returns `actionNone`; no second CLI command; `status` unchanged; GitOps drift check stays clean. |
| Delete ClusterSync CR | Single replset | Internal `release-lock` finalizer releases the Lease; Deployment, `<cr>-pcsm-secret`, and `<cr>-pcsm-target-user` Secret are GC'd via ownerReferences; the MongoDB `clustersync-<cr-name>` user still exists on the target (verified by querying the target's `system.users`). |
| Finalize migration via `mode: finalized` | Single replset | `running → finalized` runs `pcsm finalize` when caught up; `status.mode=finalized` and `Finalized` condition = `True`; ClusterSync Lease is released so a backup can proceed on the same target immediately. |
| Finalize from `mode=paused` when never started is a no-op | Single replset | `paused → finalized` on a CR whose PCSM never started returns `actionNone`; `status.mode` stays `paused`; no `pcsm finalize` runs. |
| Post-finalize resources stay | Single replset | After `status.state=finalized` the Deployment and both Secrets remain until the user deletes the CR (verified by `kubectl get`). |
| Finalize releases the backup gate | Single replset | Once `status.state=finalized`, a queued backup CR leaves `Waiting` and starts without deleting the ClusterSync CR. |
| PCSM pod crash during real-time replication | Single replset | Pod restarts via `restartPolicy: Always`; PCSM resumes from checkpoint (Observation 2); controller issues no extra CLI calls while state stays `running`. |
| PCSM reports `state=failed` (e.g., source unreachable) with `spec.mode=running` | Single replset | Controller sets `status.state=failed`, populates `status.error`, flips `Running` condition to `False`, and runs `pcsm resume --from-failure` on the next reconcile (self-heal from §3.4). |
| Recover from failed via pause (Path A) | Single replset | `running → paused → running` runs `pcsm pause` then `pcsm resume`; replication resumes from checkpoint. |
| Recover from failed via CR recreate (Path B) | Single replset | Delete CR → recreate CR; new CR runs `pcsm start` (fresh initial sync) and gets its own `clustersync-<cr-name>` user. |
| Backup while ClusterSync Lease is held is queued | Single replset | Backup CR stays in `Waiting` with a `ClusterSyncActive` event while the Lease is held; unblocks once the CR is finalized or deleted. |
| Restore while ClusterSync Lease is held is queued | Single replset | Same behavior as backup: `Waiting` + `ClusterSyncActive` event; unblocks on Lease release. |
| ClusterSync holds write action while backup is running | Single replset | With a backup Lease active, `paused → running` on the ClusterSync CR is held (no `pcsm start`/`resume`); a `ClusterBusy` event is emitted; the transition fires on a later reconcile once the backup finishes. |
| ClusterSync holds write action while restore is running | Single replset | Same as above, gated on a non-terminal PSMDBRestore CR against the same cluster. |
| `source.uri` change without pod restart | Single replset | `<cr>-pcsm-secret` is refreshed on the next reconcile but the running pod does not pick up the change until it restarts; existing replication keeps running with the old URI. |
| Version mismatch detection | Single replset | `pcsm start` fails; controller surfaces the error via `status.state=failed` and `status.error`; self-heal keeps retrying `pcsm resume --from-failure` (visible in status). |
| Two ClusterSync CRs targeting the same cluster | Single replset | Second CR fails to acquire the Lease; `status.error` names the foreign holder; second CR reconciles cleanly once the incumbent releases the Lease (finalize or delete). |
| Force-delete the CR mid-replication | Single replset | Internal `release-lock` finalizer runs before the CR is removed; Lease is released even without a graceful `kubectl delete`. |
| Sharded cluster replication | Sharded | Replication works via mongos; data and shard keys are replicated. |
| Namespace exclude filters | Single replset | Excluded collections are not replicated; all others are. |
| pcsmConfig log level flows to container args | Single replset | `spec.pcsmConfig.logLevel=debug` causes the PCSM container to run with `--log-level=debug` (verified via `kubectl describe`). |

---

## 11. Open Questions

1. **Target user creation and deletion:** The operator only manages the target user on the local (target) cluster. The source user is the user's responsibility. When should the target user be created and deleted?
   - *Resolution (revised 2026-06-04, refined at implementation time):* The operator creates the target user on the target cluster the first time it reconciles a ClusterSync CR, and **never drops it** — neither at finalize-time nor on CR deletion. Username is `clustersync-<cr-name>` so a fresh CR after a finalized one gets its own user. The K8s `<cr>-pcsm-target-user` Secret is owned by the ClusterSync CR and GCs via ownerReferences when the CR is deleted; the MongoDB user persists on the target. On every reconcile the controller re-applies password and roles from the Secret onto the user, healing drift (e.g., when the CR is recreated). This reverses the earlier decision (2026-05-21) which had the operator drop the user at finalize-time. The reversal removes the operator's need to hold `dropUser` privileges, eliminates a stuck-deletion failure mode (`dropUser` against an unreachable target blocking `kubectl delete`), and matches the broader posture that user removal is a cluster-admin decision. To suspend replication without removing PCSM, set `spec.mode=paused` rather than deleting the CR.

2. **Cluster-wide mode conflicts:** If two CRs each define `clusterSync` pointing at the same target, conflicting PCSM Deployments may be created. How should the operator handle this?
   - *Resolution (revised at implementation time):* The cardinality Lease (§4.1) is named `psmdb-<clusterName>-clustersync-lock` and lives **in the same namespace as the ClusterSync CR and the target PSMDB CR**, not in the operator's own namespace. Two PSMDB clusters sharing a name across different namespaces cannot collide because their Leases live in different namespaces; the namespace is implicit in the Lease's location. Same-namespace collisions surface as a persistent `status.error` on the losing CR (`clustersync lease ... held by "<other-cr>-<uid>", not by this CR`) that clears once the incumbent releases the Lease. (Earlier ADR revisions used the name `psmdb-clustersync-<clusterName>-lock`; the shipped name switched the order of the segments for consistency with the existing backup Lease naming.)

3. **Cluster pause interaction:** When `spec.pause=true`, should the operator automatically pause PCSM first?
   - *Resolution (revised at implementation time — deferred):* Cross-controller coordination is **not** implemented in the first iteration. Pausing the target while PCSM is running produces a `state=failed` on PCSM that the failure self-heal (§3.4) resolves once the target comes back. Users are expected to manually flip `spec.mode: paused` on the ClusterSync CR before pausing the target if they want to avoid the failure/retry churn. Auto-coordination via annotation (the original ADR design decided 2026-05-21) is a future iteration (§1.3, §8.3). This changes the "no automatic actions" story: the original design had one operator-internal exception (auto-pause on cluster pause); the shipped design has a different one (failure self-heal via `resume --from-failure`) driven by the standing `spec.mode=running` intent.

4. **Backup/restore and PCSM concurrency:** Should backups and restores be blocked on the target cluster while PCSM is active?
   - *Resolution:* Decided block both on the target cluster for the full ClusterSync lifecycle. Allowed only when no ClusterSync CR targets the cluster or the CR is `finalized` (Lease released). See Q11 for the mechanism.
   - *Why backups (not just during initial sync):* PBM holds the backup cursor open while a backup runs; with PCSM continuously applying source writes onto the target, the cursor pins WiredTiger history and on-disk usage can grow unbounded. The disk-growth risk is the same in `running` and `paused`.
   - *Why restores:* A restore overwrites data PCSM is continuously replicating onto. No safe interleave.

5. **Sync completion status:** How to expose "sync finished" in status?
   - *Resolution (revised at implementation time):* PCSM 0.9.0 does not distinguish initial sync from real-time replication — both are `state=running`. So there is no dedicated "initial sync complete" transition or condition to fire; users read `status.lagTimeSeconds` directly and set `spec.mode=finalized` when they're satisfied. The `Running` and `Finalized` conditions carry `LastTransitionTime`s so the migration timeline is still available. The original ADR proposed an `InitialSyncComplete` condition and a `state` enum that distinguished initial sync from real-time replication; both were dropped once we saw the upstream `pcsm status` surface.

6. **PCSM expose configuration:** Should PCSM be exposed via a Service with configurable `exposeType`?
   - *Resolution (revised at implementation time — deferred):* Not implemented in the first iteration. PCSM 0.9.0 binds its HTTP server to `127.0.0.1` only (upstream PCSM-345), so a Service on the pod IP would just get connection-refused. The operator uses `kubectl exec` for lifecycle control and container-local `curl` for probes, so no external endpoint is needed. Once upstream PCSM binds to `0.0.0.0`, the ADR's original `spec.expose` field becomes viable and can be added; §1.3 tracks the deferral.

7. **Log collector integration:** Should the PCSM Deployment get a Fluent Bit sidecar when `logcollector.enabled=true`?
   - *Resolution:* Decided 2026-05-29: out of scope for the first iteration. PCSM logs go to stdout/stderr on the PCSM container and can be scraped via standard Kubernetes log mechanisms. Logcollector/Fluent Bit sidecar injection may be revisited in a later iteration if there is demand.

8. **Vault integration:** Should PCSM credentials be sourced from Vault when `vaultSpec` is configured?
   - *Resolution:* Decided 2026-05-29: out of scope for the first iteration. PCSM credentials are sourced from Kubernetes Secrets only — `source.credentialsSecret` for the source, and the operator-managed `<clustersync-cr-name>-pcsm-target-user` Secret for `syncTargetUser`. Vault integration may be revisited once the broader operator surface aligns on a Vault-backed credentials pattern.

9. **TLS for the source connection:** Where do source TLS settings live on the spec?
   - *Resolution (revised at implementation time):* No typed sub-struct. `spec.source.tls` was dropped and source TLS is configured on the `spec.source.uri` connection string itself (e.g. `?tls=true&tlsCAFile=/etc/pcsm/tls/ca.crt`), with any required certificate files mounted through `spec.env` or the pod-level fields. Target TLS is auto-derived from the target PSMDB CR's TLS secrets and is not user-configurable here. Rationale: matching PCSM's actual configuration surface — every PCSM TLS knob is already reachable via URI query parameters or `PCSM_*` env vars, so a typed sub-struct would just be a renamed pass-through.

10. **syncTargetUser management pattern:** The operator currently manages all system users via a single shared Secret (`spec.secrets.users`). With PCSM living in a separate CRD, the sync target user has a lifecycle bound to a different CR than the cluster's other system users.
    - *Resolution:* Decided custom user pattern on 2026-05-21. The target user is not part of the core admin users, so it does not belong in the shared `spec.secrets.users` Secret. The ClusterSync controller owns a dedicated Secret (`<clustersync-cr-name>-pcsm-target-user`) with ownerReferences pointing at the ClusterSync CR. Aligns with "CR lifecycle = user lifecycle" and keeps K8s Secret GC tied to CR ownerReferences. The MongoDB user itself is never dropped (Q1). Custom users have different reconciliation logic than system users; the ClusterSync controller implements its own minimal user-management flow.

11. **Backup/restore controller integration with ClusterSync state:** The backup and restore controllers currently check PBM locks and K8s leases to block concurrent operations. To enforce the policy in §8.4, both controllers need to consult ClusterSync state. Options considered:
    - **(A)** List ClusterSync CRs targeting the same `clusterName` and read `status.state` (no external calls, but relies on status being up-to-date).
    - **(B)** Query PCSM directly (neither controller currently makes external calls; also requires listing ClusterSync CRs first, so adds work over A without removing it).
    - **(C)** Use a K8s Lease written by the ClusterSync controller as a signal to the backup/restore controllers.
    - *Resolution (revised at implementation time):* Decided **C**. Both controllers call `k8s.IsLeaseActive(ClusterSyncLeaseName(cluster.Name))` on their `New`/`Waiting` reconcile paths; if the Lease is held they stay in `Waiting` and emit a `ClusterSyncActive` event. The ClusterSync Lease is already needed for CR cardinality (§4.1), so folding it into the admission signal removes a whole class of code — the backup/restore controllers do not have to know about the `PerconaServerMongoDBClusterSync` type or its status enum. Trade-off vs Option A: a small eventual-consistency window between "PCSM stopped" and "Lease released," which is fine because the risks §8.4 guards against (backup-cursor disk growth, restore overwriting a target PCSM is still writing to) take minutes to materialize. Option A was the ADR's original resolution (2026-06-04); Option C shipped instead. The reverse-direction gate (ClusterSync controller checking backup Lease + in-flight restore CRs before firing write actions) is also implemented, so the coordination is bidirectional.

12. **Finalization support:** PCSM only creates indexes on the target when `pcsm finalize` is called. Without it, the target has data but incomplete indexes.
    - *Resolution:* Decided B on 2026-05-21, revised 2026-06-08 to fold finalize into the `spec.mode` enum (Q15). User asks for finalization by setting `spec.mode=finalized`; the controller runs `pcsm finalize` once PCSM is caught up and sets `status.mode=finalized` (terminal).
    - *Revised at implementation time:* Post-finalize cleanup is limited to releasing the ClusterSync Lease so backups/restores unblock. The Deployment and Secrets stay in place until the user deletes the CR (§5.5). The ADR originally proposed proactive Deployment/Secret deletion; that was deferred to keep the first iteration lean.
    - *Companion finalizer (added 2026-06-04, not shipped):* `percona.com/delete-clustersync-after-finalize` — a user-supplied entry in `metadata.finalizers` for GitOps auto-delete. Not implemented in the first iteration; users who want that behavior wrap `kubectl delete` in their own tooling. Revisit when there's demand.

13. **Sharded clusters with different topologies (asymmetric shard counts):** When replicating between sharded clusters whose shard counts differ (e.g., 3 shards on source → 7 on target), does the operator need to expose any mapping configuration?
    - *Resolution:* Decided 2026-06-04 — no. Per upstream PCSM 0.7.0+ docs, PCSM connects through mongos on both sides and "the cluster topology doesn't matter": source and target may have different numbers of shards with no extra configuration, because the target's mongos routes writes to the right shard from the preserved shard key. There is no X-to-Y mapping surface in PCSM and the operator does not introduce one. The earlier framing of this question (deferred because of "X-to-Y mapping") was based on a misread — there is nothing to map. The remaining caveat is upstream's **Technical Preview** label on sharded replication in PCSM 0.7.0+; the operator inherits that label for sharded ClusterSync CRs (see §6.1) but does not gate the feature behind a separate flag.

14. **Source user reuse across multiple ClusterSync CRs:** When several ClusterSync CRs replicate from the same source cluster (each into a different target), must each CR have its own source user, or can they share one?
    - *Resolution:* Decided 2026-05-29: the same source user can be reused. `source.credentialsSecret` is user-managed and the operator does not enforce uniqueness across ClusterSync CRs, so a single source user with read access on the source can back any number of ClusterSync CRs that point at that source. Contrast with `syncTargetUser`, which the operator owns per-ClusterSync CR on the target (see §11 Q10) — that one is intentionally not shared, because its lifecycle is bound to the ClusterSync CR. Operationally this means users avoid having to provision N source users for N parallel migrations from the same source.

15. **Lifecycle control surface — separate fields vs single enum:** The earlier ADR shape (through 2026-06-04) exposed three independent lifecycle fields: `spec.paused` (bool), `spec.finalize` (one-way bool), and `spec.resetGeneration` (monotonic counter). Each drove a different PCSM verb. Is that the right surface, or should they collapse into a single field?
    - *Context (2026-06-08, from team discussion):* PCSM has many failure modes the operator cannot diagnose (source oplog retention, version mismatches, sharding admin commands run outside the operator's view). Auto-recovery is a catch-22 risk: the operator sees an error, resets, restarts, sees the same error, loops. The team agreed the operator should not auto-restart into a catch-22 loop — some misconfigs require user action on the **source** cluster (e.g., increasing oplog retention) before any restart is safe.
    - *Resolution:* Decided 2026-06-08 — collapse the three fields into a single `spec.mode` enum. The controller acts on transitions (`spec.mode != status.mode`) and mirrors on success.
    - *Revised at implementation time — dropped `reset` and changed default to `running`:* PCSM 0.9.0 has no `pcsm reset` verb (there is `pcsm resume --from-failure`, which preserves the checkpoint), so `mode: reset` had nothing to map to. The fresh-initial-sync workflow is now "delete + recreate the CR" (§7.6 Path B). The failure catch-22 concern is addressed differently: `resume --from-failure` is idempotent and checkpoint-preserving, so the failure self-heal (§3.4) cannot silently wipe a working replication — the worst case is a repeating error the user sees in `status.error` and reacts to. Default flipped from `paused` to `running` because in practice users almost always want the CR to just start replicating; the two-step create-then-start workflow is still available via `mode: paused` at creation. Enum is now `paused | running | finalized`.
    - *Alternatives considered:*
      - **Keep three separate fields (the previous shape):** Forces the controller to reason about combinations (`paused=true & finalize=true`?), and auto-restart after reset came "for free" from the per-field logic — exactly the catch-22 the team is trying to avoid.
      - **Keep `reset` as an enum value:** Would need a fictional `pcsm reset` command or a manual pod-restart workflow to wipe the checkpoint. CR delete + recreate does the same thing with less controller state, so the enum value was dropped.
      - **Imperative `spec.command` enum + `spec.commandGeneration` counter:** Two-field intent, awkward re-fire pattern, doesn't compose with GitOps cleanly. Declarative enum + status-mirroring is simpler.

16. **Failure self-heal — does the "no automatic actions" rule allow it?** (raised at implementation time.)
    - *Context:* Once the `reset` mode was dropped (Q15), the ADR's original Path A ("user flips `running → paused → running` to retry from failure") became the only way to recover from a transient failure. That works but is noisy: transient source-connectivity failures happen often enough that requiring a manual mode dance for each one is bad ergonomics. Meanwhile PCSM's `resume --from-failure` is idempotent and checkpoint-preserving, so re-issuing it against a still-failing PCSM is harmless.
    - *Resolution:* Add one deliberate self-heal: when `spec.mode=running`, `status.mode=running`, `status.startedAt != nil`, and observed `state=failed`, the controller runs `pcsm resume --from-failure` without waiting for a spec change. The scope is tight (only checkpoint-preserving; never `start` fresh; only when the standing user intent is already `running`) so the ADR's "no catch-22 loops against unresolved source-side issues" concern still holds — a persistent failure produces a persistent, user-visible retry loop rather than a silent data event. `spec.mode: paused` stops the retry loop; the operator does not disable it automatically. Documented in §3.4 and §8.2.

---

## Appendix

### A. Glossary

| Term | Definition |
|------|------------|
| PCSM | Percona ClusterSync for MongoDB: replication tool for cross-cluster data sync |
| PBM | Percona Backup for MongoDB: backup tool for MongoDB |
| PMM | Percona Monitoring and Management |
| Sharding | A database architecture that partitions data by key ranges across multiple instances |
| PiTR | Point-in-Time Recovery via oplog replay |
| Initial sync | PCSM's first replication phase: clones data from source to target, then applies changes that occurred since the clone started |
| Real-time replication | PCSM's second phase: captures change stream events from source and applies them to target for continuous synchronization |
| Finalization | One-time operation that completes the migration by creating required indexes on the target and stopping PCSM |
| Checkpoint | Persisted position in the source's change stream for resumable real-time replication |

### B. Known PCSM Limitations

**Versions and topology:**
- MongoDB versions that reached End-of-Life are not supported
- PCSM connects only to the primary node in the replica set. You cannot force connection to secondary members using the `directConnection` option. This option is ignored.

**Sharded clusters:**
- PCSM replicates data but not metadata. The following information is not preserved from the source cluster:
  - The primary shard name for a collection (the target cluster may have a different primary shard name)
  - The chunk distribution information (the target cluster manages chunk distribution according to its own sharding configuration)
  - The configuration of zones for sharded data
- During data replication, the following commands are not supported: `movePrimary`, `reshardCollection`, `unshardCollection`, `refineCollectionShardKey`. Running them results in failed replication and you must start it anew, from the initial sync stage.

**Data types:**
- Queryable encryption is not supported
- Users and roles are not synchronized
- Timeseries collections are not supported
- `system.*` collections are not replicated
- Clustered collections with indexes that have the `expireAfterSeconds` field defined are not supported because the change stream does not provide a TTL value for the index
- Capped collections created or converted as the result of `cloneCollectionAsCapped` and `convertToCapped` commands are not supported. These operations don't change the event and are not captured by the change streams.
- Percona Memory Engine is not supported
- Persistent Query Settings (added in MongoDB 8) are not supported
- Documents that have field names with periods and dollar signs are not supported

**Other:**
- Multiple source or multiple target clusters are not supported
- You cannot resume initial synchronization if an issue occurred. You must start it from scratch.
- Database upgrade during the sync, even in the paused state, is not supported
- Reverse synchronization is not supported
- External authentication via Kerberos, AWS and LDAP: needs clarification on whether the operator should support these auth mechanisms for PCSM connections