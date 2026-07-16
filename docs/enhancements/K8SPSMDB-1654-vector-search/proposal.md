# K8SPSMDB-1654: MongoDB Vector Search Integration

| Field        | Value                                       |
|--------------|---------------------------------------------|
| Author       | Ege Güneş                                   |
| Status       | In Review                                   |
| Created      | 2026-05-11                                  |
| Last Updated | 2026-06-11                                  |
| Reviewers    | Ivan Groenewold, Slava Sarzhan, Mayank Shah |

---

## 1. Overview

Add native support for MongoDB Vector Search (and the full-text Search
engine that is included in the same binary) to the Percona Operator for
MongoDB. Vector Search is implemented by a separate process called
`mongot` that holds Lucene-style search and Approximate Nearest Neighbor
(ANN) / Exact Nearest Neighbor (ENN) vector indexes and serves
`$search` / `$vectorSearch` / `$searchMeta` aggregation stages to `mongod`
over gRPC. This proposal describes how the operator deploys Percona's
`mongot` build next to each managed PSMDB cluster and exposes the feature
declaratively on the CR.

The feature enables AI/RAG workloads, semantic search, and combined
search-and-OLTP use cases on customer-managed PSMDB clusters — the same
capability that MongoDB Atlas customers and self-managed upstream users
already have.

### 1.1 Goals

- Declarative enable/disable via a single field on the existing
  `PerconaServerMongoDB` CR (`spec.search.enabled`).
- Deploy `mongot` as a dedicated StatefulSet per replset
  (`<cluster>-<rs>-search`), with its own PVC and Service.
- Support sharded clusters: one `mongot` group per shard
  (`<cluster>-<shardName>-search`), with cross-shard
  `$vectorSearch` queries handled directly by `mongos`.
- Connect each `mongod` to its replset's (or shard's) `mongot` through
  the `mongotHost` and `searchIndexManagementHostAndPort` `setParameter`s,
  managed by the operator. Pass the equivalent values to `mongos` for
  index-management RPCs in sharded clusters.
- Reuse the cluster's existing x509 or keyfile for internal auth between
  `mongod` and `mongot`; create a MongoDB user with the
  `searchCoordinator` role through the existing user-reconciliation
  step.
- Allow per-shard override of `mongot` resources, storage, and pod
  placement (affinity / nodeSelector / tolerations) through an optional
  `spec.replsets[].search` block, on top of cluster-wide defaults set
  on `spec.search`. Enable/disable, image, and TLS stay cluster-wide.
- Expose a `status.search` block keyed by replset / shard
  (size / ready / state / message).
- Restrict the feature to PSMDB versions that include `mongot` support
  — refuse to enable on older binaries with a clear status condition.
- Document a backup/restore approach that does not back up `mongot`
  data (indexes can be rebuilt from `mongod`).

### 1.2 Non-Goals (Out of Scope)

- **Automated embedding (`autoEmbedding` / Voyage AI)** — calls an
  external embedding API. Out of scope.
- **Migration from externally-managed `mongot` deployments** — users
  with their own `mongot` running outside the operator must either
  manage it themselves or move under the operator. No import flow.
- **Backing up `mongot` indexes via PBM** — PBM doesn't support this yet.
  Indexes are derivable from `mongod` change streams; requires re-syncing after
  restore.
- **`mongot` HA through `size > 1` (multiple `mongot` pods per
  replset/shard)** — requires an L7 gRPC-aware load balancer to pin
  long-lived streams to a single backend. This is a significant
  design decision on its own (Envoy sidecar vs Gateway API). v1
  supports `size: 1` per replset/shard; postponed to a later
  release.

---

## 2. Background

### 2.1 Core Concepts

- **`mongot`** — a separate Java process. It connects to a `mongod`
  in the same replset, opens a permanent change-stream connection to
  read source data, builds Lucene-style indexes on its own disk, and
  serves `$search` / `$vectorSearch` / `$searchMeta` queries
  forwarded by `mongod` over a long-lived gRPC stream.
- **`searchCoordinator` role** — built-in MongoDB role (since 8.3)
  that grants `readAnyDatabase` plus write access to the
  `__mdb_internal_search` system database. `mongot` authenticates as
  a user with this role.
- **`setParameter` integration** — two `mongod` server parameters
  configure the integration: `mongotHost` (data-plane endpoint,
  where `mongod` forwards `$search` / `$vectorSearch`) and
  `searchIndexManagementHostAndPort` (control-plane endpoint, for
  `createSearchIndex` / `dropSearchIndex` / `listSearchIndexes`).
  They usually point to the same `mongot` Service.
- **`$vectorSearch` aggregation stage** — added in MongoDB 8.3+.
 Supports HNSW (ANN, approximate) and exact (ENN) algorithms; vectors up to 8192
  dimensions; pre-filter on a typed subset of fields.
- **Per-replset `mongot` group** — the deployment unit is one
  `mongot` group per logical replset (in a sharded cluster, one
  group per shard). `mongot` chooses which `mongod` to read from
  (usually a secondary).
- **Change-stream sourcing** — `mongot` keeps indexes up to date by
  reading change streams from `mongod`.

### 2.2 Key Constraints

1. **Server-side version requirement.** Vector Search requires MongoDB
   **8.3+** (Community). The operator code added by this proposal must refuse
   to enable `mongot` on binaries that do not support it, with a clear status
   condition.

2. **`mongot` deployment unit.** One `mongot` group per replset (or
   per shard if sharded). v1 supports `size: 1` per group; running
   multiple `mongot` replicas behind a single endpoint requires an
   **L7 (gRPC-aware) load balancer** — Kubernetes `Service` ClusterIP
   is L4-only and cannot distribute long-lived gRPC streams. The
   single-pod limit is independent of sharding: sharded clusters
   in v1 deploy one StatefulSet per shard, each with one `mongot`
   pod.

3. **Persistent storage for `mongot`.** According to upstream sizing
   guidance, index data is 0.25x–2x the source data size, with 2x
   headroom recommended for rebuild; `mongot` becomes read-only at
   90% disk usage. Each `mongot` pod needs its own PVC — it cannot
   be shared. The default 10Gi is safe for small clusters; larger
   workloads must be tuned.

4. **Auth and TLS reuse.** `mongot` authenticates as a user with the
   `searchCoordinator` role. When the cluster has TLS enabled (the
   operator default), `mongot` also uses the cluster's `ssl` Secret
   as both server certificate (for its own gRPC listener) and trust
   root (to verify `mongod`).

5. **Backward compatibility.** Existing `PerconaServerMongoDB` CRs (no
   `spec.search`) must continue to reconcile and run identically after
   the operator upgrade. No mandatory new fields.

---

## 3. Architecture

### 3.1 Architecture Before This Change

For a replset cluster (sharded layout adds a `ConfigSvr` replset and a
`mongos` Deployment, omitted here):

```
PerconaServerMongoDB CR
  └─ reconciler
      ├─ Secrets   (users, keyfile, ssl)
      ├─ ConfigMap (mongod.conf, derived from spec.replsets[*].configuration)
      ├─ Service   (<cluster>-<rs>) — headless + per-pod
      └─ StatefulSet <cluster>-<rs>
            Pod containers:
              - mongod          (27017)
              - pbm-agent       (optional)
              - pmm-client      (optional)
              - logcollector +  (optional)
                logrotator
            PVC: mongod-data
```

Aggregation queries (`$match`, `$group`, …) execute inside `mongod`.
There is no `$search` / `$vectorSearch` path.

