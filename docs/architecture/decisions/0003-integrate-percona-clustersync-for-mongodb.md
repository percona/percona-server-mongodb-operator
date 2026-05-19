# K8SPSMDB-1610: Integrate Percona ClusterSync for MongoDB

| Field        | Value                                    |
|--------------|------------------------------------------|
| Author       | George Kechagias                         |
| Status       | Draft                                    |
| Created      | 2026-05-12                               |
| Last Updated | 2026-05-13                               |
| Reviewers    |                                          |

---

## 1. Overview

The operator will deploy and manage Percona ClusterSync for MongoDB (PCSM) declaratively through the CR, enabling users to replicate data between MongoDB deployments in real time or perform one-time migrations with near-zero downtime. Today users must manage PCSM manually outside the operator. This benefits any team needing cross-cluster replication or migration without manual process orchestration.

### 1.1 Goals

- Provide a fully declarative way to set up cross-cluster replication and migrations through the CR
- Manage PCSM lifecycle (start, pause, resume, failure recovery) within the operator reconciliation loop
- Expose replication status and lag in the CR status for observability

### 1.2 Non-Goals (Out of Scope)

- Imperative operations (`reset`, `finalize`) -- these are destructive/irreversible and should not be triggered by a declarative reconciliation loop. Users must exec into the PCSM container manually. May be revisited if PCSM adds safe idempotent variants.
- Reverse synchronization -- PCSM does not support this upstream. Revisit when upstream adds support.
- Multi-source or multi-target replication -- PCSM only supports single source/target pairs.
- Persistent Query Settings migration -- PCSM does not replicate Persistent Query Settings (MongoDB 8+). Migration requires manual export/import via `$querySettings` aggregation and `setQuerySettings` admin command after finalization. The operator will not automate this.

### 1.3 Deferred (Future Iterations)

- PMM integration -- may be added in a future iteration if monitoring of PCSM through PMM is needed.

---

## 2. Background

### 2.1 Core Concepts

- **Percona ClusterSync for MongoDB (PCSM):** A standalone binary that replicates data between two MongoDB deployments. It performs an initial sync followed by real-time replication.
- **Initial sync:** The first phase of replication. PCSM clones all data from the source to the target, then applies all changes that occurred since the clone started. For sharded clusters, PCSM first retrieves shard key information from the source and creates collections on the target with the same shard keys before copying data. Cannot resume after failure -- must restart from scratch.
- **Real-time replication:** After initial sync completes, PCSM captures change stream events from the source and applies them to the target, ensuring real-time synchronization. Resumes from the last stored checkpoint after restart.
- **Checkpoint:** A persisted position in the source's change stream that allows PCSM to resume real-time replication after a restart without re-running initial sync.
- **Finalization:** A one-time operation that completes the migration. PCSM finalizes replication, creates required indexes on the target, and stops. After finalization, starting PCSM again begins a new initial sync from scratch.
- **PCSM workflow:** `start` (begin replication) → initial sync → real-time replication → `pause`/`resume` (control replication) → `finalize` (complete migration) → cutover (switch clients to target).
- **PCSM HTTP API:** PCSM exposes an HTTP API on port 2242. The operator will use `POST /start`, `POST /pause`, `POST /resume`, and `GET /status` to control the PCSM lifecycle. The operator will NOT call `POST /finalize` or `POST /reset` -- these are destructive operations left as manual actions (see Non-Goals). The API does not require authentication; the operator communicates with PCSM via a ClusterIP Service within the Kubernetes network.

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

The operator deploys PCSM as a separate Deployment and manages its full lifecycle declaratively.

```
CR update (clusterSync section)
  → Main Reconciler
    → Reconcile Secrets
    │   → Read sourceCredentialsSecret (user-provided, source credentials)
    │   → Read/create syncTargetUser Secret (operator-managed, target credentials)
    → Reconcile PCSM Deployment (Recreate strategy)
    │   → PCSM container
    │       env: PCSM_SOURCE_URI (sourceURI + source credentials)
    │       env: PCSM_TARGET_URI (auto-constructed from local cluster + syncTargetUser)
    │       ports: 2242 (HTTP API)
    → Control PCSM via HTTP API (<cluster-name>-clustersync:2242)
    │   → GET /status → read current PCSM state
    │   → POST /start, /pause, /resume based on CR spec vs current state
    → Update CR status (state, lagTimeSeconds, initialSyncComplete, error)
  ← Status update on CR
```

