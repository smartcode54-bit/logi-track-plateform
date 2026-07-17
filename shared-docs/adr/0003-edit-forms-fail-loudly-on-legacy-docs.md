# ADR 0003 — Edit forms fail loudly on legacy docs (never relax the schema to make old data saveable)

- **Status:** Accepted (2026-07-17) — implemented and verified on dev (`logi-track-wrt-dev`);
  `updateDriverAccount` deployed to prod. **The client fix is not yet live in prod** — it ships with
  the next hosting build.
- **Deciders:** Samart Kas (product owner), Claude
- **Area:** logitrack-web (`EditDriverForm`, `validate/driverSchema.ts`), Cloud Functions
  (`updateDriverAccount`), `drivers` collection + Firebase Auth

## Context

An admin edited a driver's **email** at `/app/drivers/edit`, hit Save, and the value never reached
Firestore. No error, no toast — the button simply did nothing.

Facts established while investigating (all `path:line` in `logitrack-web/`):

1. **The email write path was never the problem.** `EditDriverForm.onSubmit` → `updateDriver`
   (`features/drivers/api/drivers.ts:125`) → callable `updateDriverAccount`
   (`functions/src/triggers.ts:137`), which writes the doc at `triggers.ts:267` with `email` included.
   The edit never left the browser, so none of that code ran.

2. **The form submit was silently blocked by validation.** `form.handleSubmit(onSubmit)` was called
   with **no `onInvalid` handler**. React Hook Form's contract is that a failed resolver simply does
   not call `onSubmit` — no throw, no return value, no feedback. Save became a no-op.

