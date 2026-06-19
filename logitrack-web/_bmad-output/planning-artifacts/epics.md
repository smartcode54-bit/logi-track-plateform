---
stepsCompleted: [1, 2, 3, 4]
status: 'complete'
completedAt: '2026-06-18'
inputDocuments:
  - _bmad-output/planning-artifacts/prds/prd-logitrack-web-2026-06-18/prd.md
  - _bmad-output/planning-artifacts/prds/prd-logitrack-web-2026-06-18/.decision-log.md
  - _bmad-output/planning-artifacts/architecture.md
feature: 'Driver Compensation (คิดค่าตอบแทนคนขับ)'
---

# logitrack-web — Driver Compensation: Epic Breakdown

## Overview
Decomposes the Driver Compensation PRD + Architecture into implementable stories. Brownfield (Next.js web + Cloud Functions Gen2 + Flutter mobile + Firestore).

## Requirements Inventory

### Functional Requirements
- FR1: Effective-dated compensation config (base rates, fuel tiers, trip-volume tiers, SSO rule, penalty types).
- FR2: Base per-trip rates — weekday 300, holiday 350 (defaults, editable).
- FR3: Standby trips excluded from pay (config flag).
- FR4: Compute always reads the active config for the cycle (no hard-coding).
- FR5: Count delivered trips from trip_records, assigned to round by deliveredTimestamp; exclude standby AND multi-stop/J&T multi-drop (OQ9 resolved: not paid).
- FR6: Holiday = Sunday OR public holiday in working_holiday_calendar.
- FR7: Base pay = weekday trips×weekday rate + holiday trips×holiday rate.
- FR8: Compute monthly fuel efficiency km/L (existing fuel logic).
- FR9: Fuel incentive tiers [10,11)=1000 … [13,14)=1400, ≥14=1800, <10=0.
- FR9.1: Fuel incentive requires >5 refuels in the month (whole incentive gated).
- FR10: Attribute km/L via assigned truck; flag multi-truck conflicts (OQ2).
- FR11: Count qualifying trips for monthly trip-volume incentive.
- FR12: Trip-volume incentive — highest tier only (50→1000, 60→1500, 70→2000).
- FR13: Social-security deduction (R2): base 15,000 existing (hired <2026) / 12,000 new (2026+); >55 exempt; probation 3 months; rate 5% default, configurable.
- FR14: Driver fields — employmentType, ssoBase, birthDate, hireDate, ssoEligible/probationPassed.
- FR15: Manual penalty deductions (type, amount, reason, evidence); seeded "late/SLA breach"=3000; extensible types.
- FR15.1: Installment deductions — full or split over N rounds; carry balance; net never negative.
- FR16: Generate payout run per round → per-driver line items (base/incentives/deductions/net).
- FR17: Admin review + override line items with audit note before approval.
- FR18: Approve → immutable snapshot + post expense to transactions.
- FR18.1: Post-approval corrections via linked reversing/adjustment transaction (snapshot untouched).
- FR19: Export run (Excel/PDF payout slips), reuse billing-document styling.
- FR20: Driver mobile self-view of own breakdown, read-only, only when published.
- FR21: Driver cannot see others' data (Firestore rules driver scoping).

