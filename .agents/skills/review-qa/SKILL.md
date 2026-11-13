---
name: review-qa
description: Review test changes in this Percona operator repo against the QA guideline (e2e tests wherever they live, golden files, Go tests, CI test selection, the scripts that run tests). Use when asked to review tests, or from the verify skill for any diff that touches test code or test runners, or Go code under pkg/ to check that it comes with tests.
---

# Review against the QA guideline

Follow the review procedure in `docs/guidelines/README.md` with
`docs/guidelines/qa.md` as the guideline. For e2e test code, wherever it
lives, also apply the rules in `e2e-tests/AGENTS.md`; the `AGENTS.md` nearest
to the file adds to them and does not replace them.

Scope: the diff of the current branch against `main`, or the files or PR the
user names. Test code is any path with an `e2e-tests/` segment, `test_*.py`,
`*_test.go`, golden files, `e2e-tests/tests.yaml`, and the scripts and `make`
targets that run tests. For Go code under `pkg/` without test changes, check
that each new branch, CR field or behavior has a unit or envtest test
(`qa.md`, "What a test must prove").

For a diff of more than a few hundred lines, go through the checklist items
of `qa.md` one by one for each changed file, instead of reading the diff once
and reporting what stood out.
