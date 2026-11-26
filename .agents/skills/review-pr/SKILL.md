---
name: review-pr
description: Review a pull request or branch across all applicable disciplines (architecture, Go style, QA), add a manual test plan for Go changes, synthesize findings, and prepare a GitHub review comment. Use when asked to review a PR or branch.
---

# Review a pull request or branch

Review a PR or branch across the disciplines that match the changed files,
following the procedure in `docs/guidelines/README.md`.

## 1. Scope

Input: a pull request number or URL, or a branch name (the current branch by
default).

Collect, before any review starts:

- the diff, the changed files and the head commit:
  `gh pr diff <pr>`, `gh pr view <pr> --json files,commits,headRefOid`;
- the PR description and the comments already on the PR:
  `gh api repos/<owner>/<repo>/pulls/<pr>/reviews`, `.../pulls/<pr>/comments`
  and `.../issues/<pr>/comments`;
- the Jira ticket named in the title and its acceptance criteria: if you have
  a Jira tool, read it; otherwise ask the user to paste it, or go on without
  it and say so in the report;
- the CI status: `gh pr checks <pr>`.

Sort each changed file into one group, checking the groups in this order,
and run only the reviews of the groups that have files:

| Group | Files | Reviews |
| ----- | ----- | ------- |
| Test code | any path with an `e2e-tests/` segment, `*_test.go`, `testdata/`, golden files, `tests.yaml`, the scripts and `make` targets that run tests | `review-qa` |
| Go code | other `*.go` files | `review-architecture`, `review-go-style`, `review-qa`, `test-plan` |
| Manifests and OLM | `deploy/`, `config/`, `installers/olm/` | `review-architecture` |
| Agent files | `AGENTS.md` files, `docs/guidelines/`, `.agents/`, `.claude/` | checked in step 3 |

A diff with test code only gets `review-qa` alone; it applies the "Tests"
section of `go-style.md` to Go tests itself. Other files (docs) get no
review; say so in the report.

## 2. Review execution

If your tool runs subagents, run the matching skills in parallel on a clean
checkout (a `git worktree`) at the head commit. Otherwise run each in a fresh
session so the review does not inherit this session's assumptions.

Give each reviewer the same brief and nothing more: the PR, the head commit,
the worktree path, the diff command, the ticket and its acceptance criteria.
Facts only: no findings, suspicions or areas to focus on, so the reviews stay
independent. Tell each one to change nothing outside its worktree, to post
nothing, and that no cluster is available unless the user said otherwise.

Confirm findings locally when possible: run `make py-check`, `make test`, or
the specific unit test packages on the worktree copy.

## 3. Synthesize and report

- Merge the same finding from several reviewers into one, at the highest
  severity given.
- Drop findings already raised in the PR's comments. Name what was raised
  and fixed at the head commit in one line, so the author sees it was read.
- Check each acceptance criterion of the ticket: done, partly done or not
  done, with the test or the file and line.
- Agent files: check them against `docs/guidelines/agent-files.md` and
  against the code they describe. A rule the diff itself breaks is a finding.
- Add the plan from `test-plan` as its own section after the action items.

Write the review in the format of step 5 of `docs/guidelines/README.md`.
Before showing it, check it against the format list there, item by item, and
fix what does not match.

Output: present the complete review to the user for approval. Once approved,
post as one review comment:

```bash
gh pr review <pr> --comment --body-file <file>
```

If a commit lands on the PR before posting, rerun the reviews on the new
head, or say in the footer which commit was reviewed.
