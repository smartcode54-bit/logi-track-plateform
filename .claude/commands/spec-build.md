---
description: Implement a feature strictly from its approved spec (spec-driven dev, phase 2)
argument-hint: <spec name under shared-docs/specs/, e.g. multi-drop-fee>
---

You are in the **build phase** of spec-driven development. Implement the spec — nothing more, nothing less.

Spec to build: **shared-docs/specs/$ARGUMENTS.md** (resolve the closest match if the name is partial).

Steps:

1. **Read the spec in full** plus `shared-docs/.vibe-rules.md`. If the spec `Status` is still `🟡 Draft`, stop and ask the user to approve it via `/spec-new` review first — do not build an unapproved spec.

2. **Confirm scope.** Restate the task breakdown (section 6) and the "Out of scope" list. If anything in the spec is now stale or contradicts the current code, surface it before coding.

3. **Implement task-by-task** (section 6). For each task follow project rules:
   - Feature architecture: data access in `features/<domain>/api/`, UI in `components/`.
   - **Billing:** any change to billing logic must be mirrored in BOTH `lib/billingCompute.ts` and `functions/src/core/billingCompute.ts`.
   - **i18n:** add keys to BOTH `context/locales/en/*` and `context/locales/th/*`.
   - **Hub/SOC resolution:** keep `nameToCode` and `codeToName` maps separate (never merge).
   - **Firestore Rules / indexes:** update if the spec adds collections/fields/queries.
   - Mobile: bump `pubspec.yaml` if shipping a mobile change.
   - Check off each `- [ ]` in the spec as you complete it.

4. **Verify against acceptance criteria** (section 7). Run what applies:
   - Web: `tsc --noEmit`, ESLint, Vitest.
   - Mobile: `dart analyze` on changed files.
   - Report results honestly — if something fails, say so with the output.

5. **Update the changelog** in `shared-docs/.vibe-rules.md` per project convention, and set the spec `Status: ✅ Done`.

6. **Summarize** what changed (files + which requirements satisfied). Do not commit, push, or deploy unless the user asks.

Stay inside the spec's scope. If you discover out-of-scope work, note it under section 9 (follow-ups) or flag it — do not silently expand the change.
