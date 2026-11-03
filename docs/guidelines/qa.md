# QA

Last verified: 2026-11-03

Rules for test code: e2e tests under `e2e-tests/`, golden files, CI test
selection and Go tests. `e2e-tests/AGENTS.md` has the rules for writing e2e
tests; this file adds what a review checks and the rules for Go tests. The
review skill `review-qa` applies both.

## What a test must prove

- A test fails when the operator is broken and passes on a healthy cluster.
  Check every assertion against both.
- Logic and rendering are unit tested (`pkg/**/*_test.go`, envtest for
  reconcile behaviour). An e2e step that only checks rendering is a finding:
  move it to a unit test.
- After a restore, failover or upgrade the test reads data back. Pod status
  alone proves nothing.
- A "nothing changed" claim has a before and after snapshot: pod UIDs,
  container `restartCount`, StatefulSet revisions.
- A new reconcile branch or CR field has a test for the error path too.
- A negative check (an object must not exist, a line must not appear) has a
  positive control: the same check finds the object or line when it is
  present.

## e2e review checklist

Each item is a Major finding when missing, unless marked otherwise.

- Every wait has a ceiling and fails with the expected and the last seen
  value. An unbounded loop is Critical: it hangs until the Jenkins timeout.
- Terminal states (`error`, `Failed`) stop a wait early.
- No sleeps for readiness. A sleep that encodes real time has a comment.
- A `kubectl_bin(..., check=False)` result is asserted or retried; an empty
  read is unknown, not a state. An unasserted result is Critical when the
  step is the one under test.
- Namespaces come from the harness; no hardcoded namespace or cluster names.
- Everything the test creates is cleaned up, cluster-scoped objects included.
- Allowlists for log lines and event reasons have one specific entry each,
  with a comment.
- The version under test is asserted before an upgrade or image check.
- A step that changes a CR field asserts the starting value first. A patch
  that sets the value the fixture already has is a no-op and proves nothing.
- Bash: `set -o errexit` kept, variables quoted, helpers from `functions`
  reused instead of copied.
- pytest: dependent steps chained with `pytest.mark.dependency`,
  cluster-creating fixtures `class`-scoped, `logging.getLogger(__name__)`,
  typed helpers, no swallowed exceptions on state-changing steps,
  `make py-fmt` and `make py-check` clean.
- A pytest port keeps parity with the bash test it replaces: same steps,
  same compares, same waits. A dropped step is Critical when it was a check,
  Major otherwise.

## Golden files (`e2e-tests/<name>/compare/`)

- The compare filter removes `image` fields before comparing
  (`compare_kubectl` in `e2e-tests/functions`); do not flag them.
- Volatile fields (uid, resourceVersion, timestamps, generated passwords or
  hashes) in a golden file fail every run.
- A new Kubernetes object created by the change needs a golden file and a
  compare call; otherwise it is a coverage gap.
- Variants (`-oc` for OpenShift, and others) change together with the shared
  shape. The PR checklist asks for the `-oc` files.

## CI selection (`e2e-tests/tests.yaml`)

- A test runs in CI only when `tests.yaml` lists it. The file has three
  sections: `platformSkips`, `mongoVersionSkips` and `tests` (suites `pr`,
  `release`, `distro`, `minikube`, `backups`, plus groups and operator
  modes). `uv run e2e-tests/select_tests.py list --suite pr` shows the PR
  suite.
- A platform or MongoDB version skip goes into `tests.yaml` with a reason,
  not into the test.
- A test left out of CI needs a reason in the PR (special infrastructure).
- A full e2e run costs about 30 hours of cluster time. An e2e test for what
  envtest or a unit test can cover is a finding.

## Go tests (`pkg/**/*_test.go`)

- Every error is checked: no `_ =` on calls that can fail, `require.NoError`
  for setup, `assert` for the assertion under test. In the Ginkgo suites,
  `Expect(err).NotTo(HaveOccurred())`.
- Table-driven with `t.Run`, one case per branch, including the error path.
- `t.Parallel()` only when the cases share no fake client or package state.
- envtest specs: `Eventually` and `Consistently` with an explicit timeout and
  interval sized for a reconcile; objects deleted in `AfterEach` or
  `DeferCleanup`; no shared state between `It` blocks without `Ordered`.
- Files in `testdata/` are expected outputs. A change there is intentional
  and explained in the PR.
- `go-style.md` lists the test frameworks and helpers in use.

## Credentials

- Only the fixtures under `e2e-tests/conf/` may appear in tests;
  `conf/cloud-secret.yml` is gitignored. Any other secret in a diff is
  Critical.
