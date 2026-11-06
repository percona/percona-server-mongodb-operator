# QA

Last verified: 2026-11-04

Rules for test code wherever it lives: e2e tests, golden files, CI test
selection, the scripts and `make` targets that run tests, and Go tests.
`e2e-tests/AGENTS.md` has the rules for writing e2e tests, and they apply to
e2e tests outside `e2e-tests/` too; this file adds what a review checks and
the rules for Go tests and test runners. The review skill `review-qa` applies
both.

## What a test must prove

- A test fails when the operator is broken and passes on a healthy cluster.
  Check every assertion against both.
- Logic and rendering are unit tested (`pkg/**/*_test.go`, envtest for
  reconcile behaviour). An e2e step that only checks rendering is a finding:
  move it to a unit test.
- After a restore, failover or upgrade the test reads data back. Pod status
  alone proves nothing.
- A "nothing changed" claim has a before and after snapshot: pod UIDs,
  container `restartCount`, StatefulSet revisions. A restart check reads the
  pod it snapshotted, the operator pod included: a replaced pod has a fresh
  `restartCount` and log.
- A new reconcile branch or CR field has a test for the error path too.
- A negative check (an object must not exist, a line must not appear) has a
  positive control: the same check finds the object or line when it is
  present.
- A test takes the path a user takes. For an OLM upgrade that is the channel
  the release ships in, a Subscription as a user creates it, and the starting
  CR from the old bundle's `alm-examples`. A step no user performs, such as a
  channel switch, proves nothing about the release.

## e2e review checklist

Each item is a Major finding when missing, unless marked otherwise.

- Every wait has a ceiling and fails with the expected and the last seen
  value. An unbounded loop is Critical: it hangs until the Jenkins timeout.
- Terminal states (`error`, `Failed`) stop a wait early.
- No sleeps for readiness. A sleep that encodes real time has a comment.
- A `kubectl_bin(..., check=False)` result is asserted or retried; an empty
  read is unknown, not a state. A loop that skips empty reads, whether it
  waits for a value or samples the cluster state during an operation, fails
  when they persist (the object is gone or the API is down): a ceiling on
  consecutive empty reads, not a skip until the deadline. An unasserted
  result is Critical when the step is the one under test.
- Namespaces come from the harness; no hardcoded namespace or cluster names.
- Everything the test creates is cleaned up, cluster-scoped objects included.
- Allowlists for log lines and event reasons have one specific entry each,
  with a comment, and never cover the object under test: an allowance for the
  pods being replaced excludes the new pod, whose readiness is asserted.
- The version under test is asserted before an upgrade or image check.
- A step that changes a CR field asserts the starting value first. A patch
  that sets the value the fixture already has is a no-op and proves nothing.
- A read from one replica set member uses a direct connection
  (`directConnection=true`). `MongoManager.run_mongosh` and
  `compare_mongo_cmd` connect with `replicaSet=` and read the primary every
  time, so a per-member check through them passes with a broken secondary.
  Critical when the per-member check is the step under test.
- A new test's PR says what the test leaves out (a sharded cluster, a CR
  upgrade, backups), so the gap is a decision. Minor.
- Bash: `set -o errexit` kept, variables quoted, helpers from `functions`
  reused instead of copied.
- pytest: dependent steps chained with `pytest.mark.dependency`,
  cluster-creating fixtures `class`-scoped, `logging.getLogger(__name__)`,
  typed helpers, no swallowed exceptions on state-changing steps,
  `make py-fmt` and `make py-check` clean.
- A pytest port keeps parity with the bash test it replaces: same steps,
  same compares, same waits. A dropped step is Critical when it was a check,
  Major otherwise.

## Test runners

The scripts and `make` targets that run tests: `make e2e-test` in the root
`Makefile`, and any script that wraps `pytest`. Each item is a Major finding
when missing, unless marked otherwise.

- Every tool the run needs is checked before the first step: `kubectl`, `oc`
  on OpenShift (`conftest.py` creates the namespace with `oc new-project`),
  `jq`, `uv`. Minor.
- A read from a dependency waits for it first, with a ceiling: a
  CatalogSource is `READY` before its PackageManifests are listed, a
  Deployment is available before its pod is read.
- The runner honours every variable the test honours (`SKIP_DELETE`,
  `IMAGE_*`, the OLM channel and CSV variables) and resolves it the same way.
  Otherwise the runner and the test run different cases.
- Runs that share cluster-scoped objects (CRDs, the current kubectl
  namespace) never run in parallel. A `make` target that runs several suites
  runs them in its recipe, not as prerequisites that `make -j` runs at once.
- When a runner loops over several runs, every run cleans up; a variable that
  keeps resources applies to the last run only.
- The flow is in `main()`, which calls small functions, as in
  `installers/olm/build-bundle.sh`. Each step logs a line with a timestamp,
  and the run ends with a summary: result, duration and report path per run.
  Minor.
- A variable or path that is not obvious (`MYPYPATH`, `PYTHONPATH`) has a
  comment saying why. Minor.
- A documented command gives the documented result with the harness
  defaults: `conftest.py` sets `SKIP_DELETE=1`, so a documented `pytest`
  command that promises cleanup sets `SKIP_DELETE=0`. Minor.

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
