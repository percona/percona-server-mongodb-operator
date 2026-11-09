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
| D4-no-crversion-gate | review-architecture, review-go-style | `TZ` env on every mongod container without a `cr.CompareVersion` gate: a rolling restart of every existing cluster on operator upgrade | Major (Critical accepted) | architecture.md: compatibility, gate new behavior with `CompareVersion` | Major: CRD and bundles not regenerated; Minor: no `deploy/cr.yaml` example, no validation, no unit test |
| D5-leaked-resource | review-qa | a ClusterRoleBinding is created and never deleted | Major | qa.md: everything the test creates is cleaned up, cluster-scoped objects included | Major: the step tests Kubernetes RBAC, not the operator; Major: no wait for RBAC propagation |
| D6-go-style | review-go-style, review-architecture | `context.Background()` replaces `ctx` in `cl.List` | Major | go-style.md: pass `ctx` on, `context.Background()` does not appear in reconcile code | Minor: `fmt.Errorf("%w")` instead of `errors.Wrap`; Minor: error logged and returned. `review-architecture` has nothing to report |
| C1-behavior-test | review-qa | none: from a settled cluster the arbiter pod is deleted and its UID checked, a bounded poll waits for `ARBITER`, a generation bump proves the operator reconciled, data read back through the replica set | nothing above Minor | | Minor: the generation bump and the `observedGeneration` wait could be shared helpers in `lib/kubectl.py`; Minor: the PR says what the step leaves out; Suggestion: `check=False` in the rejoin poll; Suggestion: snapshot the data pods too |
| C2-error-wrapping | review-go-style, review-architecture | none: six `fmt.Errorf` wraps become `errors.Wrap` | nothing above Minor | | Minor: remaining `fmt.Errorf` lines outside the diff |
| C3-unit-test | review-qa, review-go-style | none: table test for two naming helpers | nothing above Minor | | |
| C4-tests-yaml-skip | review-qa | none: the `gke-arm64` skips repeat the `gke` entries, and both tests skip themselves off EKS (`e2e-tests/demand-backup-eks-credentials/run`) | nothing above Minor | | |
| C5-sleep-to-wait | review-qa | none: a sleep replaced by a bounded `kubectl wait` | nothing above Minor | | |

## Recorded cases

Real diffs that escaped review, kept in `recorded/`. Each table lists the
findings the review of that diff produced; the case passes when no Critical or
Major finding is missed.

### R2-secret-scan (review-qa)

The credentials scan of pull request #2695, added after that PR's QA review
and never reviewed before the PR was opened (commit `357ddd57d` against its
parent `e3ca99503`, on branch `K8SPSMDB-1901-olm-backup-restore`; the patch
applies only on `e3ca99503`): `e2e-tests/lib/secret_scan.py`, its unit tests,
a `make py-unit` target, a scan step at the end of the OLM backup and restore
test, README lines. Copilot reported findings 1 to 3 on the PR.

| # | Finding | Expected | Rule |
| - | ------- | -------- | ---- |
| 1 | Every read in the scan uses `kubectl_bin(..., check=False)` and treats `""` as "nothing there", and nothing asserts that a Secret value or a log was collected: a failed Secret read or an empty pod list makes the step pass with no findings | Critical (Major accepted) | qa.md: an empty read is unknown; an unasserted result is Critical when the step is the one under test |
| 2 | `secret_values` writes every line of a multi-line value to the same key, so one arbitrary line survives, and its base64 needle is of the stripped line, not the Secret's stored value: a leaked line of a service-account key is not found | Major | qa.md: a test fails when the operator is broken; logic is unit tested |
| 3 | The only scan runs after the physical restore, which recreates the replset pods: the pbm-agent and mongod logs of both logical operations and the physical backup are gone, and `--previous` does not reach a replaced pod | Major | qa.md: a test fails when the operator is broken |
| 4 | `make py-check` runs the new unit tests through `py-unit`, but `.github/workflows/e2e-py-check.yml` repeats the commands inline without them: the unit tests never run in CI | Major | qa.md: a test runs in CI |
| 5 | The scan step depends on `test_physical_restore`, so it is skipped when a backup or restore fails, where a credential in an error message is most likely | Major (Minor accepted) | qa.md: a test fails when the operator is broken |
| 6 | `mask` prints the first four characters of a real secret to CI output (the author asked for this format) | Minor | |
| 7 | `aws_secret_access_key`, `AccountKey=` and `"private_key":` match the key name without a value, so a log line naming the key is reported and invites a broad allowlist | Minor | qa.md: keep allowlists narrow |
| 8 | `test_limits` asserts constants against their own literals | Suggestion | qa.md: an assertion must be able to fail |
| 9 | `make py-unit` and unit tests next to `lib/` helpers are not mentioned in the AGENTS.md files | Suggestion | |

Known secondary finding: Minor, on a failure the harness attaches the raw
operator log and events to the report, so the module docstring's claim that
CI output cannot leak a matched value is wrong (Copilot; the scan adds no new
exposure).

