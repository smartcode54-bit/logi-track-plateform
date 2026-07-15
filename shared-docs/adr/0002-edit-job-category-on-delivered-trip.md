# ADR 0002 — Edit หลัก/เสริม on a delivered trip (re-derive price via an atomic callable)

- **Status:** Accepted (2026-07-15) — implementation pending
- **Deciders:** Samart Kas (product owner), Claude
- **Area:** logitrack-web (`EditTripDetailsDialog`, driver monitor), Cloud Functions (billing), `tasks` + `trip_records`

## Context

`jobCategory` (`"PRIMARY" | "SUPPLEMENTARY"`, shown as **หลัก / เสริม**) decides which rate card a
trip bills against, whether fuel adjustment applies, and whether the price is frozen. Today it can be
set **once**, at task-assign time, and never corrected on a trip afterward. The product owner asked to
be able to **edit หลัก/เสริม on the trip_record detail** — the case that motivated this is a trip
billed เสริม (frozen) that should have been หลัก.

Facts established during grilling (all `path:line` in `logitrack-web/`):

1. **`jobCategory` is authored on the *task*, not the trip.** Per
   [ADR-0006](../../logitrack-web/_bmad-output/planning-artifacts/adr/ADR-0006-explicit-job-category-at-assign.md)
   it's an optional field on `tasks`, set by a หลัก/เสริม dropdown in the assign dialogs. At delivery,
   `tripBillingOnDelivered.ts:185-186` reads `t.jobCategory` off the **linked task** and stamps the
   resolved value onto `trip_records.jobCategory` (`:306`, `:383`). **The trip's category is a derived
   snapshot of the task's** — the task is the source of truth.

2. **That one field drives three things at billing time:** the rate-card query
   (`selectBillingRateEntry` filters `(entry.jobCategory ?? "PRIMARY") === jobCategory`,
   `core/billingCompute.ts:157`); whether the fuel multiplier applies (SUPPLEMENTARY skips it,
   `core/billingCompute.ts:241`, `:426`); and **whether the price is frozen** — any SUPPLEMENTARY trip
   also gets `billingManualOverride: true`, and the recompute guard
   `tripFrozen = billingManualOverride === true || jobCategory === "SUPPLEMENTARY"` makes
   `forceRecompute` skip it forever (`tripBillingOnDelivered.ts:126-129`).

3. **Neither edit surface can touch it today.** `EditTripDetailsDialog` (driver monitor) edits
   route / jobType / deliveredTimestamp / photos / stops and writes route changes back to the task,
   but has **no category field**. `EditBillingDialog` (income) edits **only the final price** and
   derives category silently (try-PRIMARY-then-SUPPLEMENTARY, `EditBillingDialog.tsx:131`). The income
   table renders หลัก/เสริม as a **read-only badge**.

4. **The existing recompute call is a no-op on already-billed trips.** `computeTripBillingSnapshot`
   invokes the core **without** `forceRecompute` (`tripBillingOnDelivered.ts:426`), so guard `:120`
   (`!forceRecompute && has billingEstimateThb`) skips any delivered trip that already has a price. The
   route-recompute wired into `EditTripDetailsDialog:518` therefore does nothing on billed trips (its
   own comment says "admin can backfill from Income page"). For a เสริม trip it is skipped twice over
   (also by the freeze guard `:127`). **Consequence:** re-deriving on a category change must get past
   *both* guards and write the task first, or it silently reverts / no-ops.

5. **`tripId` is minted client-side at check-in** (`loading_trip_repository.dart:128` writes
   `.doc(tripId)`), and the normal flow is **one task → one trip_record**. There is no unique
   constraint enforcing that.

6. **Firestore rules already let a web admin update `tasks` and `trip_records` directly** (no
   field-level `hasOnly` restriction, `firestore.rules:187`), which is how `EditTripDetailsDialog`
   already writes route changes back to the task. `computeTripBillingSnapshot` only checks
   `request.auth != null` — **not** admin role (`tripBillingOnDelivered.ts:408`).

**Owner-asserted invariants** (confirmed in grilling): (a) the price is the *output* of the category,
not an independent value the admin is preserving — changing category means the current price is
suspect; (b) a wrong เสริม price is fixed by an **explicit manual edit**, which is exactly the escape
hatch [ADR-0005](../../logitrack-web/_bmad-output/planning-artifacts/adr/ADR-0005-supplementary-trips.md)
consequences and ADR-0006 #6 ("no retroactive promotion") pointed to but never built.

## Decision

1. **Add a หลัก/เสริม selector to `EditTripDetailsDialog`** (driver monitor). Not to `EditBillingDialog`
   / income. Enabled **only for delivered trips** (there must be a billing snapshot to re-derive); for
   not-yet-delivered trips the selector is hidden/read-only and category is set the existing way (the
   assign dialog dropdown).

2. **Editing the category re-derives the price** from the target category's rate card. The category is
   the input; the price is recomputed output. This is **not** relabel-only.