### 3.2 Architecture After This Change

**Replset (non-sharded) cluster:**

```
PerconaServerMongoDB CR (spec.search.enabled: true; spec.sharding.enabled: false)
  └─ reconciler
      ├─ Secrets   (… + searchCoordinator user added to users Secret)
      ├─ ConfigMap mongod.conf  ── operator-injected setParameters:
      │                              mongotHost: <cluster>-<rs>-search-0.<cluster>-<rs>-search.<ns>.<dnsSuffix>:27028
      │                              searchIndexManagementHostAndPort: <same as mongotHost>
      │                              useGrpcForSearch: true
      │                              searchTLSMode: <derived from rendered mongot.conf>
      ├─ ConfigMap <cluster>-<rs>-search-config  ── mongot YAML config
      ├─ Service   <cluster>-<rs>          (existing, mongod)
      ├─ Service   <cluster>-<rs>-search   (new, headless; gRPC 27028, metrics 9946)
      ├─ StatefulSet <cluster>-<rs>        (existing, mongod)
      └─ StatefulSet <cluster>-<rs>-search (new)
            Pod containers:
              - mongot          (gRPC 27028 + healthCheck 8080 + metrics 9946)
            PVC: mongot-data (mounted at /data/mongot)
            Mounts:  users Secret (/etc/users-secret),
                    mongot.conf ConfigMap (/etc/mongot/mongot.conf),
                    ssl internal Secret (/etc/mongodb-ssl, when cluster TLS enabled)
```

**Sharded cluster:**

```
PerconaServerMongoDB CR (spec.search.enabled: true; spec.sharding.enabled: true)
  └─ reconciler
      ├─ Secrets   (… + searchCoordinator user added to users Secret)
      ├─ ConfigMap mongos.conf  ── operator-injected setParameters:
      │                              searchIndexManagementHostAndPort: <shard0>-search:27028
      ├─ ConfigMap mongod.conf (per shard) ── as above, pointing at that shard's mongot
      ├─ StatefulSet <cluster>-cfg                  (existing, config servers)
      ├─ Deployment  <cluster>-mongos               (existing)
      ├─ for each shard <shardName>:
      │    ├─ ConfigMap <cluster>-<shardName>-search-config
      │    ├─ Service   <cluster>-<shardName>-search
      │    └─ StatefulSet <cluster>-<shardName>-search   (one mongot pod, own PVC)
      └─ … (existing per-shard mongod StatefulSets)
```

**Query paths:**

```
Replset:
  client → mongod → (gRPC) → mongot → mongod (loads matching documents) → client

Sharded:
  client → mongos
            ├─ shard0.mongod → (gRPC) → shard0.mongot → shard0.mongod → mongos
            ├─ shard1.mongod → (gRPC) → shard1.mongot → shard1.mongod → mongos
            └─ shardN.mongod …
         ← merge by $searchScore
        ← client
```

Key connections:
- Each `mongod` learns about its replset/shard's `mongot` through
  operator-managed `setParameter` lines in the generated `mongod.conf`,
  on top of any user `configuration` (operator-managed lines override
  user values for the keys it owns). The endpoint pins pod-0 of the
  headless StatefulSet
  (`<sts>-0.<svc>.<ns>.<dnsSuffix>:27028`) — single-replica today, and
  forward-compatible with the future `size > 1` L7 LB path (§5.4).
- `mongos` learns about the search-index catalog through equivalent
  operator-managed entries in `mongos.conf`. The
  `searchIndexManagementHostAndPort` parameter is set to mongot host of the
  first shard. This is aligned with what upstream MongoDB operator is doing.
- `mongot` opens a permanent change-stream connection to a `mongod`
  in its replset/shard, authenticating with the keyfile as the
  `searchCoordinator` user. When the cluster has TLS enabled,
  `mongot` verifies `mongod`'s certificate against the cluster CA
  and presents its own certificate to `mongod`.
- Each `<cluster>-<rs>-search` / `<cluster>-<shardName>-search`
  headless Service serves a single `mongot` pod and publishes
  not-ready addresses (so gRPC clients can resolve the pod IP during
  the catch-up window after a restart). It exposes the gRPC port
  (27028) and the metrics port (9946).
- Index lifecycle commands (`createSearchIndex`, …) issued through
  `mongos` (sharded) or `mongod` (replset) are forwarded to the
  matching `mongot`. `mongos` automatically scatter-gathers
  `$vectorSearch` across shards and merges results by
  `$searchScore`.

### 3.3 Key Observations

1. **The operator already creates multiple StatefulSets per
   logical component.** `pkg/psmdb/statefulset.go` `StatefulSpec()`
   builds the primary replset, arbiter, hidden, and nonvoting
   StatefulSets through the same function, switching on a
   `LabelKubernetesComponent` value. This is the basis for §5.1.
