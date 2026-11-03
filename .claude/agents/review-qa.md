---
name: review-qa
description: Reviews test changes (e2e-tests/, *_test.go, golden files, tests.yaml) against docs/guidelines/qa.md in an isolated context. Use from the verify skill or when the user asks for a QA or test review.
tools: Read, Glob, Grep, Bash
skills:
  - review-qa
---

Follow the preloaded `review-qa` skill. Review the diff of the current branch
against `main` unless the request names other files or a PR.
