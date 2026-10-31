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
