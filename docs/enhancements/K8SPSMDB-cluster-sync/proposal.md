# K8SPSMDB-1610: Integrate Percona ClusterSync for MongoDB

| Field        | Value                                    |
|--------------|------------------------------------------|
| Author       | George Kechagias                         |
| Status       | Draft                                    |
| Created      | 2026-05-12                               |
| Last Updated | 2026-06-04                               |
| Reviewers    |                                          |

---

## 1. Overview

The operator will deploy and manage Percona ClusterSync for MongoDB (PCSM) declaratively through a new `PerconaServerMongoDBClusterSync` CRD, enabling users to replicate data between MongoDB deployments in real time or perform one-time migrations with near-zero downtime. Today users must manage PCSM manually outside the operator. This benefits any team needing cross-cluster replication or migration without manual process orchestration.

### 1.1 Goals

- Provide a fully declarative way to set up cross-cluster replication and migrations through a dedicated CRD
- Manage PCSM lifecycle (start, pause, resume, finalize, failure recovery) within the operator reconciliation loop
- Expose replication status and lag in the CR status for observability
- Keep PCSM lifecycle independent of the target `PerconaServerMongoDB` CR — adding or removing replication does not require modifying the cluster CR

### 1.2 Non-Goals (Out of Scope)

- Reverse synchronization: PCSM does not support this upstream. Revisit when upstream adds support.
- Multi-source or multi-target replication: PCSM only supports single source/target pairs.
- Persistent Query Settings migration: PCSM does not replicate Persistent Query Settings (MongoDB 8+). Migration requires manual export/import via `$querySettings` aggregation and `setQuerySettings` admin command after finalization. The operator will not automate this.

### 1.3 Deferred (Future Iterations)

- Source cluster reference by CR name (`sourceCluster`): the operator manages both source and target clusters, so it could resolve connection details and create the source user automatically from a CR name reference instead of requiring manual `source.uri` and `source.credentialsSecret`. Deferred to a future iteration; first iteration uses a raw URI + Secret.
    - *Design constraint to remember when this lands:* the source URI built by the operator must honor the source PSMDB CR's `clusterServiceDNSMode`, the same way `PCSM_TARGET_URI` already does for the target. Today the source URI is user-supplied, so `clusterServiceDNSMode` does not apply — once the operator starts constructing it from a CR reference, the source side picks up the same DNS-mode responsibility as the target side.
- Version service integration for PCSM image: the PSMDB CR's version service flow auto-fills `spec.image`, `spec.backup.image`, and `spec.pmm.image`. The version service response does not yet expose a PCSM image, so the first iteration requires `spec.image` on the ClusterSync CR. Revisit once PCSM is added to the version service.
- PMM integration: may be added in a future iteration if monitoring of PCSM through PMM is needed.
- High availability / multiple PCSM instances per ClusterSync CR: the first iteration runs a single PCSM instance (`replicas: 1`, `Recreate` strategy). Constraint 6 forbids two PCSM processes writing to the same target simultaneously, so any future HA must be active/passive. The first-iteration design leaves room for this by keeping callers behind a Service and treating `GET /status` as the source of truth for state, so a future leader-elected setup can swap the underlying workload without changing the controller or CR surface. Today's failover is pod restart + checkpoint recovery (Observation 2).

---

## 2. Background

### 2.1 Core Concepts

- **Percona ClusterSync for MongoDB (PCSM):** A standalone binary that replicates data between two MongoDB deployments. It performs an initial sync followed by real-time replication.
- **Initial sync:** The first phase of replication. PCSM clones all data from the source to the target, then applies all changes that occurred since the clone started. For sharded clusters, PCSM first retrieves shard key information from the source and creates collections on the target with the same shard keys before copying data. Cannot resume after failure; must restart from scratch.
- **Real-time replication:** After initial sync completes, PCSM captures change stream events from the source and applies them to the target, ensuring real-time synchronization. Resumes from the last stored checkpoint after restart.
- **Checkpoint:** A persisted position in the source's change stream that allows PCSM to resume real-time replication after a restart without re-running initial sync.
- **Finalization:** A one-time operation that completes the migration. PCSM finalizes replication, creates required indexes on the target, and stops. After finalization, starting PCSM again begins a new initial sync from scratch.
- **PCSM workflow:** `start` (begin replication) → initial sync → real-time replication → `pause`/`resume` (control replication) → `finalize` (complete migration) → cutover (switch clients to target).
- **PCSM HTTP API:** PCSM exposes an HTTP API on port 2242. The operator uses `POST /start`, `POST /pause`, `POST /resume`, `POST /finalize`, `POST /reset`, and `GET /status` to control the PCSM lifecycle. `POST /finalize` is driven by the one-way `spec.finalize=true` switch (see §5.5 and §11 Q12). `POST /reset` is driven by the monotonic `spec.resetGeneration` counter: the controller calls `POST /reset` whenever `spec.resetGeneration > status.lastAppliedResetGeneration` and then writes the new value into status (spec is never mutated, so GitOps tools like Argo CD never see drift). The API does not require authentication; the operator communicates with PCSM via a ClusterIP Service within the Kubernetes network.

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
    → Reconcile Secrets
    │   → Read source.credentialsSecret (user-provided, source credentials)
    │   → Read/create syncTargetUser Secret (operator-managed, target credentials)
    → Reconcile PCSM Deployment (Recreate strategy)
    │   → PCSM container
    │       env: PCSM_SOURCE_URI (source.uri + source credentials)
    │       env: PCSM_TARGET_URI (auto-constructed from target PSMDB CR + syncTargetUser)
    │       ports: 2242 (HTTP API)
    → Control PCSM via HTTP API (<clustersync-cr-name>-pcsm:2242)
    │   → GET /status → read current PCSM state
    │   → POST /start, /pause, /resume, /finalize, /reset based on CR spec vs current state
    → Update ClusterSync CR status (state, lagTimeSeconds, error, conditions)
  ← Status update on ClusterSync CR

PerconaServerMongoDB (target CR, unchanged)
  → PSMDB Reconciler (existing)
    → Looks up ClusterSync CR(s) targeting this cluster for cross-controller coordination
      (cluster pause, backup admission, restore admission)
