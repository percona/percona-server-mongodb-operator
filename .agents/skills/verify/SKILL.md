---
name: verify
description: Verify an implemented change in this Percona operator repo against its acceptance criteria, run the project checks and the matching review skills, and report what was and was not verified. Use before opening or updating a PR, or when the user asks to verify, check or validate a change.
---

# Verify a change

Input: the branch to verify (the current one by default) and the plan with
its acceptance criteria. Without a plan, derive the criteria from the ticket
and the PR description, and say that you did.

Start from the list of changed files:

```
git diff --name-only main...HEAD
```

## 1. Checks

Run the commands from the "Checks to run before pushing" table in `AGENTS.md`
that match the changed files. If CR types or RBAC changed, run
`make generate manifests` and confirm that `git status` shows no new changes.

## 2. Tests

- Go: `make test`. Run the changed packages first, then the full target.
- e2e: needed when the change touches reconcile logic, CR fields, images,
  backups, restores or upgrades. Pick the test under `e2e-tests/` that covers
  the behaviour and run it as `e2e-tests/AGENTS.md` describes. Before
  running, check that `kubectl config current-context` points at a cluster
  you own. If no cluster is available, write that in the report; never skip
  silently.

## 3. Acceptance criteria

For each criterion report done, partly done or not done, with the evidence:
the command and its result, the test name, or the file and line.

## 4. Reviews

Run the review skills that match the diff:

- Go code: `review-go-style` and `review-architecture`;
- anything under `e2e-tests/` or in `*_test.go`: `review-qa`.

If your tool runs subagents, run them in parallel. Otherwise run each in a
fresh session, so the review does not inherit this session's assumptions.
Fix Critical and Major findings before the PR.

## 5. Report

```
Checks: <command>: pass | fail (<summary>)
Tests: <command>: pass | fail | not run (<reason>)
Acceptance criteria:
  1. <criterion>: done | partly | not done. Evidence: <...>
Not verified: <item>: <reason>
Review findings: <n> fixed, <n> open (<list>)
```

If a bug later turns out to be something this skill should have caught, apply
the escape rule in `docs/guidelines/agent-files.md`.
