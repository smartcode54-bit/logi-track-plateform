# ADR 0004 — One shared `onInvalid` handler for every form (close the class, not the instance)

- **Status:** Accepted (2026-07-17) — implemented (commit `3fef379`), verified on dev
- **Deciders:** Samart Kas (product owner), Claude
- **Area:** logitrack-web — all 12 `zodResolver` forms (drivers, customers, subcontractors, trucks,
  hubs, tasks, company profile), `lib/formInvalidHandler.ts`, `context/locales/{en,th}/common.ts`

## Context

[ADR 0003](0003-edit-forms-fail-loudly-on-legacy-docs.md) fixed one silent-save bug in
`EditDriverForm` and set the rule in its decision #2: *a form must never fail silently; every
`zodResolver`-backed `handleSubmit` gets an `onInvalid` handler.* It left an explicit follow-up —
audit the other forms for the same one-line trap. This ADR records what the audit found and how the
rule was applied across the codebase.

Facts established during the audit (all `path:line` in `logitrack-web/`):

1. **The gap was near-universal: 11 of 12 `zodResolver` forms called `handleSubmit(onSubmit)`
   bare.** Only `EditDriverForm` had a handler, and only because ADR 0003 had just added it. Every
   other form could veto a save with no throw, no toast, and no write.

