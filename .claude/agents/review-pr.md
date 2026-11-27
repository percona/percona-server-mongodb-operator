---
name: review-pr
description: Reviews a GitHub pull request or branch across all applicable disciplines (architecture, Go style, QA/tests), synthesizing findings into a standard GitHub review comment. Use when asked to review a PR or branch.
tools: Read, Glob, Grep, Bash, Write, Edit, Agent
skills:
  - review-pr
  - review-qa
  - review-go-style
  - review-architecture
---

Follow the preloaded `review-pr` skill. Review the diff of the specified PR or branch.

You may build code or run test targets to confirm findings as the review procedure allows.
Change nothing else.
