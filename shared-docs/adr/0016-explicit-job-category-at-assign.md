# ADR 0016 — Explicit หลัก/เสริม selection at task assign time

- **Status:** Accepted (2026-07-02)
- **Deciders:** Samart Kas (product owner), Claude
- **Area:** Billing / Task Assignment (logitrack-web, Cloud Functions)

> **Provenance.** Originally `ADR-0006-explicit-job-category-at-assign` in the BMAD driver-compensation
> planning pipeline. Migrated into the canonical set on 2026-08-09 when the BMAD output folder was
> retired — see [0017](0017-retire-bmad-wds-tooling.md). Content preserved; only cross-references and
> the metadata block were adapted. **Supersedes** [0015](0015-supplementary-trips.md) decision #3 only
> ("`jobCategory` is DERIVED, not manually selected… no หลัก/เสริม selector in the assign dialogs").

## Context

[0015](0015-supplementary-trips.md) shipped `jobCategory` (PRIMARY/SUPPLEMENTARY) as a value **derived at billing time**: when a trip is delivered, the system probes the PRIMARY rate card first and falls back to the SUPPLEMENTARY rate card only if no PRIMARY entry matches for that customer+hub+destination+vehicle+date. Admins had no way to state up front, when creating the task, whether a route was หลัก or เสริม.

In practice the operator setting the pickup/dropoff (source hub → destination) already knows which kind of job this is — it doesn't need to be inferred after the fact from whichever rate card happens to have a matching row. The user asked to separate หลัก/เสริม starting from the point of setting the pickup/dropoff, with เสริม pricing still keyed off the pickup/dropoff (i.e. still resolved via the existing SUPPLEMENTARY rate card, not a new flat-price mechanism) and never changing once billed (existing freeze behavior).

## Decision

1. **`jobCategory` returns to `tasks`** as an optional field (`taskSchema.ts`), selected by a หลัก/เสริม dropdown in both the First-Mile and Line-Haul assign dialogs, placed next to source hub / destination. Default: PRIMARY (หลัก).
2. **Explicit task value is authoritative at billing time.** In `tripBillingOnDelivered.ts`, when the linked task has `jobCategory === "SUPPLEMENTARY"`, the PRIMARY probe is skipped entirely — billing goes straight to the SUPPLEMENTARY rate card. If no SUPPLEMENTARY rate entry matches, billing fails loudly (`"No rate: ..."`), it does **not** silently fall back to PRIMARY: the operator explicitly marked this เสริม, so a missing เสริม rate is a data problem to fix, not paper over.
3. **Legacy tasks (no explicit `jobCategory`) and tasks explicitly marked PRIMARY keep the exact [0015](0015-supplementary-trips.md) derivation**: try PRIMARY, fall back to SUPPLEMENTARY if no PRIMARY entry matches. This makes the change purely additive — no backfill/migration of existing task documents is required.
4. **Pricing mechanism is unchanged.** เสริม pricing still comes from the SUPPLEMENTARY `customer_rate_entries` rate card (keyed by customerId+hubId+destinationCode+vehicleClass+effectiveFrom) — there is no flat/manual price entered directly on the task.
5. **Freeze behavior is unchanged.** Any trip that resolves to `jobCategory: "SUPPLEMENTARY"` — however it got there — still gets `billingManualOverride: true` and is skipped by `forceRecompute` forever ([0015](0015-supplementary-trips.md) decision #5, `tripFrozen` guard in `tripBillingOnDelivered.ts`).
6. **No retroactive promotion.** If a PRIMARY rate card entry later appears for a route that was explicitly billed as เสริม, already-delivered trips for that route stay เสริม forever — the freeze guard doesn't distinguish "explicitly chosen" from "derived" เสริม.

## Consequences

- `createOrUpdateTask` (Cloud Function) gains an optional `jobCategory` request field, written to the task doc (defaulted to PRIMARY).
- [0015](0015-supplementary-trips.md) decisions #1, #2, #4, #5, #7–#12 (schema location note aside, separate rate card, freeze mechanism, report display rules) are **unaffected**.
- Operators can now get เสริม pricing without depending on the primary rate card missing a matching row — useful when a customer's primary card *does* have an overlapping entry that shouldn't apply to this particular ad-hoc trip.

## Alternatives considered

- **Flat price entered directly on the task at assign time** — rejected per user: เสริม pricing should still reference the pickup/dropoff via the existing SUPPLEMENTARY rate card, not a new per-task price field.
- **Retroactively re-derive category when rate cards change** — rejected, consistent with [0015](0015-supplementary-trips.md)'s existing "price is agreed and fixed" stance.

## Related

- Glossary: [../glossary.md](../glossary.md) — *jobCategory (หลัก/เสริม)*.
- Supersedes [0015](0015-supplementary-trips.md) decision #3 (derived → explicit at assign time).
- Also recorded in canonical ADR [0002](0002-edit-job-category-on-delivered-trip.md), which lets an admin correct หลัก/เสริม on an already-delivered trip.
- Retirement of the BMAD pipeline that authored this: [0017](0017-retire-bmad-wds-tooling.md).
