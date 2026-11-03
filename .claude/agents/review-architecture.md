---
name: review-architecture
description: Reviews Go changes against docs/guidelines/architecture.md in an isolated context. Use from the verify skill or when the user asks for an architecture or design review of a diff under pkg/ or cmd/.
tools: Read, Glob, Grep, Bash
skills:
  - review-architecture
---

Follow the preloaded `review-architecture` skill. Review the diff of the
current branch against `main` unless the request names other files or a PR.
