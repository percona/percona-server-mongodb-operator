---
name: review-pr
description: Review a pull request or branch across all applicable disciplines (architecture, Go style, QA), synthesize findings, and prepare a GitHub review comment. Use when asked to review a PR or branch.
---

# Review a pull request or branch

Review a PR or branch across the disciplines that match the changed files,
following the procedure in `docs/guidelines/README.md`.

## 1. Scope

Input: a pull request number or URL, or a branch name (the current branch by
default).

Inspect the diff and list of changed files:

```bash
gh pr diff <pr>
gh pr view <pr> --json files,commits
```

Sort each changed file into one group, checking the groups in this order,
and run only the reviews of the groups that have files:

| Group | Files | Reviews |
| ----- | ----- | ------- |
| Test code | any path with an `e2e-tests/` segment, `*_test.go`, `testdata/`, golden files, `tests.yaml`, the scripts and `make` targets that run tests | `review-qa` |
| Go code | other `*.go` files | `review-architecture`, `review-go-style`, `review-qa` |
| Manifests and OLM | `deploy/`, `config/`, `installers/olm/` | `review-architecture` |

A diff with test code only gets `review-qa` alone; it applies the "Tests"
section of `go-style.md` to Go tests itself. Files in no group (docs, agent
files) get no review; say so in the report.

## 2. Review execution

If your tool runs subagents, run the matching review skills in parallel
(`review-architecture`, `review-go-style`, `review-qa`) on a clean checkout of
the branch or PR head commit. Otherwise run each in a fresh session so the
review does not inherit this session's assumptions.

Confirm findings locally when possible: run `make py-check`, `make test`, or
the specific unit test packages on a worktree copy.

## 3. Synthesize and report

Merge findings across all active disciplines into a single review following
step 5 of `docs/guidelines/README.md`:
- One summary table with severity counts (🔴 Critical, 🟠 Major, 🔵 Minor,
  ⚪ Suggestion).
- A GitHub alert callout with the verdict: `ready` (no Critical or Major
  findings) or `needs work`.
- Findings ordered by severity (Critical first, then Major, then Minor).
- Positives section.
- Action items checklist with severity markers.
- Verification footer stating the commit SHA, worktree checks run, and tests run.

Output: present the complete review to the user for approval. Once approved,
post as one review comment:

```bash
gh pr review <pr> --comment --body-file <file>
```