2. **`setParameter`s are generated through the existing
   `configuration` path.** The existing logic merges
   `spec.replsets[].configuration` YAML into a ConfigMap mounted at
   `/etc/mongod.conf`. Operator-managed `setParameter` lines can be
   appended (or merged so the operator's value wins) without new
   code paths. They should be **operator-managed**, not user-managed,
   to avoid drift; see §5.5.
3. **`mongos` already has its own ConfigMap-rendered configuration.**
   The same operator-managed `setParameter` overlay approach applies,
   so sharded support adds no new infrastructure.
4. **TLS reuse: the cluster's `ssl` Secret already contains
   everything `mongot` needs.** The operator already creates
   per-cluster server certs (cert-manager or user-provided) with
   SANs that cover the cluster's Services. Extending the cert
   template to include the `<cluster>-<rs>-search` /
   `<cluster>-<shardName>-search` SANs is a small, local change; no
   new PKI components are added (Constraint 4).
5. **Backup/restore is simple.** `mongot` indexes can be rebuilt
   from `mongod` data through change streams — PBM should not
   snapshot `mongot` PVCs. After a restore, `mongot` resyncs (see
   Observation 6 for the trigger mechanism); the only cost is
   initial-sync time, added to the effective RTO for the *search*
   surface (regular query RTO is not affected). This is the basis
   for §5.6.
6. **PBM restore requires restarting the search pods.** After PBM
   rewinds `mongod` to an earlier point in time, the change-stream
   cursor that `mongot` is tailing no longer matches the restored
   oplog position. The operator must delete each `<...>-search` pod
   after the PBM restore completes; the StatefulSet controller
   recreates it, and the new `mongot` does a full initial sync from
   the restored `mongod`. This is operator-driven, not automatic —
   without the restart, `mongot` would keep tailing from a stream
   position that no longer exists on `mongod` and its indexes would
   diverge from the restored data. This drives §8.3.
7. **PVC autoscaling for the search PVC.** `mongot` becomes
   read-only at 90% disk usage (Constraint 3), so a search PVC that
   fills up silently makes the search surface read-only without any
   `mongod`-side warning. Growing the PVC before reaching that
   threshold — either by showing an early warning condition or by
   automatically resizing through the underlying StorageClass (where
   `allowVolumeExpansion: true`) — would prevent this silent
   degradation. Whether this is included in v1 is open (§11.7).

---

## 4. CRD and Interface Changes

### 4.1 CRD Spec Changes

Two related new structs are added: a cluster-level `SearchSpec` on
`PerconaServerMongoDBSpec`, and an optional per-replset
`SearchReplsetOverride` on each `ReplsetSpec`. The cluster-level
block holds defaults and the fields that must be the same across
the whole cluster; the per-replset block overrides the fields where
shards may need to differ. See §5.7 for the reasoning.

#### Cluster-level — `spec.search`

The cluster-level block holds enable/disable, image, and raw
`mongot` configuration (fields that must be uniform), and the
cluster-wide *defaults* for the overridable fields.

- **`spec.search`** *(optional, default: feature disabled)* —
  top-level block; if absent or `enabled: false`, current behavior
  is unchanged.
- **`spec.search.enabled`** *(bool, default `false`)* — cluster-wide
  switch. When `true`, the operator creates one `<...>-search`
  StatefulSet, Service, ConfigMap, and PVC per replset (non-sharded)
  or per shard (sharded), adds `setParameter`s to each `mongod`
  configuration and to `mongos.conf`, extends the cluster's TLS
  certificate SANs to cover the new Services, and creates the
  `searchCoordinator` user. **There is no per-shard enable/disable**
  (see §5.7 for the reasoning).
- **`spec.search.image`** *(string, required)* — the Percona
  `mongot` container image (for example,
  `percona/percona-server-mongodb-search:<tag>`). The same value
  applies to the whole cluster.
- **`spec.search.imagePullPolicy`** *(corev1.PullPolicy, optional)* —
  the same value applies to the whole cluster.
- **`spec.search.configuration`** *(string, optional)* — raw
  `mongot` YAML applied on top of the operator-generated
  `mongot.conf`. The same value applies to the whole cluster.
- **`spec.search.size`** *(int32, default `1`, **must be `1` in
  v1**)* — cluster-wide default for the per-replset `mongot` pod
  count. Per-replset overrides are allowed (kept for future HA), but
  every effective value must equal `1` in v1.
- **`spec.search.storage`** *(VolumeSpec; default storage request
  `10Gi`)* — cluster-wide default PVC spec for `mongot` data.
  Per-replset override allowed.
- **`spec.search.resources`** *(corev1.ResourceRequirements,
  optional)* — cluster-wide default CPU/memory; defaults to 2 CPU /
  2Gi. Per-replset override allowed.
- **`spec.search.jvmFlags`** *([]string, optional)* — cluster-wide
  default. Per-replset override allowed. The operator sets JVM max
  heap to 50% of the effective `resources.memory` if not specified.
- **`spec.search.affinity`, `nodeSelector`, `tolerations`,
  `annotations`, `labels`, `containerSecurityContext`,
  `podSecurityContext`** — cluster-wide defaults for these per-pod
  settings. Per-replset override allowed (full replacement, not
  merge).

#### Per-replset overrides — `spec.replsets[].search`

Optional per-replset block. When present, **its fields fully
replace the matching cluster-wide defaults for that replset/shard's
`mongot` StatefulSet**. Fields that are not set use the cluster-wide
value. Setting any field here only takes effect if
`spec.search.enabled` is true at the cluster level.

```go
type SearchReplsetOverride struct {
    Size                     *int32                        `json:"size,omitempty"`
    Storage                  *VolumeSpec                   `json:"storage,omitempty"`
    Resources                *corev1.ResourceRequirements  `json:"resources,omitempty"`
    JVMFlags                 []string                      `json:"jvmFlags,omitempty"`
    Affinity                 *PodAffinity                  `json:"affinity,omitempty"`
    NodeSelector             map[string]string             `json:"nodeSelector,omitempty"`
    Tolerations              []corev1.Toleration           `json:"tolerations,omitempty"`
    Annotations              map[string]string             `json:"annotations,omitempty"`
    Labels                   map[string]string             `json:"labels,omitempty"`
    ContainerSecurityContext *corev1.SecurityContext       `json:"containerSecurityContext,omitempty"`
    PodSecurityContext       *corev1.PodSecurityContext    `json:"podSecurityContext,omitempty"`
}
```

Fields **not** overridable (cluster-wide only): `enabled`, `image`,
`imagePullPolicy`, `configuration`. Setting any of these in
`spec.replsets[].search` is a validation error.

In single-replset (non-sharded) clusters the per-replset block is
still allowed — it simply becomes a more specific configuration for
the single `mongot` StatefulSet. No special handling is needed.

### 4.2 CRD Status Changes

`PerconaServerMongoDBStatus` gets:

- **`status.search`** — `map[string]SearchStatus` keyed by replset
  name in non-sharded clusters, and by shard name in sharded
  clusters (the same pattern as `status.replsets` today). The
  ConfigSvr replset is excluded; entries for replsets that no longer
  exist are pruned. When `spec.search.enabled=false` the map is
  cleared (and omitted from the serialized status):
  ```go
  type SearchStatus struct {
      Size    int32    `json:"size"`
      Ready   int32    `json:"ready"`
      Status  AppState `json:"status,omitempty"`
      Message string   `json:"message,omitempty"`
  }
  ```
  The `Status` field tracks the same `AppState` values as
  `status.replsets` (`initializing` / `ready` / `paused` /
  `stopping` / `error`); the state machine mirrors `rsStatus`:
  `AppStateInit` until every replica is Ready, `AppStateReady` once
  Ready==Size, `AppStateError` on prolonged Unschedulable.

### 4.3 Internal Contracts

- **`mongod` `setParameter` block (operator-managed):** the values
  below are overlaid onto the user-supplied `spec.replsets[].configuration`
  by `vectorsearch.InjectMongodConfig`; the operator's values always
  win on conflict. The replset playing `ClusterRoleConfigSvr` skips
  injection (ConfigSvr never gets a `mongot`).
  - `mongotHost: <sts>-0.<svc>.<ns>.<dnsSuffix>:27028` — pinned to
    pod-0 of the search StatefulSet (single-replica today).
  - `searchIndexManagementHostAndPort:` same value as `mongotHost`.
  - `useGrpcForSearch: true`
  - `skipAuthenticationToSearchIndexManagementServer: false`
  - `skipAuthenticationToMongot: false`
  - `searchTLSMode:` derived from the rendered `mongot.conf`
    (`server.grpc.tls.mode`). If `mongot`'s gRPC listener has TLS
    enabled (any value other than `Disabled`), `searchTLSMode` is
    set to `requireTLS`. If `mongot`'s gRPC listener has TLS
    disabled, `searchTLSMode` is set to `disabled`. The value is
    the string form of `api.TLSMode`.

- **`mongos.conf` `setParameter` block (operator-managed, sharded
  only):** rendered by `vectorsearch.InjectMongosConfig`. `mongos`
  needs a cluster-wide view of the search-index catalog, so:
  - `mongotHost: <sts>-0.<svc>.<ns>.<dnsSuffix>:27028` — pinned to
    pod-0 of the search StatefulSet of the first shard. This aligns with what
    upstream MongoDB operator is doing.
  - `searchIndexManagementHostAndPort:` same value as `mongotHost`.
  - `useGrpcForSearch: true`
  - `skipAuthenticationToSearchIndexManagementServer: false`
  - `skipAuthenticationToMongot: false`
  - `searchTLSMode:` mirrors the `mongod` rule above.

- **`mongot.conf` (operator-generated, in `<...>-search-config`
  ConfigMap):** YAML config with:

  - `syncSource.replicaSet.hostAndPort` -- the per-pod FQDNs of the mongod replset/shard
  - `syncSource.replicaSet.username: searchCoordinator`
  - `syncSource.replicaSet.password: /etc/users-secret/MONGODB_SEARCH_PASSWORD`

  - `syncSource.router.hostAndPort` (sharded) — FQDN of `mongos` service or if
    service-per-pod is enabled for `mongos` list of `mongos` FQDNs.
  - `syncSource.router.username: searchCoordinator`
  - `syncSource.router.password: /etc/users-secret/MONGODB_SEARCH_PASSWORD`

  - `storage.dataPath: /data/mongot`

  - `server.grpc.address: 0.0.0.0:27028`
  - `server.grpc.tls.mode` -- derived from cluster TLS:
    `mTLS` when `cr.TLSEnabled()`, otherwise `Disabled`.
    Overridable via `spec.search.configuration`.
  - `server.grpc.tls.certificateKeyFile: /tmp/tls.pem` (only when
    cluster TLS is enabled) -- tls.key+tls.crt, concatenated in
    mongot entrypoint
  - `server.grpc.tls.caFile: /etc/mongodb-ssl/ca.crt` (only when
    cluster TLS is enabled)

  - `healthCheck.address: 0.0.0.0:8080` -- used by liveness and readiness probes

  - `metrics.address: 0.0.0.0:9946`

  - `logging.verbosity: INFO`

  - `spec.search.configuration` is unmarshaled on top via `yaml.v2`,
    so only the fields the user sets are overridden — every other
    default is preserved.

- **TLS material:** when cluster TLS is enabled, the existing
  `<cluster>-ssl-internal` Secret is mounted into the `mongot` pod at
  `/etc/mongodb-ssl`. The operator extends the cert-manager
  `Certificate` template (or, for user-provided certs, the
  documented SAN requirements) through `tls.searchSans` — see
  `pkg/psmdb/tls/tls.go` — to add the `<cluster>-<rs|shardName>-search`
  short name plus its namespaced variants and wildcards under both
  the cluster DNS suffix and the multi-cluster suffix. `mongot`
  presents this cert on its gRPC listener and uses the cluster CA
  to verify `mongod`.

- **`searchCoordinator` user:** stored in the system users Secret
  under `MONGODB_SEARCH_USER` / `MONGODB_SEARCH_PASSWORD` (constants
  `api.EnvMongoDBSearchUser` / `api.EnvMongoDBSearchPassword`).
  `fillSecretData` adds the user key only when `spec.search.enabled`
  is true (so disabled clusters do not provision a stray account),
  and `createOrUpdateSystemUsers` creates the MongoDB user with the
  built-in `searchCoordinator` role (`api.RoleSearch`) on the next
  reconcile. The same secret entry is also enqueued for password
  rotation by the users reconciler once it exists.

### 4.4 User-Facing Behavior Changes

- `kubectl get psmdb -o yaml` shows
  `status.search.<rs|shardName>.{size,ready,status,message}`.
- A new sample CR is included under `deploy/cr.yaml` with
  `spec.search` commented out, matching how other optional features
  are presented.

---

## 5. Design Decisions and Alternatives

### 5.1 Separate StatefulSet vs Sidecar

**Chosen approach:** Deploy `mongot` as a dedicated StatefulSet
`<cluster>-<rs>-search` per replset, with its own PVC and Service —
not as a sidecar inside the `mongod` pod.

**Why:** independent scaling, independent PVC lifecycle, image and
rollout schedule that is not tied to `mongod`, and a clean
one-StatefulSet-per-shard model for sharded clusters (Observation 1
in §3.3). It also avoids adding `mongot`'s 2-CPU / 2Gi baseline into
the `mongod` pod's resource budget.

**Alternatives considered:**

| Alternative | Why Rejected |
|---|---|
| Sidecar inside the `mongod` pod | Ties `mongot` image/restart/resources to `mongod`; difficult in sharded clusters where one `mongot` group serves N `mongod` shard members; a rolling restart of `mongod` would lose the `mongot` index cache. |
| Standalone Deployment (no stable PVC identity) | Needs stable identity + a PVC; Deployment + PVC works for size=1 but loses ordinal guarantees and makes the future size>1 case harder. StatefulSet is the standard choice. |

### 5.2 API Surface: Inline `spec.search` Block vs Separate CRD

Both options are workable, and both have supporters in the
community. The choice is significant, so this section describes
both in detail instead of marking one as "rejected".

**Option A — Inline `spec.search` on `PerconaServerMongoDB`**
- Pros: consistent with the PMM, Backup, and LogCollector patterns;
  one CR for users to manage; reuses the main reconciler; lower
  install/upgrade complexity (no new CRD, no new RBAC); search
  lifecycle is tied to the cluster lifecycle automatically (cluster
  delete → search gone).
- Cons: every search change goes through the main reconcile loop;
  adds to an already large CR; harder to model an "external mongod
  source" or multiple search profiles per cluster; cannot give
  search its own RBAC.

**Option B — Separate `PerconaServerMongoDBSearch` CRD**
- Spec sketch: `spec.source.psmdbRef: { name: cluster1 }`
  (in-cluster) or `spec.source.external: { uri, secretRef }`
  (future); plus all the `mongot` settings from §4.1.
- Pros: clean separation of concerns; matches the upstream MongoDB
  K8s operator's `MongoDBSearch` API; independent lifecycle (disable
  search without touching the cluster CR); easier to support a
  "search-only" install profile; per-CRD RBAC; small, isolated
  controller; allows future support for an "external mongod source"
  and even "search across multiple PSMDB clusters".
