# ADR 0010 — jobCategory must live on the trip independently of billing (seed at creation, display falls back to the task, fail loud)

- **Status:** Accepted (2026-08-09) — implemented (code complete, runtime QA pending); spec [`shared-docs/specs/job-category-carried-on-trip.md`](../specs/job-category-carried-on-trip.md)
- **Deciders:** Samart Kas (product owner), Claude
- **Area:** logitrack-mobile (check-in / loading trip creation), logitrack-web (Income, Billing Document, `EditTripDetailsDialog`), Cloud Functions (`tasks`, `tripBillingOnDelivered`), backfill script, `tasks` + `trip_records`

## Context

The product owner reported: **`jobCategory` (หลัก/เสริม) sometimes does not display, even though it is set on every assignment.** Grilling traced this to a data-flow gap, not a lost write. `path:line` are in `logitrack-web/` unless noted.

1. **The task always carries `jobCategory`; the *display* never reads it from the task.** The assign
   callable writes `jobCategory: data.jobCategory === "SUPPLEMENTARY" ? "SUPPLEMENTARY" : "PRIMARY"`
   (`functions/src/tasks.ts:134`) and the assign dialogs always send it
   (`features/tasks/components/FirstMileTaskDialog.tsx:235`). So `tasks.jobCategory` is reliably
   present. **But every display surface reads from `trip_records`, not the task.**

2. **The only bridge from `tasks.jobCategory` → `trip_records.jobCategory` is the billing snapshot.**
   The trip field is written only inside `tryWriteBillingSnapshotFromTripData`
   (`functions/src/tripBillingOnDelivered.ts:322` multi-delivery, `:403` single-delivery), plus the
   `setTripJobCategory` correction callable (`:686`/`:691`) and the one-time backfill script.
   **Mobile never writes `jobCategory` to the trip** — a grep across `logitrack-mobile` finds zero
   references, so the trip is born without it.

3. **That bridge has no Firestore trigger and is invoked best-effort, fire-and-forget.** The DB region
   supports no document triggers, so nothing re-runs the derivation automatically. Mobile calls
   `computeTripBillingSnapshot` after delivery inside a `try { … } catch (_) {}` whose comment reads
   *"failure does not block delivery"* (`logitrack-mobile/lib/features/delivery_phase/data/repositories/delivery_trip_repository.dart:103-109`, and again `:229-234`). No retry.

4. **The bridge early-returns and writes nothing on many normal conditions** — in each case the trip
   keeps *no* `jobCategory` while the task had one:

   | condition | line |
   |---|---|
   | trip not delivered yet | `tripBillingOnDelivered.ts:123` |
   | already billed and not `forceRecompute` | `:127` |
   | frozen (เสริม/override) and `forceRecompute` | `:134` |
   | trip missing `taskId` | `:139` |
   | linked task not found | `:154` |
   | task has no linked customer | `:182` |
   | **no matching rate card ("No rate")** | `:368-385` |
   | multi-delivery with < 2 delivered stops | `:287` |

   The "No rate" path is the one that fires most in practice, matching the reported *"บางครั้ง"*.

5. **When the trip field is absent, the display either shows nothing or silently lies:**
   - **Income** reads `d.jobCategory` raw with no task fallback
     (`app/app/accounting/income/page.tsx:337-342`); undefined falls through the badge's `else` branch
     to a literal `"—"` (`:1235-1237`). This is the "ไม่แสดง" the owner sees.
   - **Billing Document** — the real invoice — renders
     `trip.jobCategory === "SUPPLEMENTARY" ? เสริม : หลัก` (`app/app/accounting/billing-document/page.tsx:665-673`).
     Undefined silently becomes **หลัก on a customer invoice**. This is worse than blank: it is a
     silent mis-classification with money attached.
   - **`EditTripDetailsDialog`** defaults `trip.jobCategory ?? "PRIMARY"` (`features/drivers/components/EditTripDetailsDialog.tsx:112`, `:261`), and the Zod
     `tripRecordSchema` has `jobCategory: …default("PRIMARY")` (`validate/tripRecordSchema.ts:73`), so
     any parse coerces missing → หลัก. The Driver Monitor row model does not carry the field at all
     (`features/drivers/hooks/useDriverMonitor.ts`).