```

**Resources managed by the operator for PCSM (owned by the ClusterSync CR):**

| Resource | Purpose |
|----------|---------|
| Deployment | Runs the PCSM container. Uses `Recreate` strategy. |
| Service | Exposes the PCSM HTTP API (port 2242). Type is configurable via `spec.expose`, defaults to ClusterIP for operator-only access; LoadBalancer/NodePort for external monitoring (per §11 Q6). |
| Secret (syncTargetUser) | Target cluster credentials, created by the operator. The K8s Secret is deleted proactively when `status.state` transitions to `finalized`, or on ClusterSync CR deletion (via ownerReferences). The underlying MongoDB user on the target cluster is **not** dropped in either case (see §5.3). |
| Secret (source.credentialsSecret) | Source cluster credentials, created by the user. Read-only for the operator; not owned by the CR. |

**Reconciliation flow:**

1. **Deployment reconciliation:** On each reconcile, the ClusterSync controller ensures the
   PCSM Deployment exists and matches the desired state (image, env vars, resource limits).
   The PCSM Deployment runs a single replica (`replicas: 1`) with the `Recreate` strategy --
   see §5.1 and §5.2 for rationale, and §1.3 for the future HA path.
   On CR deletion, the controller does not run a pre-delete hook — child resources
   (Deployment, Service, `syncTargetUser` K8s Secret) GC via ownerReferences, and the
   `syncTargetUser` MongoDB user on the target is intentionally left in place (see §5.3).
   When `status.state` transitions to `finalized`, the controller proactively deletes the
   PCSM Deployment, Service, and `syncTargetUser` K8s Secret (see §5.5); the MongoDB user
   still stays on the target.

2. **Lifecycle control via HTTP API:** After the Deployment is ready, the controller calls
   `GET /status` to read the current PCSM state. It compares this against the desired state
   from the CR spec and issues the appropriate HTTP call:
   - PCSM not started → `POST /start`
   - `spec.paused=true` and PCSM state is `running` → `POST /pause`
   - `spec.paused=false` and PCSM state is `paused` → `POST /resume`
   - `spec.finalize=true` and PCSM is caught up → `POST /finalize` (one-time, terminal)
   - `spec.resetGeneration > status.lastAppliedResetGeneration` and `status.state ≠ finalized`
     → `POST /reset`, then advance `status.lastAppliedResetGeneration` to match spec via
     the status subresource (spec is never mutated; see §4.2 field notes for the GitOps
     rationale).

3. **Status propagation:** The controller maps the `GET /status` response to CR status fields:
   - `state` → `status.state`
   - `lagTimeSeconds` → `status.lagTimeSeconds`
   - `error` → `status.error`
   - `status.startedAt` is set once, the first time the controller observes PCSM transition
     out of `pending` (i.e., the first successful `POST /start`); it is not updated on
     subsequent restarts.
   - `status.lastAppliedResetGeneration` is written by the controller after a successful
     `POST /reset` (item 2 above); it is not derived from the `GET /status` payload.
   - State transitions also append/update entries in `status.conditions` (e.g., `InitialSyncComplete`, `Replicating`, `Finalized`) with their own `LastTransitionTime`, so transition history is captured in conditions rather than requiring dedicated timestamp fields for every state change.

4. **Interaction with the PSMDB reconciler:** The two controllers coordinate through CR
   state, not direct calls:
   - The PSMDB reconciler lists ClusterSync CRs in its namespace whose `spec.clusterName`
     matches the cluster being reconciled.
   - Before cluster pause (`spec.pause=true` on the PSMDB CR): if any matching ClusterSync
     CR is in `replicating` state, the PSMDB reconciler sets a pause request (annotation or
     status condition) that the ClusterSync controller acts on by calling `POST /pause`,
     then proceeds with scale-down.
   - The backup controller rejects new backup requests if any matching ClusterSync CR
     exists in a non-terminal state (`pending`, `initialSync`, `replicating`, `paused`).
     PBM holds the backup cursor open for the duration of the backup; with PCSM
     continuously applying source writes onto the target, the cursor pins large
     amounts of WiredTiger history and disk usage can grow unbounded.
   - The restore controller rejects new restore requests under the same conditions.
     A restore overwrites data PCSM is actively replicating, so it must wait until
     replication is finalized or the ClusterSync CR is deleted.

### 3.3 Key Observations

1. **PCSM is a standalone process:** It does not need to run on every replset member, making a separate Deployment the natural fit rather than a sidecar.
2. **PCSM auto-recovers on restart:** The operator does not need to detect the replication phase and issue different commands. Simply restarting the process triggers recovery (restart initial sync or resume real-time replication from checkpoint).
3. **Connection strings require credential injection:** The operator reads credentials from `source.credentialsSecret` (source) and the operator-managed `syncTargetUser` Secret (target), then injects them into `PCSM_SOURCE_URI` and `PCSM_TARGET_URI` with percent-encoding per RFC 3986. Credentials are never stored in the CR spec.
4. **Interaction with cluster pause:** If `spec.pause=true` scales down MongoDB, PCSM loses its connection. The operator must coordinate the pause/unpause sequence across the two controllers.
5. **Interaction with backups and restores:** Both must be blocked for the entire ClusterSync lifecycle. PBM holds the backup cursor open while a backup runs; with PCSM continuously applying source writes onto the target, the cursor pins WiredTiger history and disk usage can grow unbounded, on top of the oplog contention between PBM and PCSM. Restores are similarly incompatible: they overwrite data while PCSM is still applying change-stream events from the source, with no safe interleave.
6. **CR existence is the lifecycle signal:** A ClusterSync CR exists for the duration of a replication relationship. There is no `enabled` flag — creating the CR starts replication, deleting it tears everything down (Deployment, Service, syncTargetUser via ownerReferences).

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

The CR existence is the lifecycle signal — creating the CR starts replication;
deleting it tears down all managed resources via ownerReferences. There is no
`enabled` field.

**Cardinality:** At most one non-finalized ClusterSync CR may target a given
target cluster (namespace + `spec.clusterName`) at a time (PCSM Constraint 2 —
single source/target pair). Enforced by a `coordination.k8s.io/v1` Lease named
`psmdb-clustersync-<clusterName>-lock` **in the same namespace as the
ClusterSync CR and the target PSMDB CR** (not in the operator's own namespace).
The controller acquires the Lease before transitioning out of `pending` and
releases it when the CR reaches `finalized` or is deleted. Putting the Lease
next to the cluster it guards means cluster-wide-mode deployments are
collision-free for free — two PSMDB clusters in different namespaces sharing a
`clusterName` each get their own Lease in their own namespace — and the
operator only needs `coordination.k8s.io/leases` RBAC in the namespaces it
watches, not a privileged Lease scope in its own namespace. A second CR
targeting the same namespace+`clusterName` fails to acquire the Lease and
transitions to `status.state=failed` with a clear reason.

**OwnerReferences:** Following the backup/restore precedent, the ClusterSync CR is
NOT owned by the target PSMDB CR — deleting the cluster does not auto-delete
ClusterSync history. Child resources (Deployment, Service, syncTargetUser Secret)
ARE owned by the ClusterSync CR.

### 4.2 Spec

```go
type PerconaServerMongoDBClusterSyncSpec struct {
    ClusterName string `json:"clusterName"`

    Image            string                        `json:"image"`
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

    Source ClusterSyncSource `json:"source"`

    Expose Expose `json:"expose,omitempty"`

    ExcludeNamespaces []string `json:"excludeNamespaces,omitempty"`

    Paused          bool  `json:"paused,omitempty"`
    Finalize        bool  `json:"finalize,omitempty"`
    ResetGeneration int64 `json:"resetGeneration,omitempty"`
}

