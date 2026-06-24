# Story 3.6: Helper / training-day pay

> **Backfill story** — the helper-pay capability was built across commits `f31bde0 → 0d10735 → 5dc4370 → fbfebf0 → e171b36` with **no FR and no story** in `epics.md`. This story documents the capability as decided in the 2026-06-24 grilling session, marks what is already done, and lists the gaps still open. See [ADR-0001](../adr/ADR-0001-helper-pay-data-model.md) and the [glossary](../glossary.md).

**Epic:** 3 (Review, Approval, Ledger & Export) — settles in payout runs.

As an admin (and, as a fallback, a main driver),
I want to designate the **one** helper who trained/assisted on a task,
So that the helper is paid a flat per-day rate for days they helped but did not drive their own trips.

## New FRs (add to PRD inventory)

- **FR22** — Helper/training-day pay: a driver acting as helper on a task earns `helperDayRateThb` per **eligible helper-day**.
- **FR22.1** — Exactly **one** helper per task. One flat rate for training and assisting (no distinction).
- **FR22.2** — Helper is set by the **admin at task assignment** (primary); the **main driver may set it at check-in** when the admin has not (urgent fallback). Both write `tasks.helperDriverIds` (Auth UID, length 1).
- **FR22.3** — Admin can **review and edit** a task's helper before the payout run is generated.
- **FR22.4** — One eligible helper-day = one **actual work date** (`tasks.date`, Bangkok), de-duplicated per day, **excluding** the driver's own delivered-trip days. Assigned to a round by that date.
- **FR22.5** — Changing a task's helper **recomputes** the affected DRAFT payout; an APPROVED payout is corrected via the Story 3.4 adjustment path.

## Acceptance Criteria

**Given** the active config has `helperDayRateThb`, **When** a payout run is generated for a round, **Then** for each driver, days where they appear in `tasks.helperDriverIds` (and had no own delivered trip) are counted once per calendar day and paid `count × helperDayRateThb` as a `HELPER_PAY` EARNING line. ✅ done (`functions/src/driverCompensation.ts`, `computeHelperPay`)

**And** the helper field holds at most one driver; the mobile picker and chips present a **single** selection (no plural/multi affordance). ⚠️ gap — see Tasks.

**And** an admin can set the helper while assigning a task in the admin portal. ⚠️ gap — not built.

**And** an admin can edit a task's helper from the web before the run is generated. ⚠️ gap — `EditTripDetailsDialog` is read-only.

**And** changing the helper before approval recomputes the DRAFT payout; after approval, correction goes through Story 3.4. ⚠️ gap — recompute trigger not wired.

**And** all helper UI strings exist in EN + TH. ✅ done.

## Tasks / Subtasks

- [x] Config `helperDayRateThb` (default 400) + editor field — `5b7edc4`
- [x] Pure `computeHelperPay` mirrored `lib ↔ functions/src/core` + unit test — `f31bde0`
- [x] Orchestrator: per-round helper-day pay, exclude own delivered days, 1/day dedup, `HELPER_PAY` line — `fbfebf0`
- [x] Data model `tasks.helperDriverIds` (Auth UID) + composite index `helperDriverIds CONTAINS + date ASC` — `fbfebf0`
- [x] Security rules: main driver updates own task; any auth reads `drivers` for the picker — existing
- [x] Mobile: main-driver helper picker at check-in (fallback path) — `fbfebf0`
- [x] Web: `EditTripDetailsDialog` displays helper names (read-only) — `fbfebf0`
- [x] i18n EN + TH (mobile + web) — done
- [ ] **Mobile single-select alignment** — picker/chips/i18n imply multiple but model is one; make singular and unambiguous (`check_in_page.dart` ~807, ~1473 + chip blocks). De-duplicate the two near-identical pickers/write-sites (lines ~992 / ~1724).
- [x] **Admin-assign helper at task assignment** (2026-06-24) — shared `features/tasks/components/HelperDriverField.tsx` (single-select, stores Auth UID, excludes main driver) wired into `FirstMileTaskDialog` + `LineHaulTaskDialog` (+ hooks defaults & multi-delivery payload) and `createOrUpdateTask` CF persists `helperDriverIds` (capped at 1).
- [x] **Admin review/edit helper before payroll** (2026-06-24) — `EditTripDetailsDialog` now resolves the linked task doc and edits `tasks.helperDriverIds` via the same `HelperDriverField`.
- [~] **Recompute-on-change** — DRAFT case works manually: re-running "Generate Payroll" overwrites the DRAFT and picks up the edited helper (orchestrator is idempotent). Still TODO: auto-trigger on edit + APPROVED → Story 3.4 adjustment. Edits are NOT yet blocked once a run is approved.
- [ ] **Orchestrator eligibility tests** — `computeHelperPay` only tests the multiply; add tests for the eligibility/dedup/round-window logic that lives in `driverCompensation.ts` (exclude own delivered day, one-per-day, R1/R2 boundary).
- [ ] **Surface `HELPER_PAY` in payout review UI** — ensure the new category renders with an EN/TH label, not a raw key.
- [ ] **E2E**: deploy (Gen2) and verify the closed loop with a real helper-day.

## Dev Notes

- Helper IDs are **Auth UIDs** end-to-end (mobile stores `authId`, orchestrator queries `array-contains authId`) — verified consistent.
- `HELPER_PAY` passes `payrollSchema` because `category` is `z.string()` (no enum) — no schema change needed, but document the new category in the schema comment.
- Sunday is already a company holiday in `makeHolidayChecker`; helper-days follow the same round assignment as trips.
- Fraud/collusion control = admin review (FR22.3); ensure edits are auditable per NFR2.
