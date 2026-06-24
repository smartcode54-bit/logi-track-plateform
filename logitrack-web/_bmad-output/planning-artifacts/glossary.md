# Driver Compensation — Glossary

Captured during the helper-pay grilling session, 2026-06-24. Definitions here are authoritative for `epics.md`, `architecture.md`, the compute engine, and all UI strings.

## Helper / Training-day pay

- **Helper (ผู้ช่วย)** — a driver who rides along on another driver's task to **assist or to train**, without being the assigned (main) driver of that task. "Helper" and "trainee/training" are the **same role for pay purposes** — there is no separate rate or rule; the intent (training vs assisting) does not change compensation. UI may say "Helpers (training / assisting)" but the model treats them as one concept.

- **Main driver (คนขับหลัก)** — the driver assigned to a task (`tasks.driverId`). Earns trip pay for the task, not helper pay.

- **Helper assignment** — the act of designating the helper for a task. Stored on `tasks.helperDriverIds` (Auth UID). **Exactly one helper per task** (see [ADR-0001](adr/ADR-0001-helper-pay-data-model.md)); the field remains an array only for Firestore `array-contains` index compatibility, and is capped at length 1.
  - **Primary path:** the **admin** sets the helper when assigning the task in the admin portal.
  - **Fallback path:** if the task is urgent and the admin has not assigned a helper, the **main driver** may select one at check-in (mobile).
  - **Review:** the admin can review and edit the helper before the payout run is generated.

- **Helper-day (วันทำงานของผู้ช่วย)** — **one** unit of helper pay = **one actual working day** on which a driver acted as helper, regardless of how many tasks they helped on that day. "One day" = the span from picking up the first task until the last task of that day is finished. Counted by the task's **actual work date** (`tasks.date`, Asia/Bangkok day key), de-duplicated to one per calendar day.

- **Eligible helper-day** — a helper-day on which the driver had **no own delivered trip** (`trip_records` for that driver on the same day). A driving day always pays as trips, never additionally as a helper-day.

- **Helper day rate (`helperDayRateThb`)** — flat THB per eligible helper-day from the active compensation config (default 400). One rate for both training and assisting.

- **`HELPER_PAY`** — the payroll line-item category (type `EARNING`) emitted by the orchestrator for helper pay. Settled in the round window that contains the helper-day.

## Related (already defined elsewhere)

- **Round (R1 / R2)** — semi-monthly pay window; R1 = days 1–15, R2 = 16–end. Helper-days are assigned to a round by their actual work date, same as trips.
- **Recompute** — re-running `generateDriverPayoutRun` for a period+round. Overwrites `DRAFT` payouts only; an `APPROVED` payout is corrected via a post-approval adjustment ([Story 3.4](epics.md)).
