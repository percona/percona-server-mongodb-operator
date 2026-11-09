# Guidelines

Last verified: 2026-11-03

Each guideline holds the rules of one discipline. A review skill under
`.agents/skills/` applies a guideline to a diff; humans use the same files.

| Guideline | Covers | Review skill |
| --------- | ------ | ------------ |
| [`architecture.md`](architecture.md) | code placement, reconcile flow, CR changes, compatibility | `review-architecture` |
| [`go-style.md`](go-style.md) | errors, logging, context, naming, tests, lint | `review-go-style` |
| [`qa.md`](qa.md) | e2e tests, golden files, Go tests, CI test selection | `review-qa` |
| [`agent-files.md`](agent-files.md) | `AGENTS.md`, guidelines and skills themselves | none, reviewed by code owners |

## Review procedure

Every review skill follows these steps.

1. **Scope.** Review the diff against `main` (`git diff main...HEAD`), or the
   files or PR the user names. Read the changed files and only what you need
   to understand them.
2. **Rules.** Read the guideline the skill names and the `AGENTS.md` nearest
   to each changed file. Apply those rules, not personal preference.
3. **Findings.** For each finding give the file and line, what is wrong, why
   it matters with the rule it breaks, and a concrete fix.
4. **Severity.**
   - Critical: a bug, data loss, a security problem, or a test that passes
     while the operator is broken.
   - Major: a likely bug, flake, resource leak or compatibility break, or a
     change that makes the code clearly harder to maintain.
   - Minor: a clear improvement with low risk.
   - Suggestion: style or a nice-to-have.
5. **Output.** A summary of two or three sentences with a verdict: ready, or
   needs work. Findings numbered, highest severity first. Positives: what the
   change does well. Action items: a checklist, Critical and Major first.
6. **Verification.** You may build the code and run tests to confirm a
   finding, on the branch or on a copy. A copy is a git worktree at the head
   of the branch under review; some tools make their copies from `main`, so
   check with `git log -1` before you trust a result. Use an existing test
   when one covers the case. When proving a finding needs a new test, keep it
   in the code base: add it to the branch and name it in the report. Do not
   add a test that duplicates an existing one.
7. **Limits.** Do not repeat what the linters and `make test` report; CI runs
   them. Do not review generated files except to check that they were
   regenerated. When you cannot verify something, say so instead of guessing.
