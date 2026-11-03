# Expected results

Reviewers must not read this file. The person running the set scores each
review against it. Severities are the ones in `docs/guidelines/README.md`:
Critical, Major, Minor, Suggestion.

A known secondary finding is a correct, rule-based finding the case also
contains. It does not count as noise.

| Case | Skills | Planted defect | Expected | Rule | Known secondary findings |
| ---- | ------ | -------------- | -------- | ---- | ------------------------ |
| D1-unbounded-wait | review-qa | `while True` poll with no ceiling in `test_cluster_back_to_ready` | Critical | qa.md: an unbounded loop is Critical | Major: `check=False` read treated as a state; Major: `error` state does not stop the wait; Major: literal CR name |
| D2-readiness-sleep | review-qa | `time.sleep(300)` instead of waiting for the two new pods | Major | qa.md: no sleeps for readiness | Major: starting size not asserted; Major: member count only, no data read back; Minor: literal CR name |
| D3-assertion-cannot-fail | review-qa | the PVC listing is logged, never asserted, and it is the only check | Critical | qa.md: an unasserted result is Critical when the step is the one under test | Major: no positive control; Major: `check=False` read not asserted; Minor: rendering belongs in a unit test |
| D4-no-crversion-gate | review-architecture, review-go-style | `TZ` env on every mongod container without a `cr.CompareVersion` gate: a rolling restart of every existing cluster on operator upgrade | Major (Critical accepted) | architecture.md: compatibility, gate new behaviour with `CompareVersion` | Major: CRD and bundles not regenerated; Minor: no `deploy/cr.yaml` example, no validation, no unit test |
| D5-leaked-resource | review-qa | a ClusterRoleBinding is created and never deleted | Major | qa.md: everything the test creates is cleaned up, cluster-scoped objects included | Major: the step tests Kubernetes RBAC, not the operator; Major: no wait for RBAC propagation |
| D6-go-style | review-go-style, review-architecture | `context.Background()` replaces `ctx` in `cl.List` | Major | go-style.md: pass `ctx` on, `context.Background()` does not appear in reconcile code | Minor: `fmt.Errorf("%w")` instead of `errors.Wrap`; Minor: error logged and returned. `review-architecture` has nothing to report |
| C1-behaviour-test | review-qa | none: arbiter pod deleted, bounded poll for `ARBITER`, data read back | nothing above Minor | | Suggestion: the poll could be a shared helper |
| C2-error-wrapping | review-go-style, review-architecture | none: six `fmt.Errorf` wraps become `errors.Wrap` | nothing above Minor | | Minor: remaining `fmt.Errorf` lines outside the diff |
| C3-unit-test | review-qa, review-go-style | none: table test for two naming helpers | nothing above Minor | | |
| C4-tests-yaml-skip | review-qa | none: the `gke-arm64` skips repeat the `gke` entries, and both tests skip themselves off EKS (`e2e-tests/demand-backup-eks-credentials/run`) | nothing above Minor | | |
| C5-sleep-to-wait | review-qa | none: a sleep replaced by a bounded `kubectl wait` | nothing above Minor | | |
