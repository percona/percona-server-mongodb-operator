# Example: a review posted as a pull request comment

The format from step 5 of the review procedure in [`README.md`](README.md).
The pull request is invented: it adds `unhealthyPodEvictionPolicy` to the
PodDisruptionBudget settings and defaults it to `AlwaysAllow`. Everything
below the line is the comment body.

---

**review-qa: needs work** (1 Critical, 2 Major, 2 Minor, 1 Suggestion)

The new field reaches the PerconaServerMongoDB PDBs, but the ClusterSync PDB
builder drops it, and the existing test passes because it never checks the
field. The builder has no unit test, and the `crVersion` gate has no test on
either side.

1. **Critical.** `pkg/psmdb/clustersync/pdb.go:31`
   copies only `minAvailable` and `maxUnavailable`, so a ClusterSync user who
   sets `unhealthyPodEvictionPolicy` gets a PDB without it and no error.
   `TestPodDisruptionBudget` passes while the field is lost: a test that
   passes while the operator is broken (`qa.md`, "What a test must prove").
   Proof: a table case added to `pkg/psmdb/clustersync/deployment_test.go`
   fails on the head commit with `expected AlwaysAllow, actual nil`.
   Fix: build both PDBs through one helper and add this case.
2. **Major.** `pkg/psmdb/pdb.go:14` has no unit
   test. No test in `pkg/psmdb` or in the controller tests renders a PDB, so
   the default and an explicit `IfHealthyBudget` are both unchecked.
   Fix: add a table-driven `pkg/psmdb/pdb_test.go` for the default, each
   allowed value and an unset spec.
3. **Major.** The `AlwaysAllow` default applies to every `crVersion`, and no
   test covers the gate. A cluster on an older `crVersion` gets its PDBs
   rewritten on the first reconcile after the operator upgrade (`architecture.md`,
   "Compatibility").
   Fix: once the default is gated, add cases for the `crVersion` below and at
   the gate.

<details>
<summary>2 Minor, 1 Suggestion</summary>

4. **Minor.** No negative test: an invalid value such as `alwaysAllow`
   should be rejected by the CRD. Add an envtest case once the field has an
   enum marker.
5. **Minor.** `deploy/cr.yaml` has no example of the
   new field under the `podDisruptionBudget` blocks.
6. **Suggestion.** No e2e golden file contains a PDB, so nothing in the `pr`
   suite would catch a change to the rendered policy. Say in the PR
   description that no drain test is added, or add the PDB to an existing
   golden file.

</details>

**Positives.** All five PDBs of a PerconaServerMongoDB cluster go through one
builder, the generated CRDs were regenerated, and `make test` passes.

**Action items**
- [ ] Critical: wire the field through the ClusterSync builder and add the
  failing test case.
- [ ] Major: add `pkg/psmdb/pdb_test.go`.
- [ ] Major: test both sides of the `crVersion` gate.
- [ ] Minor: add the invalid-value test and the `deploy/cr.yaml` example.
- [ ] Suggestion: state the e2e gap in the PR description.

Verified by running: `go test ./pkg/psmdb/clustersync/` with the added case,
in a worktree at the head of the branch. Nothing was run on a cluster.
