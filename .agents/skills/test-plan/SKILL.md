---
name: test-plan
description: Write a manual test plan for a change in this Percona operator repo (a PR, a branch or a diff, mostly Go changes under pkg/) that a developer or QA engineer can run on their own cluster, and say what the automated tests already cover. Use when asked for a test plan, manual test steps or how to check a PR by hand, or from a CI review that posts the plan as a PR comment.
---

# Write a manual test plan

Input: the change to test, as a PR number, a branch (the current one by
default) or a diff. Read the PR description and the Jira ticket too: if you
have a GitHub or Jira tool, use it; otherwise ask the user to paste them, or
work from the diff alone and say so.

This skill writes a plan. It does not run anything on a cluster. Running the
plan follows the "Commands" rules in the root `AGENTS.md`.

## 1. Find what a user can see

From `git diff main...HEAD` (or the PR diff), list each change a user or an
operator of the cluster can observe:

- a new or changed CR field, default or validation (`pkg/apis/`);
- a change to rendered objects: StatefulSets, Services, Secrets, PDBs, Jobs;
- a reconcile change: when the operator acts, what it changes, the status and
  conditions it reports, the events and log lines it writes;
- backup, restore, TLS, users, scaling, sharding or upgrade behavior;
- a `crVersion` gate: what happens with the old and the new `crVersion`.

Refactors with no observable change get one line saying so and no steps.

## 2. Check what is already covered

For each change, find the tests that cover it: unit and envtest tests next to
the changed code, and e2e tests under `e2e-tests/` (search the test
directories for the field or behavior, and `e2e-tests/tests.yaml` for what
CI runs). A manual step is for what no test covers, or for what needs a
human look: logs, events, timing on a real cluster.

## 3. Write the scenarios

For each uncovered change, one scenario:

- Setup: the cluster type, the operator build (image tag or `make deploy`
  from the branch) and the CR, as a change to `deploy/cr.yaml`.
- Steps: the exact commands (`kubectl apply`, `kubectl patch`,
  `kubectl get ... -o jsonpath=...`), in order.
- Expected: what each step shows on a healthy operator, as a value to compare
  (a field, a status, a count, a log line), never "works".
- Failure signs: what you would see if the change is broken.

Add, where the change allows them:

- the negative case: an invalid value is rejected with a clear error;
- the existing cluster: a cluster created by the previous release, then the
  operator upgraded to the branch, with its pods' UIDs and `restartCount`
  recorded before and compared after (`docs/guidelines/qa.md`);
- the `crVersion` boundary: the old `crVersion` keeps the old behavior;
- cleanup: the commands that remove what the plan created.

Keep data checks real: write a document before, read it back after.

## 4. Output

```
Change: <PR or branch>, <one-line summary>
Covered by tests: <change>: <test name> (unit | envtest | e2e <name>)
Needs a manual check:
  1. <scenario title> (<why: not covered | needs a human look>)
     Setup: ...
     Steps:
       - [ ] <command>
     Expected: ...
     Failure signs: ...
Cleanup: ...
Not covered by this plan: <item>: <reason>
Suggested automated tests: <scenario that should become a unit or e2e test>
```

Order the scenarios by risk, highest first. When the plan is posted as a PR
comment, keep the scenarios in a collapsed block under a three-line summary.
