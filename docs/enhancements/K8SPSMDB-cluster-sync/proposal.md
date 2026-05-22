# K8SPSMDB-1610: Integrate Percona ClusterSync for MongoDB

| Field        | Value                                    |
|--------------|------------------------------------------|
| Author       | George Kechagias                         |
| Status       | Draft                                    |
| Created      | 2026-05-12                               |
| Last Updated | 2026-05-21                               |
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

- Imperative `reset` -- destructive/irreversible (wipes checkpoint state) and has no declarative analogue; users must exec into the PCSM container manually. May be revisited if PCSM adds a safe idempotent variant. (`finalize` is in scope: it is driven declaratively via `spec.finalize=true`; see §5.5 and §11 Q12.)
- Reverse synchronization -- PCSM does not support this upstream. Revisit when upstream adds support.
- Multi-source or multi-target replication -- PCSM only supports single source/target pairs.
- Persistent Query Settings migration -- PCSM does not replicate Persistent Query Settings (MongoDB 8+). Migration requires manual export/import via `$querySettings` aggregation and `setQuerySettings` admin command after finalization. The operator will not automate this.

### 1.3 Deferred (Future Iterations)

- Source cluster reference by CR name (`sourceCluster`) -- the operator manages both source and target clusters, so it could resolve connection details and create the source user automatically from a CR name reference instead of requiring manual `source.uri` and `source.credentialsSecret`. Deferred to a future iteration; first iteration uses a raw URI + Secret.
- Version service integration for PCSM image -- the PSMDB CR's version service flow auto-fills `spec.image`, `spec.backup.image`, and `spec.pmm.image`. The version service response does not yet expose a PCSM image, so the first iteration requires `spec.image` on the ClusterSync CR. Revisit once PCSM is added to the version service.
- PMM integration -- may be added in a future iteration if monitoring of PCSM through PMM is needed.
- High availability / multiple PCSM instances per ClusterSync CR -- the first iteration runs a single PCSM instance (`replicas: 1`, `Recreate` strategy). Constraint 6 forbids two PCSM processes writing to the same target simultaneously, so any future HA must be active/passive. The first-iteration design leaves room for this by keeping callers behind a Service and treating `GET /status` as the source of truth for state, so a future leader-elected setup can swap the underlying workload without changing the controller or CR surface. Today's failover is pod restart + checkpoint recovery (Observation 2).

---

## 2. Background

### 2.1 Core Concepts

- **Percona ClusterSync for MongoDB (PCSM):** A standalone binary that replicates data between two MongoDB deployments. It performs an initial sync followed by real-time replication.
- **Initial sync:** The first phase of replication. PCSM clones all data from the source to the target, then applies all changes that occurred since the clone started. For sharded clusters, PCSM first retrieves shard key information from the source and creates collections on the target with the same shard keys before copying data. Cannot resume after failure -- must restart from scratch.
- **Real-time replication:** After initial sync completes, PCSM captures change stream events from the source and applies them to the target, ensuring real-time synchronization. Resumes from the last stored checkpoint after restart.
- **Checkpoint:** A persisted position in the source's change stream that allows PCSM to resume real-time replication after a restart without re-running initial sync.
- **Finalization:** A one-time operation that completes the migration. PCSM finalizes replication, creates required indexes on the target, and stops. After finalization, starting PCSM again begins a new initial sync from scratch.
- **PCSM workflow:** `start` (begin replication) → initial sync → real-time replication → `pause`/`resume` (control replication) → `finalize` (complete migration) → cutover (switch clients to target).
- **PCSM HTTP API:** PCSM exposes an HTTP API on port 2242. The operator uses `POST /start`, `POST /pause`, `POST /resume`, `POST /finalize`, and `GET /status` to control the PCSM lifecycle. `POST /finalize` is driven by the one-way `spec.finalize=true` switch (see §5.5 and §11 Q12). The operator will NOT call `POST /reset` -- it wipes checkpoint state and is left as a manual action (see Non-Goals). The API does not require authentication; the operator communicates with PCSM via a ClusterIP Service within the Kubernetes network.

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
    │   → POST /start, /pause, /resume, /finalize based on CR spec vs current state
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
| Service | Exposes the PCSM HTTP API (port 2242). Type is configurable via `spec.expose` -- defaults to ClusterIP for operator-only access; LoadBalancer/NodePort for external monitoring (per §11 Q6). |
| Secret (syncTargetUser) | Target cluster credentials, created and rotated by the operator. Deleted when the ClusterSync CR is deleted. |
| Secret (source.credentialsSecret) | Source cluster credentials, created by the user. Read-only for the operator; not owned by the CR. |

