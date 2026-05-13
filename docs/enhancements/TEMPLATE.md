# [TICKET-ID]: [Feature Title]

| Field        | Value                                    |
|--------------|------------------------------------------|
| Author       | [name]                                   |
| Status       | Draft / In Review / Approved / Completed |
| Created      | [date]                                   |
| Last Updated | [date]                                   |
| Reviewers    | [names]                                  |

<!--
TEMPLATE USAGE NOTES (delete this block before publishing):

Purpose:
  Design document template for the Percona Operator for MySQL.
  Captures design and architecture decisions.

  The source code is always the single source of truth for "what exists."
  This document answers "why it exists" and "what alternatives were rejected."

Guidance for authors:
  - Sections marked [REQUIRED] must be filled. [OPTIONAL] sections can be removed.
  - Replace all bracketed placeholders ([...]) with real content.
  - Focus on decisions, constraints, and rationale — not file paths or code.
  - When referencing implementation details, describe the pattern or approach,
    not the exact location. The code will move; the reasoning should not.
  - AI agents reading this document should use it to understand intent and
    constraints, then consult the codebase for current implementation details.
-->

---

## 1. Overview [REQUIRED]

2-3 sentences: what this feature does, why it matters, and who benefits.

### 1.1 Goals

- Goal 1
- Goal 2
- Goal 3

### 1.2 Non-Goals (Out of Scope)

Equally important as goals. Prevents scope creep and tells implementors
(human or AI) what NOT to build. Include brief reasoning or when it might
be revisited.

- Non-goal 1 — reason / when it might be revisited
- Non-goal 2

---

## 2. Background [REQUIRED]

Technical context a developer (or AI agent) needs before reading the design.
Cover: how the underlying MySQL / Kubernetes / operator concept works,
relevant protocols, and hard constraints.

### 2.1 Core Concepts

Define terms, MySQL internals, Kubernetes patterns, or domain knowledge
that the rest of the document depends on. Examples:
  - Group Replication vs Async Replication behavior
  - How XtraBackup streaming works
  - How VolumeSnapshots work
  - How incremental backups work in PXB
  - cert-manager flow
  - Kubernetes finalizer semantics
If a concept is well-known in the team, a one-liner is fine. If it's
niche, explain it fully.

### 2.2 Key Constraints

Hard technical constraints that shape the design. Number them so later
sections can reference them (e.g., "due to Constraint 2").
Common constraint categories for this operator:
  - MySQL limitations (replication protocol, InnoDB requirements)
  - PXB (Percona Xtrabackup) limitations
  - Kubernetes limitations (StatefulSet ordering, PVC lifecycle)
  - Backward compatibility with existing CRs in the wild
  - Percona Server version support matrix

1. **Constraint name**: Explanation and why it matters.
2. **Constraint name**: Explanation and why it matters.

---

## 3. Architecture [REQUIRED]

Describe the architecture at a component level. Focus on how the feature
fits into the operator's reconciliation model and which components are
involved.

Components in this operator:
  - Main reconciler (PerconaServerMySQL controller)
  - Backup/Restore reconcilers
  - MySQL instances (StatefulSet pods)
  - Orchestrator (async replication topology)
  - HAProxy / MySQL Router (proxy layer)
  - PMM agent (monitoring sidecar)
  - XtraBackup sidecar (backup/restore)
  - Binary log server (PiTR)
  - Bootstrap init container
  - cert-manager (TLS)
  - Cloud storage (S3, GCS, Azure)

### 3.1 Architecture Before This Change

How the relevant part of the system works before this feature.
Use ASCII diagrams or numbered steps. Name the components and
boundaries involved.

```
Trigger (e.g., CR update, scheduled backup, failover event)
  → Reconciler
    → Component A
      → External System (MySQL, Orchestrator, cloud storage, etc.)
    ← Response
  ← Status update
```

### 3.2 Architecture After This Change

