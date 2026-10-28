# End-to-end tests: notes for coding agents

Read the root [`AGENTS.md`](../AGENTS.md) first. These rules cover
`e2e-tests/`. Setup and tool installation are in [`README.md`](README.md).

## Layout

- One directory per test: `e2e-tests/<name>/`. A test is either a bash script
  `run` (shared helpers in `e2e-tests/functions`) or a pytest file
  `test_<name>.py` (fixtures in `conftest.py`, helpers in `lib/`). The suite
  is being ported from bash to pytest; follow the existing pytest tests.
- `make e2e-test TEST=<name>` runs one test. It prefers `test_*.py` and falls
  back to the bash `run` through `test_pytest_wrapper.py`.
- Expected objects live in `<name>/compare/` (`*.yml` Kubernetes objects,
  `*.json` query results). OpenShift variants end in `-oc`; when a shared
  shape changes, update every variant.
- CI picks tests from `tests.yaml` (suites `pr`, `release`, `distro`,
  `minikube`, `backups`, plus platform and MongoDB version skips), listed with
  `uv run e2e-tests/select_tests.py list --suite pr`. A test directory that is
  not in `tests.yaml` never runs in CI. Leaving it out is right only for a test
  that needs special infrastructure; say so in the PR.

## Running locally

- Tests create nothing but namespaces: they use the current kubectl context.
  Check `kubectl config current-context` before every run.
- Harness defaults (`conftest.py`): `SKIP_DELETE=1` keeps namespaces after a
  run, and `DELETE_CRD_ON_START=1` deletes the operator CRDs before one, which
  removes every PerconaServerMongoDB in the cluster. Use a dedicated cluster.
- Images default to `perconalab/...:<branch>` and `main-*`; override with
  `IMAGE`, `IMAGE_MONGOD` and the other `IMAGE_*` variables.
- Cloud backup tests need `conf/cloud-secret.yml` (gitignored). Without it,
  `SKIP_BACKUPS_TO_AWS_GCP_AZURE` is set and those steps are skipped.
- OpenShift on macOS: right after an install, the system resolver can cache
  "no such host" for the new API name, while `dig` resolves it. Go tools
  (kubectl, oc, openshift-install) skip that cache with `GODEBUG=netdns=go`.

## Rules for test code

A test is useful only if it fails when the operator is broken. Review every
check against that.

- **Every wait has a ceiling** and fails with what it expected and the last
  value it saw. No unbounded `until` loops.
- **No sleeps for readiness.** Poll the condition. Sleeps that encode real
  time (a soak, a PITR window, a known upstream bug workaround) stay, with a
  comment saying why.
- **Assertions must be able to fail.** A `kubectl_bin(..., check=False)` or
  `grep` result that nothing asserts is not a check.
- **Check data, not only status.** After a restore, failover or upgrade,
  read the data back (`compare_mongo_cmd`, `simple_data_check`), do not stop
  at pods being `Running`.
- **"Nothing changed" needs a before/after snapshot.** To prove no restart,
  compare pod UIDs, container `restartCount` and StatefulSet
  `currentRevision`/`updateRevision`, not just the current state.
- **Prove the controller acted.** An unchanged cluster also looks healthy
  when the operator never reconciled. Bump the CR generation with a field that
  does not touch the pod template and wait for `status.observedGeneration`.
- **Keep allowlists narrow.** Allowed log lines or event reasons get one
  entry each, matched as specifically as possible, with a comment on why they
  are expected.
- **Clean up what you create**, cluster-scoped objects included (CRDs,
  ClusterRoles, webhooks), and use the unique namespace the harness creates.
- **Bash:** keep `set -o errexit`, quote variables, and reuse helpers from
  `functions` instead of copying them.
- **pytest:** chain dependent steps with `pytest.mark.dependency`, give
  cluster-creating fixtures `class` scope, log with
  `logging.getLogger(__name__)`, type every helper, and never swallow errors
  of a step that changes state. `make py-fmt` and `make py-check` must pass.

## OLM and OpenShift tests

Lessons from the OLM bundle upgrade test (K8SPSMDB-1753):

- Use fully qualified OLM resource names (`subscriptions.operators.coreos.com`,
  `installplans.operators.coreos.com`, ...). On OpenShift, `subscription` can
  resolve to Knative's Subscription.
- Select a PackageManifest by its catalog label (`-l catalog=<source>`): the
  same package name is served by several catalogs.
- With `installPlanApproval: Manual`, OLM prepares the next upgrade as soon as
  a version is installed. After a channel switch the Subscription can still
  point at that stale pending plan, or at a plan or CSV that was already
  deleted. Delete pending plans you do not want, and read referenced plans and
  CSVs as "may be gone".
- Approve only the plan you intend to test. Our bundles skip every older
  version (`olm.skipRange` in community bundles, `spec.skips` in certified
  ones), so an upgrade to the newest version is one hop.
- OLM does not delete CRDs when a CSV is removed, and a newer CRD blocks
  installing an older bundle. Delete the CRDs before and after such a test.
- Applying our CRD makes an InstallPlan emit `AppliedWithWarnings` for the
  deprecated `psmdb.percona.com/v1-x-0` versions. Allow that event only when
  every warning in it is a deprecation notice.
- Build the starting CR from the old bundle's `alm-examples`, so its
  `crVersion` and images match the version being upgraded from.