type ClusterSyncSource struct {
    URI               string          `json:"uri"`
    CredentialsSecret string          `json:"credentialsSecret"`
    TLS               *ClusterSyncTLS `json:"tls,omitempty"`
}

type ClusterSyncTLS struct {
    Enabled bool   `json:"enabled,omitempty"`
    Secret  string `json:"secret,omitempty"`
}
```

**Field notes:**

- `clusterName`: references the target `PerconaServerMongoDB` CR in the same namespace. The controller resolves it to construct `PCSM_TARGET_URI`. Multiple PSMDB clusters in one namespace are supported — each ClusterSync CR picks exactly one by name; the cardinality Lease (§4.1) is keyed by namespace + `clusterName` so co-located clusters cannot collide.
- `image`: required in the first iteration. See §1.3 (Deferred) for the version service plan.
- `source.uri`: connection string for the source cluster without credentials. Format: `mongodb://h1:p1,h2:p2/admin?replicaSet=rs0`. Credentials from `source.credentialsSecret` are injected at runtime.
- `source.credentialsSecret`: name of a Kubernetes Secret with `username` and `password` keys. The operator percent-encodes both per RFC 3986 before injecting them into `PCSM_SOURCE_URI`.
- `source.tls`: when `source.tls.enabled=true`, `source.tls.secret` must point to a Secret containing the TLS certificates PCSM should use for the source connection. Target TLS is not user-configurable — the operator auto-derives it from the target PSMDB CR's existing TLS secrets (`spec.secrets.ssl` / `spec.secrets.sslInternal`).
- `expose`: Service configuration for the PCSM HTTP API. Reuses the existing `Expose` struct from `psmdb_types.go` (type, loadBalancerSourceRanges, loadBalancerClass, annotations, labels, traffic policies). Defaults to ClusterIP (operator-only access). Set `expose.type=LoadBalancer` or `NodePort` to allow monitoring or interacting with PCSM from outside the cluster.
- `excludeNamespaces`: list of **MongoDB namespaces** (NOT Kubernetes namespaces) to exclude from replication. A MongoDB namespace is `<database>.<collection>` (e.g., `db3.collection3`); a database-wide exclude is `<database>.*`. Passed through to PCSM verbatim as its `--exclude-namespaces` argument. The name is inherited from PCSM's CLI surface; see §11 for the rename discussion if the API is still under review.
- `paused`: when `true` and PCSM is `running`, the controller calls `POST /pause`. Setting back to `false` calls `POST /resume`.
- `finalize`: one-way switch. When set to `true`, the controller calls `POST /finalize` once PCSM is caught up, then transitions `status.state` to `finalized` (terminal). Once finalized, the controller deletes the PCSM Deployment, Service, and `syncTargetUser` Secret; the ClusterSync CR itself remains as a read-only historical record by default, or is auto-deleted if the user opted in via the `percona.com/delete-clustersync-after-finalize` finalizer string (see §4.2.1). Subsequent spec changes other than CR deletion are rejected (see §5.5).
- `resetGeneration`: monotonic counter that requests a destructive reset. To trigger a reset, the user bumps `spec.resetGeneration` to a value greater than `status.lastAppliedResetGeneration` (typically `lastApplied + 1`). The controller observes the mismatch, calls `POST /reset` against PCSM (wiping checkpoint state and forcing a fresh initial sync), then writes the new value into `status.lastAppliedResetGeneration` via the status subresource. The controller **never** writes back to spec, so GitOps tools (Argo CD, Flux) do not see drift and re-applying the same manifest is idempotent. Rejected when `status.state=finalized`. The field defaults to `0`; setting it to `1` (or any positive value greater than the last applied) on a brand-new CR has no special semantic — PCSM will simply start fresh as it would have anyway. Operators should treat this as a destructive operation — initial sync starts over from scratch.

### 4.2.1 Finalizer Strings

Following the convention used by the `PerconaServerMongoDB` CR (`percona.com/delete-psmdb-pods-in-order`, `percona.com/delete-psmdb-pvc`, `percona.com/delete-pitr-chunks`), the ClusterSync CR exposes a small set of well-known finalizer strings in `metadata.finalizers` that the user adds or removes to toggle a behavior.

| Finalizer string | Set by | Purpose |
|------------------|--------|---------|
| `percona.com/delete-clustersync-after-finalize` | User (opt-in) | Post-finalize hook: when `status.state` transitions to `finalized` and the post-finalize cleanup of Deployment/Service/`syncTargetUser` K8s Secret completes, the controller calls `Delete` on the ClusterSync CR itself, instead of leaving it as a historical record. Covers GitOps workflows where finalized CRs accumulating in the cluster fights the Git-as-source-of-truth model. May be added at CR creation or any time before finalize completes; adding it after `status.state=finalized` triggers deletion on the next reconcile. Removing it before finalize keeps the default historical-record behavior. |

Example — opt into auto-delete after finalize:

```yaml
metadata:
  name: cluster1-sync
  finalizers:
    - percona.com/delete-clustersync-after-finalize   # opt-in; comment out to keep the CR as a historical record
```

The controller does not register a Kubernetes finalizer of its own — child resources (Deployment, Service, `syncTargetUser` Secret) GC via ownerReferences, and the operator does **not** drop the `syncTargetUser` MongoDB user on the target (see §5.3), so there is no work that needs to happen before ownerReferences-driven GC.

### 4.3 Status