**Resources managed by the operator for PCSM:**

| Resource | Purpose |
|----------|---------|
| Deployment | Runs the PCSM container. Uses `Recreate` strategy. |
| Service (ClusterIP) | Exposes the PCSM HTTP API (port 2242) for operator-to-PCSM communication within the cluster. |
| Secret (syncTargetUser) | Target cluster credentials, created and rotated by the operator. |
| Secret (sourceCredentialsSecret) | Source cluster credentials, created by the user. Read-only for the operator. |

**Reconciliation flow:**

1. **Deployment reconciliation:** On each reconcile, the operator ensures the PCSM Deployment
   exists and matches the desired state (image, env vars, resource limits). If
   `clusterSync.enabled=false` or the `clusterSync` section is removed, the operator deletes
   the Deployment.

2. **Lifecycle control via HTTP API:** After the Deployment is ready, the operator calls
   `GET /status` to read the current PCSM state. It compares this against the desired state
   from the CR spec and issues the appropriate HTTP call:
   - PCSM not started and `enabled=true` → `POST /start`
   - `paused=true` and PCSM state is `running` → `POST /pause`
   - `paused=false` and PCSM state is `paused` → `POST /resume`

3. **Status propagation:** The operator maps the `GET /status` response to CR status fields:
   - `state` → `status.clusterSync.state`
   - `lagTimeSeconds` → `status.clusterSync.lagTimeSeconds`
   - `initialSync.completed` → `status.clusterSync.initialSyncComplete`
   - `error` → `status.clusterSync.error`

4. **Interaction with other reconcilers:** The main reconciler checks PCSM state before
   allowing certain operations:
   - Before cluster pause (`spec.pause=true`): call `POST /pause` on PCSM first.
   - Before accepting a backup request: reject if initial sync is in progress.

### 3.3 Key Observations

1. **PCSM is a standalone process:** It does not need to run on every replset member, making a separate Deployment the natural fit rather than a sidecar.
2. **PCSM auto-recovers on restart:** The operator does not need to detect the replication phase and issue different commands. Simply restarting the process triggers recovery (restart initial sync or resume real-time replication from checkpoint).
3. **Connection strings require credential injection:** The operator reads credentials from `sourceCredentialsSecret` (source) and the operator-managed `syncTargetUser` Secret (target), then injects them into `PCSM_SOURCE_URI` and `PCSM_TARGET_URI` with percent-encoding per RFC 3986. Credentials are never stored in the CR spec.
4. **Interaction with cluster pause:** If `spec.pause=true` scales down MongoDB, PCSM loses its connection. The operator must coordinate the pause/unpause sequence.
5. **Interaction with backups:** PCSM and PBM both interact with the oplog. Concurrent initial sync and backup can cause issues.

---

## 4. CRD and Interface Changes

### 4.1 CRD Spec Changes

- **`spec.clusterSync.enabled`** *(optional, default: `false`)*:
  Enables PCSM deployment and lifecycle management. When `false` or omitted, no PCSM resources are created.

- **`spec.clusterSync.paused`** *(optional, default: `false`)*:
  When `true` and replication is running, the operator calls `POST /pause`. When set back to `false`, the operator calls `POST /resume`. Only applies when `enabled=true`.

- **`spec.clusterSync.image`** *(required when enabled)*:
  The PCSM container image to deploy. Example: `percona/percona-clustersync-mongodb:1.0.0`.

- **`spec.clusterSync.sourceURI`** *(required when enabled)*:
  MongoDB connection string for the source cluster, without credentials. Format: `mongodb://host1:port1,host2:port2/admin?replicaSet=rs0`. The operator injects credentials from `sourceCredentialsSecret` at runtime.