2. **The audit found no second live bug.** The 2026-06-13 change (`CLAUDE.md` item #38) made exactly
   two fields required *after* documents already existed:
   - `fullNameTh` on `drivers` — the ADR 0003 bug, fixed.
   - `source_name_th` on `hubs` — **already mitigated**. `hub-dialog.tsx:103-104` seeds it from the
     legacy `source_name_en` when absent, so the field is never empty for an old hub. Someone hit
     this class of bug there and handled it locally.

3. **No other schema has the "required field added later" shape.** `truckSchema`'s required fields
   (`province`, `brand`, `model`, `color`, `type`) and `subcontractorSchema`'s `contactPerson` /
   `phone` both date to the initial monorepo commit (`6f767ab`) — required from day one, so the docs
   were created through the form that enforced them.

4. **`EditTruckForm` was the only other form sharing the driver bug's exact shape:**
   `reset({...truckData})` spreads the fetched doc with no defaults merge (`:92`), against 7
   required fields. Structurally identical, with no known live trigger.

5. **The residual risk is real but indirect: docs written outside the form can violate the schema.**
   Firestore console edits, the import dialogs, and mobile all write directly. The already-recorded
   symptom "hand-editing `jobCategory` in the console traps the Edit Trip dialog" is exactly this.
   When such a doc reaches an edit form, the form refuses — silently, before this change.

6. **A naive handler gets two things wrong.** RHF nests errors under object fields, so
   `Object.keys(errors)[0]` can return a *group* (`customerDriverIds`) rather than an error — the
   toast would name the group and count 1 where two leaves failed. And `setFocus` throws on fields
   rendered by `DatePicker`, which are not registered inputs.

## Decision

1. **Apply the ADR 0003 rule to all 12 forms, not just the one with a reported bug.** The audit found
   no second live instance, and the work proceeds anyway: the value is closing the *class*, so the
   next required field added to any schema degrades into a named error instead of a dead button.

2. **One shared helper, not 12 pasted handlers.** `lib/formInvalidHandler.ts` exports
   `createInvalidHandler(form, t)` (`:51`), used as `handleSubmit(onSubmit, createInvalidHandler(form, t))`.
   A copy per form would drift, and the leaf-walking logic (fact #6) is exactly the kind of subtlety
   that gets simplified wrong on the third paste.

3. **The handler walks to the first leaf error and counts leaves** (`firstLeafError` `:15`,
   `countLeafErrors` `:29`), reports the field's authored **message** in preference to its key, and
   guards `setFocus` in `try/catch` (`:65`) so a non-focusable field degrades to "no scroll" rather
   than "no feedback".

4. **The message key is `common.toast.validationError`, not a per-domain key.** The drivers-specific
   `drivers.toast.validationError` added by ADR 0003 is removed. One shared handler implies one
   shared key (en + th).

5. **`EditTruckForm` also gets the defaults merge** — `reset({...truckDefaultValues, ...truckData})`
   (`:92`), reusing the `truckDefaultValues` the schema module already exports. It is the only other
   form with the spread-reset shape, so it is fixed now rather than left for the next incident.

6. **The helper's logic is unit-tested, since it is now load-bearing for every form.**
   `lib/formInvalidHandler.test.ts` covers the nested walk, leaf counting, message-over-key, the
   throwing `setFocus`, and empty errors.

## Consequences

**Positive**
- Every `zodResolver` form now names its blocking field. The class of bug from ADR 0003 cannot recur
  silently in any of them.
- One place to change the behavior (wording, focus, scroll-into-view) for all 12 forms.
- `EditTruckForm`'s latent spread-reset trap is closed before it produced an incident.

**Negative / risks**
- **Behavior changed in 12 forms to fix a bug reported in one.** Mitigated by scope: only the
  *invalid* path changes; a valid submit is byte-for-byte what it was. Verified on dev against the
  driver edit form (2026-07-17); the other 11 forms rest on the shared helper's unit tests and the
  identical wiring, not on individual clicks.
- The toast surfaces the raw Zod message, so a schema change that drops a custom message regresses
  the wording to Zod's default (inherited from ADR 0003, now spread across 12 forms).
- Forms that never used `zodResolver` are untouched and keep whatever behavior they had —
  `EditTripDetailsDialog`, `standby-backfill-dialog`, `edit-billing-dialog`, the import dialogs. The
  rule is "every `zodResolver` form", not "every form".
- `company-profile` discarded its form object on destructure and had to be restructured to capture it
  (`const form = useForm(...)`, then destructure from `form`). Mechanical, but it touched a file
  whose only defect was stylistic.

**Follow-ups**
- ~~Drive at least one form in a browser to confirm the toast and focus behave.~~ **Done on dev
  (2026-07-17)** — driver edit form, legacy doc with no `fullNameTh`: toast named the field, save
  succeeded once filled. The remaining 11 forms are unverified by click.
- Consider scroll-into-view in the helper — `setFocus` does not scroll for fields inside a collapsed
  section or a long dialog.
- If a non-`zodResolver` form ever grows validation, route it through the same helper rather than a
  local variant.

## Alternatives considered

- **Fix only `EditTruckForm` and the wizards (the ranked-risk subset), leave the rest.** Rejected:
  the remaining forms would still be one console edit away from the same silent veto, and the
  argument for skipping them ("no required field added later") is a fact about *today's* schemas, not
  a property that holds.
- **Do nothing — the audit found no second bug.** Tempting and honest, but the trap is a one-line
  omission that recurs every time someone writes a new form. The helper makes the correct call the
  easy one.
- **Copy the ADR 0003 handler into each form.** Rejected: 12 copies of the leaf-walk and the guarded
  `setFocus` will drift, and the subtle parts (fact #6) are the ones a copy tends to drop.
- **Put the handler in a `useInvalidToast` hook instead of a factory function.** Rejected as
  needless: it holds no state and calls no hook. A plain function composes directly inside the
  `handleSubmit(...)` call.
- **Make the handler a wrapper around `handleSubmit` itself** (e.g. `safeSubmit(form, t, onSubmit)`).
  Rejected: it hides RHF's own API behind a project-specific one, and the two-argument
  `handleSubmit(onValid, onInvalid)` form is already the documented, idiomatic shape.
- **Keep `drivers.toast.validationError` and add per-domain keys.** Rejected: one handler, one
  message, one key. Per-domain keys would drift in wording across languages for no gain.

## Related

- Implements the follow-up and decision #2 of
  [0003 — Edit forms fail loudly on legacy docs](0003-edit-forms-fail-loudly-on-legacy-docs.md).
  Supersedes nothing.
- Glossary: [../glossary.md](../glossary.md).
- Code: `logitrack-web/lib/formInvalidHandler.ts` (+ `.test.ts`); the 12 call sites listed under
  **Area**; `logitrack-web/context/locales/{en,th}/common.ts`.
- Commits: `ac1070d` (ADR 0003 instance fix), `3fef379` (this ADR's refactor).
- Conventions: [0000-adr-conventions.md](0000-adr-conventions.md).