```go
type PerconaServerMongoDBClusterSyncStatus struct {
    State          ClusterSyncState `json:"state,omitempty"`
    LagTimeSeconds int64            `json:"lagTimeSeconds,omitempty"`
    Error          string           `json:"error,omitempty"`
    StartedAt      *metav1.Time     `json:"startedAt,omitempty"`

    // LastAppliedResetGeneration mirrors spec.resetGeneration once a reset has
    // been applied. The controller calls POST /reset whenever spec is greater
    // than this value, then advances this field via the status subresource.
    // Used to make spec.resetGeneration idempotent under GitOps re-apply.
    LastAppliedResetGeneration int64 `json:"lastAppliedResetGeneration,omitempty"`

    Conditions []metav1.Condition `json:"conditions,omitempty"`
}

type ClusterSyncState string

const (
    ClusterSyncStateNew         ClusterSyncState = ""
    ClusterSyncStatePending     ClusterSyncState = "pending"
    ClusterSyncStateInitialSync ClusterSyncState = "initialSync"
    ClusterSyncStateReplicating ClusterSyncState = "replicating"
    ClusterSyncStatePaused      ClusterSyncState = "paused"
    ClusterSyncStateFailed      ClusterSyncState = "failed"
    ClusterSyncStateFinalized   ClusterSyncState = "finalized"
)
```

### 4.4 Printer Columns

```
NAME | CLUSTER | STATE | LAG(s) | AGE
```

Mapped from: `metadata.name`, `spec.clusterName`, `status.state`, `status.lagTimeSeconds`, `metadata.creationTimestamp`.

`State=initialSync` already conveys "initial sync in progress"; `replicating`/`paused`/`finalized` already convey "initial sync done." A separate INITIAL-SYNC column would duplicate `state`.

### 4.5 Internal Contracts

- **`PCSM_SOURCE_URI`** environment variable: Constructed from `spec.source.uri` with credentials from `spec.source.credentialsSecret` injected and percent-encoded per RFC 3986.
- **`PCSM_TARGET_URI`** environment variable: Constructed automatically by the controller from the target PSMDB CR (resolved via `spec.clusterName`, replset members or mongos endpoints) with `syncTargetUser` credentials.

### 4.6 User-Facing Behavior Changes

**New resources visible in the namespace:**
- A `PerconaServerMongoDBClusterSync` CR (visible via `kubectl get psmdb-clustersync`).
- A PCSM Deployment (visible via `kubectl get deployments`), named `<clustersync-cr-name>-pcsm`.
- A `syncTargetUser` Secret (visible via `kubectl get secrets`), created when the ClusterSync CR is created.

**PSMDB CR:** No fields added or modified. The PSMDB CR remains unchanged.

**Kubernetes Events emitted by the controller (on the ClusterSync CR):**
- `ClusterSyncStarted`: replication started via `POST /start`.
- `ClusterSyncPaused`: replication paused via `POST /pause`.
- `ClusterSyncResumed`: replication resumed via `POST /resume`.
- `ClusterSyncFinalized`: migration finalized via `POST /finalize`.
- `ClusterSyncReset`: destructive reset applied via `POST /reset`; emitted after `status.lastAppliedResetGeneration` is advanced to match `spec.resetGeneration`. The event message includes the generation value applied.
- `ClusterSyncFailed`: PCSM entered a failed state; `error` field has details.
- `InitialSyncComplete`: initial sync finished, PCSM switched to real-time replication.

**Operator log messages:**
- Lifecycle transitions (start, pause, resume, finalize) are logged at info level.
- Errors and failed HTTP API calls are logged at error level with the PCSM response.

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
| `syncTargetUser` | Operator | Target cluster (local) | `restore`, `clusterMonitor`, `clusterManager`, `readWriteAnyDatabase` |

**Chosen approach (decided 2026-06-04):** The ClusterSync controller creates `syncTargetUser` on the target cluster the first time it reconciles a ClusterSync CR for that cluster. The operator does **not** drop the user — it persists on the target across `status.state` transitions to `finalized` and across ClusterSync CR deletion. The K8s `syncTargetUser` Secret is still owned by the ClusterSync CR and GCs via ownerReferences when the CR is deleted (or proactively at finalize-time, per §5.5); the MongoDB user itself stays on the target.

User creation is idempotent — re-running the create flow against an existing `syncTargetUser` is a no-op. **Credentials are static for the user's lifetime** (decided 2026-06-04): the operator does not rotate the password on a schedule, does not regenerate it on K8s Secret recreation, and does not detect drift if a cluster admin changes the password out of band. Password reset is treated as a cluster-admin operation outside the operator's scope. If rotation is needed later, it will be a separate design.

**Why:** Treating user creation as one-way matches the operator's broader posture on user management — once a user is created on a cluster, removing it is a cluster-admin decision rather than an operator action. It also avoids requiring the operator to hold `dropUser` privileges and removes a class of failure modes where a stuck `dropUser` call blocks CR deletion (no `dropUser` call → no Kubernetes finalizer needed; child resources GC via ownerReferences without a pre-hook). To suspend replication without removing PCSM, set `spec.paused=true` instead of deleting the CR.

The source cluster credentials are provided by the user via `spec.source.credentialsSecret`, a Kubernetes Secret with `username` and `password` keys. The operator reads the Secret and injects the credentials into `PCSM_SOURCE_URI` with percent-encoding per RFC 3986. Credentials are never stored in the CR spec.

**Alternatives considered:**

| Alternative | Why Rejected |
|------------|--------------|
| Drop the target user at finalize and on CR delete | Earlier design choice (until 2026-06-04). Required a Kubernetes finalizer to keep MongoDB-user drop ahead of K8s-Secret GC, gave the operator a class of stuck-deletion failure modes (`dropUser` against an unreachable target blocks `kubectl delete`), and required broader privileges on the target. Reversed in favor of "operator never drops users it created." |
| Operator creates source user on the source cluster | The source cluster may be external and not managed by this operator instance |

### 5.4 Configuration Change Handling

**Chosen approach:** Make `spec.source` (URI and credentialsSecret) immutable after CR creation; reject the update via an admission webhook. To change source, the user deletes and recreates the CR. Image updates, pod knobs, `spec.paused`, and `spec.expose` (including `expose.type`) remain mutable. Namespace filter changes are mutable but warn in status (PCSM does not re-evaluate filters retroactively). After `status.state=finalized`, all spec changes are rejected (the CR is effectively read-only).

**Why:** The checkpoint is tied to the original source's oplog (Constraint 4); a mid-flight source change has no safe semantics. Making the field immutable surfaces this at admission time with a clear error rather than later via a `failed` state; it's the same outcome (user has to recreate) without the half-broken intermediate state. Image updates remain safe due to PCSM's built-in recovery (Observation 2). `spec.expose` (including switching between ClusterIP / LoadBalancer / NodePort) is mutable: PCSM connects to MongoDB on the source and target — not through its own Service — so the brief connection drop a Service swap might cause clients of `GET /status` is contained to the operator's own monitoring path, and PCSM's checkpoint resume on reconnect makes it safe. Deleting and recreating the CR is acceptable because the CR lifecycle is already the unit of teardown (Deployment, Service, syncTargetUser all GC via ownerReferences), and the replacement CR triggers a fresh initial sync from the new source. Finalization is terminal for the CR; running PCSM again means creating a new ClusterSync CR (the finalized CR can stay as a record; §4.1 cardinality only counts non-finalized CRs).

