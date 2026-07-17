# Architecture Decision Records (ADRs)

The single canonical home for architecture decisions on LogiTrack. Read
[0000-adr-conventions.md](0000-adr-conventions.md) before adding one — it defines numbering, filename,
required sections, status vocabulary, and the glossary rule.

Companion glossary: [../glossary.md](../glossary.md).

## Canonical ADRs (`shared-docs/adr/`)

| # | Title | Status |
|---|-------|--------|
| [0000](0000-adr-conventions.md) | ADR & glossary conventions (one standard) | Accepted 2026-07-15 |
| [0001](0001-checkin-time-on-trip-records.md) | Denormalize check-in time onto `trip_records`; drive the Driver Monitor "Check-in" column by it | Accepted 2026-07-15 (impl pending) |
| [0002](0002-edit-job-category-on-delivered-trip.md) | Edit หลัก/เสริม on a delivered trip (re-derive price via an atomic admin callable) | Accepted 2026-07-15 (impl pending) |
| [0003](0003-edit-forms-fail-loudly-on-legacy-docs.md) | Edit forms fail loudly on legacy docs (never relax the schema to make old data saveable) | Accepted 2026-07-17 (dev verified, prod pending) |
| [0004](0004-shared-oninvalid-handler-for-all-forms.md) | One shared `onInvalid` handler for every form (close the class, not the instance) | Accepted 2026-07-17 (dev verified) |

**Next free number: `0005`.** Take the next unused number, never reuse or renumber (see
[conventions](0000-adr-conventions.md)).

## Legacy — BMAD, module-scoped (`logitrack-web/_bmad-output/planning-artifacts/adr/`)

Historical records produced by the BMAD planning pipeline for the **driver-compensation** module.
They are a **separate numbering namespace** — this table's `ADR-0001` is *not* the same document as
canonical `0001` above. Kept in place (they cross-link each other and are cited by BMAD
epics/stories); not renumbered or moved. New cross-cutting decisions go in the canonical set above.

| # | Title | Status |
|---|-------|--------|
| [ADR-0001](../../logitrack-web/_bmad-output/planning-artifacts/adr/ADR-0001-helper-pay-data-model.md) | Helper / training-day pay — data model | Accepted 2026-06-24 |
| [ADR-0002](../../logitrack-web/_bmad-output/planning-artifacts/adr/ADR-0002-helper-day-window.md) | Helper / training-day pay — the "day" is a 12:00→11:59 work window | Accepted 2026-06-26 |
| [ADR-0003](../../logitrack-web/_bmad-output/planning-artifacts/adr/ADR-0003-payroll-lineitem-breakdown.md) | Payroll line items carry a self-contained breakdown | Accepted 2026-06-26 |
| [ADR-0004](../../logitrack-web/_bmad-output/planning-artifacts/adr/ADR-0004-cash-advance.md) | Cash advance (เบิกล่วงหน้า) — recorded, deducted next pay round | Accepted 2026-06-26 |
| [ADR-0005](../../logitrack-web/_bmad-output/planning-artifacts/adr/ADR-0005-supplementary-trips.md) | Supplementary trips (เที่ยวเสริม) — jobCategory, separate rate card, frozen pricing | Accepted 2026-06-30 |
| [ADR-0006](../../logitrack-web/_bmad-output/planning-artifacts/adr/ADR-0006-explicit-job-category-at-assign.md) | Explicit หลัก/เสริม selection at task assign time (supersedes ADR-0005 #3) | Accepted 2026-07-02 |