- **`spec.clusterSync.sourceCredentialsSecret`** *(required when enabled)*:
  Name of a Kubernetes Secret containing the source cluster credentials. The Secret must have `username` and `password` keys. The operator injects these into `sourceURI` with percent-encoding per RFC 3986.

- **`spec.clusterSync.tls.enabled`** *(optional, default: `false`)*:
  Enables TLS for PCSM connections.

- **`spec.clusterSync.tls.secret`** *(required when tls.enabled=true)*:
  Name of the Kubernetes Secret containing TLS certificates for PCSM.

- **`spec.clusterSync.excludeNamespaces`** *(optional)*:
  List of `db.collection` patterns to exclude from replication. When omitted, all namespaces are replicated.

### 4.2 CRD Status Changes

- **`status.clusterSync.state`** *(string)*:
  Current PCSM state. One of: `running`, `paused`, `failed`.

- **`status.clusterSync.initialSyncComplete`** *(bool)*:
  `true` when initial sync is done and PCSM has switched to real-time replication.

- **`status.clusterSync.lagTimeSeconds`** *(int)*:
  Replication lag in seconds. `0` means fully caught up.

- **`status.clusterSync.error`** *(string)*:
  Populated on failure with a descriptive error message.

### 4.3 Internal Contracts

- **`PCSM_SOURCE_URI`** environment variable: Constructed from `clusterSync.sourceURI` with credentials from `sourceCredentialsSecret` injected and percent-encoded per RFC 3986.
- **`PCSM_TARGET_URI`** environment variable: Constructed automatically by the operator from the local cluster's connection details (replset members or mongos endpoints) with `syncTargetUser` credentials.

### 4.4 User-Facing Behavior Changes

**CR status:**
- New `status.clusterSync` section visible in `kubectl get psmdb` output.

**New resources visible in the namespace:**
- A PCSM Deployment (visible via `kubectl get deployments`), named after the cluster (e.g., `<cluster-name>-clustersync`).
- A `syncTargetUser` Secret (visible via `kubectl get secrets`), created by the operator when `clusterSync.enabled=true`.

**Kubernetes Events emitted by the operator:**
- `ClusterSyncStarted` -- replication started via `POST /start`.
- `ClusterSyncPaused` -- replication paused via `POST /pause`.
- `ClusterSyncResumed` -- replication resumed via `POST /resume`.
- `ClusterSyncFailed` -- PCSM entered a failed state; `error` field has details.
- `InitialSyncComplete` -- initial sync finished, PCSM switched to real-time replication.

**Operator log messages:**
- Lifecycle transitions (start, pause, resume) are logged at info level.
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

**Chosen approach:** The operator creates `syncTargetUser` on the local cluster when `clusterSync.enabled=true`. It does not delete the user when `enabled` is set to `false`. The user is only deleted when the entire `clusterSync` section is removed from the CR.

**Why:** Creating the user only when needed avoids leaving a broad-privilege user (`readWriteAnyDatabase`) on clusters that never use PCSM. Not deleting on `enabled=false` prevents accidental credential loss when temporarily disabling replication. Deleting when the `clusterSync` section is fully removed signals a clear intent to clean up.

The source cluster credentials are provided by the user via `clusterSync.sourceCredentialsSecret`, a Kubernetes Secret with `username` and `password` keys. The operator reads the Secret and injects the credentials into `PCSM_SOURCE_URI` with percent-encoding per RFC 3986. Credentials are never stored in the CR spec.

**Alternatives considered:**

| Alternative | Why Rejected |
|------------|--------------|
| Create target user unconditionally on all clusters | Leaves a broad-privilege user on clusters that never use PCSM |
| Delete target user when `enabled=false` | Risk of accidental credential loss when temporarily disabling replication |
| Operator creates source user on the source cluster | The source cluster may be external and not managed by this operator instance |

### 5.4 Configuration Change Handling

**Chosen approach:** Allow image-only updates freely; for `sourceURI` changes that invalidate the checkpoint, set `state=error` and require the user to manually reset PCSM; for namespace filter changes, warn in status.