3. **The change is atomic and goes through a new admin-only server callable** (working name
   `setTripJobCategory({ tripId, jobCategory })`). The callable:
   - verifies the caller is an **admin** (stricter than `computeTripBillingSnapshot`, which only checks
     `request.auth`);
   - loads the trip and its linked task; no-ops if the requested category equals the current one;
   - **computes the new price first** for the target category — single trips via
     `computeTripBillingFromParts`, multi-stop trips via `computeMultiDeliveryBilling`, mirroring the
     branching in `tryWriteBillingSnapshotFromTripData`;
   - **if no matching rate entry exists for the target category, throws (`HttpsError`) and writes
     nothing** — the trip keeps its old category and old price. "Fail loudly," no partial state;
   - **only on success**, writes `task.jobCategory` (the source of truth, ADR-0006) **and** the trip's
     billing snapshot (`billingEstimateThb`, lookup keys, `jobCategory`) **together**. It sets
     `billingManualOverride: true` when the resolved category is SUPPLEMENTARY (re-freeze) and
     **clears** `billingManualOverride` when PRIMARY (so the trip is no longer frozen).

4. **Both single-destination and multi-delivery trips are covered** in v1, reusing the two existing
   compute paths — no new billing math.

5. **The task stays the single source of truth; the trip stays a derived snapshot.** The callable
   defeats the recompute guards *for this one intentional edit only*; the guards themselves are
   **unchanged**, so bulk `forceRecompute` / fuel re-imports still never move a frozen เสริม price.

## Consequences

**Positive**
- The sanctioned manual escape hatch that ADR-0005/0006 described finally exists: a mis-categorized
  billed trip can be corrected, and the correction is durable because it writes the authoritative task
  field.
- Atomic compute-then-write means task and trip never disagree, and a missing target rate card can
  never silently wipe a good price.
- Reuses existing compute functions and the dialog that already owns task-write + recompute wiring.

**Negative / risks**
- Changing category **overwrites any prior manual price** on that trip (e.g. a หลัก price hand-set via
  `EditBillingDialog`). Accepted — it follows directly from "category is the input, price is derived."
  The callable only fires when the category actually changes, limiting the blast to intentional edits.
- **Blast radius is the task.** In the normal 1:1 task→trip flow this is exactly the edited trip. If a
  task ever has multiple *billable* trips (not enforced against — fact #5), they would re-derive from
  the new `task.jobCategory` on their next recompute; delivered+frozen siblings are skipped by the
  freeze guard, cancelled siblings have no billing, so real-world impact is minimal but non-zero.
- `EditBillingDialog`'s silent try-PRIMARY-then-SUPPLEMENTARY derivation (`EditBillingDialog.tsx:131`)
  is now inconsistent with an explicit task-level category. Out of scope here; noted as a future
  reconciliation.

**Follow-ups**
- Write the build spec via `/spec-new` before coding (per `CLAUDE.md` spec-driven workflow).
- i18n (en + th) for the selector label, the หลัก/เสริม options, and the "No PRIMARY/SUPPLEMENTARY
  rate for this route" error toast.
- Confirm the admin-role check helper available in Cloud Functions and apply it to the new callable.
- `billingCompute` is duplicated across `lib/billingCompute.ts` and `functions/src/core/billingCompute.ts`
  (must stay in sync) — the callable lives on the functions side and adds no new duplication.
- The เสริม report remark already handled by `generateDetailExcelBuffer` (ADR-0005 #9) needs no change.

## Alternatives considered

- **Relabel-only (keep the price, just change the tag).** Rejected by owner: the price is derived from
  the category, so a wrong category implies a suspect price; re-derivation is the point.
- **Client-only write (null the trip's price + freeze markers, then call `computeTripBillingSnapshot`).**
  Works around both guards without a new function, but cannot be atomic: if the target category has no
  rate card, the trip is left **unpriced** (dropped into Missing Billing) with label and price
  disagreeing. Rejected in favor of the atomic "change nothing on failure" behavior.
- **Extend `computeTripBillingSnapshot` with a `bypassFreeze`/`overrideCategory` flag.** Rejected:
  it still needs the task written first, and a client-passed freeze-bypass flag weakens the freeze
  invariant for every caller. A dedicated, admin-gated callable with a single explicit intent is safer.
- **Put the editor on the income page `EditBillingDialog`.** Rejected: owner chose the driver-monitor
  `EditTripDetailsDialog`, which already owns the task-write + recompute machinery.
- **Introduce a trip-level category override with new billing-engine precedence (trip wins over task).**
  Rejected: adds a new precedence rule and invariant to the billing engine and contradicts ADR-0006's
  "task value is authoritative." Writing the task keeps one source of truth.
- **Allow editing category on any status, including pre-delivery.** Rejected for v1: pre-delivery there
  is no price to derive and the assign dialog already sets category; scope stays "fix a billed trip."

## Related

- Glossary: [../glossary.md](../glossary.md) — [[jobCategory (หลัก/เสริม)]], [[Frozen price]].
- Supersedes nothing; **extends** ADR-0006 (adds a correction path for an already-billed trip) and
  realizes the manual-edit escape hatch noted in ADR-0005 consequences.
- [ADR-0005 — Supplementary trips](../../logitrack-web/_bmad-output/planning-artifacts/adr/ADR-0005-supplementary-trips.md),
  [ADR-0006 — Explicit หลัก/เสริม at assign](../../logitrack-web/_bmad-output/planning-artifacts/adr/ADR-0006-explicit-job-category-at-assign.md).