First run (review-qa at `eef4657ad`, blind): findings 1 to 9, no noise.

### R1-olm-upgrade (review-qa)

The OLM upgrade e2e test of pull request #2662 as first reviewed (commit
`cf1d3224b` against its merge base): the pytest test and OLM helpers under
`installers/olm/e2e-tests/`, the runner `installers/olm/run-tests.sh`, a
`make tests` target, README and lint changes.

| # | Finding | Expected | Rule |
| - | ------- | -------- | ---- |
| 1 | `test_write_data` and `test_data_after_upgrade` read "every member" through `compare_mongo_cmd` with a pod host, which connects with `replicaSet=rs0`: every read hits the primary, so a secondary without the data passes | Critical (Major accepted) | qa.md: a read from one member uses a direct connection |
| 2 | `_psmdb_state` turns a failed `kubectl get` (`check=False`) into `""`, and both sampling loops drop the empty sample until the deadline: a CR that disappears during the upgrade and comes back still passes the state check | Major | qa.md: a loop that skips empty reads fails when they persist |
| 3 | `_assert_operator_clean` reads the restart count and log of whichever target pod exists now; without a pod UID snapshot, a pod replaced during the soak hides restarts and errors | Major | qa.md: a restart check reads the pod it snapshotted, the operator pod included |
| 4 | `allowed()` accepts `Unhealthy` events of every pod of the target Deployment for the whole soak, and the target pod's Ready condition is never checked | Major | qa.md: an allowlist never covers the object under test |
| 5 | `run-tests.sh` lists the starting CSVs before the CatalogSource is `READY`: right after `make deploy` it dies with "no starting CSVs" | Major | qa.md: a read from a dependency waits for it first |
| 6 | `run-tests.sh` excludes only the default channel head, while the test honors `OLM_TARGET_CHANNEL`: with an older target channel its head and newer CSVs become starting points and fail the `from < target` assertion | Major | qa.md: the runner honors every variable the test honors |
| 7 | `tests: $(BUNDLE_TYPES:%=tests/%)` in `installers/olm/Makefile`: `make -j tests` runs both suites at once, each deleting the same cluster-scoped CRDs | Major | qa.md: runs that share cluster-scoped objects never run in parallel |
| 8 | With `SKIP_DELETE=1` and several starting CSVs every run keeps its OLM install, and the next run's `_delete_crds` deletes the previous run's cluster | Major | qa.md: every run cleans up, a keep variable applies to the last run only |
| 9 | `test_upgrade_operator` switches the Subscription to another channel, which no user does, so the test does not take the path of a real OLM upgrade | Major | qa.md: a test takes the path a user takes |
| 10 | `run-tests.sh` checks `kubectl jq make uv` but not `oc`; `conftest.py` creates the namespace with `oc new-project` on OpenShift | Minor | qa.md: every tool the run needs is checked first |
| 11 | The README's manual `pytest` command keeps every resource: `conftest.py` defaults to `SKIP_DELETE=1`, and the README promises cleanup | Minor | qa.md: a documented command gives the documented result |
| 12 | `MYPYPATH=e2e-tests` in the root `Makefile` and the workflow has no comment | Minor | qa.md: a non-obvious variable has a comment |
| 13 | `run-tests.sh` has no `main()`, no timestamps and no per-run summary | Minor | qa.md: runner flow in `main()`, a log line per step, a summary |
| 14 | A three-member replica set with sharding, backups and PMM disabled and no CR upgrade, and nothing says what the test leaves out | Minor | qa.md: a new test's PR says what the test leaves out |

Known secondary findings: Major, `_delete_crds` removes the operator CRDs and
every `psmdb` resource in the cluster before the test, on whatever cluster the
context points at (kept by the author: OLM leaves CRDs behind, the shared
harness does the same with `DELETE_CRD_ON_START=1`, and the README says to use
a dedicated cluster); Minor, the `test_paths` change in `e2e-tests/conftest.py`
is unnecessary because the test can override the fixture, and it also moves
`src_dir` for the bash wrapper; Minor, the test is not in `tests.yaml`, with
the reason in the README; Major or Minor, the reconcile probe does not assert
the starting generation; Major, terminal states do not stop the waits (a
cluster `error` during the soak is only collected, a Subscription
`ResolutionFailed` shows only in the timeout message); Minor, the final
`ready` assert fails on an empty read; Minor, `olm_available` turns a failed
call into a skip; Minor, `CATALOG_BUNDLE_LIMIT=0` passes the check but the
`OLD_VERSIONS` loops fail under bash 3.2; Minor, the limit path keeps
OperatorHub versions newer than the one built; Minor, copied CSVs break
`csv_names == [target]`; Minor, the `AppliedWithWarnings` parsing is loose;
Minor, the new `VERSION` default makes every feature branch push the same dev
bundle tags; Minor, no positive control for the log scan; Suggestion, the CSV
warning allowlist shares one comment.

Not expected from a static review: the target operator pod fails its readiness
probe once before it listens (the probe has no `initialDelaySeconds`). Found
by running the test.