**Reconciliation flow:**

1. **Deployment reconciliation:** On each reconcile, the ClusterSync controller ensures the
   PCSM Deployment exists and matches the desired state (image, env vars, resource limits).
   The PCSM Deployment runs a single replica (`replicas: 1`) with the `Recreate` strategy --
   see §5.1 and §5.2 for rationale, and §1.3 for the future HA path.
   When the ClusterSync CR is deleted, ownerReferences GC the Deployment, Service, and
   syncTargetUser Secret. The same child resources are also GC'd automatically once
   `status.state` transitions to `finalized` (see §5.5) -- the CR itself stays as a
   read-only historical record until the user deletes it.

2. **Lifecycle control via HTTP API:** After the Deployment is ready, the controller calls
   `GET /status` to read the current PCSM state. It compares this against the desired state
   from the CR spec and issues the appropriate HTTP call:
   - PCSM not started → `POST /start`
   - `spec.paused=true` and PCSM state is `running` → `POST /pause`
   - `spec.paused=false` and PCSM state is `paused` → `POST /resume`
   - `spec.finalize=true` and PCSM is caught up → `POST /finalize` (one-time, terminal)

3. **Status propagation:** The controller maps the `GET /status` response to CR status fields:
   - `state` → `status.state`
   - `lagTimeSeconds` → `status.lagTimeSeconds`
   - `error` → `status.error`
   - State transitions also append/update entries in `status.conditions` (e.g., `InitialSyncComplete`, `Replicating`, `Finalized`) with their own `LastTransitionTime`, so historical timing is captured without dedicated timestamp fields.

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
`spec.clusterName` at a time (PCSM Constraint 2 — single source/target pair).
Enforced by an admission webhook or by the controller setting `status.state=failed`
if a conflicting CR already exists.

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

    TLS *ClusterSyncTLS `json:"tls,omitempty"`

    Expose Expose `json:"expose,omitempty"`

    ExcludeNamespaces []string `json:"excludeNamespaces,omitempty"`

    Paused   bool `json:"paused,omitempty"`
    Finalize bool `json:"finalize,omitempty"`
}

type ClusterSyncSource struct {
    URI               string `json:"uri"`
    CredentialsSecret string `json:"credentialsSecret"`
}

type ClusterSyncTLS struct {
    Enabled bool   `json:"enabled,omitempty"`
    Secret  string `json:"secret,omitempty"`
}
```

**Field notes:**

- `clusterName` -- references the target `PerconaServerMongoDB` CR in the same namespace. The controller resolves it to construct `PCSM_TARGET_URI`.
- `image` -- required in the first iteration. See §1.3 (Deferred) for the version service plan.
- `source.uri` -- connection string for the source cluster without credentials. Format: `mongodb://h1:p1,h2:p2/admin?replicaSet=rs0`. Credentials from `source.credentialsSecret` are injected at runtime.
- `source.credentialsSecret` -- name of a Kubernetes Secret with `username` and `password` keys. The operator percent-encodes both per RFC 3986 before injecting them into `PCSM_SOURCE_URI`.
- `tls` -- when `tls.enabled=true`, `tls.secret` must point to a Secret containing the TLS certificates PCSM should use.
- `expose` -- Service configuration for the PCSM HTTP API. Reuses the existing `Expose` struct from `psmdb_types.go` (type, loadBalancerSourceRanges, loadBalancerClass, annotations, labels, traffic policies). Defaults to ClusterIP (operator-only access). Set `expose.type=LoadBalancer` or `NodePort` to allow monitoring or interacting with PCSM from outside the cluster.
- `paused` -- when `true` and PCSM is `running`, the controller calls `POST /pause`. Setting back to `false` calls `POST /resume`.
- `finalize` -- one-way switch. When set to `true`, the controller calls `POST /finalize` once PCSM is caught up, then transitions `status.state` to `finalized` (terminal). Once finalized, the controller deletes the PCSM Deployment, Service, and `syncTargetUser` Secret; the ClusterSync CR itself remains as a read-only historical record until the user deletes it. Subsequent spec changes other than CR deletion are rejected (see §5.5).

