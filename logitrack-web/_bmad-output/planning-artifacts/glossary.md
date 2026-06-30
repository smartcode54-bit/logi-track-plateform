# Driver Compensation — Glossary

Captured during the helper-pay grilling sessions, 2026-06-24 and 2026-06-26. Definitions here are authoritative for `epics.md`, `architecture.md`, the compute engine, and all UI strings.

## Helper / Training-day pay

- **Helper (ผู้ช่วย)** — a driver who rides along on another driver's task to **assist or to train**, without being the assigned (main) driver of that task. "Helper" and "trainee/training" are the **same role for pay purposes** — there is no separate rate or rule; the intent (training vs assisting) does not change compensation. UI may say "Helpers (training / assisting)" but the model treats them as one concept.

- **Main driver (คนขับหลัก)** — the driver assigned to a task (`tasks.driverId`). Earns trip pay for the task, not helper pay.

- **Helper assignment** — the act of designating the helper for a task. Stored on `tasks.helperDriverIds` (Auth UID). **Exactly one helper per task** (see [ADR-0001](adr/ADR-0001-helper-pay-data-model.md)); the field remains an array only for Firestore `array-contains` index compatibility, and is capped at length 1.
  - **Primary path:** the **admin** sets the helper when assigning the task in the admin portal.
  - **Fallback path:** if the task is urgent and the admin has not assigned a helper, the **main driver** may select one at check-in (mobile).
  - **Review:** the admin can review and edit the helper before the payout run is generated.

- **Helper-day (วันทำงานของผู้ช่วย)** — **one** unit of helper pay = **one work window** on which a driver acted as helper, regardless of how many tasks they helped on in that window. The "day" is **not** the calendar day; it is the **work window 12:00:00 of day D → 11:59:59 of day D+1** (Asia/Bangkok), **keyed to day D**, so a shift that crosses midnight is still one day (see [ADR-0002](adr/ADR-0002-helper-day-window.md)). The window is anchored by the main driver's task **`checkInAt`**; if `checkInAt` is absent, fall back to **`tasks.date`** at 12:00. De-duplicated to one per window key. A helper still earns the day even if **no trip was delivered** in the window (check-in is proof of work).

- **Window key (D)** — the day the work window starts (its 12:00 side). Computed as `bangkokDateKey(checkInAt − 12h)`. Determines both de-duplication and **round** membership: a check-in at 11:00 on the 16th keys to the 15th → R1.

- **Eligible helper-day** — a window key on which the driver had **no own assigned task / delivered trip** in the same window. A driving window always pays as trips, never additionally as a helper-day; the exclusion is evaluated **per window key**, not per calendar day.

- **Helper day rate (`helperDayRateThb`)** — flat THB per eligible helper-day from the active compensation config (default 400). One rate for both training and assisting.

- **`HELPER_PAY`** — the payroll line-item category (type `EARNING`) emitted by the orchestrator for helper pay. Settled in the round window that contains the helper-day.

## Payroll detail / line-item breakdown ([ADR-0003](adr/ADR-0003-payroll-lineitem-breakdown.md))

- **Line-item breakdown** — every payroll line item stores its own breakdown at **generation time** (`quantity`, `unitRate`, `description`, `meta`) so the detail view (`PayrollReviewDialog`, kebab → "View details") shows *how* each figure was reached and stays correct even if config later changes. The UI falls back to name+amount for older payrolls that lack the fields.
- **Trip-pay split** — `TRIP_COMMISSION` is emitted as **two** lines when both apply: weekday (`quantity` = weekday trips × `weekdayRateThb`) and holiday (`quantity` = holiday trips × `holidayRateThb`).
- **Penalty breakdown** — penalty line items carry `meta` = `{ installmentIndex, installmentsTotal, remainingThb, totalThb }` so the view shows installment progress.

## Cash advance (เบิกล่วงหน้า) ([ADR-0004](adr/ADR-0004-cash-advance.md))

- **Cash advance** — money paid to a driver **before** a pay round closes, recorded by admin/HR at the withdrawal date in the new `driver_advances` collection and deducted **in full, once**, in the **next** pay round. No installments, no interest, no carry-over.
- **Deduction round** — computed once at creation from `withdrawnAt` (Asia/Bangkok): withdrawn day ≤ 15 → **R2 same month**; day ≥ 16 → **R1 next month**. Stored as `deductPeriod` + `deductRound` so the orchestrator query is one equality. **R1 can now carry a deduction** — the old "deductions only in R2" assumption no longer holds.
- **Advance cap** — at most ½ of what the driver has earned so far; **enforced by admin/HR judgement, not the system**. The system only records and deducts. Because of the cap, the next round's net pay always covers the advance (no carry-over balance is modelled).
- **`CASH_ADVANCE`** — the payroll line-item category (type `DEDUCTION`) for a settled advance.

## Penalty types (config)

- **Damage / lost-goods penalty** — a driver-at-fault deduction (damaged or lost cargo) is **not** a new collection; it is a new **penalty type in config `penaltyTypes`**, reusing the existing `driver_penalties` model, page, and R2 deduction logic ([ADR-0001](adr/ADR-0001-helper-pay-data-model.md) context; penalties predate it).

## Supplementary trips ([ADR-0005](adr/ADR-0005-supplementary-trips.md))

- **jobCategory (`PRIMARY` / `SUPPLEMENTARY`)** — a trip/task axis marking whether it is the **primary** contracted route (หลัก) or a **supplementary** ad-hoc trip (เสริม). Stored on `tasks.jobCategory` and `trip_records.jobCategory`, default `PRIMARY`. **Distinct from `jobType`** (`first_mile | line_haul`, auto-detected from origin) — do not conflate. UI shows หลัก/เสริม.
- **เที่ยวเสริม (supplementary trip)** — a trip billed at a **separately agreed price** that does not come from the primary rate card; its price is **frozen** once the trip has happened.
- **Supplementary rate card** — `customer_rate_entries` rows tagged `jobCategory = SUPPLEMENTARY`. Same collection/shape as the primary card; the category is a **filter dimension** on `selectBillingRateEntry`, not a separate collection.
- **Frozen pricing** — a SUPPLEMENTARY trip's billing snapshot is computed **once** (at delivery, from the supplementary rate card) with `billingManualOverride = true`, and **never recomputed**. The `forceRecompute` path skips any trip with `billingManualOverride === true` or `jobCategory === SUPPLEMENTARY`, so primary rate-card / fuel re-imports never move a เสริม price.
- **Report display rules (Excel detail)** — (1) for **J&T**, the source-hub origin in เส้นทาง shows the hub **code** (`SPK_GW`) instead of the billing name (`J&T EXPRESS บางปู`); (2) globally, vehicle class **`PICKUP` displays as `4WH`** in ประเภท; (3) supplementary rows carry **`เสริม`** in หมายเหตุ.

## Related (already defined elsewhere)

- **Round (R1 / R2)** — semi-monthly pay window; R1 = days 1–15, R2 = 16–end. Helper-days are assigned to a round by their **window key day D** ([ADR-0002](adr/ADR-0002-helper-day-window.md)), not the raw check-in timestamp — a check-in at 11:00 on the 16th belongs to R1.
- **Recompute** — re-running `generateDriverPayoutRun` for a period+round. Overwrites `DRAFT` payouts only; an `APPROVED` payout is corrected via a post-approval adjustment ([Story 3.4](epics.md)).
