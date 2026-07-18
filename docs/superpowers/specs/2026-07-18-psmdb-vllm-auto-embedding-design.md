# Design: Auto-Embedding Pipeline for PSMDB Operator (vLLM Integration)

**Date:** 2026-07-18
**Status:** Approved (brainstorming phase)
**Repo:** percona-server-mongodb-operator

## 1. Goal & Context

Make the PSMDB operator the data layer for self-hosted RAG/AI applications. The operator
gains an **auto-embedding pipeline**: it keeps vector embeddings of document fields in
sync by calling an **external, OpenAI-compatible embeddings endpoint** (vLLM, vLLM
production-stack router, or any compatible service). Percona does **not** deploy or
manage vLLM/inference infrastructure.

Market validation: MongoDB shipped the equivalent feature ("Automated Embedding" in
MongoDB Vector Search, public preview on Atlas May 2026, built on Voyage AI) and markets
it for RAG, semantic search, recommendations, and AI agents. Percona's differentiator is
the fully self-hosted variant: bring-your-own open model via vLLM, data never leaves the
user's cluster — the fit for regulated industries (finance, healthcare, government).

Relationship to mongot vector search (existing HLD, not yet implemented): **standalone,
mongot-aware**. The pipeline works against any PSMDB cluster and writes vector fields
regardless. When vector search (mongot) is enabled on the cluster, the operator also
manages the vector index. No hard dependency; ships independently.

Cross-operator note: the CRD shape is designed so other Percona operators (PG with
pgvector first) can adopt the same UX later. This spec covers PSMDB only.

## 2. Use Cases

1. **RAG ("chat with your docs")** — knowledge bases, support tickets, manuals stored in
   MongoDB; embeddings must stay fresh as documents change. Today users hand-write this
   sync pipeline; with this feature it is one CR.
2. **Semantic search** — meaning-based search ("warm jacket for hiking" finds a parka).
3. **Similarity/recommendations** — similar tickets/products, duplicate detection.
4. **Regulated industries** — Atlas-style auto-embedding without data leaving the
   cluster or vendor lock-in.

## 3. API Design (CRD)

New namespaced CRD **`PerconaServerMongoDBEmbedding`** (`psmdb.percona.com/v1`),
alongside the existing `PerconaServerMongoDB`, `...Backup`, `...Restore`,
`...ClusterSync` kinds. One CR = one embedding pipeline against one cluster. Multiple
CRs per cluster are allowed (different collections/models).

```yaml
apiVersion: psmdb.percona.com/v1
kind: PerconaServerMongoDBEmbedding
metadata:
  name: product-catalog-embeddings
spec:
  clusterName: my-cluster            # like Backup CR's clusterName
  inference:
    endpoint: http://vllm-router.ai-stack.svc:8000/v1   # OpenAI-compatible base URL
    model: intfloat/e5-mistral-7b-instruct
    credentialsSecret: vllm-api-key  # optional; key: apiKey, sent as Bearer token
    tlsSecret: vllm-ca               # optional CA / client certs
  source:
    database: shop
    collection: products
    fields: [title, description]     # source text fields
    filter: {}                       # optional server-side match filter
  target:
    vectorField: embedding           # where the vector is written
    dimensions: 4096                 # validated against model output at startup
  vectorIndex:                       # optional; acted on only if cluster has search enabled
    name: products_vector_idx
    similarity: cosine
  batchSize: 64                      # docs per embeddings API call; also the throttle
  initialSync: true                  # backfill existing documents, then tail change streams
  resources: {}                      # worker pod resources (requests/limits)
status:
  state: initializing | syncing | ready | error
  documentsEmbedded: 12345
  lagSeconds: 2
  error: ""
```

Design points:

- `inference.endpoint` is any OpenAI-compatible `/v1/embeddings` base URL; vLLM is the
  reference target but nothing is vLLM-specific.
- Credentials and TLS material live in Secrets, never inline.
- `vectorIndex` degrades gracefully: without search enabled on the cluster, status
  records `VectorIndexPending`.

## 4. Components

### 4.1 Embedding controller (in the operator)

New controller in `pkg/controller/perconaservermongodbembedding/`, registered alongside
backup/restore controllers. Per CR it:

- Validates the referenced cluster exists and is ready (as the backup controller does).
- Validates inference config: secrets exist; optionally probes the endpoint's
  `/v1/models`.
- Creates/updates one **worker Deployment** (replicas: 1) and a dedicated MongoDB user
  scoped to `readWrite` on the target collection plus change-stream privileges, managed
  the same way as existing system users.
