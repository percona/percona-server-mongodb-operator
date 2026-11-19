---
name: implement
description: Implement an agreed plan or proposal in this Percona operator repo, tests first, following AGENTS.md and the guidelines. Use when the user asks to implement, code or build a planned change.
---

# Implement a plan

Input: the agreed plan, either in the PR description, in a proposal under
`docs/enhancements/`, or as the output of the `plan` skill in this
conversation. If there is no plan, run the `plan` skill first.

Before writing Go, read `docs/guidelines/architecture.md` and
`docs/guidelines/go-style.md`. Before writing tests, read
`docs/guidelines/qa.md` and, for `e2e-tests/`, `e2e-tests/AGENTS.md`.

## Loop, one step of the plan at a time

1. Where a unit test can express the step's criteria (how a CR field
   renders into Kubernetes objects, defaults, validation, version logic),
   write the test first and watch it fail.
2. Write the code where `AGENTS.md` says it belongs. Reuse existing helpers.
3. If you changed CR types, run `make generate manifests` and include the
   generated files. `docs/guidelines/architecture.md` lists what else changes
   with a CR field.
4. Run the checks from the "Checks to run before pushing" table in
   `AGENTS.md` that match what you changed.
5. Commit the step. The message starts with the Jira key.
6. Stop and show the user the step's diff and what the checks reported.
   Wait for the user to review it, answer questions and ask for changes
   before you start the next step. Skip the stop only when the user asked
   for an unattended run.

## Rules

- Stay inside the plan. Small cleanups in code you touch are fine; larger
  refactors go in a separate PR.
- When the plan turns out to be wrong, say so and update the plan before
  changing course.
- Add an e2e test only for what needs a real cluster. `e2e-tests/AGENTS.md`
  says how tests are laid out and registered for CI.
- Log the operator actions you add, and add new or changed options to the
  default YAML files (see the pull request checklist).
- Do not push until the `verify` skill passes. Every push restarts the
  Jenkins e2e run for the PR, so push in batches.

## Done when

Every acceptance criterion has code and tests, the checks pass, and the
`verify` skill reports no open Critical or Major findings.