- Cons: new CRD + controller + install surface (Helm chart, OLM
  bundle, RBAC, manager wiring all grow); two CRs per cluster;
  cross-CR validation (search-CR references PSMDB-CR; needs a
  webhook or controller-side existence/version checks); users have
  to manage two objects instead of one; finalizer ordering on
  cluster delete requires care.

**Comparison axes:**

| Axis | Option A: Inline block | Option B: Separate CRD |
|---|---|---|
| Consistency with existing CR | High (PMM/Backup pattern) | Diverges from existing pattern |
| User cognitive load | Lower (single CR) | Higher (two CRs) |
| Lifecycle coupling | Tight | Loose |
| RBAC level of detail | Inherits cluster RBAC | Per-CRD RBAC possible |
| External / cross-cluster sources | Hard | Natural |
| Reconciler isolation | Shares main loop | Dedicated controller |
| Install surface | Unchanged | New CRD, RBAC, manager wiring |
| Migration story (v1 → v2) | "Add a field" | "Add a CRD" |
| Upstream alignment | Diverges | Matches `MongoDBSearch` |

**Recommendation:** Start with **Option A (inline block)** in v1 for
ease of use and consistency with existing patterns, while shaping
`SearchSpec` so its fields are almost the same as what would belong
on a future separate CRD. This leaves room for Option B as a
straightforward migration (`spec.search` → child CR generated by the
operator; the inline block kept as a deprecated alias) instead of a
full redesign.

**Rejected alternatives:**

| Alternative | Why Rejected |
|---|---|
| Per-replset *enable/disable* (`spec.replsets[].search.enabled`) | `$vectorSearch` correctness depends on `mongot` running on every shard that holds data for the indexed collection; disabling search on some shards produces incomplete query results without any warning. This is only useful for zone-sharded layouts, which are rare and fragile — the risk is too high for the small benefit. Note: per-replset *override* of resources/affinity/etc. **is** supported (see §5.7). |
| Config driven by annotations or ConfigMaps | Not a declarative API; poor user experience; breaks status reporting; does not match how this operator does anything else. |

### 5.3 Sharded Cluster Support

**Chosen approach:** Support sharded clusters from v1 together with
replset clusters. Each shard gets its own
`<cluster>-<shardName>-search` StatefulSet, Service, ConfigMap, and
PVC. `$vectorSearch` queries are scatter-gathered
across shards directly by `mongos` and merged by `$searchScore`.
ConfigSvr replsets get no `mongot`.

