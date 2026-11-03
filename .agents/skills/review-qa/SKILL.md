---
name: review-qa
description: Review test changes in this Percona operator repo against the QA guideline (e2e tests under e2e-tests/, golden files, Go tests, CI test selection). Use when asked to review tests, or from the verify skill for any diff that touches e2e-tests/ or *_test.go files.
---

# Review against the QA guideline

Follow the review procedure in `docs/guidelines/README.md` with
`docs/guidelines/qa.md` as the guideline. For files under `e2e-tests/`, also
apply the rules in `e2e-tests/AGENTS.md`.

Scope: the diff of the current branch against `main`, or the files or PR the
user names.
