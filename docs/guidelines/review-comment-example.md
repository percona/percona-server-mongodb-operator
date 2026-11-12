# Example: a review posted as a pull request comment

The format from step 5 of the review procedure in [`README.md`](README.md).
The pull request is invented: it adds `unhealthyPodEvictionPolicy` to the
PodDisruptionBudget settings and defaults it to `AlwaysAllow`. Everything
below the line is the comment body.

---

## 🔴 review-qa: needs work

| 🔴 Critical | 🟠 Major | 🔵 Minor | ⚪ Suggestion |
| :---------: | :------: | :------: | :-----------: |
| **1**       | **2**    | 2        | 1             |

> [!CAUTION]
> The new field reaches the PerconaServerMongoDB PDBs, but the ClusterSync
> PDB builder drops it, and the existing test passes because it never checks
> the field. The builder has no unit test, and the `crVersion` gate has no
> test on either side.

### Findings

<details open>
<summary>🔴 <b>1. Critical</b>: the ClusterSync PDB loses <code>unhealthyPodEvictionPolicy</code></summary>

**Where:** `pkg/psmdb/clustersync/pdb.go:31`

**What:** the builder copies only `minAvailable` and `maxUnavailable`. A
ClusterSync user who sets the new field gets a PDB without it and no error.

**Rule:** a test that passes while the operator is broken (`qa.md`, "What a
test must prove"). `TestPodDisruptionBudget` passes because it never checks
the policy.

**Proof:** a table case added to `pkg/psmdb/clustersync/deployment_test.go`
fails on the head commit:

```text
--- FAIL: TestPodDisruptionBudget/unhealthy_pod_eviction_policy_is_kept
    expected: "AlwaysAllow"
    actual  : <nil>
```

**Fix:** build both PDBs through one helper and add the case above.

```diff
 		Spec: policyv1.PodDisruptionBudgetSpec{
-			MinAvailable:   spec.MinAvailable,
-			MaxUnavailable: spec.MaxUnavailable,
-			Selector:       &metav1.LabelSelector{MatchLabels: labels},
+			MinAvailable:               spec.MinAvailable,
+			MaxUnavailable:             spec.MaxUnavailable,
+			UnhealthyPodEvictionPolicy: psmdb.UnhealthyPodEvictionPolicy(spec),
+			Selector:                   &metav1.LabelSelector{MatchLabels: labels},
 		},
```

</details>

<details open>
<summary>🟠 <b>2. Major</b>: the PDB builder has no unit test</summary>

**Where:** `pkg/psmdb/pdb.go:14`

**What:** no test in `pkg/psmdb` or in the controller tests renders a PDB, so
the default and an explicit `IfHealthyBudget` are both unchecked.

**Fix:** add a table-driven `pkg/psmdb/pdb_test.go`:

| Case              | Expected policy   |
| ----------------- | ----------------- |
| field unset       | `AlwaysAllow`     |
| `IfHealthyBudget` | `IfHealthyBudget` |
| `AlwaysAllow`     | `AlwaysAllow`     |

</details>

<details open>
<summary>🟠 <b>3. Major</b>: no test for the <code>crVersion</code> gate</summary>

**Where:** `pkg/psmdb/pdb.go:14`

**What:** the `AlwaysAllow` default applies to every `crVersion`. A cluster
on an older `crVersion` gets its PDBs rewritten on the first reconcile after
the operator upgrade.

**Rule:** `architecture.md`, "Compatibility".

**Fix:** once the default is gated, add test cases for the `crVersion` below
the gate and at the gate.

</details>

<details>
<summary>🔵 <b>4. Minor</b>: no test that an invalid value is rejected</summary>

An invalid value such as `alwaysAllow` should be rejected by the CRD. Add an
envtest case once the field has an enum marker.

</details>

<details>
<summary>🔵 <b>5. Minor</b>: no example in <code>deploy/cr.yaml</code></summary>

`deploy/cr.yaml` has no example of the new field under the
`podDisruptionBudget` blocks.

</details>

<details>
<summary>⚪ <b>6. Suggestion</b>: no e2e check of the rendered PDB</summary>

No e2e golden file contains a PDB, so nothing in the `pr` suite would catch a
change to the rendered policy. Say in the PR description that no drain test
is added, or add the PDB to an existing golden file.

</details>

### ✅ Positives

- All five PDBs of a PerconaServerMongoDB cluster go through one builder.
- The generated CRDs were regenerated.
- `make test` passes.

### Action items

- [ ] 🔴 Wire the field through the ClusterSync builder and add the failing
  test case.
- [ ] 🟠 Add `pkg/psmdb/pdb_test.go`.
- [ ] 🟠 Test both sides of the `crVersion` gate.
- [ ] 🔵 Add the invalid-value test and the `deploy/cr.yaml` example.
- [ ] ⚪ State the e2e gap in the PR description.

<sub>Verified by running <code>go test ./pkg/psmdb/clustersync/</code> with
the added case, in a worktree at the head of the branch. Nothing was run on a
cluster. Rules: <code>docs/guidelines/qa.md</code>.</sub>