**Alternatives considered:**

| Alternative | Why Rejected |
|------------|--------------|
| Allow source updates freely | Silently restarting a multi-day initial sync against a different source is too expensive and surprising |
| Accept update, transition to `failed`, require manual PCSM reset | Same end state as immutability (recreate), but leaves the CR in a confusing half-broken state and adds a manual reset step |
| Block all updates during initial sync | Too restrictive for safe changes like image updates |

### 5.5 Post-Finalize Resource Cleanup

**Chosen approach:** Once `status.state=finalized`, the controller deletes the PCSM Deployment, Service, and `syncTargetUser` K8s Secret. The `syncTargetUser` MongoDB user on the target is **not** dropped (see §5.3 — the operator never removes users it created). By default the ClusterSync CR itself stays as a read-only record until the user deletes it. Users who do not want the historical-record behavior add the `percona.com/delete-clustersync-after-finalize` string to `metadata.finalizers` (see §4.2.1); the controller then deletes the CR itself as the last step of post-finalize cleanup.

**Why:** PCSM has stopped after finalize and restarting it would only trigger a fresh initial sync, so the Deployment serves no purpose. The `syncTargetUser` Secret on the K8s side is removed because its only consumer (the PCSM pod) is gone, but the MongoDB user on the target is left in place — consistent with the broader "operator does not drop users it created" posture in §5.3. The default of keeping the CR preserves an auditable record of the migration and keeps the backup/restore admission policy in §8.4 simple ("no CR or CR is `finalized`", both allowed). The finalizer-string opt-in covers GitOps workflows where leaving finalized CRs around in the cluster fights the Git-as-source-of-truth model, and reuses the existing PSMDB convention (`percona.com/delete-psmdb-pvc`, `percona.com/delete-pitr-chunks`) so the surface is familiar.

**Alternatives considered:**

| Alternative | Why Rejected |
|------------|--------------|
| Leave Deployment/Service/Secret running after finalize | Idle pod consumes resources; user has stopped consuming the credentials, so the Secret is dead weight |
| Drop the `syncTargetUser` MongoDB user on the target at finalize | Earlier design choice; see §5.3 for the rationale behind reversing it |
| Auto-delete the CR itself unconditionally | Erases the historical record for users who want it; the finalizer-string opt-in covers the GitOps use case without forcing it on everyone |
| `spec.deleteAfterFinalize` bool on the spec | Duplicates the existing PSMDB convention for opt-in delete behavior; the finalizer-string pattern is already familiar to PSMDB users from `delete-psmdb-pvc` / `delete-pitr-chunks` |

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

Create a `PerconaServerMongoDBClusterSync` CR alongside the target PSMDB CR:

```yaml
apiVersion: psmdb.percona.com/v1
kind: PerconaServerMongoDBClusterSync
metadata:
  name: cluster1-sync
spec:
  clusterName: cluster1
  image: percona/percona-clustersync-mongodb:1.0.0
  source:
    uri: mongodb://host1:27017,host2:27017,host3:27017/admin?replicaSet=rs0
    credentialsSecret: pcsm-source-credentials   # Secret with username/password keys
    tls:
      enabled: true
      secret: pcsm-ssl
  excludeNamespaces:
    - db3.collection3
```

### 7.3 Pause and Resume Replication

```yaml
# Pause replication
spec:
  paused: true    # Controller calls POST /pause

# Resume replication
spec:
  paused: false   # Controller calls POST /resume
```

### 7.4 Sharded Cluster Replication

```yaml
apiVersion: psmdb.percona.com/v1
kind: PerconaServerMongoDBClusterSync
metadata:
  name: cluster1-sync
spec:
  clusterName: cluster1   # PSMDB CR with sharding.enabled=true
  image: percona/percona-clustersync-mongodb:1.0.0
  source:
    uri: mongodb://mongos1:27017,mongos2:27017/admin
    credentialsSecret: pcsm-source-credentials
```

### 7.5 Finalize Migration

After verifying lag is acceptable, set `finalize: true` to complete the migration. The controller calls `POST /finalize` once, creates required indexes on the target, transitions the CR to `status.state=finalized`, and then deletes the PCSM Deployment, Service, and `syncTargetUser` Secret (see §5.5). The CR itself stays as a read-only historical record until the user removes it. This is terminal for that CR; to run a new migration the user creates a new ClusterSync CR (any name); the finalized CR does not block it because the cardinality rule in §4.1 only counts non-finalized CRs.

```yaml
metadata:
  name: cluster1-sync
  finalizers:
    - percona.com/delete-clustersync-after-finalize   # opt-in (see §4.2.1); omit to keep the CR as a historical record
spec:
  clusterName: cluster1
  image: percona/percona-clustersync-mongodb:1.0.0
  source:
    uri: mongodb://host1:27017/admin?replicaSet=rs0
    credentialsSecret: pcsm-source-credentials
  finalize: true   # one-way switch; controller calls POST /finalize
```

For GitOps workflows where finalized CRs piling up in the cluster fights Git as source of truth, include the `percona.com/delete-clustersync-after-finalize` finalizer in `metadata.finalizers`. The controller does its post-finalize cleanup as usual, then deletes the CR itself. Same convention as `delete-psmdb-pvc` / `delete-pitr-chunks` on the PSMDB CR.

### 7.6 Restart Replication From Scratch (Reset)

If replication is broken in a way PCSM cannot auto-recover from and a fresh initial sync is needed, bump `spec.resetGeneration`:

```yaml
spec:
  clusterName: cluster1
  image: percona/percona-clustersync-mongodb:1.0.0
  source:
    uri: mongodb://host1:27017/admin?replicaSet=rs0
    credentialsSecret: pcsm-source-credentials
  resetGeneration: 1   # bump to any value greater than status.lastAppliedResetGeneration
```

The controller observes `spec.resetGeneration > status.lastAppliedResetGeneration`, calls `POST /reset` against PCSM (wiping checkpoint state), and advances `status.lastAppliedResetGeneration` to match. Subsequent resets bump the value again (`2`, `3`, …). The controller never writes back to spec, so re-applying the same manifest is a no-op and GitOps tools like Argo CD never see drift. This is destructive — initial sync restarts from scratch — and is rejected once `status.state=finalized`.