**Why:** sharded clusters are the standard PSMDB deployment for the
data sizes where vector search is most important. Splitting the
release into "replset first, sharded later" would delay the most
important use case and leave a gap that competitors already fill.
The mechanics are well understood (per-shard StatefulSets reuse the
same `StatefulSpec()` function; mongos config reuses the existing
ConfigMap rendering) and the additional testing needed is limited
to the cases already listed in §10.

**Alternatives considered:**

| Alternative | Why Rejected |
|---|---|
| Replset only in v1, sharded in v2 | Does not support the largest workloads at GA; pushes users to stay on competitors for sharded vector workloads. |
| Allow `sharding.enabled=true` but silently disable search | Poor user experience; users would think search works on their sharded cluster. |

### 5.4 `mongot` HA through `size > 1`

**Chosen approach:** v1 restricts `spec.search.size` to `1` per
replset/shard and rejects `>1` at validation time. The single
`mongot` pod is served by a regular Kubernetes Service (gRPC over
L4 works for a single endpoint). In sharded clusters this still
produces N pods (one per shard) — just not N replicas per shard.

**Why:** running multiple `mongot` pods behind a single endpoint
requires an L7 gRPC-aware load balancer to pin each long-lived
stream to a single backend (ClusterIP L4 cannot distribute gRPC
across multiple `mongot` pods, and `mongot` needs each stream to
reach the same instance for index affinity). Choosing that solution
— adding an Envoy sidecar Deployment or requiring a user-provided
Gateway — is a separate feature.

**Alternatives considered:**

| Alternative | Why Rejected (deferred) |
|---|---|
| Add an embedded Envoy LB Deployment per replset/shard | Adds another StatefulSet/Deployment, certificate management, and an LB upgrade path. Worth doing, but not in v1. |
| Require a user-provided Gateway API resource | The cleanest cloud-native option; depends on the cluster having a Gateway implementation. This is likely the long-term solution for later releases. |

### 5.5 `setParameter` Injection: Operator-Managed vs User-Managed

**Chosen approach:** The operator owns `mongotHost`,
`searchIndexManagementHostAndPort`, `useGrpcForSearch`, and
`searchTLSMode` on `mongod`, and the equivalent search-management
keys on `mongos` for sharded clusters. If users try to set them in
`spec.replsets[].configuration` or
`spec.sharding.mongos.configuration`, the YAML overlay applied by
`vectorsearch.InjectMongodConfig` / `InjectMongosConfig`
unconditionally overwrites the user's value — the operator value
wins. (Emitting an explicit drift-detection status condition is a
documented follow-up; today the overlap is silent.)

**Why:** these values are derived from the operator's own Service
DNS naming. If a user sets them manually, the generated config goes
out of sync each time the operator reconciles. Letting the operator
manage them prevents drift and gives a single source of truth.

**Alternatives considered:**

| Alternative | Why Rejected |
|---|---|
| Let users set these freely | Causes silent drift; the reconciler would have to either accept stale values (bad) or overwrite the user (worse). |
| Hybrid: operator-default with user override | Doubles the validation work without any practical benefit — there is no use case for pointing `mongot` somewhere other than the operator's own Service. |

### 5.6 Backup / Restore Handling

**Chosen approach:** PBM does **not** back up `mongot` PVCs. After
a restore, the operator triggers a rolling restart of the
`<cluster>-<rs>-search` StatefulSet so each `mongot` resyncs from
the restored `mongod`. Restore completion does **not** wait for
`mongot` to be ready (search availability is eventual; data
availability is not).

**Why:** `mongot` indexes can be rebuilt fully from `mongod` data;
the source of truth is already in `mongod`. Backing up `mongot`
PVCs would require keeping two snapshots consistent at different
LSNs — a problem we do not need to create. The cost is initial-sync
time on the search surface after restore (depends on the workload;
users with large vector indexes should plan for it).

**Alternatives considered:**

| Alternative | Why Rejected |
|---|---|
| Snapshot `mongot` PVCs together with PBM backups | Requires keeping two snapshots consistent; larger backups; no clear benefit — `mongot` resyncs are fast at small-to-medium scale. |
| Wait for `mongot` to be ready before marking restore complete | Mixes data RTO with search RTO; would extend the reported restore time for users who do not care about search. |

### 5.7 Layered Configuration: Cluster-Wide Defaults + Per-Replset Overrides

**Chosen approach:** Divide `SearchSpec` into two parts.
Cluster-wide fields, where uniformity matters for correctness or
operations (`enabled`, `image`, `imagePullPolicy`,
`configuration`), live only on `spec.search`. Settings where
shards may differ in production for valid reasons (`size`,
`storage`, `resources`, `jvmFlags`, `affinity`, `nodeSelector`,
`tolerations`, `annotations`, `labels`, security contexts) appear
as cluster-wide defaults on `spec.search` AND can be overridden
per-replset through `spec.replsets[].search`. The per-replset block
fully replaces (does not merge) the matching cluster-wide value for
that replset/shard's `mongot` StatefulSet. TLS settings and log
level live inside the raw `configuration` YAML overlay (they are
not exposed as typed fields), so they share the cluster-wide
restriction that `configuration` itself has.

**Why:**

- **Enable/disable must be cluster-wide.** `$vectorSearch`
  correctness depends on every shard holding indexes for the
  searched collection. Disabling search on some shards produces
  incomplete results without any warning — a serious risk that
  offers no real benefit. Zone-sharded layouts that pin data to
  specific shards on purpose are rare and fragile; we do not want
  users to hit that pattern by accident.

- **Resources, storage, JVM flags must be overridable per shard.**
  Sharded PSMDB clusters in production often have shards that
  differ: uneven data distribution, different collection mixes,
  different hardware pools. Forcing one `mongot` resource profile
  across all shards either wastes capacity on cold shards or runs
  out of resources on hot ones. The operator already supports
  per-shard `mongod` resources through `spec.replsets[]`; per-shard
  `mongot` resources is the same idea applied to `mongot`.

- **Affinity, nodeSelector, tolerations must be overridable per
  shard.** This is the most important case. Production sharded
  clusters often pin each shard's `mongod` to a specific node pool
  or availability zone to limit the impact of failures. A single
  cluster-wide affinity rule cannot express "shard 0's `mongot` to
  pool A, shard 1's `mongot` to pool B." Without per-shard
  affinity, customers either run all `mongot`s on one pool (which
  defeats shard isolation) or cannot use search at all on
  deployments with different node pools. This is the field where
  one-size-fits-all would prevent real customers from using the
  feature.

- **Image and raw `mongot` configuration must be cluster-wide.**
  Different `mongot` image versions across shards are very hard to
  operate (the debug matrix grows quickly). Letting one shard ship
  with different TLS settings or a different log level would break
  the cluster-wide security guarantee and make logs harder to
  correlate across shards, so the raw `configuration` overlay
  (which is where TLS and log level live) is also cluster-only.
  Users who really need a different log level on one shard for
  debugging can do it temporarily with `kubectl edit` on the
  StatefulSet, outside the operator's declarative interface.

**No affinity-inheritance shortcut.** Users who want a shard's
`mongot` to match its `mongod`'s affinity must copy and write the
value explicitly in `spec.replsets[].search.affinity`. We
considered and rejected an `inheritFromReplset: true` shortcut.
Explicit copy/paste is more verbose but easier to understand.

**Alternatives considered:**

