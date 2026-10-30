# percona-server-mongodb-operator - working notes for Claude

Kubernetes operator (Go, controller-runtime) for Percona Server for
MongoDB. This file focuses on the automation testing part.

## Test layers

| Layer | Where | Runs against | Framework |
| ----- | ----- | ------------ | --------- |
| Unit / controller | `pkg/**/*_test.go` (69 files) | envtest local API server | Ginkgo v2 + Gomega |
| E2E (CI reality) | `e2e-tests/<name>/run` | real k8s cluster (GKE in CI) | bash + `e2e-tests/functions` |
| E2E pytest port | branch `pr-2058` (`pytest-complete`) | real k8s cluster | pytest, run with `uv run pytest` |

## E2E suite layout

- One directory per test: `e2e-tests/<test-name>/run` (bash, executable).
- Shared library: `e2e-tests/functions` - all helpers (`wait_for_running`,
  `wait_backup`, `wait_restore`, `wait_for_pbm_operations`, `retry`,
  `kubectl_bin`, `compare_kubectl`, ...). `kubectl_bin` retries 3x, so
  never pipe stdin through it (use plain `kubectl` for `-f -`).
- Golden files: `e2e-tests/<test>/compare/*.yml` - expected k8s objects.
  The compare filter deletes all `image` fields (image names do not
  matter) but keeps `imagePullPolicy: Always` (it is asserted).
- PR test list: `e2e-tests/run-pr.csv` (109 tests). Jenkins spreads it
  over 15 GKE clusters; workers pull tests in file order, 90 min timeout
  per test, 4h for the stage. Per-test timings are posted as a PR
  comment table and to a public S3 bucket
  (percona-jenkins-artifactory-public/cloud-psmdb-operator).

## Conventions (enforced by recent work, keep them)

- No blind `sleep N` for readiness. Poll the condition with the same
  ceiling and proceed on timeout (see `wait_for_pbm_resync`,
  `wait_for_oplog_chunk_after`, `wait_for_service_loadbalancers`,
  cert-manager webhook probe in `deploy_cert_manager`).
- Sleeps that encode real time semantics stay: PBM-1265 workarounds
  (2x360s in pitr-physical tests, ticket still open), arbiter 240s soak
  (negative restart check), init-deploy 300s connection soak, PITR
  window gaps, chaos durations.
- `run-pr.csv` is ordered longest-test-first (LPT) to balance CI
  workers. When adding a test, insert it by expected duration, not
  alphabetically at the end.

## Running e2e locally (macOS, kind)

- Tests do NOT create a cluster; they use the current kubectl context.
  Local: kind cluster `percona-test` (context `kind-percona-test`).
- Image gotcha: golden files must match the operator image branch.
  CR uses `imagePullPolicy: Always`, so `kind load` is NOT enough - run
  a local registry (`kind-registry` container, 127.0.0.1:5001->5000 on
  the kind docker network) and set
  `IMAGE=kind-registry:5000/percona-server-mongodb-operator:<branch>`.
- `IMAGE_BACKUP` defaults to `perconalab/...:main-backup` (PBM main).
- `SKIP_DELETE=1` is default: namespaces survive test runs, clean with
  `kubectl get ns` + delete manually.
- Single test: `./e2e-tests/<name>/run` with env vars set.
- Pytest port (branch pr-2058): `uv sync` (Python >=3.13), then
  `uv run pytest` from repo root.

## Git workflow

- Remotes: `origin` = percona upstream (read only), `fork` = personal
  fork. `remote.pushDefault=fork`.
- Do NOT open PRs against percona upstream; all work stays in the fork.

## Timing facts (measured 2026-07, medians from 42 CI runs)

- Full clean PR run: job ~2:55, test stage ~2:36, ~27h serial test time.
- Slowest tests: pitr-physical 1:00:33, pitr-physical-backup-source
  0:53:49, upgrade-consistency-sharded-tls 0:54:03.
- The suite is volume-bound (sum/15 workers), not floor-bound: shaving
  the slowest test barely moves wall-clock; removing tests from GKE
  (e.g. to an envtest tier) or adding workers is what moves it.
- Timings can be re-derived any time from the report comments that CI
  posts on merged upstream PRs (per-test time table + job duration).