### 7.7 Disable Replication

To stop replication entirely, delete the ClusterSync CR. The controller's ownerReferences GC the PCSM Deployment, Service, and `syncTargetUser` Secret. The user-provided `source.credentialsSecret` is not deleted (not owned by the CR).

```bash
kubectl delete psmdb-clustersync cluster1-sync
```

---

## 8. Error Handling and Edge Cases

### 8.1 PCSM Process Crash or Pod Restart

**Scenario:** The PCSM pod crashes or is evicted during replication.

**Expected behavior:**
- Kubernetes restarts the pod automatically (`restartPolicy: Always`).
- If crash occurred during initial sync: PCSM restarts initial sync from scratch automatically.
- If crash occurred during real-time replication: PCSM resumes from the last stored checkpoint automatically.
- Controller updates the ClusterSync CR's `status` after restart to reflect current state.
- No operator intervention needed beyond monitoring.

### 8.2 Source Cluster Unreachable

**Scenario:** The source MongoDB cluster becomes unreachable (network partition, source cluster down).

**Expected behavior:**
- PCSM enters a failed state.
- Controller sets `status.state=failed` and populates `error` field.
- Controller requeues reconciliation to monitor recovery.
- When connectivity is restored, PCSM auto-recovers (resumes real-time replication from checkpoint or restarts initial sync).

### 8.3 Cluster Pause While PCSM Is Running

**Scenario:** User sets `spec.pause=true` on the PSMDB CR, which scales down MongoDB StatefulSets, causing PCSM to lose its target connection.

**Expected behavior:**
- PSMDB reconciler detects `spec.pause=true` and a matching ClusterSync CR in `replicating` state. It signals the ClusterSync controller (annotation or status condition on the ClusterSync CR) to pause PCSM.
- The ClusterSync controller calls `POST /pause`. Once `status.state=paused`, the PSMDB reconciler proceeds with scale-down.
- On unpause, the PSMDB reconciler brings MongoDB back up, waits for readiness, then clears the pause signal. The ClusterSync controller resumes PCSM.
- This prevents unnecessary failure/recovery cycles.

### 8.4 Backup or Restore While ClusterSync Is Active

**Scenario:** A backup or restore is requested against a cluster that has a non-terminal ClusterSync CR targeting it.

**Expected behavior (both):**
- Backup and restore controllers list ClusterSync CRs with `spec.clusterName` matching the cluster. If any has `status.state ∈ {pending, initialSync, replicating, paused}`, the request is rejected (extends existing concurrent backup/restore prevention from K8SPSMDB-1643).
- Backups and restores are allowed only when no ClusterSync CR targets the cluster, or when the CR's `status.state=finalized` (terminal, PCSM has stopped).

**Why block backups for the full lifecycle (not just initial sync):** PBM holds the backup cursor open while a backup runs. With PCSM continuously applying source writes onto the target, that cursor pins WiredTiger history aggressively and on-disk usage can grow unbounded for the duration of the backup, on top of PBM and PCSM contending for the oplog. The risk is the same in `initialSync`, `replicating`, and `paused` states.

**Why block restores for the full lifecycle:** A restore writes the full dataset onto the target, which conflicts with PCSM continuously applying source change-stream events. There is no safe interleave.

**Recommended workflow if a backup or restore is needed during an active migration:** delete the ClusterSync CR first (tears down PCSM), perform the operation, then (only for restores or if continuing migration) re-create the ClusterSync CR to restart from a fresh initial sync.

### 8.5 Source URI Changed During Active Replication

**Scenario:** User attempts to modify `spec.source.uri` (or `spec.source.credentialsSecret`) on an existing ClusterSync CR.

**Expected behavior:**
- The admission webhook rejects the update with an error: `"spec.source is immutable; delete and recreate the ClusterSync CR to change source"`.
- The CR is never put into a half-broken state; the existing replication keeps running unchanged.
- To change source: user deletes the ClusterSync CR (ownerReferences GC the Deployment, Service, and syncTargetUser), then creates a new CR pointing at the new source. This triggers a fresh initial sync.

**Constraint:** The checkpoint is tied to the original source's oplog. Changing the source invalidates the checkpoint (Constraint 4 makes restarting initial sync expensive).

### 8.6 MongoDB Version Mismatch

**Scenario:** Source and target clusters have different major MongoDB versions.

**Expected behavior:**
- If versions differ, PCSM will fail with its own error. The controller propagates this via `status.state=failed` and the `error` field.
- The operator does NOT perform a proactive version check against the source cluster. The operator currently only connects to its own local cluster; creating a MongoDB client connection to an external source using user-provided credentials adds complexity and may not always be possible (network access, firewalls). PCSM itself validates version compatibility on `POST /start`.

**Constraint:** Due to Constraint 1, cross-major replication is unsupported.

---

## 9. Migration and Backward Compatibility

### 9.1 Existing Clusters

- The PSMDB CR schema is not modified by this change. Existing CRs behave identically after operator upgrade.
- No PCSM resources are created until the user creates a `PerconaServerMongoDBClusterSync` CR.
- `syncTargetUser` is only created on clusters that have a ClusterSync CR targeting them.

### 9.2 CRD Compatibility

- The change introduces a new CRD (`PerconaServerMongoDBClusterSync`). Existing CRDs are not modified.
- The new CRD must be installed alongside the operator upgrade (added to `deploy/crd.yaml`, `deploy/bundle.yaml`, `deploy/cw-bundle.yaml`).
- CRD manifests need generation via `make generate`.
- Operator RBAC needs additional rules for the new resource (get/list/watch/create/update/patch/delete on `perconaservermongodbclustersyncs`).
- The new controller registers in `pkg/apis/psmdb/v1/register.go` and is wired up in `cmd/manager/main.go` (following the backup/restore controller pattern).

---

## 10. Testing Strategy

### 10.1 E2E Test Scenarios

