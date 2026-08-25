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
| [0003](0003-edit-forms-fail-loudly-on-legacy-docs.md) | Edit forms fail loudly on legacy docs (never relax the schema to make old data saveable) | Accepted 2026-07-17 (shipped to prod) |
| [0004](0004-shared-oninvalid-handler-for-all-forms.md) | One shared `onInvalid` handler for every form (close the class, not the instance) | Accepted 2026-07-17 (dev verified) |
| [0005](0005-truck-plate-filter-billing-document-driver-monitor.md) | Filter by truck licence plate on Billing Document and Driver Monitor (identity, provenance, and the invoice guard) | Accepted 2026-07-20 (impl local, pending verify) |
| [0006](0006-origin-destination-filter-driver-monitor.md) | Filter by origin and destination on Driver Monitor (place identity over free text) | Accepted 2026-07-20 (impl local, pending verify) |
| [0007](0007-mobile-forced-update-pipeline.md) | Mobile forced-update pipeline (announce with the build, enforce with a button) | Accepted 2026-07-28 (impl local, pending verify) |
| [0008](0008-standby-billing-visibility-and-recompute-semantics.md) | Standby billing: self-contained records, a service-completion billing date, and draft-only recompute | Accepted 2026-08-04 (impl pending) |
| [0009](0009-multiple-rate-rounds-within-one-billing-period.md) | Multiple rate rounds inside one billing period (fuel bands, half-open dates, immutable announcements) | Accepted 2026-08-04 (impl pending) |
| [0010](0010-job-category-carried-on-trip-independent-of-billing.md) | jobCategory must live on the trip independently of billing (seed at creation, display falls back to the task, fail loud) | Accepted 2026-08-09 (impl local, pending verify) |
| [0011](0011-helper-pay-data-model.md) | Helper / training-day pay — data model (`tasks.helperDriverIds`) | Accepted 2026-06-24 |
| [0012](0012-helper-day-window.md) | Helper / training-day pay — the "day" is a 12:00→11:59 work window | Accepted 2026-06-26 |
| [0013](0013-payroll-lineitem-breakdown.md) | Payroll line items carry a self-contained breakdown | Accepted 2026-06-26 |
| [0014](0014-cash-advance.md) | Cash advance (เบิกล่วงหน้า) — recorded, deducted next pay round | Accepted 2026-06-26 (impl pending) |
| [0015](0015-supplementary-trips.md) | Supplementary trips (เที่ยวเสริม) — jobCategory, separate rate card, frozen pricing | Accepted 2026-06-30 |
| [0016](0016-explicit-job-category-at-assign.md) | Explicit หลัก/เสริม selection at task assign time (supersedes 0015 #3) | Accepted 2026-07-02 |
| [0017](0017-retire-bmad-wds-tooling.md) | Retire the BMAD/WDS tooling vendored into the repo | Accepted 2026-08-09 |
| [0018](0018-driver-self-download-trip-photos.md) | Driver self-download of trip evidence photos to the phone gallery (bulk, workflow-ordered, own trips only; incident photos added by amendment) | Accepted 2026-08-22 (shipped 2026-08-23) |
| [0019](0019-app-screenshots-as-mandatory-evidence.md) | Customer-app screenshots as mandatory, un-overlaid evidence at check-in, loading, and delivery | Accepted 2026-08-23 (impl pending) |
| [0020](0020-buzzebee-last-mile-distribution.md) | Buzzebee last-mile distribution domain — order as SSOT, per-SKU items, conservation of goods, subdistrict zones, semi-auto allocation on Google Maps | Accepted 2026-08-25 (impl pending) |
| [0021](0021-transactional-email-smtp-workspace.md) | Transactional email via a Google Workspace SMTP callable (greenfield; provider-swappable; server-controlled recipients) | Accepted 2026-08-25 (impl pending) |
| [0022](0022-phone-gps-fallback-for-trucks-without-device.md) | Phone-GPS fallback for trucks without a hardware GPS device (source-tagged `vehicle_locations`, active-route only) | Accepted 2026-08-25 (impl pending) |
| [0023](0023-buzzebee-distribution-billing.md) | Buzzebee distribution billing — per-pack × zone × tier + per-trip minimum + fuel surcharge (from approved quote QT-202608-001) | Accepted 2026-08-25 (impl pending) |

**Next free number: `0024`.** Take the next unused number, never reuse or renumber (see
[conventions](0000-adr-conventions.md)).

## Migrated from BMAD (formerly `logitrack-web/_bmad-output/planning-artifacts/adr/`)

The six module-scoped ADRs the BMAD pipeline produced for the **driver-compensation** module were
**migrated into the canonical set above as 0011–0016** on 2026-08-09, and the BMAD output folder was
retired — see [0017](0017-retire-bmad-wds-tooling.md). Mapping: `ADR-0001`→`0011`, `ADR-0002`→`0012`,
`ADR-0003`→`0013`, `ADR-0004`→`0014`, `ADR-0005`→`0015`, `ADR-0006`→`0016`. Their planning companions
(PRD, architecture, epics, stories, decision-log) now live under
[`../driver-compensation/`](../driver-compensation/). There is no longer a second ADR namespace.