3. **The blocking field was not email — it was `fullNameTh`.** `driverSchema.ts:16` declares
   `fullNameTh: z.string().min(1, "Thai full name is required")`. That field became required on
   2026-06-13 (`CLAUDE.md` item #38); **driver docs created before that date do not have the key**.
   Verified by parsing a representative legacy doc through the real schema:

   ```
   OLD DOC valid: false
     blocked by: fullNameTh -> Invalid input: expected string, received undefined
   WITH fullNameTh valid: true | email: new.email@example.com
   ```

   The edited email was valid the whole time. An unrelated, untouched field vetoed the save.

4. **`reset()` made the failure less legible.** `form.reset(formData)` (`EditDriverForm.tsx:127`)
   replaces *every* value, so a key absent from the Firestore doc lands as `undefined` rather than
   falling back to `defaultValues`. Zod then reports the type error
   `expected string, received undefined` instead of the authored message `"Thai full name is
   required"` — the message a human can act on.

5. **The same trap is latent on other fields, and not only for legacy docs.** `idCard`
   (`.length(13)`, `:32`), `truckLicenseId` (`.length(8)`, `:35`), and `birthDate` (required, age
   20–55, `:20`) are all exact-match constraints on real-world data that drifts. Any one of them
   silently vetoes an unrelated edit.

6. **The invalid field can be off-screen.** `FormMessage` renders inline in the Personal Info card at
   the top; the Save button is at the bottom of a long form. Even when RHF sets the error, the admin
   watching the button sees nothing at all.

7. **Auth and Firestore email must agree.** `updateDriverAccount` updates the Auth record *before*
   the Firestore doc (`triggers.ts:171-201` then `:267`) and aborted the whole write on any Auth
   failure — correct, but it surfaced as the generic `"Failed to update driver"`, indistinguishable
   from the silent case above. The driver signs in with the **Auth** email, so a `drivers.email` that
   drifts from it locks the driver out with no visible symptom.

**Owner intent** (stated during the fix): *"is has to update when admin has been update driver
profile all of field"* — editing a driver profile must actually persist, and the admin must never be
left guessing why it did not.

## Decision

1. **Keep `driverSchema` strict. Do not relax a required field to make legacy docs saveable.**
   `fullNameTh` in particular stays `min(1)`: it is the name every report and billing document
   renders (`lib/driverName.ts` priority `fullNameTh → name → firstName+lastName → email → id`).
   Weakening the schema to clear the error would let a nameless driver reach an invoice — trading a
   visible form error for an invisible data defect.

2. **A form must never fail silently. Every `zodResolver`-backed `handleSubmit` gets an `onInvalid`
   handler.** For `EditDriverForm` (`:248`, wired at `:298`) it raises a toast naming the **first
   blocking field and the error count**, then calls `form.setFocus` to jump to it. A Save that does
   nothing and says nothing is a defect, not a validation outcome.

3. **`reset()` merges the form defaults under the fetched doc** — `reset({...FORM_DEFAULTS, ...driver})`
   (`:109`, defaults hoisted to `:47`). A field absent from the doc then arrives as `""`, so the
   admin reads `"Thai full name is required"` instead of `"expected string, received undefined"`.
   Defaults live in one named constant rather than being duplicated between `useForm` and `reset`.

4. **`drivers.email` never diverges from the Auth email.** `updateDriverAccount` keeps its
   abort-on-Auth-failure ordering — on `auth/email-already-exists` it throws an explicit
   `already-exists` HttpsError naming the address and stating *"No changes were saved"*
   (`triggers.ts:196-200`), rather than a generic abort. The client appends the server message to its
   error toast instead of swallowing it. **Nothing is written when the two stores cannot be made to
   agree.**

5. **Legacy docs are repaired by an admin filling the field, not by a script.** `fullNameTh` is a
   Thai name; it cannot be derived from the English `firstName`/`lastName`, and inventing one is
   worse than an empty field. The form now names the missing field, which makes the repair a normal
   part of the next edit.

## Consequences

**Positive**
- The reported bug is fixed at its cause: the admin sees exactly which field blocks the save instead
  of a dead button.
- The fix generalizes. Every future required field added to `driverSchema` degrades into a *named
  error* on legacy docs rather than a silent veto — the class of bug is closed, not just this one.
- Schema strictness is preserved, so `fullNameTh` remains a real guarantee for billing documents.
- An email collision now tells the admin the address is taken and that nothing was saved.

**Negative / risks**
- **Editing a legacy driver now costs more than the intended edit.** To change one email the admin
  must also supply `fullNameTh` (and satisfy `idCard`/`truckLicenseId`/`birthDate`). Accepted: it is
  the honest cost of the invariant, and it was already being paid — previously as a save that
  appeared to work and did not.
- `form.setFocus` cannot focus fields rendered by `DatePicker` (`birthDate`, expiry dates) — they are
  not registered inputs. The call is wrapped in `try/catch`; the toast still names the field, so the
  failure mode is "no scroll", not "no feedback".
- The toast surfaces the raw Zod message. That is acceptable while the messages are authored strings
  (`"Thai full name is required"`), but a schema change that drops a custom message will regress the
  toast to Zod's default wording.
- `NewDriverForm` validates per-step (`NewDriverForm.tsx:156`) and is not affected today, but it
  shares `driverSchema` — the two forms can drift.

**Follow-ups**
- ~~Audit other `zodResolver` forms for a missing `onInvalid`.~~ **Done** — see
  [0004](0004-shared-oninvalid-handler-for-all-forms.md). The audit found no second live bug (the
  only other post-hoc required field, `source_name_th`, was already handled in `hub-dialog`); the
  rule was applied to all 12 forms via a shared helper anyway.
- ~~Deploy `updateDriverAccount` and verify the fix in a browser.~~ **Done on dev (2026-07-17)** —
  deployed to `logi-track-wrt-dev` and confirmed against a legacy driver doc: Save now names the
  blocking field instead of dying silently, and the email persists once `fullNameTh` is supplied.
  `updateDriverAccount` also deployed to prod (`logitrack-prod`) on 2026-07-17.
- **Ship the client fix to prod.** The prod deploy covered the Cloud Function only, which carries
  just the email-conflict message. Until a hosting build reaches `logitrack-prod`, prod admins still
  get the silent no-op this ADR exists to fix.
- Consider a read-only admin report of driver docs failing `driverSchema`, so the backlog of legacy
  docs is visible before an admin stumbles into one.
- ADR files are covered by the repo-wide `*.md` gitignore rule — commit with `git add -f`.

## Alternatives considered

- **Make `fullNameTh` optional (or `.default("")`) so legacy docs save.** Rejected: it is the name
  used on every report and invoice via `lib/driverName.ts`. This converts a loud form error into a
  silent billing-document defect, and contradicts the name-fields rule set in `CLAUDE.md` item #38.
- **Validate only dirty/touched fields, so an email edit checks only email.** Rejected: the schema
  states doc-level invariants, not per-edit ones. It would let a doc that violates the schema be
  written *by* the form that enforces it, and each edit would persist a slightly different subset of
  the rules.
- **Strip invalid fields server-side and save the rest (partial update).** Rejected: the admin
  believes the whole profile saved. It replaces a silent no-op with a silent *partial* write — the
  worse of the two, because it looks like success.
- **Backfill `fullNameTh` for legacy docs from `firstName`/`lastName`.** Rejected: those are English;
  a Thai name cannot be derived from them, and a fabricated value would flow onto invoices. Filling
  it is a human judgement, which is why decision #5 routes it through the form.
- **Toast a generic "Please check the form" on invalid.** Rejected: the whole failure was that the
  blocking field was invisible and off-screen. Naming the field is the entire value of the handler.
- **Write Firestore first, sync Auth after (so the profile saves even when the email collides).**
  Rejected: it is exactly the divergence fact #7 warns about — `drivers.email` would show the new
  address while the driver still signs in with the old one, or cannot sign in at all.

## Related

- Glossary: [../glossary.md](../glossary.md).
- `CLAUDE.md` item #38 (2026-06-13) — `fullNameTh` made required; `source_name_th`/`source_name_en`
  split; `lib/driverName.ts` resolution priority.
- Code: `logitrack-web/validate/driverSchema.ts`,
  `logitrack-web/features/drivers/components/EditDriverForm.tsx`,
  `logitrack-web/features/drivers/api/drivers.ts`,
  `logitrack-web/functions/src/triggers.ts` (`updateDriverAccount`).
- Conventions: [0000-adr-conventions.md](0000-adr-conventions.md).