| Scenario | Cluster Type | What It Validates |
|----------|-------------|-------------------|
| Create ClusterSync CR, verify data flows from source to target | Single replset | Basic PCSM lifecycle: start, initial sync, real-time replication, status updates |
| Pause and resume replication | Single replset | `spec.paused=true` triggers pause; `spec.paused=false` triggers resume; data continues flowing |
| Delete ClusterSync CR | Single replset | PCSM Deployment, Service, and `syncTargetUser` K8s Secret are GC'd via ownerReferences. The MongoDB `syncTargetUser` on the target cluster still exists after CR deletion (verified by querying the target's `system.users`). |
| Finalize migration | Single replset | `spec.finalize=true` triggers `POST /finalize`; `status.state=finalized`; further spec changes rejected |
| Finalize with `delete-clustersync-after-finalize` finalizer present | Single replset | With the `percona.com/delete-clustersync-after-finalize` string in `metadata.finalizers`, post-finalize cleanup (delete Deployment, Service, `syncTargetUser` K8s Secret) is followed by the controller deleting the ClusterSync CR itself; the CR is gone from the cluster on next reconcile. The MongoDB `syncTargetUser` on the target persists. Without the finalizer string, the CR also persists as a historical record. |
| Reset replication via `spec.resetGeneration` | Single replset | Bumping `spec.resetGeneration` triggers `POST /reset`; `status.lastAppliedResetGeneration` advances to match; initial sync restarts; re-applying the same manifest is a no-op (no second `POST /reset`); reset is rejected when `status.state=finalized` |
| PCSM pod crash during real-time replication | Single replset | Pod restarts; replication resumes from checkpoint; lag recovers |
| Cluster pause while PCSM is running | Single replset | PSMDB reconciler signals ClusterSync controller; PCSM is paused before MongoDB scales down; PCSM resumes after unpause |
| Backup while ClusterSync is active is blocked | Single replset | Backup request is rejected while a matching ClusterSync CR is in `pending`, `initialSync`, `replicating`, or `paused`; allowed when CR is `finalized` or absent |
| Restore while ClusterSync is active is blocked | Single replset | Restore request is rejected under the same conditions as backup |
| `source.uri` change attempt during replication | Single replset | Admission webhook rejects the update; existing replication continues; user must delete and recreate the CR to change source |
| Version mismatch detection | Single replset | PCSM start is blocked; error message in status |
| Two ClusterSync CRs targeting the same cluster | Single replset | Second CR fails to acquire the cardinality Lease and transitions to `status.state=failed` |
| Sharded cluster replication | Sharded | Replication works via mongos; data and shard keys are replicated |
| Namespace exclude filters | Single replset | Excluded collections are not replicated; all others are |

---

## 11. Open Questions

1. **Target user creation and deletion:** The operator only manages `syncTargetUser` on the local (target) cluster. The source user is the user's responsibility. When should `syncTargetUser` be created and deleted?
   - *Resolution (revised 2026-06-04):* The operator creates `syncTargetUser` on the target cluster the first time it reconciles a ClusterSync CR for that cluster, and **never drops it** — neither at finalize-time nor on CR deletion. The K8s `syncTargetUser` Secret is still owned by the ClusterSync CR and GCs via ownerReferences (or proactively at finalize-time per §5.5); the MongoDB user persists on the target. This reverses the earlier decision (2026-05-21) which had the operator drop the user at finalize-time and use a `psmdb.percona.com/clustersync-cleanup` Kubernetes finalizer to drop it on CR delete. The reversal removes the operator's need to hold `dropUser` privileges, eliminates a stuck-deletion failure mode (`dropUser` against an unreachable target blocking `kubectl delete`), and matches the broader posture that user removal is a cluster-admin decision rather than an operator action. To suspend replication without removing PCSM, set `spec.paused=true` rather than deleting the CR.

2. **Cluster-wide mode conflicts:** If two CRs each define `clusterSync` pointing at each other or at the same source, conflicting PCSM Deployments may be created. How should the operator handle this?
   - *Resolution (revised 2026-06-04):* The cardinality Lease (§4.1) is named `psmdb-clustersync-<clusterName>-lock` and lives **in the same namespace as the ClusterSync CR and the target PSMDB CR**, not in the operator's own namespace. Two PSMDB clusters sharing a name across different namespaces cannot collide because their Leases live in different namespaces; the namespace is implicit in the Lease's location. Same-namespace collisions still fail the second CR as intended. Earlier shape (2026-05-29) put the Lease in the operator's namespace with a `<targetNamespace>-` prefix in the name; that worked but required the operator to hold Lease RBAC in its own namespace and made the relationship between Lease and CR less direct. The revised shape co-locates the Lease with the resource it guards.

3. **Cluster pause interaction:** When `spec.pause=true`, should the operator automatically pause PCSM first?
   - *Resolution:* Decided A (auto-pause) on 2026-05-21. Before the PSMDB reconciler scales down the StatefulSets, it signals the ClusterSync controller (annotation or status condition on matching ClusterSync CRs) to call `POST /pause`. Once `status.state=paused`, the PSMDB reconciler proceeds with scale-down. On unpause, the PSMDB reconciler brings MongoDB back up, waits for readiness, then clears the signal so the ClusterSync controller can resume PCSM. Avoids unnecessary failure/recovery cycles. See §8.3 for the full flow.

4. **Backup/restore and PCSM concurrency:** Should backups and restores be blocked on the target cluster while PCSM is active?
   - *Resolution:* Decided block both on the target cluster for the full ClusterSync lifecycle (`pending`, `initialSync`, `replicating`, `paused`). Allowed only when no ClusterSync CR targets the cluster or the CR is `finalized`.
   - *Why backups (not just during initial sync):* PBM holds the backup cursor open while a backup runs; with PCSM continuously applying source writes onto the target, the cursor pins WiredTiger history and on-disk usage can grow unbounded. Lag-spike framing missed this risk; the disk-growth risk is the same in `initialSync`, `replicating`, and `paused`.
   - *Why restores:* A restore overwrites data PCSM is continuously replicating onto. No safe interleave.

5. **Sync completion status:** How to expose "sync finished" in status?
   - *Resolution:* Decided 2026-05-21. Initial-sync completion is conveyed by `status.state` transitioning out of `initialSync` (to `replicating`, `paused`, or `finalized`) and by an `InitialSyncComplete` condition in `status.conditions`. Steady-state "caught up" is `status.lagTimeSeconds=0`. A separate `initialSyncComplete` bool was dropped as redundant with `state`. A `readyToFinalize` field is not added in the first iteration; users can read `lagTimeSeconds` directly and set `spec.finalize=true` when it's acceptable.

6. **PCSM expose configuration:** Should PCSM be exposed via a Service with configurable `exposeType`?
   - *Resolution:* Decided 2026-05-21. External access is needed so users can monitor or interact with PCSM from outside the cluster. The ClusterSync CR exposes `spec.expose` (reuses the existing `Expose` struct in `psmdb_types.go`). Defaults to ClusterIP. Users who need external access set `expose.type=LoadBalancer` (or `NodePort`) and optionally `loadBalancerSourceRanges`, `loadBalancerClass`, annotations, labels, traffic policies, same surface as the PSMDB CR's expose. The `clusterServiceDNSMode` × source-URI interaction is not a question for the first iteration (source URI is user-supplied verbatim, so DNS mode does not apply); it is recorded in §1.3 as a constraint on the future `sourceCluster` work.