### NonFunctional Requirements
- NFR1 (N1): i18n EN + TH for all UI strings.
- NFR2 (N2): Auditability — approved payouts immutable; overrides logged with actor+timestamp.
- NFR3 (N3): Money precision — whole-THB half-up rounding; idempotent recompute; net never negative.
- NFR4 (N4): RBAC capabilities + driver-scoped Firestore rules.
- NFR5 (N5): Batch full-cycle run server-side without timeout.
- NFR6 (N6): Shared compute logic synced lib/* ↔ functions/src/core/*.

### Additional Requirements (from Architecture)
- No starter/scaffold (brownfield); first work = schema + config + drivers fields + Zod.
- New collections: driver_compensation_config, driver_payouts (id `${driverId}_${period}_${round}`), driver_penalties.
- Pure compute module mirrored lib/compensationCompute.ts ↔ functions/src/core/compensationCompute.ts.
- Cloud Function generateDriverPayoutRun (batch, idempotent, high timeout) + approve + recompute callables.
- transactions posting on approve; adjustment entries on correction.
- Firestore rules + indexes updates; 4 new capabilities (compensation_manage_config/run_payout/approve/view).
- Mobile read-only feature module consuming published payouts.

### UX Design Requirements
None — no UX specification document for this feature (web admin reuses existing accounting UI patterns; mobile reuses existing read view patterns).

### FR Coverage Map
- **Epic 1:** FR1, FR2, FR14
- **Epic 2:** FR3, FR4, FR5, FR6, FR7, FR8, FR9, FR9.1, FR10, FR11, FR12, FR13, FR15.1, FR16
- **Epic 3:** FR15, FR17, FR18, FR18.1, FR19
- **Epic 4:** FR20, FR21
- NFRs cross-cut: N1/N4 every epic; N2 (E3); N3/N5/N6 (E2); driver-scope rules (E4).

## Epic List

### Epic 1: Compensation Setup & Payroll Data
Admin can configure effective-dated compensation rules (base rates, fuel tiers, trip-volume tiers, SSO rule, penalty types) and maintain driver payroll fields. New web pages `app/app/accounting/driver-compensation/config/page.tsx` + overview.
**FRs covered:** FR1, FR2, FR14

### Epic 2: Payout Calculation Engine
Pure mirrored compute (base/fuel/trip-volume/SSO/installment math) + `generateDriverPayoutRun` Cloud Function producing per-driver draft payouts admin can view.
**FRs covered:** FR3, FR4, FR5, FR6, FR7, FR8, FR9, FR9.1, FR10, FR11, FR12, FR13, FR15.1, FR16

### Epic 3: Review, Approval, Ledger & Export
Admin adds penalties (with installments), reviews/overrides, approves (immutable snapshot), posts to transactions, makes corrections via adjustment entries, and exports payout slips.
**FRs covered:** FR15, FR17, FR18, FR18.1, FR19

### Epic 4: Driver Self-View (Mobile)
Drivers view their own published compensation breakdown read-only in the Flutter app, scoped by Firestore rules.
**FRs covered:** FR20, FR21

---

## ⚠️ REVISION 2026-06-18 — Re-scoped onto existing `payroll`
A payroll shell already exists (collection, schema, list+review/approve UI, hr capabilities, "Generate payroll" button, sidebar/i18n) — see architecture Revision. Epics adjusted:
- **E1:** keep config (new) + driver fields (new). Reuse `hr_view_payroll`/`hr_manage_payroll` (no new caps). Story 1.1 = config collection + Zod + rules only.
- **E2:** compute engine generates **`payroll`** lineItems and **wires the existing "Generate payroll" button**; reuse payrollSchema (extend with `round`). Story 2.5 = wire generate, not a new collection.
- **E3:** review/approve UI EXISTS → re-scope to: penalties (new), **transactions posting on approve (new)**, adjustments (new), export (verify if exists). Do NOT rebuild list/review/approve.
- **E4:** mobile self-view of own `payroll` (status APPROVED/PAID) — new.
- **Status vocab:** use existing DRAFT/PENDING_APPROVAL/APPROVED/PAID/CANCELLED.

## Epic 1: Compensation Setup & Payroll Data

Admin can configure compensation rules and maintain driver payroll fields — the foundation other epics read from.

### Story 1.1: Compensation config data model, RBAC & rules
As an admin/platform owner,
I want the compensation config collection, capabilities, and security rules in place,
So that compensation settings can be stored securely and access-controlled.

**Acceptance Criteria:**
**Given** the codebase, **When** the story is implemented, **Then** a `driver_compensation_config` collection is defined with a Zod schema in `validate/compensationSchema.ts` (base rates, fuel tiers, trip-volume tiers, SSO rule, penalty types, `effectiveFrom`).
**And** 4 capabilities (`compensation_manage_config`, `compensation_run_payout`, `compensation_approve`, `compensation_view`) exist in `lib/capabilities.ts` and are granted to Admin/Accounting in `lib/roles.ts`.
**And** `firestore.rules` restricts `driver_compensation_config` read/write to admins; `firestore.indexes.json` updated if queried by `effectiveFrom`.

### Story 1.2: Compensation config editor (effective-dated)
As an admin,
I want to set base per-trip rates, fuel tiers, trip-volume tiers, SSO rule, and penalty types with an effective-from date,
So that pay rules are explicit, versioned, and changeable over time.
**FRs:** FR1, FR2

**Acceptance Criteria:**
**Given** the config page `app/app/accounting/driver-compensation/config/page.tsx`, **When** I save a config with weekday=300, holiday=350 and the tier/SSO/penalty tables, **Then** a new effective-dated config doc is written (prior versions preserved).
**And** the form validates via the Zod schema and shows EN+TH labels.
**And** loading the page shows the currently active config for today.

### Story 1.3: Driver payroll fields
As an admin,
I want driver records to carry employment type, SSO base, birth date, hire date, and probation status,
So that social-security and eligibility can be computed correctly.
**FRs:** FR14

**Acceptance Criteria:**
**Given** the driver schema/edit form, **When** I edit a driver, **Then** I can set `hireDate`, `birthDate`, `employmentType` (auto-derived: <2026 existing/15000, 2026+ new/12000, editable), `ssoBase`, and `probationPassed`.
**And** values persist on the `drivers` doc and are validated.
**And** form strings are EN+TH.

## Epic 2: Payout Calculation Engine

Pure, mirrored compute that turns trips/fuel/config into draft payouts.

### Story 2.1: Base-pay compute (weekday/holiday, exclusions)
As an admin,
I want base per-trip pay computed from delivered trips with correct weekday/holiday classification and exclusions,
So that the core of each driver's pay is accurate.
**FRs:** FR3, FR4, FR5, FR6, FR7
**OQ9 resolved:** multi-stop / J&T multi-drop trips are NOT paid (excluded entirely) — no longer blocked.

**Acceptance Criteria:**
**Given** `lib/compensationCompute.ts` and `functions/src/core/compensationCompute.ts` (kept in sync), **When** `computeBasePay` runs over delivered trips for a period, **Then** trips are assigned to round by `deliveredTimestamp`, standby and multi-stop trips excluded, holiday = Sunday OR `working_holiday_calendar`.
**And** base pay = weekday×weekdayRate + holiday×holidayRate using the active config (FR4), result via `roundTHB`.
**And** unit tests cover weekday/holiday/standby/multi-stop and round-boundary cases.

### Story 2.2: Fuel-efficiency incentive compute
As an admin,
I want the fuel incentive computed from monthly km/L with the >5-refuel gate,
So that fuel-efficient drivers are rewarded reliably.
**FRs:** FR8, FR9, FR9.1, FR10

**Acceptance Criteria:**
**Given** monthly fuel records, **When** `computeFuelIncentive` runs, **Then** km/L maps to tiers ([10,11)=1000 … ≥14=1800, <10=0) using existing fuel logic.
**And** if ≤5 refuels in the month, incentive = 0 (whole incentive gated).
**And** km/L is attributed via the driver's assigned truck and multi-truck conflicts are flagged (FR10).

### Story 2.3: Trip-volume incentive compute
As an admin,
I want a monthly trip-count bonus applied at the highest tier reached,
So that high-volume drivers get the correct single bonus.
**FRs:** FR11, FR12

**Acceptance Criteria:**
**Given** the monthly qualifying trip count, **When** `computeTripVolumeIncentive` runs, **Then** 50→1000, 60→1500, 70→2000, highest-tier-only, <50→0.

### Story 2.4: Deductions — SSO + installments compute
As an admin,
I want social-security and installment deductions computed so net is never negative,
So that deductions are correct and lawful.
**FRs:** FR13, FR15.1

**Acceptance Criteria:**
**Given** a driver past probation and ≤55, **When** `computeSso` runs in R2, **Then** SSO = configured rate (default 5%) × ssoBase (existing 15000→750, new 12000→600); >55 or in-probation → 0.
**And** `applyInstallmentDeductions` deducts full or split-by-N-rounds, carries the balance, and clamps net ≥ 0.

### Story 2.5: Generate payout run (Cloud Function)
As an admin,
I want to generate a payout run for a period+round and see per-driver draft payouts,
So that I can review computed pay before approving.
**FRs:** FR16

**Acceptance Criteria:**
**Given** the callable `generateDriverPayoutRun(period, round)`, **When** I run it, **Then** it batches all drivers, computes line items via the pure engine, and writes `driver_payouts` docs id `${driverId}_${period}_${round}` with `status: draft`.
**And** recompute overwrites draft docs only (idempotent), never `approved`/`published`.
**And** it completes within timeout for the full driver set (batch + Promise.all).

## Epic 3: Review, Approval, Ledger & Export

### Story 3.1: Penalty management
As an admin,
I want to add penalties to a driver with type/amount/reason/evidence and an installment plan,
So that deductions like SLA breaches are recorded and scheduled.
**FRs:** FR15

**Acceptance Criteria:**
**Given** `driver_penalties` collection + `PenaltyDialog`, **When** I add a penalty (seeded "late/SLA breach"=3000, types configurable), **Then** it is stored with `status: pending`, optional evidence URL, and an installment count.
**And** rules restrict penalties to admins.

### Story 3.2: Payout review & override
As an admin,
I want to review each driver's breakdown and override line items with an audit note,
So that I can correct edge cases before approval.
**FRs:** FR17

**Acceptance Criteria:**
**Given** a draft run in `PayoutReviewTable`, **When** I override a line item, **Then** the change is saved with actor+timestamp+note and the net recomputes.
**And** overrides are blocked once the run is approved.

### Story 3.3: Approve & post to ledger
As an admin,
I want to approve a run so amounts lock and post to the transactions ledger,
So that payouts are final and accounted for.
**FRs:** FR18

**Acceptance Criteria:**
**Given** a reviewed draft, **When** I approve, **Then** the payout snapshot becomes immutable (`status: approved`) and one expense `transactions` entry per driver payout is created with `relatedPayoutId`.
**And** approved payouts cannot be edited or recomputed.

### Story 3.4: Post-approval adjustments
As an admin,
I want to correct an approved payout via a linked adjustment entry,
So that corrections are auditable without mutating the original.
**FRs:** FR18.1

**Acceptance Criteria:**
**Given** an approved payout, **When** I issue a correction, **Then** a linked reversing/adjustment `transactions` entry (`reverses`) is created and the original snapshot is untouched.
**And** the driver-facing net reflects original + adjustments.

### Story 3.5: Export payout slips
As an admin,
I want to export the run as Excel/PDF payout slips,
So that I can distribute/file pay records.
**FRs:** FR19

**Acceptance Criteria:**
**Given** a run, **When** I export, **Then** an Excel/PDF is produced reusing `lib/billingDocument.ts` styling, with per-driver breakdown and EN/TH labels.

## Epic 4: Driver Self-View (Mobile)

### Story 4.1: Driver-scoped payout read rules
As the platform,
I want Firestore rules that let a driver read only their own published payouts,
So that pay data stays private.
**FRs:** FR21

**Acceptance Criteria:**
**Given** `firestore.rules`, **When** a driver queries `driver_payouts`, **Then** read is allowed only for docs they own AND `status == published`; all writes are functions/admin only.
**And** an index supports the driver+status query.

### Story 4.2: Mobile compensation view
As a driver,
I want to see my own compensation breakdown in the app,
So that I understand how my pay was calculated.
**FRs:** FR20

**Acceptance Criteria:**
**Given** `my_compensation_page.dart`, **When** I open it, **Then** I see my published payout(s) by period/round with base/incentives/deductions/net, read-only.
**And** unpublished/draft payouts are not visible; strings are EN+TH.