**Why:** Image updates are safe due to PCSM's built-in recovery (Observation 2). But `sourceURI` changes invalidate the checkpoint (tied to the original source's oplog), and silently restarting initial sync is dangerous for large datasets (Constraint 4).

**Alternatives considered:**

| Alternative | Why Rejected |
|------------|--------------|
| Allow all updates freely | Silently restarting a multi-day initial sync is too expensive and surprising |
| Block all updates during initial sync | Too restrictive for safe changes like image updates |

---

## 6. Sharding Impact

### 6.1 Sharded Cluster Behavior

When the CR defines a sharded cluster (`sharding.enabled=true`):
- PCSM connects via mongos on both source and target clusters. Since it connects through mongos, the cluster topology doesn't matter -- source and target can have different numbers of shards.
- Before initial sync, PCSM checks which collections are sharded on the source and creates corresponding sharded collections on the target. The only sharding configuration preserved is the shard key; all other sharding details are handled internally by the target cluster.
- The balancer does not need to be disabled on either cluster. The target cluster's balancer continues to operate normally and manages chunk distribution according to its own configuration.
- PCSM replicates data, not metadata. Chunk distribution, primary shard names, and zone configuration are NOT preserved from the source. The target cluster manages these internally.
- Running `movePrimary`, `reshardCollection`, `unshardCollection`, or `refineCollectionShardKey` on the source during replication causes a failure requiring a full initial sync restart.

### 6.2 Single Replset Behavior

When sharding is disabled:
- PCSM connects directly to the replset.
- Full feature support.
- No additional flags required beyond `clusterSync.enabled: true`.

---

## 7. User Experience

### 7.1 Existing CR (Unchanged)

```yaml
# Existing CRs without clusterSync continue to work identically.
# No PCSM resources are created when clusterSync is absent or disabled.
apiVersion: psmdb.percona.com/v1
kind: PerconaServerMongoDB
metadata:
  name: cluster1
spec:
  replsets:
    - name: rs0
      size: 3
  # No clusterSync section -- behavior unchanged
```

### 7.2 Enable Cross-Cluster Replication

```yaml
apiVersion: psmdb.percona.com/v1
kind: PerconaServerMongoDB
metadata:
  name: cluster1
spec:
  replsets:
    - name: rs0
      size: 3
  clusterSync:
    enabled: true
    image: percona/percona-clustersync-mongodb:1.0.0
    sourceURI: mongodb://host1:27017,host2:27017,host3:27017/admin?replicaSet=rs0
    sourceCredentialsSecret: pcsm-source-credentials  # Secret with username/password keys
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
  clusterSync:
    enabled: true
    paused: true   # Operator calls POST /pause
    # ...

# Resume replication
spec:
  clusterSync:
    enabled: true
    paused: false  # Operator calls POST /resume
    # ...
```

### 7.4 Sharded Cluster Replication