| Alternative | Why Rejected |
|---|---|
| Cluster-wide only (no per-replset overrides) | Forces the same resources / affinity across different shards. Prevents real production deployments. |
| Per-replset only (no cluster-wide defaults) | Forces users to write the search config in every replset entry; verbose for the common case (uniform shards). Breaks the PMM/Backup/LogCollector pattern that users already know. |
| Per-shard enable/disable | Correctness risk — see the §5.2 rejection table. |

---

## 6. Sharding Impact

### 6.1 Sharded Cluster Behavior

Implementation:

- One `mongot` group per shard:
  `<cluster>-<shardName>-search` StatefulSet, Service, ConfigMap,
  PVC. The ConfigSvr replset gets no `mongot`.
- Each shard's `mongod` pods have `mongotHost` and
  `searchIndexManagementHostAndPort` pointed at **their own
  shard's** `mongot` Service — not at any cluster-wide service.
- `mongos` learns about the per-shard search-index endpoints
  through operator-managed `setParameter` entries in `mongos.conf`,
  so `createSearchIndex` / `dropSearchIndex` /
  `listSearchIndexes` calls routed through `mongos`.
- Each shard's `mongot.conf` must set its sync source to the
  shard's `mongod` replset via `syncSource.replicaSet.*`.
  `syncSource.router` refers to the `mongos` endpoint (as described
  in §4.3).
- `$vectorSearch` queries against `mongos` scatter-gather across
  shards and merge by `$searchScore`. This is built-in MongoDB
  behavior; the operator is not involved at query time.
- TLS: identical to replset (see §4.3, §5.1, and §8.5). `mongot`
  SANs cover each per-shard Service.

### 6.2 Single Replset Behavior

Replset clusters get one `mongot` group in total (because there is
only one replset). `mongod` is configured with the
operator-managed `setParameter` block; there is no `mongos`, so
there is no `mongos`-side configuration to manage. All
functionality is available.

### 6.3 Differences and Why

Sharded clusters need N `mongot` StatefulSets (one per shard)
**plus** `mongos`-side `setParameter` configuration for index
management; replset clusters need one `mongot` StatefulSet and no
`mongos` work. The difference exists because vector indexes are
always per-shard by design: each shard owns a separate part of the
data, and `mongot` cannot cross shard boundaries. The query-side
merge happens in `mongos` automatically, so no operator-side merge
logic is needed — but the catalog must cover the whole cluster so
users can issue `createSearchIndex` once and have it apply to all
shards.

---

## 7. User Experience

### 7.1 Existing CR (Unchanged)

```yaml
# An existing replset CR continues to reconcile identically after the
# operator upgrade. spec.search is absent → no mongot, no setParameters.
apiVersion: psmdb.percona.com/v1
kind: PerconaServerMongoDB
metadata:
  name: cluster1
spec:
  crVersion: 1.22.0
  image: percona/percona-server-mongodb:8.0.21-9
  replsets:
    - name: rs0
      size: 3
      volumeSpec:
        persistentVolumeClaim:
          resources:
            requests:
              storage: 50Gi
```

### 7.2 Enable Vector Search on a Replset Cluster

```yaml
apiVersion: psmdb.percona.com/v1
kind: PerconaServerMongoDB
metadata:
  name: cluster1
spec:
  crVersion: 1.23.0
  image: percona/percona-server-mongodb:<vector-capable-tag>   # ≥ vector-search-capable PSMDB
  replsets:
    - name: rs0
      size: 3
      volumeSpec:
        persistentVolumeClaim:
          resources:
            requests:
              storage: 50Gi

  # NEW: declarative search/vector-search support.
  search:
    enabled: true
    image: percona/percona-server-mongodb-search:<tag>
    size: 1                                            # v1: must be 1
    storage:
      persistentVolumeClaim:
        resources:
          requests:
            storage: 20Gi                              # ~ index size headroom
    resources:
      requests:
        cpu: "2"
        memory: 2Gi
    # mongot's gRPC TLS mode follows the cluster's TLS state
    # automatically (mTLS when cluster TLS is enabled, Disabled
    # otherwise). To force a different mode, override through the
    # raw mongot configuration overlay:
    # configuration: |
    #   server:
    #     grpc:
    #       tls:
    #         mode: Disabled
```

What the operator does on apply:

1. Adds `mongotHost`, `searchIndexManagementHostAndPort`, and
   `useGrpcForSearch` to the generated `mongod.conf` and restarts
   `mongod`.
2. Creates the `cluster1-rs0-search` StatefulSet (1 pod), the
   `cluster1-rs0-search` Service, the `cluster1-rs0-search-config`
   ConfigMap, and a PVC.
3. Creates the `searchCoordinator` MongoDB user (password stored in
   the system users Secret).
4. Extends the cluster's TLS certificate SANs to cover
   `<cluster>-rs0-search` and the matching wildcard DNS name.
5. Reports `status.search.rs0 = {size:1, ready:1, status:ready}`
   and condition `SearchReady=True`.

### 7.3 Enable Vector Search on a Sharded Cluster

```yaml
apiVersion: psmdb.percona.com/v1
kind: PerconaServerMongoDB
metadata:
  name: cluster1
spec:
  crVersion: 1.22.0
  image: percona/percona-server-mongodb:<vector-capable-tag>
  sharding:
    enabled: true
    configsvrReplSet:
      size: 3
      volumeSpec:
        persistentVolumeClaim:
          resources:
            requests:
              storage: 20Gi
    mongos:
      size: 3
  replsets:
    # shard 0 — hot shard: large vector dataset, pinned to high-mem pool
    - name: rs0
      size: 3
      volumeSpec:
        persistentVolumeClaim:
          resources:
            requests:
              storage: 200Gi
      affinity:
        advanced:
          nodeAffinity:
            requiredDuringSchedulingIgnoredDuringExecution:
              nodeSelectorTerms:
                - matchExpressions:
                    - { key: workload, operator: In, values: [vector] }

      # Per-shard override: bigger mongot for the hot shard, same
      # node-pool pinning as its mongod.
      search:
        resources:
          requests: { cpu: "8", memory: 32Gi }
          limits:   { cpu: "8", memory: 32Gi }
        storage:
          persistentVolumeClaim:
            resources:
              requests:
                storage: 200Gi
        affinity:
          advanced:
            nodeAffinity:
              requiredDuringSchedulingIgnoredDuringExecution:
                nodeSelectorTerms:
                  - matchExpressions:
                      - { key: workload, operator: In, values: [vector] }

    # shard 1 — cold shard: smaller vector footprint, default pool.
    # No per-shard search block → inherits cluster-wide defaults.
    - name: rs1
      size: 3
      volumeSpec:
        persistentVolumeClaim:
          resources:
            requests:
              storage: 100Gi

  search:
    enabled: true
    # Cluster-wide defaults (used by rs1; partially overridden by rs0).
    size: 1
    storage:
      persistentVolumeClaim:
        resources:
          requests:
            storage: 50Gi
    resources:
      requests:
        cpu: "2"
        memory: 4Gi
```

What the operator does on apply:

1. Creates one `<cluster>-<shardName>-search` StatefulSet, Service,
   ConfigMap, and PVC per shard (in this example,
   `cluster1-rs0-search` and `cluster1-rs1-search`).
2. For `rs0`, the `mongot` StatefulSet uses the per-shard override
   (8 CPU / 32Gi memory, 200Gi PVC, vector node-pool affinity). For
   `rs1`, it uses the cluster-wide defaults (2 CPU / 4Gi memory,
   50Gi PVC, default affinity).
