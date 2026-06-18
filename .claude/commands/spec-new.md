---
description: Create a feature spec before writing code (spec-driven dev, phase 1)
argument-hint: <feature name / short description>
---

You are starting the **planning phase** of spec-driven development for LogiTrack.
Goal of this command: produce an approved written spec — **do NOT write feature code yet.**

Feature requested: **$ARGUMENTS**

Follow these steps:

1. **Read context first**
   - `shared-docs/.vibe-rules.md` (all rules)
   - `shared-docs/database-migration-plan.md` if the feature touches billing, rate cards, vehicle expenses, transactions, or analytics
   - The relevant `features/<domain>/` folder and existing similar code, to ground the design in real patterns

2. **Clarify before designing.** Use AskUserQuestion for genuinely ambiguous decisions (scope boundaries, which collection, web vs mobile vs both, i18n impact). Do not invent requirements — ask.

3. **Delegate the design to the `spec-architect` subagent** (read-only analysis) when the feature is non-trivial, or do it inline for small ones. Produce the spec from the template `shared-docs/specs/_TEMPLATE.md`.

4. **Write the spec** to `shared-docs/specs/<kebab-feature-name>.md`:
   - Fill every section. Number requirements (R1, R2…) and map acceptance criteria back to them.
   - Be concrete about affected files, Firestore fields, i18n keys (en+th), and whether billing logic must be synced across `lib/billingCompute.ts` + `functions/src/core/billingCompute.ts`.
   - Keep `Status: 🟡 Draft`.

5. **Stop and present the spec for approval.** Summarize the key decisions and ask the user to approve or revise. Do not start implementation — that is `/spec-build`.

Constraints:
- This is a NEW markdown file under `shared-docs/specs/` which is gitignored by the broad `*.md` rule — it will still be created on disk; flag that it needs `git add -f` when committing.
- No code edits in this phase except creating/updating the spec file itself.