```yaml
spec:
  sharding:
    enabled: true
    # ...
  clusterSync:
    enabled: true
    image: percona/percona-clustersync-mongodb:1.0.0
    sourceURI: mongodb://mongos1:27017,mongos2:27017/admin
    sourceCredentialsSecret: pcsm-source-credentials
    # ...
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
- Operator sets `status.clusterSync.state=failed` and populates `error` field.
- Operator requeues reconciliation to monitor recovery.
- When connectivity is restored, PCSM auto-recovers (resumes real-time replication from checkpoint or restarts initial sync).

### 8.3 Cluster Pause While PCSM Is Running

**Scenario:** User sets `spec.pause=true` which scales down MongoDB StatefulSets, causing PCSM to lose its target connection.

**Expected behavior:**
- Operator detects `spec.pause=true` and calls `POST /pause` before scaling down replsets.
- On unpause, operator brings MongoDB back up, waits for readiness, then resumes PCSM.
- This prevents unnecessary failure/recovery cycles.

### 8.4 Backup During Initial Sync

**Scenario:** A backup is requested while PCSM is performing initial sync (`initialSyncComplete=false`).

**Expected behavior:**
- Operator rejects the backup request (extends existing concurrent backup/restore prevention from K8SPSMDB-1643).
- Once initial sync is complete and PCSM is in real-time replication mode, concurrent backups are allowed (lag may spike temporarily).

### 8.5 Source URI Changed During Active Replication

**Scenario:** User modifies `clusterSync.sourceURI` while replication is running.

**Expected behavior:**
- Operator detects the `sourceURI` change.
- Sets `status.clusterSync.state=failed` with error: `"sourceURI changed during active replication; checkpoint is invalid. Reset PCSM manually before restarting."`.
- Does NOT silently restart initial sync.

**Constraint:** The checkpoint is tied to the original source's oplog. Changing the source invalidates the checkpoint (Constraint 4 makes restarting initial sync expensive).

### 8.6 MongoDB Version Mismatch

**Scenario:** Source and target clusters have different major MongoDB versions.

**Expected behavior:**
- If versions differ, PCSM will fail with its own error. The operator propagates this via `status.clusterSync.state=failed` and the `error` field.
- The operator does NOT perform a proactive version check against the source cluster. The operator currently only connects to its own local cluster; creating a MongoDB client connection to an external source using user-provided credentials adds complexity and may not always be possible (network access, firewalls). PCSM itself validates version compatibility on `POST /start`.

**Constraint:** Due to Constraint 1, cross-major replication is unsupported.

---

## 9. Migration and Backward Compatibility

### 9.1 Existing Clusters

- Existing CRs without the `clusterSync` section behave identically after operator upgrade.
- No PCSM resources are created when `clusterSync` is absent or `enabled=false`.
- The `syncTargetUser` is only created when `clusterSync.enabled=true`, so existing clusters are unaffected.

### 9.2 CRD Compatibility

- All changes are additive: new optional fields under a new `spec.clusterSync` section and new `status.clusterSync` section.
- No existing fields are modified or removed.
- CRD manifests need regeneration via `make generate`.

---

## 10. Testing Strategy

### 10.1 E2E Test Scenarios

| Scenario | Cluster Type | What It Validates |
|----------|-------------|-------------------|
| Enable replication, verify data flows from source to target | Single replset | Basic PCSM lifecycle: start, initial sync, real-time replication, status updates |
| Pause and resume replication | Single replset | `paused=true` triggers pause; `paused=false` triggers resume; data continues flowing |
| Disable replication (`enabled=false`) | Single replset | PCSM Deployment is removed; status is cleared |
| PCSM pod crash during real-time replication | Single replset | Pod restarts; replication resumes from checkpoint; lag recovers |
| Cluster pause while PCSM is running | Single replset | PCSM is paused before MongoDB scales down; PCSM resumes after unpause |
| Backup during initial sync is blocked | Single replset | Backup request is rejected while `initialSyncComplete=false` |
| Source URI change during replication | Single replset | State set to error; manual reset required |
| Version mismatch detection | Single replset | PCSM start is blocked; error message in status |
| Sharded cluster replication | Sharded | Replication works via mongos; data and shard keys are replicated |
| Namespace exclude filters | Single replset | Excluded collections are not replicated; all others are |

---

## 11. Open Questions

1. **Target user creation and deletion:** The operator only manages `syncTargetUser` on the local (target) cluster. The source user is the user's responsibility. When should `syncTargetUser` be created and deleted?
   - *Option A (unconditional):* Create on all clusters regardless of `clusterSync`. Never delete.
   - *Option B (conditional create, no auto-delete):* Create when `clusterSync.enabled=true`. Do not delete when `enabled=false`. Delete only when the `clusterSync` section is removed from the CR.
   - *Option C (fully conditional):* Create when enabled, delete when disabled.
   - *Resolution:* Decided B on 2026-05-13. Avoids leaving a broad-privilege user on clusters that never use PCSM, while preventing accidental credential loss from temporarily disabling replication.

2. **Cluster-wide mode conflicts:** If two CRs each define `clusterSync` pointing at each other or at the same source, conflicting PCSM Deployments may be created. How should the operator handle this?
   - *Resolution:* Pending -- open for team discussion.

3. **Cluster pause interaction:** When `spec.pause=true`, should the operator automatically pause PCSM first?
   - *Option A (auto-pause):* Avoids unnecessary failure/recovery cycles.
   - *Option B (let it fail):* Relies on PCSM's failure recovery.
   - *Recommendation:* A, because it avoids unnecessary failure states.
   - *Resolution:* Pending.

4. **Backup and PCSM concurrency:** Should backups be blocked during PCSM initial sync?
   - *Option A (block):* Extend existing concurrent operation prevention (K8SPSMDB-1643). Allow backups once in real-time replication mode.
   - *Option B (allow):* Let PCSM and PBM run concurrently; document lag spikes.
   - *Option C (warn):* Set a status condition warning.
   - *Recommendation:* A, because initial sync is the fragile phase.
   - *Resolution:* Pending.

5. **Sync completion status:** How to expose "sync finished" in status?
   - *Recommendation:* Expose `initialSyncComplete` (initial sync done) and `lagTimeSeconds` (0 = caught up). Optionally add `readyToFinalize` when lag is below a configurable threshold.
   - *Resolution:* Pending.

6. **Multi-cluster DNS and expose configuration:** How should `sourceURI` interact with `clusterServiceDNSMode`? Should PCSM be exposed via a Service with configurable `exposeType`?
   - *Resolution:* Pending.

7. **Log collector integration:** Should the PCSM Deployment get a Fluent Bit sidecar when `logcollector.enabled=true`?
   - *Resolution:* Pending.

8. **Vault integration:** Should PCSM credentials be sourced from Vault when `vaultSpec` is configured?
   - *Resolution:* Pending.

9. **Authentication mechanisms and TLS:** Should the operator expose granular TLS options or auto-construct them? Which auth mechanisms should be supported for the source connection?
   - *Resolution:* Pending.

10. **syncTargetUser management pattern:** The operator currently manages all system users via a single shared Secret (`spec.secrets.users`). The ADR proposes `syncTargetUser` as a conditionally-created user with its own lifecycle. Should `syncTargetUser` follow the existing system user pattern (added to the shared users Secret) or the custom user pattern (managed independently)?
    - System user pattern: consistent with existing code, but all system users are created together unconditionally.
    - Custom user pattern: allows conditional creation, but custom users have different reconciliation logic.
    - *Resolution:* Pending -- open for team discussion.

11. **Backup controller integration with PCSM status:** The backup controller currently checks PBM locks and K8s leases to block concurrent operations. To block backups during PCSM initial sync, the backup controller would need to check PCSM status. Options:
    - Read `status.clusterSync.initialSyncComplete` from the CR (no HTTP calls needed, but relies on status being up-to-date).
    - Call `GET /status` on the PCSM HTTP API directly (the backup controller currently does not call external HTTP APIs).
    - Use a K8s lease or annotation as a signal from the main reconciler to the backup controller.
    - *Resolution:* Pending -- open for team discussion.

12. **Finalization support:** PCSM only creates indexes on the target when `POST /finalize` is called. Without it, the target has data but incomplete indexes. Currently `finalize` is listed as a non-goal (manual operation), but this means every migration requires the user to exec into the PCSM container to complete it.
    - *Option A (manual):* Keep `finalize` as a manual operation. User execs into the pod. Simple but breaks the declarative model.
    - *Option B (`spec.clusterSync.finalize` field):* When set to `true`, the operator calls `POST /finalize` once, then sets `status.clusterSync.state=finalized` and stops reconciling PCSM. The operator never calls `/start` when state is `finalized`, preventing accidental re-sync. To start fresh, the user removes the `clusterSync` section and re-adds it (or manually resets).
    - *Option C (`spec.clusterSync.mode` field):* Values: `replicating` (default) vs `finalized`. Switching to `finalized` triggers the `/finalize` call. Switching back is blocked unless the user manually resets first.
    - *Resolution:* Pending -- open for team discussion.

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