3. Configures each shard's `mongod` with `mongotHost` and
   `searchIndexManagementHostAndPort` pointed at its own shard's
   search Service.
4. Configures `mongos` with the first shard's search endpoint so
   `createSearchIndex` issued through `mongos` reaches the correct
   `mongot`.
5. Reports `status.search.{rs0,rs1} = {size:1, ready:1, ...}`.
6. `$vectorSearch` queries against `mongos` scatter-gather across
   shards and merge automatically by `$searchScore`.

### 7.4 Tune `mongot` Resources and Config

```yaml
spec:
  search:
    enabled: true
    size: 1
    storage:
      persistentVolumeClaim:
        resources:
          requests:
            storage: 100Gi
    resources:
      requests:
        cpu: "4"
        memory: 16Gi
      limits:
        cpu: "4"
        memory: 16Gi
    # The operator passes jvmFlags to mongot via `--jvm-flags`. If
    # the user does not set `-Xmx` / `-Xms`, the operator defaults
    # both to 50% of the effective `resources.memory` (request, or
    # limit if request is unset).
    jvmFlags:
    - "-XX:+UseG1GC"
    - "-XX:MaxGCPauseMillis=200"
    configuration: |
      # Raw mongot YAML merged on top of operator-generated defaults.
      server:
        grpc:
          tls:
            mode: Disabled
```

### 7.5 Create a Vector Index and Query It

```bash
kubectl exec -it cluster1-rs0-0 -c mongod -- mongosh ...
```

```js
// Create a vector index for ANN search over a 1536-dim embedding field.
db.products.createSearchIndex({
  name: "products_vector_idx",
  type: "vectorSearch",
  definition: {
    fields: [
      { type: "vector", path: "embedding",
        numDimensions: 1536, similarity: "cosine" },
      { type: "filter", path: "category" }
    ]
  }
});

// Query.
db.products.aggregate([
  { $vectorSearch: {
      index: "products_vector_idx",
      path: "embedding",
      queryVector: [0.01, 0.02, /* ... */ 0.99],
      numCandidates: 200,
      limit: 10,
      filter: { category: "books" }
  }}
]);
```

---

## 8. Error Handling and Edge Cases

### 8.1 PSMDB Image Does Not Support Vector Search

**Scenario:** User sets `spec.search.enabled=true` while
`spec.image` points at a PSMDB binary without `mongotHost` /
`searchIndexManagementHostAndPort` parameters.

**Expected behavior:**
- The reconciler **does not** create the `mongot` StatefulSet.
- `mongod` `setParameter`s **are not** added.
- Status condition `SearchUnsupportedByPSMDBImage=True` with a
  message that points to the PSMDB version note.
- An event is emitted on the CR.
- No tight requeue loop: re-check only when the image changes.

**Implementation status:** the version gate is not yet enforced.
Today an unsupported PSMDB image will let the `mongot` StatefulSet come up,
but `$search` / `$vectorSearch` will fail with a `mongod`-side
unknown-parameter error and no clear condition.

### 8.2 `mongot` Pod Unreachable

**Scenario:** `mongot` pod is `CrashLoopBackOff` / `NotReady`, but
`mongod` is healthy.

**Expected behavior:**
- `mongod` continues to serve non-search queries normally.
- `$search` / `$vectorSearch` aggregations return a MongoDB error
  to the client (the existing mongod-side error path when the
  search backend is unreachable).
- PerconaServerMongoDB status is `initializing`.

### 8.3 PBM Restore

**Scenario:** PBM restores `mongod` data to an earlier point in time.

**Expected behavior:**
- The PBM restore runs without touching the `mongot` PVC.

**Implementation status:** The restore controller has no
search-aware step today; without it, `mongot` keeps tailing from a
change-stream position that no longer matches the rewound oplog
and its indexes diverge from the restored data. Users who restore
with search enabled must `kubectl delete pod -l
app.kubernetes.io/component=search` themselves until the
reconciler hook lands.

### 8.4 Anti-affinity in Small Clusters

**Scenario:** 1-node test clusters or `mongod` deployments with few
replicas where soft anti-affinity for `mongot` may not be
schedulable.

**Expected behavior:**
- Default to **soft** anti-affinity
  (`preferredDuringScheduling…`), not required. Users can override
  through `spec.search.affinity`.
- No special handling needed — the Kubernetes scheduler proceeds.

### 8.5 Cluster TLS Disabled

**Scenario:** User explicitly disables cluster TLS
(`spec.tls.mode: disabled` or equivalent). `mongot` cannot use the
`ssl` Secret because it does not exist.

**Expected behavior:**
- `defaultMongotConfig` reads `cr.TLSEnabled()` and emits
  `server.grpc.tls.mode: Disabled` with no certificate paths.
  `mongot` starts without TLS material.
- `vectorsearch.InjectMongodConfig` / `InjectMongosConfig` then
  set `searchTLSMode: disabled` on `mongod` and `mongos` because
  the rendered `mongot.conf` has TLS disabled.
- The user can still override either side through
  `spec.search.configuration` if they want a different mode.

---

## 9. Migration and Backward Compatibility

### 9.1 Existing Clusters

- A `PerconaServerMongoDB` CR without `spec.search` reconciles the
  same way before and after the operator upgrade. No `mongot`
  StatefulSet is created, no `setParameter`s are added to `mongod`,
  no `searchCoordinator` user is created, and no status fields
  appear.
- Enabling search on an existing cluster restarts `mongod` once
  (because `setParameter`s require a restart). The restart uses the
  existing smart-update mechanism. This is expected and documented.

### 9.2 CRD Compatibility

- Changes are **only additions**: a new optional `spec.search`
  block, and a new optional `status.search` map. No fields are
  removed or renamed. There are no breaking schema changes.

### 9.3 Operator Version Skew

- Old operator + new CR (user upgrades CRD/CR before the operator):
  the old operator ignores `spec.search` and reconciles as before.
- New operator + old CR: no behavior change; `spec.search` is
  absent and treated as disabled.
- During rollout (old and new mongod pods exist at the same time):
  the operator-managed `setParameter` block is added to the
  ConfigMap; pods that have already restarted pick it up. Pods that
  have not restarted still serve normally — they just do not know
  about `mongot` yet. `$vectorSearch` against such pods returns the
  same error as in §8.1 (no `mongot` configured). This temporary
  state is acceptable.

---

## 10. Testing Strategy

All newly added functions need to be covered with unit tests.

Two e2e tests will be added:
1. `vector-search`
1. `vector-search-sharded`

---

## 11. Open Questions

To resolve before implementation begins.

1. **L7 load balancer strategy for `mongot` HA (size > 1).** Status:
   postponed (not a goal in v1, see §1.2 and §5.4).
   - *Option A:* Add a managed Envoy Deployment per replset/shard.
   - *Option B:* Require a user-provided Gateway API resource.
   - *Recommendation:* postpone; collect customer demand after v1
     is released.

2. **Automated embedding (Voyage AI) integration.** Upstream
   Community preview feature; calls `api.voyageai.com`. Useful for
   RAG demos but raises egress / API-key / air-gap questions.
   - *Resolution:* Possible via custom configuration but operator doesn't help
    automating it.

3. **JVM heap defaults.** The upstream MongoDB K8s operator sets the
   JVM max heap to 50% of the memory request automatically. We plan
   to follow this, but we should confirm with internal performance
   testing on representative PSMDB-scale data once a search-capable
   image is available.
   - *Resolution:* Start with mirroring upstream behavior; the
     implementation will default to 50%, and users can override it through
     `spec.search.jvmFlags`.