7. **Log collector integration:** Should the PCSM Deployment get a Fluent Bit sidecar when `logcollector.enabled=true`?
   - *Resolution:* Decided 2026-05-29: out of scope for the first iteration. PCSM logs go to stdout/stderr on the PCSM container and can be scraped via standard Kubernetes log mechanisms. Logcollector/Fluent Bit sidecar injection may be revisited in a later iteration if there is demand.

8. **Vault integration:** Should PCSM credentials be sourced from Vault when `vaultSpec` is configured?
   - *Resolution:* Decided 2026-05-29: out of scope for the first iteration. PCSM credentials are sourced from Kubernetes Secrets only — `source.credentialsSecret` for the source, and the operator-managed `<clustersync-cr-name>-pcsm-target-user` Secret for `syncTargetUser`. Vault integration may be revisited once the broader operator surface aligns on a Vault-backed credentials pattern.

9. **TLS for the source connection:** Where do source TLS settings live on the spec?
   - *Resolution:* Decided 2026-06-04. Source TLS lives under `spec.source.tls` on the ClusterSync CR. Target TLS is operator-derived from the target PSMDB CR's existing TLS secrets and is not user-configurable here.

10. **syncTargetUser management pattern:** The operator currently manages all system users via a single shared Secret (`spec.secrets.users`). With PCSM living in a separate CRD, `syncTargetUser` has a lifecycle bound to a different CR than the cluster's other system users.
    - *Resolution:* Decided custom user pattern on 2026-05-21. `syncTargetUser` is not part of the core admin users, so it does not belong in the shared `spec.secrets.users` Secret. The ClusterSync controller owns a dedicated Secret (`<clustersync-cr-name>-pcsm-target-user`) with ownerReferences pointing at the ClusterSync CR. Aligns with "CR lifecycle = user lifecycle" and keeps user GC tied to CR ownerReferences. Custom users have different reconciliation logic than system users; the ClusterSync controller implements its own minimal user-management flow rather than extending the system-user path.

11. **Backup/restore controller integration with PCSM status:** The backup and restore controllers currently check PBM locks and K8s leases to block concurrent operations. To enforce the policy in §8.4, both controllers need to consult ClusterSync state. Options considered:
    - **(A)** List ClusterSync CRs targeting the same `clusterName` and read `status.state` (no HTTP calls needed, but relies on status being up-to-date).
    - **(B)** Call `GET /status` on the PCSM HTTP API directly (neither controller currently makes external HTTP calls; also requires listing ClusterSync CRs first to find the Service, so adds work over A without removing it).
    - **(C)** Use a K8s lease or annotation written by the ClusterSync controller as a signal to the backup/restore controllers (introduces a second source of truth that must stay in sync with `status.state`).
    - *Resolution:* Decided **A** on 2026-06-04. Both controllers list `PerconaServerMongoDBClusterSync` CRs in the same namespace with `spec.clusterName` matching the cluster being backed up or restored. If any such CR has `status.state ∈ {pending, initialSync, replicating, paused}`, the request is rejected with a clear reason; admitted only when no matching CR exists or all matching CRs are `finalized`. Picked for simplicity and consistency with existing operator conventions for cross-CR admission. The eventual-consistency window (status may be a few seconds stale) is acceptable because the risks §8.4 guards against (backup-cursor disk growth, restore overwriting a target PCSM is still writing to) take minutes to materialize. Future improvements (e.g., a lease-based signal per Option C) can be evaluated if the window proves problematic in practice.

12. **Finalization support:** PCSM only creates indexes on the target when `POST /finalize` is called. Without it, the target has data but incomplete indexes.
    - *Resolution:* Decided B on 2026-05-21. `spec.finalize` is a one-way bool on the ClusterSync CR. When set to `true` and PCSM is caught up, the controller calls `POST /finalize` once and transitions `status.state` to `finalized` (terminal). After finalize, the controller deletes the PCSM Deployment, Service, and `syncTargetUser` Secret; the CR stays as a read-only historical record (see §5.5). To run a new migration the user just creates a new ClusterSync CR; the finalized one doesn't block it (§4.1 cardinality rule only counts non-finalized CRs). Picked over the `mode` enum for simplicity; the state machine is one-way, so a bool reflects the actual semantics.
    - *Companion finalizer (added 2026-06-04):* `percona.com/delete-clustersync-after-finalize` — a user-supplied entry in `metadata.finalizers` (see §4.2.1). When present, the controller deletes the ClusterSync CR itself as the last step of post-finalize cleanup, after the MongoDB user has been dropped and the child K8s resources removed. Covers GitOps workflows where leaving finalized CRs around fights the Git-as-source-of-truth model; default behavior is unchanged. Chosen over a `spec.deleteAfterFinalize` bool to match the existing PSMDB CR convention for opt-in delete behavior (`percona.com/delete-psmdb-pvc`, `percona.com/delete-pitr-chunks`).

13. **Sharded clusters with different topologies (asymmetric shard counts):** When replicating between sharded clusters whose shard counts differ (e.g., 3 shards on source → 7 on target), does the operator need to expose any mapping configuration?
    - *Resolution:* Decided 2026-06-04 — no. Per upstream PCSM 0.7.0+ docs, PCSM connects through mongos on both sides and "the cluster topology doesn't matter": source and target may have different numbers of shards with no extra configuration, because the target's mongos routes writes to the right shard from the preserved shard key. There is no X-to-Y mapping surface in PCSM and the operator does not introduce one. The earlier framing of this question (deferred because of "X-to-Y mapping") was based on a misread — there is nothing to map. The remaining caveat is upstream's **Technical Preview** label on sharded replication in PCSM 0.7.0+; the operator inherits that label for sharded ClusterSync CRs (see §6.1) but does not gate the feature behind a separate flag.

14. **Source user reuse across multiple ClusterSync CRs:** When several ClusterSync CRs replicate from the same source cluster (each into a different target), must each CR have its own source user, or can they share one?
    - *Resolution:* Decided 2026-05-29: the same source user can be reused. `source.credentialsSecret` is user-managed and the operator does not enforce uniqueness across ClusterSync CRs, so a single source user with read access on the source can back any number of ClusterSync CRs that point at that source. Contrast with `syncTargetUser`, which the operator owns per-ClusterSync CR on the target (see §11 Q10) — that one is intentionally not shared, because its lifecycle is bound to the ClusterSync CR. Operationally this means users avoid having to provision N source users for N parallel migrations from the same source.

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