# Review set

Diffs with known defects, and clean diffs, that check the review skills in
`.agents/skills/review-*` against the guidelines in `docs/guidelines/`. Run
it when a guideline or a review skill changes.

## Files

- `make_cases.py` writes one `diff.patch` per case from exact edits to
  tracked files, then restores them. It refuses to run when a file it edits
  has local changes, and it stops when an anchor string is missing, so a
  stale case fails instead of producing a different diff.
- `expected.md` lists, per case, the planted defect, the expected severity,
  the rule that names it, and the secondary findings the case is known to
  contain. Reviewers do not read it.

## Run

1. Generate the patches:

   ```
   python3 -I .agents/evals/review-set/make_cases.py . /tmp/review-set
   ```

   Name cases after the output directory to generate a subset.
2. For each case and each skill `expected.md` lists for it, start a fresh
   session or subagent with this request and nothing else:

   > Follow `.agents/skills/<skill>/SKILL.md` in `<repo>`. Review the patch
   > `<out>/<case>/diff.patch` as the diff of a branch against `main`. Do
   > not read `.agents/evals/review-set/expected.md`. Report in the format
   > the review procedure gives.

   If your tool runs subagents, run the cases in parallel.
3. Score each review against `expected.md`.

## Score

- Defect case: pass when the planted defect is reported at the expected
  severity. One level off is partial. Missed is a fail.
- Clean case: pass when nothing above Minor is reported.
- Noise: a Major or Critical finding that is wrong about the code or names
  no rule in the guideline. Noise is a fail.
- A correct finding on something the case did not plant is a flaw in the
  case. Fix the case and add the finding to `expected.md`.
- A fail blocks the guideline or skill change until the rule or the case is
  fixed.

## Add a case

- Pick a real file and plant one defect, or none, with `replace` or
  `append` edits. The anchor of a `replace` must occur once in the file.
- Add the row to `expected.md`: the defect, the severity, the rule in the
  guideline that names it, and the secondary findings the case contains.
- A bug that escaped review goes in as a case together with the rule that
  catches it (`docs/guidelines/agent-files.md`, "When a bug escapes").
