# ADR-0006: Explicit หลัก/เสริม selection at task assign time (supersedes ADR-0005 decision #3)

- **Status:** Accepted (2026-07-02)
- **Context owners:** Billing / Task Assignment
- **Supersedes:** [ADR-0005](./ADR-0005-supplementary-trips.md), decision #3 only ("`jobCategory` is DERIVED, not manually selected... no หลัก/เสริม selector in the assign dialogs")
- **Related:** `taskSchema.ts`, `tripBillingOnDelivered.ts`, `createOrUpdateTask` (`functions/src/tasks.ts`)

## Context

ADR-0005 shipped `jobCategory` (PRIMARY/SUPPLEMENTARY) as a value **derived at billing time**: when a trip is delivered, the system probes the PRIMARY rate card first and falls back to the SUPPLEMENTARY rate card only if no PRIMARY entry matches for that customer+hub+destination+vehicle+date. Admins had no way to state up front, when creating the task, whether a route was หลัก or เสริม.

In practice the operator setting the pickup/dropoff (source hub → destination) already knows which kind of job this is — it doesn't need to be inferred after the fact from whichever rate card happens to have a matching row. The user asked to separate หลัก/เสริม starting from the point of setting the pickup/dropoff, with เสริม pricing still keyed off the pickup/dropoff (i.e. still resolved via the existing SUPPLEMENTARY rate card, not a new flat-price mechanism) and never changing once billed (existing freeze behavior).

## Decision

1. **`jobCategory` returns to `tasks`** as an optional field (`taskSchema.ts`), selected by a หลัก/เสริม dropdown in both the First-Mile and Line-Haul assign dialogs, placed next to source hub / destination. Default: PRIMARY (หลัก).
2. **Explicit task value is authoritative at billing time.** In `tripBillingOnDelivered.ts`, when the linked task has `jobCategory === "SUPPLEMENTARY"`, the PRIMARY probe is skipped entirely — billing goes straight to the SUPPLEMENTARY rate card. If no SUPPLEMENTARY rate entry matches, billing fails loudly (`"No rate: ..."`), it does **not** silently fall back to PRIMARY: the operator explicitly marked this เสริม, so a missing เสริม rate is a data problem to fix, not paper over.
3. **Legacy tasks (no explicit `jobCategory`) and tasks explicitly marked PRIMARY keep the exact ADR-0005 derivation**: try PRIMARY, fall back to SUPPLEMENTARY if no PRIMARY entry matches. This makes the change purely additive — no backfill/migration of existing task documents is required.
4. **Pricing mechanism is unchanged.** เสริม pricing still comes from the SUPPLEMENTARY `customer_rate_entries` rate card (keyed by customerId+hubId+destinationCode+vehicleClass+effectiveFrom) — there is no flat/manual price entered directly on the task.
5. **Freeze behavior is unchanged.** Any trip that resolves to `jobCategory: "SUPPLEMENTARY"` — however it got there — still gets `billingManualOverride: true` and is skipped by `forceRecompute` forever (ADR-0005 decision #5, `tripFrozen` guard in `tripBillingOnDelivered.ts`).
6. **No retroactive promotion.** If a PRIMARY rate card entry later appears for a route that was explicitly billed as เสริม, already-delivered trips for that route stay เสริม forever — the freeze guard doesn't distinguish "explicitly chosen" from "derived" เสริม.

## Consequences

- `createOrUpdateTask` (Cloud Function) gains an optional `jobCategory` request field, written to the task doc (defaulted to PRIMARY).
- ADR-0005 decisions #1, #2, #4, #5, #7–#12 (schema location note aside, separate rate card, freeze mechanism, report display rules) are **unaffected**.
- Operators can now get เสริม pricing without depending on the primary rate card missing a matching row — useful when a customer's primary card *does* have an overlapping entry that shouldn't apply to this particular ad-hoc trip.

## Alternatives considered

- **Flat price entered directly on the task at assign time** — rejected per user: เสริม pricing should still reference the pickup/dropoff via the existing SUPPLEMENTARY rate card, not a new per-task price field.
- **Retroactively re-derive category when rate cards change** — rejected, consistent with ADR-0005's existing "price is agreed and fixed" stance.