How the system looks with the new feature in place.
Highlight what is new or changed relative to Section 3.1.

### 3.3 Key Observations

What about the current architecture enables, constrains, or complicates
the new feature? number these — they justify design decisions in section 5.

1. **Observation**: Implication for the design.
2. **Observation**: Implication for the design.

---

## 4. CRD and Interface Changes [REQUIRED]

Describe new or changed interfaces. Focus on semantics, defaults,
validation rules, and interactions with existing fields.

For this operator, interfaces typically include:
  - PerconaServerMySQL spec/status fields
  - PerconaServerMySQLBackup spec/status fields
  - PerconaServerMySQLRestore spec/status fields
  - New status conditions
  - Labels / annotations with operator-specific meaning
  - ConfigMap or Secret contracts between operator and sidecars

### 4.1 CRD Spec Changes

For each new or changed field, document:
  1. What it controls and why it exists
  2. Which CRD it belongs to (PerconaServerMySQL, Backup, or Restore)
  3. Where it lives in the spec hierarchy (e.g., spec.mysql.X, spec.proxy.haproxy.X)
  4. Default value and behavior when omitted
  5. Validation rules (CEL or webhook)
  6. Interaction with existing fields (e.g., only valid when clusterType is "async")
  7. Interaction with unsafeFlags (if bypassing safety checks)

- **`spec.path.to.newField`** *(optional, default: `"value"`)*:
  Description of what this field controls. Must be one of `[a, b]`.
  When omitted, the system behaves as [describe]. Only applies when
  [describe conditions, e.g., clusterType is group-replication].

### 4.2 CRD Status Changes [OPTIONAL]

New status fields or conditions. Describe what state they represent,
when they transition, and what a user should infer from them.

### 4.3 Internal Contracts [OPTIONAL]

New or changed contracts between the operator and its sidecars,
init containers, or external components. Examples:
  - Environment variables passed to the MySQL container
  - ConfigMap keys the bootstrap init container reads
  - Annotations the orchestrator handler reacts to
  - Secret keys expected by the XtraBackup sidecar
Describe the agreement, not the exact code.

### 4.4 User-Facing Behavior Changes [OPTIONAL]

Changes to kubectl output, status conditions, events emitted,
log messages, or any other output a user directly observes.

---

## 5. Design Decisions and Alternatives [REQUIRED]

Code shows what was chosen; only this document explains what was
considered and why alternatives were rejected. This prevents future
developers (and AI agents) from re-proposing rejected approaches.

Common decision categories for this operator:
  - Reconciler placement: main controller vs. dedicated controller
  - Data flow: sidecar vs. init container vs. operator-driven exec
  - State management: CR status vs. ConfigMap vs. annotation
  - Replication model: behavior differences between GR and async
  - Proxy layer: HAProxy vs. Router implications
  - Storage: PVC lifecycle, cloud storage choices
  - Upgrade path: how existing clusters adopt the new feature

### 5.1 [Decision Topic]

**Chosen approach:** Brief description.

**Why:** Reasoning — link back to constraints or observations from earlier
sections where relevant (e.g., "due to Constraint 2 in Section 2.3").

**Alternatives considered:**

| Alternative | Why Rejected |
|------------|--------------|
| Alternative A | Reason — be specific about the trade-off |
| Alternative B | Reason |

Repeat 5.N subsections for each significant decision.

---

## 6. Replication Model Impact [OPTIONAL]