- If `vectorIndex` is set and the cluster has search/mongot enabled, creates/updates the
  vector index; otherwise records `VectorIndexPending`.
- Propagates worker state into CR status (state, documentsEmbedded, lagSeconds, error).

### 4.2 Embedding worker (new binary + image)

Small Go program in `cmd/embedding-worker/`, shipped as a dedicated image (pattern:
PBM/PMM sidecar images). Responsibilities: initial backfill, change-stream tailing,
batching, calling `/v1/embeddings`, writing vectors back. Persists resume token and
backfill checkpoint in a system collection (`<db>.__pcsmdb_embedding_state`) to survive
restarts. Exposes Prometheus metrics and a health endpoint used by Deployment probes.

**Why a single-replica Deployment, not a sidecar:** change streams are cluster-scoped;
one tailing process per pipeline is correct, per-pod would duplicate work. v1 scaling
knobs are `batchSize` and worker resources; parallel change-stream partitioning is v2.

## 5. Data Flow

**Initial backfill** (`initialSync: true`): scan the collection in natural order in
batches of `batchSize`, skipping documents whose vector is already fresh (see
`sourceHash`). Checkpoint (last `_id`) after each batch. The change stream is opened
**before** the scan starts and events are buffered, so writes during backfill are not
lost.

**Steady state**: tail the change stream for inserts/updates/replaces. Re-embed on
update only when a configured source field actually changed. Deletes need no action.
Each write-back sets:

```
embedding: [...]                                   # the vector (target.vectorField)
__embedding_meta: {model, sourceHash, updatedAt}
```

`sourceHash` = hash of concatenated source fields + model name. It is the idempotency
key: prevents re-embedding unchanged text, makes backfill resumable, and detects stale
vectors after a model change. The worker ignores change events that only touch the
vector/meta fields, so its own writes do not loop.

## 6. Error Handling

- **Endpoint down / 5xx / timeout**: exponential backoff and retry; the change-stream
  position is not advanced past unprocessed documents. After 5 consecutive failures
  (default, configurable later if needed) the CR status goes to `error` with the
  message; recovery is automatic.
- **429 rate limiting**: backoff; `batchSize` is the throttle.
- **Dimension mismatch** (model output size != `target.dimensions`): hard fail with a
  clear status error — wrong-size vectors must never be written.
- **Resume token expired** (worker down longer than the oplog window): log, fall back to
  a full re-scan using `sourceHash` to skip current documents — expensive but correct.
- **Model changed in the CR**: existing `sourceHash` values become stale, naturally
  triggering a full re-embed through the same mechanism.

## 7. Security

- Dedicated MongoDB user per pipeline, least privilege (`readWrite` on the target
  collection + change-stream access), credentials in a Secret.
- vLLM API key from `credentialsSecret`, sent only as a Bearer header; custom CA /
  client certs via `tlsSecret`.
- Document content is sent to the inference endpoint by design; documentation must state
  the endpoint should be inside the same trust boundary.
- No document content in logs or CR status.

## 8. Observability

Worker Prometheus metrics: documents embedded (counter), embedding latency, vLLM request
errors, change-stream lag seconds, backfill progress. CR status mirrors the
human-readable subset. Kubernetes Events on state transitions (as backup CRs do).

## 9. Testing

- Unit tests: controller (envtest, existing style) and worker logic (hashing, batching,
  resume).
- Worker integration tests: real mongod + a mock OpenAI-compatible embeddings server
  (deterministic vectors, no GPU in CI).
- One e2e test in `e2e-tests/` (existing conventions): deploy cluster, apply Embedding
  CR against the stub server, insert/update/delete documents, assert vectors appear and
  stay in sync, kill the worker mid-stream and assert resume.

## 10. Out of Scope (v1)

- Deploying/managing vLLM or any inference infrastructure.
- Document chunking (one document = one vector).
- Multiple embedding models per collection (use multiple CRs/fields later).
- Sharded-cluster change-stream partitioning (works via mongos with a single tailer).
- Re-ranking.
- mongot/vector-search deployment itself (covered by the separate vector-search HLD).

## 11. Alternatives Considered

- **Inline `spec.embeddings` in the cluster CR** — rejected: bloats the cluster spec,
  couples app-level config to infrastructure lifecycle, awkward for multiple pipelines.
- **Standalone tool + docs only** — rejected: no reconciliation/status/index
  integration; weak product story.
- **Sidecar per mongod pod** — rejected: change streams are cluster-scoped; per-pod
  tailers duplicate work.
