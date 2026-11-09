---
name: plan
description: Plan a change in this Percona operator repo before writing code. Use when the user names a Jira ticket or asks to plan, scope or design a change. Produces acceptance criteria and either a plan for the PR description or an enhancement proposal.
---

# Plan a change

Input: a Jira ticket key, or a description of the change, in the user's
message. If both are missing, ask for one.

## 1. Collect the facts

- Read the ticket, including its comments, if you have a Jira tool. Otherwise
  ask the user to paste the ticket text.
- Read the root `AGENTS.md` and the `AGENTS.md` nearest to the code you expect
  to change.
- Find the code involved, using the layout in `AGENTS.md`: the CR types, the
  reconcilers, the domain packages and the e2e tests. List the existing
  helpers the change must reuse.
- Check `docs/enhancements/` for a proposal on the same topic.
- Read `docs/guidelines/architecture.md` for the constraints that apply.

## 2. Pick the plan format

Write an enhancement proposal when the change does any of the following:

- adds, removes or changes a field in the CR spec or status;
- changes backup, restore, upgrade, TLS or user management behavior;
- touches more than one controller;
- needs a decision reviewers should see before the code.

Create it as `docs/enhancements/<TICKET>-<slug>/proposal.md` from
`docs/enhancements/TEMPLATE.md`. Its Open Questions section holds what is
still unresolved.

Otherwise plan in the PR: the plan fills the Problem, Cause and Solution
fields of `.github/pull_request_template.md`.

## 3. Ask instead of guessing

Ask the user in numbered rounds. Each question lists the options you see and
marks the one you recommend. Stop when you can write every acceptance
criterion without guessing. Record the answers in the plan as decisions.

## 4. Write the plan

The plan contains:

- the problem, in one to three sentences;
- numbered acceptance criteria, each checkable by a test or a command;
- the files and packages that change;
- the test plan: unit tests for logic and rendering, envtest for reconcile
  behavior, e2e tests only for what needs a real cluster (see
  `e2e-tests/AGENTS.md`), and what the tests leave out;
- compatibility: behavior of existing clusters, the `crVersion` gate, the
  upgrade path, and the oldest and newest supported database and Kubernetes
  versions;
- decisions taken and open questions.

## 5. Hand over

Show the plan and wait for the user to agree before implementing. Then use
the `implement` skill.