### 4.3 Status

```go
type PerconaServerMongoDBClusterSyncStatus struct {
    State          ClusterSyncState `json:"state,omitempty"`
    LagTimeSeconds int64            `json:"lagTimeSeconds,omitempty"`
    Error          string           `json:"error,omitempty"`
    StartedAt      *metav1.Time     `json:"startedAt,omitempty"`

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
- **`PCSM_TARGET_URI`** environment variable: Constructed automatically by the controller from the target PSMDB CR (resolved via `spec.clusterName`) -- replset members or mongos endpoints -- with `syncTargetUser` credentials.

### 4.6 User-Facing Behavior Changes

**New resources visible in the namespace:**
- A `PerconaServerMongoDBClusterSync` CR (visible via `kubectl get psmdb-clustersync`).
- A PCSM Deployment (visible via `kubectl get deployments`), named `<clustersync-cr-name>-pcsm`.
- A `syncTargetUser` Secret (visible via `kubectl get secrets`), created when the ClusterSync CR is created.

**PSMDB CR:** No fields added or modified. The PSMDB CR remains unchanged.

**Kubernetes Events emitted by the controller (on the ClusterSync CR):**
- `ClusterSyncStarted` -- replication started via `POST /start`.
- `ClusterSyncPaused` -- replication paused via `POST /pause`.
- `ClusterSyncResumed` -- replication resumed via `POST /resume`.
- `ClusterSyncFinalized` -- migration finalized via `POST /finalize`.
- `ClusterSyncFailed` -- PCSM entered a failed state; `error` field has details.
- `InitialSyncComplete` -- initial sync finished, PCSM switched to real-time replication.

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

**Chosen approach:** The ClusterSync controller creates `syncTargetUser` on the target cluster when the ClusterSync CR is created, and deletes it when the CR is deleted. The lifecycle is bound to the CR.

**Why:** With a separate CRD, "intent to use PCSM" is expressed by the existence of the CR. Creating the user only when a ClusterSync CR exists avoids leaving a broad-privilege user (`readWriteAnyDatabase`) on clusters that never use PCSM. Deleting it on CR delete is unambiguous — there is no "temporarily disabled" sub-state to worry about (the user pauses replication via `spec.paused`, which doesn't affect the user). To suspend replication without losing the user, set `spec.paused=true` instead of deleting the CR.

The source cluster credentials are provided by the user via `spec.source.credentialsSecret`, a Kubernetes Secret with `username` and `password` keys. The operator reads the Secret and injects the credentials into `PCSM_SOURCE_URI` with percent-encoding per RFC 3986. Credentials are never stored in the CR spec.

**Alternatives considered:**

| Alternative | Why Rejected |
|------------|--------------|
| Create target user unconditionally on all clusters | Leaves a broad-privilege user on clusters that never use PCSM |
| Persist target user after CR deletion | No clear signal for when to clean up; CR delete is the natural boundary |
| Operator creates source user on the source cluster | The source cluster may be external and not managed by this operator instance |

### 5.4 Configuration Change Handling

**Chosen approach:** Make `spec.source` (URI and credentialsSecret) immutable after CR creation -- reject the update via an admission webhook. To change source, the user deletes and recreates the CR. Image updates, pod knobs, and `spec.paused` remain mutable. Namespace filter changes are mutable but warn in status (PCSM does not re-evaluate filters retroactively). After `status.state=finalized`, all spec changes are rejected (the CR is effectively read-only).

**Why:** The checkpoint is tied to the original source's oplog (Constraint 4); a mid-flight source change has no safe semantics. Making the field immutable surfaces this at admission time with a clear error rather than later via a `failed` state -- it's the same outcome (user has to recreate) without the half-broken intermediate state. Image updates remain safe due to PCSM's built-in recovery (Observation 2). Deleting and recreating the CR is acceptable because the CR lifecycle is already the unit of teardown (Deployment, Service, syncTargetUser all GC via ownerReferences), and the replacement CR triggers a fresh initial sync from the new source. Finalization is terminal for the CR -- running PCSM again means creating a new ClusterSync CR (the finalized CR can stay as a record; §4.1 cardinality only counts non-finalized CRs).

**Alternatives considered:**

| Alternative | Why Rejected |
|------------|--------------|
| Allow source updates freely | Silently restarting a multi-day initial sync against a different source is too expensive and surprising |
| Accept update, transition to `failed`, require manual PCSM reset | Same end state as immutability (recreate), but leaves the CR in a confusing half-broken state and adds a manual reset step |
| Block all updates during initial sync | Too restrictive for safe changes like image updates |

### 5.5 Post-Finalize Resource Cleanup

**Chosen approach (first iteration):** Once `status.state=finalized`, the controller deletes the PCSM Deployment, Service, and `syncTargetUser` Secret. The ClusterSync CR itself stays as a read-only record until the user deletes it.

**Why:** PCSM has stopped after finalize and restarting it would only trigger a fresh initial sync, so the Deployment serves no purpose. Removing `syncTargetUser` shrinks the privilege footprint on the target. Keeping the CR preserves an auditable record of the migration and keeps the backup/restore admission policy in §8.4 simple ("no CR or CR is `finalized`" -- both allowed).

**Alternatives considered:**

| Alternative | Why Rejected |
|------------|--------------|
| Leave Deployment/Service/Secret running after finalize | Idle pod consumes resources; broad-privilege target user lingers |
| Auto-delete the CR itself | Erases the historical record |

---

## 6. Sharding Impact

### 6.1 Sharded Cluster Behavior

When the CR defines a sharded cluster (`sharding.enabled=true`):
- PCSM connects via mongos on both source and target clusters. Since it connects through mongos, the cluster topology doesn't matter -- source and target can have different numbers of shards (e.g., a 3-shard source replicating onto a 7-shard target is supported with no extra configuration; the target's mongos routes writes to the right shard based on the preserved shard key).
- Before initial sync, PCSM checks which collections are sharded on the source and creates corresponding sharded collections on the target. The only sharding configuration preserved is the shard key; all other sharding details are handled internally by the target cluster.
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

After verifying lag is acceptable, set `finalize: true` to complete the migration. The controller calls `POST /finalize` once, creates required indexes on the target, transitions the CR to `status.state=finalized`, and then deletes the PCSM Deployment, Service, and `syncTargetUser` Secret (see §5.5). The CR itself stays as a read-only historical record until the user removes it. This is terminal for that CR -- to run a new migration the user creates a new ClusterSync CR (any name); the finalized CR does not block it because the cardinality rule in §4.1 only counts non-finalized CRs.

```yaml
spec:
  clusterName: cluster1
  image: percona/percona-clustersync-mongodb:1.0.0
  source:
    uri: mongodb://host1:27017/admin?replicaSet=rs0
    credentialsSecret: pcsm-source-credentials
  finalize: true   # one-way switch; controller calls POST /finalize
