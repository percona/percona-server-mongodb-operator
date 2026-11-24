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

Identify which disciplines apply:
- Go code (`pkg/`, `cmd/`, `*.go`): apply `docs/guidelines/architecture.md`
  and `docs/guidelines/go-style.md`. Also apply `docs/guidelines/qa.md` ("What a
  test must prove", "Go tests") to check that new branches, fields, and error
  paths have unit or envtest coverage.
- Test code (`e2e-tests/`, `*_test.go`, golden files, `tests.yaml`, test
  runners): apply `docs/guidelines/qa.md` and the nearest `AGENTS.md`.
- Manifests and OLM (`deploy/`, `installers/olm/`): apply
  `docs/guidelines/architecture.md` (compatibility, generated files) and
  `installers/olm/AGENTS.md`.

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