6. **A latent second hazard on the assign write.** `createOrUpdateTask` coerces `jobCategory` to
   `PRIMARY` on *every* write (`tasks.ts:134`). On an **update** that omits `jobCategory`, the value is
   coerced to `"PRIMARY"` *before* the strip-undefined filter (`:184`), so the filter can't protect it —
   an unrelated task edit would silently reset เสริม → หลัก. The UI always sends `jobCategory` today, so
   this is latent, not active, but it undermines "task is the source of truth."

7. **The Billing Document already joins trip → task**, building a `taskMap` keyed by `taskId`
   (`features/accounting/api/billing.ts:813-822`) and reading `taskInfo` per row (`:888`, `:976`). A
   fallback to `task.jobCategory` on that surface costs one extra field in the projection.

8. **The existing backfill script is unsafe.** `scripts/backfill-job-category.mjs` sets `PRIMARY` on any
   `trip_records` doc lacking a value, on the stated assumption that *all* such legacy trips predate the
   supplementary feature. That assumption is false: supplementary legacy trips exist (a supp-fuel
   data-fix on 2026-07-16 corrected real เสริม trips), so blind PRIMARY can permanently mislabel them.

**Owner-asserted framing** (confirmed in grilling): `tasks.jobCategory` is the source of truth
(consistent with [ADR 0002](0002-edit-job-category-on-delivered-trip.md) and [ADR 0016](0016-explicit-job-category-at-assign.md)); `trip_records.jobCategory`
is a **denormalized cache**; a cache that only ever gets written as a by-product of a fragile,
retry-less, early-returning price computation is the root defect. On an invoice, the system must never
*guess* หลัก.

## Decision

1. **`trip_records.jobCategory` is a denormalized cache of the authoritative `tasks.jobCategory`, and it
   must be populated independently of price computation.**
   - **Seed at trip creation (mobile).** At check-in / loading-trip creation, the mobile client copies
     `jobCategory` from the task it is fulfilling onto the new `trip_record`. When the driver created the
     job themselves and there is no task, it defaults to `PRIMARY` explicitly.
   - **Billing keeps refreshing it.** The writes at `tripBillingOnDelivered.ts:322`/`:403` stay; on a
     successful billing they self-heal the cache to the resolved task value. Billing is no longer the
     *only* writer, so its early-returns no longer leave the trip blank.
   - **`setTripJobCategory` remains the correction path** (ADR-0002), unchanged.

2. **Display reads with a fail-loud fallback chain, and never silently assumes หลัก.** Order:
   `trip.jobCategory` → **`task.jobCategory` (source of truth)** → a **loud neutral marker**
   (e.g. "ตรวจสอบ / —"), never a defaulted หลัก badge. Remove the silent defaults: the `else → หลัก`
   branch on Billing Document (`:669`), the `"—"`-only rendering on Income, and `?? "PRIMARY"` in
   `EditTripDetailsDialog`. On Billing Document the task is already loaded (fact 7) — add `jobCategory`
   to the `taskMap` projection and read through the chain.

3. **Fix the assign write to stop coercing.** `createOrUpdateTask` must **omit** `jobCategory` from the
   write when the client did not send it (write only when explicitly present), instead of coercing to
   `PRIMARY` (`tasks.ts:134`). On create with nothing sent, apply the `PRIMARY` default once; on update
   with nothing sent, leave the stored value untouched.

4. **Backfill existing trips by copying from the task**, not blindly PRIMARY: for each `trip_records`
   doc lacking `jobCategory`, look up its `taskId` and copy `task.jobCategory`; default `PRIMARY` only
   when the task itself has no value. Retire / rewrite `scripts/backfill-job-category.mjs` accordingly.

