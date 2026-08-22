# ADR 0011 — Helper / training-day pay: data model

- **Status:** Accepted (2026-06-24)
- **Deciders:** Samart Kas (product owner), Claude
- **Area:** Driver Compensation (logitrack-web, Cloud Functions)

> **Provenance.** Originally `ADR-0001-helper-pay-data-model` in the BMAD driver-compensation
> planning pipeline. Migrated into the canonical set on 2026-08-09 when the BMAD output folder was
> retired — see [0017](0017-retire-bmad-wds-tooling.md). Content preserved; only cross-references and
> the metadata block were adapted. **Supersedes** two earlier in-code attempts (no ADR existed at the
> time). **Amended by** [0012](0012-helper-day-window.md) (redefines "one day").

## Context

The driver-compensation feature must pay drivers who ride along on another driver's task to **train or assist** ("helper"). This requirement was **not in the original PRD or epics** — it emerged during build and was implemented directly in code, changing shape three times. This ADR backfills the decision and locks the model captured in the 2026-06-24 grilling session.

### Decisions locked in the grilling session

1. **One helper per task** (not multiple).
2. **One flat rate** for both training and assisting (`helperDayRateThb`, default 400) — intent does not change pay.
3. **Admin review required** before a payout run is generated.
4. **Helper is assigned by the admin at task assignment** (primary); the **main driver may set it at check-in** as an urgent fallback when the admin hasn't.
5. **One helper-day = one actual working day** (pickup → last task done), counted by `tasks.date`, de-duplicated per calendar day, **excluding** the driver's own delivered-trip days.
6. **Recompute required** when the helper changes after a draft payout exists.

## Decision

Store the helper assignment **on the task**: `tasks.helperDriverIds` — an array of Auth UIDs, **capped at one element**.

- Helper pay = tasks where `helperDriverIds array-contains <authId>` and `tasks.date` ∈ round window, reduced to one helper-day per Bangkok calendar day, minus the driver's own delivered-trip days, × `helperDayRateThb`.
- Composite index: `tasks(helperDriverIds CONTAINS, date ASC)`.
- The array type is retained **only** for `array-contains` query/index compatibility; the application layer enforces a single element. Migrating to a scalar `helperDriverId` would require a new index and a data migration with no functional gain.

## Consequences

- **Positive:** single source of truth; admin-assign and check-in converge on one field; reuses existing tasks read/write rules and a single index; orchestrator query is one `array-contains`.
- **Negative / follow-ups:**
  - The array-capped-at-1 is a latent footgun — UI must enforce single and never present a multi-select affordance. (Mobile picker currently does `clear()` then add-one, which is correct for single but the surrounding chips/plural i18n imply multi and must be aligned.)
  - Helper assignment from the **admin portal at task-assignment time is not yet built**.
  - **Admin review/edit of the helper before payroll is not yet built** (`EditTripDetailsDialog` is read-only).
  - **Recompute-on-change after draft is not yet specified/implemented**.
  - Anyone who can edit a task can set its helper → fraud/collusion surface; admin review (decision 3) is the control. Needs auditability per NFR2.

These follow-ups are tracked in [Story 3.6](../driver-compensation/stories/story-3.6-helper-training-pay.md) (helper / training-day pay).

## Alternatives considered (and rejected)

1. **Separate `driver_helper_days` collection** (first attempt, `f31bde0`) — a standalone record per helper-day.
   - Rejected: duplicated the source of truth (a helper-day is already implied by a task), needed its own write path, rules, index, and admin UI, and could drift from the task it describes.

2. **`trip_records` with `status: "helper"`** (second attempt, `0d10735`) — model a helper-day as a pseudo trip.
   - Rejected: overloaded `trip_records` (delivery records) with a non-delivery concept, polluted the `TRIP_STATUS_ENUM` and every status-based query/report, and made "helper" indistinguishable from real trips in billing/monitoring.

3. **`tasks.helperDriverIds` (CHOSEN, `5dc4370` → `fbfebf0`)** — the helper belongs to the task it was performed on.
   - The task is the natural home: it already carries the driver, the date, and the assignment lifecycle. Admin-assign and driver-check-in both write the same field. No new collection, reuses the tasks rules and one composite index.

## Related

- Glossary: [../glossary.md](../glossary.md) — *Helper*, *Helper-day*, *Helper assignment*.
- Amended by [0012](0012-helper-day-window.md) (the "day" becomes a 12:00→11:59 work window).
- Driver-compensation epics/stories: [../driver-compensation/epics.md](../driver-compensation/epics.md), [Story 3.6](../driver-compensation/stories/story-3.6-helper-training-pay.md).
- Retirement of the BMAD pipeline that authored this: [0017](0017-retire-bmad-wds-tooling.md).
