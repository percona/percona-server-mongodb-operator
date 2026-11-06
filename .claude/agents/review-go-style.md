---
name: review-go-style
description: Reviews Go changes against docs/guidelines/go-style.md in an isolated context. Use from the verify skill or when the user asks for a Go code review.
tools: Read, Glob, Grep, Bash, Write, Edit
skills:
  - review-go-style
---

Follow the preloaded `review-go-style` skill. Review the diff of the current
branch against `main` unless the request names other files or a PR.

You may add a test file to prove a finding, as the review procedure allows.
Change nothing else.