5. **Scope is the full belt-and-suspenders** (owner's choice): mobile seed (1) + display fallback (2) +
   assign-write fix (3) + copy-from-task backfill (4). The seed only reaches trips created by *new* APK
   builds (mobile change ships with the next release per ADR-0007), so the display fallback (2) is the
   safety net that also makes old-client and already-created trips correct. Neither half alone closes
   the gap; together they do.

## Consequences

**Positive**
- A trip becomes self-describing from birth, and even when it isn't, the display resolves to the
  authoritative task value — so the invoice can no longer print หลัก for a เสริม job by omission.
- Decouples "record the category" from "compute the price": a "No rate" billing failure no longer also
  loses the category.
- Fail-loud on both-missing matches the project's established stance (ADR-0003/0004): the system surfaces
  an unknown rather than guessing a billable classification.

**Negative / risks**
- **Denormalization drift window.** If an admin edits `tasks.jobCategory` *after* the trip was seeded but
  *before* delivery, the trip's cached copy is stale until the next billing refresh (self-heals on a
  successful billing) or a `setTripJobCategory` correction. Reconciliation rule of record: **task is the
  source of truth; the trip copy is (a) seeded at creation, (b) refreshed at billing, (c) corrected by
  `setTripJobCategory`**; display always prefers the trip value but falls back to the task, so a stale
  trip copy is at worst momentarily out of date, never invisible.
- The display fallback re-introduces a trip→task read on display surfaces. Free on Billing Document
  (already joined); Income and the dialog may need a small task lookup where they don't already have one.
- Backfill must read each trip's task; on trips whose task was deleted, fall back to `PRIMARY` and log,
  don't guess เสริม.

**Follow-ups**
- Write the build spec via `/spec-new` before coding (per `CLAUDE.md` spec-driven workflow).
- Confirm Income actually has (or cheaply can load) the linked task for its fallback; if not, decide
  whether Income relies on the seed + backfill alone.
- i18n (en + th) for the loud neutral "unknown / ตรวจสอบ" marker on all three surfaces.
- Verify the mobile seed covers both single-delivery and multi-delivery trip creation.
- The DB has no local admin access and no Firestore triggers (see project memory); the copy-from-task
  backfill must run as an admin callable or from the console, not a local `node` script hitting Firestore
  directly.

## Alternatives considered

- **Only display fallback (read `task.jobCategory` when the trip lacks it), no mobile seed.** Rejected as
  the sole fix: leaves the trip permanently not self-describing and keeps every reader dependent on
  loading the task. Kept as *part* of the solution (the safety net), not the whole.
- **Only harden the billing bridge** (retry the callable, or move the `jobCategory` write above every
  rate-lookup early-return so it always lands). Rejected as the sole fix: still depends on the billing
  callable running at all (fire-and-forget, no trigger), and does nothing for not-yet-delivered trips.
  A cheaper variant — writing the category right after loading the task, before the rate-lookup returns —
  is a reasonable *supplement* but not a substitute for the seed + fallback.
- **Keep the silent `?? "PRIMARY"` / `else → หลัก` display defaults.** Rejected: on an invoice this is a
  silent mis-classification with money attached; it violates the fail-loud posture of ADR-0003/0004.
- **Keep the blindly-PRIMARY backfill.** Rejected: supplementary legacy trips demonstrably exist, so it
  would permanently mislabel real เสริม trips as หลัก.
- **Make the trip the source of truth (trip wins over task).** Rejected: contradicts ADR 0002 / ADR 0016
  ("task value is authoritative") and would add a new precedence rule to the billing engine.

## Related

- Glossary: [../glossary.md](../glossary.md) — [[jobCategory (หลัก/เสริม)]], [[Frozen price]].
- **Extends** [ADR-0002 — Edit หลัก/เสริม on a delivered trip](0002-edit-job-category-on-delivered-trip.md)
  (which established task = source of truth, trip = derived snapshot). This ADR makes that snapshot
  reliable end-to-end rather than a billing by-product.
- Mobile release sequencing constraint: [ADR-0007 — Mobile forced-update pipeline](0007-mobile-forced-update-pipeline.md).
- Fail-loud posture: [ADR-0003](0003-edit-forms-fail-loudly-on-legacy-docs.md), [ADR-0004](0004-shared-oninvalid-handler-for-all-forms.md).
- Origins of the field: [ADR 0015 — Supplementary trips](0015-supplementary-trips.md) and
  [ADR 0016 — Explicit หลัก/เสริม at assign](0016-explicit-job-category-at-assign.md). (These were migrated
  from the BMAD namespace on 2026-08-09 — see [ADR 0000](0000-adr-conventions.md) / the ADR index — and are
  the "ADR-0005/0006" the billing code comments still cite; a later cleanup can retag those comments.)