Some features behave differently (or don't apply)
depending on the replication model. Explicitly state the behavior
for each model to prevent ambiguity.

If the feature is entirely replication-agnostic, state that and
explain why, then remove the subsections.

### 6.1 Group Replication Behavior

How the feature works with clusterType: group-replication.

### 6.2 Async Replication Behavior

How the feature works with clusterType: async.

### 6.3 Differences and Why

Summarize the differences between 6.1 and 6.2 and explain
the technical reason for each difference.

---

## 7. User Experience [REQUIRED]

Concrete YAML examples for every user-facing workflow. These illustrate
the intended UX and double as acceptance criteria.

Include at least:
  1. The existing CR (unchanged — proves backward compatibility)
  2. Each new workflow the feature introduces

Show only the relevant portion of the CR, not the entire spec.

### 7.1 Existing CR (Unchanged)

```yaml
# Show that existing CRs continue to work identically.
# Only include the relevant spec section.
apiVersion: ps.percona.com/v1
kind: PerconaServerMySQL
metadata:
  name: cluster1
spec:
  # ... relevant section unchanged ...
```

### 7.2 [New Workflow]

```yaml
# Show the new capability end-to-end.
# Add inline comments explaining operator behavior.
apiVersion: ps.percona.com/v1
kind: PerconaServerMySQL
metadata:
  name: cluster1
spec:
  # ... new or changed fields with comments ...
```

---

## 8. Error Handling and Edge Cases [REQUIRED]

One subsection per scenario. Each must specify:
  - Trigger condition (when does this happen?)
  - Expected operator behavior (what should the reconciler do?)
  - User-visible feedback (status condition, event, log message)

Examples:
  - Pod not ready during reconciliation
  - MySQL unreachable (network partition, crash loop)
  - Cloud storage credentials invalid or expired
  - Partial failure during rolling update
  - Upgrade from older operator version missing new fields
  - PVC resize or storage class mismatch

### 8.1 [Scenario Name]

**Scenario:** Description of the trigger condition.

**Expected behavior:**
- What the reconciler should do, step by step.
- Status condition or event emitted.
- Whether to requeue and with what delay.

### 8.2 [Scenario Name]

**Scenario:** Description.

**Constraint:** Rule that prevents this scenario, enforced at [where: CEL
validation / webhook / reconciler logic].

**Rationale:** Why this constraint exists.

---

## 9. Migration and Backward Compatibility [REQUIRED]

How do existing clusters adopt this feature? This is critical —
there are clusters running older CR versions.

### 9.1 Existing Clusters

- How existing CRs (without the new fields) behave after operator upgrade.
- Whether defaults preserve current behavior (they must, unless there's a
  strong reason documented in Section 5).

### 9.2 CRD Compatibility

- Are changes additive (new optional fields) or breaking?
- Impact on `make generate` / CRD manifests.

### 9.3 Operator Version Skew [OPTIONAL]

What happens if the operator is upgraded but the CR is not updated?
What happens during rolling restart when old and new pods coexist?

---

## 10. Testing Strategy [REQUIRED]

Describe WHAT to test and WHY, not exact test file paths or function names.
The test code is the source of truth for how tests are implemented.

### 10.1 E2E Test Scenarios

Describe scenarios that need KUTTL e2e coverage. Include which
cluster type(s) each scenario applies to (GR, async, or both).

| Scenario | Cluster Type | What It Validates |
|----------|-------------|-------------------|
| Scenario 1 | GR + Async | Setup, action, and expected outcome |
| Scenario 2 | GR only | Setup, action, and expected outcome |
| Scenario 3 | Async only | Setup, action, and expected outcome |

---

## 11. Open Questions [REQUIRED]

Resolve these BEFORE implementation begins. Do not delete resolved
questions — mark them as resolved so future readers see the outcome.

1. **[Topic]:** Question description.
   - *Option A:* Pros/cons.
   - *Option B:* Pros/cons.
   - *Recommendation:* A, because [reason].
   - *Resolution:* [Decided X on \<date\>. Reason.]

2. **[Topic]:** Question description.

---

## Appendix [OPTIONAL]

### A. Glossary

| Term | Definition |
|------|------------|
| GR | Group Replication — MySQL's multi-primary replication protocol |
| Async | Asynchronous replication — traditional primary-replica replication |
| PiTR | Point-in-Time Recovery via binary log replay |
| PMM | Percona Monitoring and Management |

### B. References

- [Description](URL)
- [Description](URL)