4. **Status reporting detail.** Should we expose index counts,
   sync lag, and disk usage directly in `status.search`, or leave
   that to PMM dashboards? The upstream operator exposes very
   little; PMM is the natural place for this.
   - *Resolution:* TBD; v1 limited to size/ready/state/message;
     richer metrics through PMM.

5. **PBM coordination on `mongot` reindex after restore.** Should operator
   handle reindexing after restore somehow?
     - *Resolution:* `mongot` handles reindexing itself, it only requires
        restarting after physical restore.

6. **Cert-manager `Certificate` template extension.** Adding the
   per-replset / per-shard search SANs to the cluster cert template
   is straightforward, but there are two open sub-questions:
   - Should `mongot` get its own `Certificate` resource (cleaner
     separation, independent rotation) or share the cluster cert
     (less rotation noise)? Recommendation: share; the cluster cert
     is already the shared trust root for internal traffic.
   - For user-provided certs (no cert-manager), how do we
     communicate the SAN requirements? Recommendation: add this to
     the operator docs and set a startup status condition when SANs
     are missing.
   - *Resolution:* Decided to reuse same certificates. For custom certificates,
     docs need to be updated.

7. **PVC autoscaling for the search PVC.** `mongot` becomes
   read-only at 90% disk usage, so a search PVC that fills up
   silently makes the search surface read-only without any
   `mongod`-side warning (see Observation 7 in §3.3). The operator
   already includes `mongod` PVC autoscaling
   (`spec.storageScaling.autoscaling.{enabled,triggerThresholdPercent,
   growthStep,maxSize}` in `pkg/apis/psmdb/v1/psmdb_types.go`; the
   reconciler in
   `pkg/controller/perconaservermongodb/volume_autoscaling.go`
   reads usage by `exec`-ing `df` into the data container, then
   patches the CR's volumeSpec when usage crosses the threshold).
   The current code is mongod-specific (hardcoded `mongod-data` PVC
   prefix, `naming.ComponentMongod` container,
   `config.MongodContainerDataDir` path), but if we make those
   three values parameters, `mongot` can reuse the same reconciler.
   - *Option A:* Out of scope of v1.
   - *Option B:* v1 includes automated expansion. Two sub-options
     for the API:
     - *B.1:* let cluster-wide `spec.storageScaling.autoscaling`
       apply to both `mongod` and `mongot` PVCs (one setting, one
       default).
     - *B.2:* add a parallel `spec.search.storage.autoscaling`
       block (independent threshold / step / maxSize for `mongot`,
       since its sizing is not related to `mongod`'s).
   - *Recommendation:* **Option A for v1.** Follow up in future releases.

8. **PMM monitoring for `mongot`.** PMM does not yet support
   vector-search / `mongot` monitoring — there is no
   `mongot`-aware exporter, no preset dashboard, and PMM has no
   awareness of `$search` / `$vectorSearch` query statistics. The
   operator's existing PMM integration (`spec.pmm`) attaches the
   `pmm-client` sidecar to `mongod` / `mongos` / `cfg` pods and
   would not extend to `mongot` pods without changes on the PMM
   side. We record this here so it is not forgotten: v1 is released
   without PMM coverage for `mongot`, and the gap should be
   mentioned in the release notes. Sub-questions:
   - Do we attach a `pmm-client` sidecar to the `<...>-search`
     StatefulSet now (collecting node-level metrics — CPU, memory,
     disk on the `mongot` PVC) as a partial solution, or wait for
     full PMM `mongot` support before adding any sidecar?
   - Does `mongot` expose Prometheus-format metrics natively (the
     upstream `mongot` config has a `metrics.enabled` setting, but
     the exposed format is unclear) that PMM could scrape directly
     once PMM learns about the endpoint?
   - What is the PMM roadmap for `mongot` support — is there a
     PMM-side ticket we should link this proposal to?
   - *Resolution:* TBD. v1 explicitly excludes PMM coverage for
     `mongot`; document the gap. Revisit once PMM adds `mongot`
     support; the operator change to attach the sidecar to the
     search StatefulSet is small and only adds new code.

9. **`mongos`-side configuration for the per-shard search
   catalog.** Sharded clusters need `mongos` to know the per-shard
   `mongot` endpoints so index-management RPCs reach the correct
   shard.
   - *Resolution:* The upstream MongoDB Operator configures `mongos` with the
    `mongot` endpoint of the first shard. We need to ensure this is accurate and
    acceptable.

---

## Appendix

### A. Glossary

| Term | Definition |
|---|---|
| `mongot` | The separate Java process that serves search/vector-search queries; sources data from `mongod` via change streams. |
| `$vectorSearch` | Aggregation stage that runs an ANN/ENN query against a vector-search index served by `mongot`. |
| `$search` | Aggregation stage for full-text search against a `mongot`-served Lucene index. |
| `searchCoordinator` | Built-in MongoDB role (8.3+) granting the permissions `mongot` needs against `mongod`. |
| `mongotHost` | `setParameter` on `mongod`: data-plane endpoint where `$search`/`$vectorSearch` are forwarded. |
| `searchIndexManagementHostAndPort` | `setParameter` on `mongod`: control-plane endpoint for index-management RPCs. |
| ANN / ENN | Approximate Nearest Neighbor (HNSW) / Exact Nearest Neighbor — vector search algorithms supported by `mongot`. |
| Change stream | MongoDB's tailable stream of oplog events that `mongot` consumes to keep indexes warm. |
| PBM | Percona Backup for MongoDB. |
| PMM | Percona Monitoring and Management. |
| Drift | The state where the generated config differs from what the operator would write. Detected by comparing user-supplied fields against operator-owned keys. |
| Scatter-gather | The query pattern where `mongos` sends a query to every shard in parallel and combines the results before returning to the client. |
| SAN | Subject Alternative Name — a DNS name (or wildcard) listed in a TLS certificate that the certificate is valid for. |
| RTO | Recovery Time Objective — the time it takes to make a service usable again after an outage or restore. |
| RAG | Retrieval-Augmented Generation — an AI pattern where a search step retrieves relevant documents that are then passed to a language model. |
| OLTP | Online Transaction Processing — short, frequent read/write queries typical of application workloads. |

### B. References

- [MongoDB Vector Search docs](https://www.mongodb.com/docs/vector-search/)
- [`mongot` source repo](https://github.com/mongodb/mongot)
- [MongoDB blog: Supercharge Self-Managed Apps With Search and Vector Search](https://www.mongodb.com/company/blog/product-release-announcements/supercharge-self-managed-apps-search-vector-search-capabilities)
- [MongoDB K8s operator: deploy FTS/Vector Search](https://www.mongodb.com/docs/kubernetes/current/fts-vs-deployment/)
- [MongoDB K8s operator: FTS/Vector Search settings reference](https://www.mongodb.com/docs/kubernetes/current/reference/fts-vs-settings/)
- [`mongot` deployment sizing — architecture](https://www.mongodb.com/docs/manual/tutorial/mongot-sizing/advanced-guidance/architecture/)
- [Deploy a replica set with keyfile auth for `mongot`](https://www.mongodb.com/docs/manual/core/search-in-community/deploy-rs-keyfile-mongot/)
- [Percona Community event — Vector Search for Percona Software for MongoDB (March 2026)](https://percona.community/events/2026-coh-radek-psmdb/)
- [Percona forum — Vector search in Percona Server for MongoDB (2024)](https://forums.percona.com/t/vector-search-in-percona-server-for-mongodb/28859/2)
