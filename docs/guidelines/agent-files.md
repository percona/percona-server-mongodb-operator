# Agent files

Last verified: 2026-11-03

Rules for the files that configure coding agents: the `AGENTS.md` files, the
guidelines in `docs/guidelines/`, and the skills in `.agents/skills/`.

## One source of truth

- Content for agents lives in tool-neutral files only: `AGENTS.md` (the
  AGENTS.md convention), `docs/guidelines/*.md`, and
  `.agents/skills/*/SKILL.md` (the [Agent Skills](https://agentskills.io)
  standard). A contributor with any coding agent, or with none, gets the same
  rules.
- Tool-specific directories (`.claude/`, `.cursor/`, `.github/agents/`,
  `.gemini/`) only point at those files. They carry no content of their own
  and are optional. Claude Code reads skills from `.claude/skills/`, so each
  skill has a symlink there, and `.claude/agents/review-*.md` only start the
  matching review skill.
- Enforcement lives in the repo, not in agent hooks: `make` targets, CI and
  the pull request checklist.

## Writing rules

- Facts and rules only. No history and no provenance. Give a reason only
  when the rule is surprising, in one clause.
- Under 200 lines per file. Split by topic instead of growing a file.
- Every guideline starts with `Last verified: YYYY-MM-DD`. Update the date
  when you re-check the content against the code, not on every edit.
- A rule must be checkable. Name the path, command or pattern that shows it.
- The nearest `AGENTS.md` owns its subtree. Do not repeat root rules in nested
  files, and do not repeat guideline content in `AGENTS.md`; link instead.

## Skills

- Frontmatter: `name`, equal to the directory name, in lowercase letters,
  digits and hyphens; `description`, what the skill does and when to use it,
  under 1024 characters. Optional: `compatibility`, `metadata`. No other
  keys; tools that follow the standard ignore or reject them.
- Body: plain instructions. No `$ARGUMENTS` or other placeholders, no shell
  substitution syntax, no subagent, model or hook directives. Inputs come
  from the user's message; when one is missing, the skill asks for it.
- When a step depends on what the agent can do, say so and give the
  fallback: "if you have a Jira tool, read the ticket; otherwise ask the user
  to paste it", "if your tool runs subagents, run the reviews in parallel;
  otherwise run them one after another in fresh sessions".
- Keep a skill under 100 lines. Put detail in a guideline, or in
  `references/` inside the skill directory.
- Each skill names its input, its steps and its output.
- Adding a skill: create `.agents/skills/<name>/SKILL.md`, add the symlink
  `.claude/skills/<name>` pointing to `../../.agents/skills/<name>`, and add a
  row to the skills table in the root `AGENTS.md`.

## Review set

- `.agents/evals/review-set/` holds a generator for diffs with known defects
  and clean diffs, and `expected.md` with the finding each diff must produce.
  Its `README.md` says how to run and score it.
- Rerun the set when a guideline or a review skill changes. A missed defect,
  or a wrong Major or Critical finding on a clean diff, blocks the change.
- Reviewers never read `expected.md`; the person running the set does.

## When a bug escapes

When a bug reaches review or a release that a skill or guideline should have
caught: fix the bug, then find the step that missed it. A wrong or missing
acceptance criterion points at the `plan` skill, a missing test at the
`implement` skill or `qa.md`, a check that was not run at the `verify` skill,
a missing rule at the guideline. Add the smallest rule that would have caught
this bug, in the same PR or a follow-up. Do not add general advice. When a
review skill should have caught the bug, add the diff as a case to the review
set.
