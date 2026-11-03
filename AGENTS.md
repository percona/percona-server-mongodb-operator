# Percona Server for MongoDB Operator: notes for coding agents

Kubernetes operator (Go, controller-runtime) for Percona Server for MongoDB.
These notes are for any coding agent (Codex, Cursor, Copilot, Claude Code,
Gemini CLI and others) and for humans. Agents read the nearest `AGENTS.md`, so
rules for a subtree live next to it:

| Area | File |
| ---- | ---- |
| End-to-end tests (bash and pytest) | [`e2e-tests/AGENTS.md`](e2e-tests/AGENTS.md) |
| OLM bundles and catalogs for OpenShift | [`installers/olm/AGENTS.md`](installers/olm/AGENTS.md) |

Contribution rules (CLA, Jira, branches, review) are in
[`CONTRIBUTING.md`](CONTRIBUTING.md); this file does not repeat them.

## Layout

- `pkg/`: operator code. `pkg/apis/psmdb/v1` holds the CRD types,
  `pkg/controller/*` the reconcilers.
- `deploy/`: generated manifests (`crd.yaml`, `rbac.yaml`, `operator.yaml`,
  bundles) and the example `cr.yaml`. `crVersion` in `deploy/cr.yaml` is the
  version e2e tests treat as current.
- `pkg/version/version.txt`: the operator version.
- `e2e-tests/`: end-to-end tests against a real cluster.
- `installers/olm/`: OLM bundle and catalog tooling.

## Code placement

- Put domain logic (backups, restores, TLS, users) in its package under
  `pkg/psmdb/<domain>/`. Controllers in `pkg/controller/*` call it.
- Before adding a helper, look for an existing one. Reuse or merge
  duplicates instead of adding another. Example: `isBackupRunning` in
  `pkg/controller/perconaservermongodb/backup.go` and `HasActiveJobs` in
  `pkg/psmdb/backup/backup.go`.
- Clean up the code you touch: small fixes go in the same PR, large
  refactors in a separate one.

## Checks to run before pushing

These are the checks CI runs on every PR. Run the ones that match your change.

| Change | Command |
| ------ | ------- |
| Go code | `make test` (unit and envtest), golangci-lint |
| CRD types or RBAC markers | `make generate manifests`, then commit the generated files |
| Python under `e2e-tests/` | `make py-fmt` and `make py-check` (ruff and mypy) |
| Shell scripts | `shellcheck`, and `shfmt -bn -ci -s` (the CI formatter flags) |

## Conventions

- Commit messages and PR titles start with the Jira key, for example
  `K8SPSMDB-1753: Add OLM bundle upgrade e2e test`.
- Fill in `.github/pull_request_template.md`; say which checklist items do not
  apply instead of deleting them.
- Never commit credentials. Test secrets come from `e2e-tests/conf/`, and
  `e2e-tests/conf/cloud-secret.yml` is gitignored.
- Scripts must run on Linux CI and on macOS: GNU make 3.81, bash 3.2 and BSD
  `sed`/`awk` are what a Mac has by default. Test there or avoid GNU-only
  features.

## Skills and guidelines

Skills in `.agents/skills/` follow the [Agent Skills](https://agentskills.io)
standard; Claude Code finds them through the symlinks in `.claude/skills/`.
Guidelines in `docs/guidelines/` hold the rules each review skill applies.
Content for agents lives only in these tool-neutral files and in `AGENTS.md`;
tool-specific directories only point at them. The rules for maintaining them
are in [`docs/guidelines/agent-files.md`](docs/guidelines/agent-files.md).

| Skill | Use it to |
| ----- | --------- |
| `plan` | turn a ticket into acceptance criteria and a plan, or a proposal |
| `implement` | build the plan, tests first, one criterion at a time |
| `verify` | run the checks, tests and reviews, and report what was verified |
| `review-architecture` | review a diff against `docs/guidelines/architecture.md` |
| `review-go-style` | review a diff against `docs/guidelines/go-style.md` |
| `review-qa` | review tests against `docs/guidelines/qa.md` |

## Commands

- Run without asking: read-only commands (`git status`, `git diff`,
  `kubectl get`, `kubectl describe`, `kubectl logs`), builds, unit tests,
  generators and linters from the table above.
- Confirm with the user first: anything that changes a cluster
  (`kubectl apply`, `kubectl delete`, `make e2e-test`, `make deploy`), pushing
  container images, and `git push`. Every push restarts the Jenkins e2e run
  for the PR, so push in batches.
- Never: delete resources on a cluster you did not create, force-push a
  shared branch, or write credentials or kubeconfig files into the repo.