```

### 7.6 Disable Replication

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
- Operator updates `status.clusterSync` after restart to reflect current state.
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
- Backups and restores are allowed only when no ClusterSync CR targets the cluster, or when the CR's `status.state=finalized` (terminal -- PCSM has stopped).

**Why block backups for the full lifecycle (not just initial sync):** PBM holds the backup cursor open while a backup runs. With PCSM continuously applying source writes onto the target, that cursor pins WiredTiger history aggressively and on-disk usage can grow unbounded for the duration of the backup -- on top of PBM and PCSM contending for the oplog. The risk is the same in `initialSync`, `replicating`, and `paused` states.

**Why block restores for the full lifecycle:** A restore writes the full dataset onto the target, which conflicts with PCSM continuously applying source change-stream events. There is no safe interleave.

**Recommended workflow if a backup or restore is needed during an active migration:** delete the ClusterSync CR first (tears down PCSM), perform the operation, then -- only for restores or if continuing migration -- re-create the ClusterSync CR to restart from a fresh initial sync.

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
| Delete ClusterSync CR | Single replset | PCSM Deployment, Service, and `syncTargetUser` Secret are GC'd via ownerReferences |
| Finalize migration | Single replset | `spec.finalize=true` triggers `POST /finalize`; `status.state=finalized`; further spec changes rejected |
| PCSM pod crash during real-time replication | Single replset | Pod restarts; replication resumes from checkpoint; lag recovers |
| Cluster pause while PCSM is running | Single replset | PSMDB reconciler signals ClusterSync controller; PCSM is paused before MongoDB scales down; PCSM resumes after unpause |
| Backup while ClusterSync is active is blocked | Single replset | Backup request is rejected while a matching ClusterSync CR is in `pending`, `initialSync`, `replicating`, or `paused`; allowed when CR is `finalized` or absent |
| Restore while ClusterSync is active is blocked | Single replset | Restore request is rejected under the same conditions as backup |
| `source.uri` change attempt during replication | Single replset | Admission webhook rejects the update; existing replication continues; user must delete and recreate the CR to change source |
| Version mismatch detection | Single replset | PCSM start is blocked; error message in status |
| Two ClusterSync CRs targeting the same cluster | Single replset | Second CR is rejected (admission webhook) or transitions to failed state |
| Sharded cluster replication | Sharded | Replication works via mongos; data and shard keys are replicated |
| Namespace exclude filters | Single replset | Excluded collections are not replicated; all others are |

---

## 11. Open Questions

1. **Target user creation and deletion:** The operator only manages `syncTargetUser` on the local (target) cluster. The source user is the user's responsibility. When should `syncTargetUser` be created and deleted?
   - *Resolution:* Bound to the ClusterSync CR lifecycle (decided 2026-05-21 after moving to a separate CRD). Created when the ClusterSync CR is created; deleted when the CR is deleted. Avoids leaving a broad-privilege user on clusters that never use PCSM. Users who want to suspend replication without losing the user set `spec.paused=true` rather than deleting the CR.

2. **Cluster-wide mode conflicts:** If two CRs each define `clusterSync` pointing at each other or at the same source, conflicting PCSM Deployments may be created. How should the operator handle this?
   - *Resolution:* Pending -- open for team discussion.

3. **Cluster pause interaction:** When `spec.pause=true`, should the operator automatically pause PCSM first?
   - *Resolution:* Decided A (auto-pause) on 2026-05-21. Before the PSMDB reconciler scales down the StatefulSets, it signals the ClusterSync controller (annotation or status condition on matching ClusterSync CRs) to call `POST /pause`. Once `status.state=paused`, the PSMDB reconciler proceeds with scale-down. On unpause, the PSMDB reconciler brings MongoDB back up, waits for readiness, then clears the signal so the ClusterSync controller can resume PCSM. Avoids unnecessary failure/recovery cycles. See §8.3 for the full flow.

4. **Backup/restore and PCSM concurrency:** Should backups and restores be blocked on the target cluster while PCSM is active?
   - *Resolution:* Decided block both on the target cluster for the full ClusterSync lifecycle (`pending`, `initialSync`, `replicating`, `paused`). Allowed only when no ClusterSync CR targets the cluster or the CR is `finalized`.
   - *Why backups (not just during initial sync):* PBM holds the backup cursor open while a backup runs; with PCSM continuously applying source writes onto the target, the cursor pins WiredTiger history and on-disk usage can grow unbounded. Lag-spike framing missed this risk -- the disk-growth risk is the same in `initialSync`, `replicating`, and `paused`.
   - *Why restores:* A restore overwrites data PCSM is continuously replicating onto. No safe interleave.

5. **Sync completion status:** How to expose "sync finished" in status?
   - *Resolution:* Decided 2026-05-21. Initial-sync completion is conveyed by `status.state` transitioning out of `initialSync` (to `replicating`, `paused`, or `finalized`) and by an `InitialSyncComplete` condition in `status.conditions`. Steady-state "caught up" is `status.lagTimeSeconds=0`. A separate `initialSyncComplete` bool was dropped as redundant with `state`. A `readyToFinalize` field is not added in the first iteration -- users can read `lagTimeSeconds` directly and set `spec.finalize=true` when it's acceptable.

6. **Multi-cluster DNS and expose configuration:** How should `spec.source.uri` interact with `clusterServiceDNSMode` on the target PSMDB CR? Should PCSM be exposed via a Service with configurable `exposeType`?
   - *Resolution:* Decided 2026-05-21. External access is needed so users can monitor or interact with PCSM from outside the cluster. The ClusterSync CR exposes `spec.expose` (reuses the existing `Expose` struct in `psmdb_types.go`). Defaults to ClusterIP. Users who need external access set `expose.type=LoadBalancer` (or `NodePort`) and optionally `loadBalancerSourceRanges`, `loadBalancerClass`, annotations, labels, traffic policies -- same surface as the PSMDB CR's expose.
   - *Still pending:* The `clusterServiceDNSMode` interaction with `spec.source.uri`. The source URI is user-supplied today, so `clusterServiceDNSMode` (which governs target-side DNS) does not influence it. Revisit if/when `sourceCluster` (CR name reference) lands (§1.3) -- at that point the operator constructs the source URI from the source PSMDB CR and must honor that cluster's DNS mode.

7. **Log collector integration:** Should the PCSM Deployment get a Fluent Bit sidecar when `logcollector.enabled=true`?
   - *Resolution:* Pending.

8. **Vault integration:** Should PCSM credentials be sourced from Vault when `vaultSpec` is configured?
   - *Resolution:* Pending.

9. **Authentication mechanisms and TLS:** Should the operator expose granular TLS options or auto-construct them? Which auth mechanisms should be supported for the source connection?
   - *Resolution:* Pending.

10. **syncTargetUser management pattern:** The operator currently manages all system users via a single shared Secret (`spec.secrets.users`). With PCSM living in a separate CRD, `syncTargetUser` has a lifecycle bound to a different CR than the cluster's other system users.
    - *Resolution:* Decided custom user pattern on 2026-05-21. `syncTargetUser` is not part of the core admin users, so it does not belong in the shared `spec.secrets.users` Secret. The ClusterSync controller owns a dedicated Secret (`<clustersync-cr-name>-pcsm-target-user`) with ownerReferences pointing at the ClusterSync CR. Aligns with "CR lifecycle = user lifecycle" and keeps user GC tied to CR ownerReferences. Custom users have different reconciliation logic than system users; the ClusterSync controller implements its own minimal user-management flow rather than extending the system-user path.

11. **Backup/restore controller integration with PCSM status:** The backup and restore controllers currently check PBM locks and K8s leases to block concurrent operations. To enforce the policy in §8.4, both controllers need to consult ClusterSync state. Options:
    - List ClusterSync CRs targeting the same `clusterName` and read `status.state` (no HTTP calls needed, but relies on status being up-to-date).
    - Call `GET /status` on the PCSM HTTP API directly (neither controller currently makes external HTTP calls).
    - Use a K8s lease or annotation as a signal from the ClusterSync controller to the backup/restore controllers.
    - *Resolution:* Pending -- open for team discussion.

12. **Finalization support:** PCSM only creates indexes on the target when `POST /finalize` is called. Without it, the target has data but incomplete indexes.
    - *Resolution:* Decided B on 2026-05-21. `spec.finalize` is a one-way bool on the ClusterSync CR. When set to `true` and PCSM is caught up, the controller calls `POST /finalize` once and transitions `status.state` to `finalized` (terminal). After finalize, the controller deletes the PCSM Deployment, Service, and `syncTargetUser` Secret; the CR stays as a read-only historical record (see §5.5). To run a new migration the user just creates a new ClusterSync CR -- the finalized one doesn't block it (§4.1 cardinality rule only counts non-finalized CRs). Picked over the `mode` enum for simplicity -- the state machine is one-way, so a bool reflects the actual semantics.

---

## Appendix

### A. Glossary

| Term | Definition |
|------|------------|
| PCSM | Percona ClusterSync for MongoDB -- replication tool for cross-cluster data sync |
| PBM | Percona Backup for MongoDB -- backup tool for MongoDB |
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
- External authentication via Kerberos, AWS and LDAP -- needs clarification on whether the operator should support these auth mechanisms for PCSM connections