---
title: "Driver Compensation (คิดค่าตอบแทนคนขับ) — PRD"
status: final
created: 2026-06-18
updated: 2026-06-18
project: logitrack-web
---

# Driver Compensation PRD (คิดค่าตอบแทนคนขับ)

> ✅ Final (Fast path; light reviewer pass applied — criticals/highs resolved). Remaining OQ2,5,6,8,9,10 deferred (non-blocking) to architecture/build — see §11 & `.decision-log.md`. Tech-how → `addendum.md`.

## 1. Overview
A system that computes each driver's pay per pay cycle from data the platform already captures — completed trips, fuel efficiency, trip volume — plus deductions (social security, penalties), then lets admins review/approve payouts and lets drivers see their own pay in the mobile app. Brownfield: reuses `trip_records`, `standby_records`, fuel records, `working_holiday_calendar`, `drivers`, and the existing `transactions` ledger.

## 2. Goals & Success Metrics
- **G1** Replace manual/spreadsheet payout calculation with an auditable in-app calculation.
- **G2** Pay drivers correctly and on time across the two monthly rounds.
- **G3** Give drivers transparency into how their pay is derived.

**Success metrics**
- SM1: ≥95% of payouts computed with no manual correction.
- SM2: Payout for a cycle generated within minutes (vs. manual hours).
- SM3: Driver pay-related inquiries drop after mobile self-view ships.
- Counter-metric: number of post-approval corrections (should stay near zero).

## 3. Users
- **Admin / Accounting** — configures rates, runs the cycle, reviews, approves, exports.
- **Driver** — views own compensation breakdown in the mobile app (read-only).
- [ASSUMPTION] Partner/subcontractor scope not in scope for v1.

## 4. Pay Cycle (core model)
Semi-monthly, two rounds:

| Round | Period | Pay date | Includes |
|-------|--------|----------|----------|
| **R1** | 1–15 | 20th (same month) | Trip base pay only |
| **R2** | 16–end of month | 5th (next month) | Trip base pay (2nd half) + **all incentives** (fuel, trip-volume, computed over the **full month**) + **all deductions** (social security, penalties) |

**R1 and R2 are disjoint windows, not an advance/reconciliation:** R1 pays trip base pay for trips delivered on days 1–15; R2 pays trip base pay for trips delivered on days 16–end, plus the month's incentives and deductions. A trip is assigned to a round by its `deliveredTimestamp` date (the project's billing-effective date). **Rounding:** all line items and net rounded to whole THB [ASSUMPTION].

> **No fixed salary.** Driver income = per-trip pay + incentives only. There is no base-salary line in the payout; the 12,000 / 15,000 figures are only a notional base for the social-security deduction (see FR13).

## 5. Scope
**In scope (v1)**
- Compensation config (effective-dated).
- Per-trip base pay (weekday/holiday, standby excluded).
- Fuel-efficiency incentive.
- Trip-volume incentive (highest tier only).
- Deductions: social security; manual penalties.
- Two-round payout generation, admin review/approve, export.
- Posting approved payouts to the `transactions` ledger.
- Driver mobile self-view.

**Out of scope (later)**
- Automatic SLA/late-delivery detection from submit timestamp (manual entry in v1).
- Partner/subcontractor payout scoping.
- Payslip statutory/tax beyond social security (e.g. withholding tax).

## 6. Functional Requirements

### 6.1 Compensation Configuration
- **FR1** Admin can configure, with an `effectiveFrom` date (history preserved, like rate cards): base per-trip rates, fuel-incentive tiers, trip-volume tiers, social-security rule, penalty types.
- **FR2** Base per-trip rates: weekday = 300 THB, holiday = 350 THB (defaults; editable).
- **FR3** Standby trips are excluded from pay; this exclusion is a config flag so it can change later.
- **FR4** All amounts are read from the active config for the cycle being computed (never hard-coded).

### 6.2 Per-Trip Base Pay
- **FR5** Count each driver's trips in the period from `trip_records` where status = delivered, assigned to a round by `deliveredTimestamp` date. **Excluded from pay:** `standby_records` (FR3) and **multi-stop / J&T multi-drop trips** (OQ9 resolved: not paid under this model — excluded entirely).
- **FR6** Classify each trip as weekday or holiday. Holiday = **Sunday OR a public holiday listed in `working_holiday_calendar`** (วันหยุดนักขัตฤกษ์). [ASSUMPTION] per-driver custom rest-day = future.
- **FR7** Base pay = (weekday trips × weekday rate) + (holiday trips × holiday rate), per period.

### 6.3 Fuel-Efficiency Incentive (monthly, R2)
- **FR8** Compute each driver's fuel efficiency (km/L) for the month using existing fuel logic (grouped by truck/odometer).
- **FR9** Map km/L to a flat incentive via configurable tiers: `[10,11)=1000, [11,12)=1100, [12,13)=1200, [13,14)=1400, ≥14=1800`; below 10 km/L = 0.
- **FR9.1** **Eligibility gate:** the **whole** fuel incentive (all tiers) requires **more than 5 refuels in the month** (km/L from too few fills is unreliable / anti-gaming); if ≤5 refuels, fuel incentive = 0.
- **FR10** [ASSUMPTION] Attribute km/L to the driver via their assigned truck for the month; flag conflicts when a driver used multiple trucks. **Needs confirmation (OQ2).**

### 6.4 Trip-Volume Incentive (monthly, R2)
- **FR11** Count qualifying trips for the month [ASSUMPTION: same delivered-trip count as FR5, standby excluded].
- **FR12** Apply **highest tier reached only** (not additive): 50→+1000, 60→+1500, 70→+2000. Below 50 → 0.

### 6.5 Deductions
- **FR13 Social security (ประกันสังคม)** deducted in R2. **No base-salary payout** — driver income is per-trip pay + incentives only. 12,000 / 15,000 are a **notional SSO contribution base** (never paid), set by employment type:
  - **Existing** (hire date **before 2026**) → base **15,000**
  - **New** (hire date **2026 onward**) → base **12,000**
  - **Age > 55 → exempt (0)**
  - **Probation gate:** SSO deduction begins only **after the driver completes 3 months with no issues / passes evaluation**; during probation → no SSO.
  - Contribution rate = **5% by default, configurable** (government may subsidize/adjust); at 5%: new = 600, existing = 750.
- **FR14** Driver fields (add to `drivers`): `employmentType` derived from **hire date** (before 2026 = existing/15,000; 2026+ = new/12,000), `ssoBase` (notional, deduction-calc only — not a wage), `birthDate` (>55 rule), `hireDate`, and an **SSO eligibility / probation-passed flag** (probation start → 3-month completion + pass).
- **FR15 Penalties** — admin can add penalty deductions to a driver's cycle with type, amount, reason, and evidence attachment. Seeded type: "late/SLA breach" = 3,000 THB. Penalty types are configurable/extensible.
- **FR15.1 Installment deductions (net never negative).** When a deduction (typically a penalty) would push the round's **net below 0**, the admin chooses per deduction: **(a) deduct in full** this round (only allowed if net stays ≥ 0), or **(b) split into installments** over a chosen number of pay rounds (งวด). Each round deducts its installment; the outstanding balance carries forward and is tracked until cleared. Net pay for any round is never negative.

### 6.6 Payout Generation & Review (web admin)
- **FR16** Admin generates a payout run for a round; system computes per-driver line items (base, incentives, deductions, net).
- **FR17** Admin reviews a breakdown per driver, can adjust/override line items with an audit note before approval.
- **FR18** Admin approves a run; approved amounts are locked (snapshot, immutable) and post to the `transactions` ledger as expense entries.
- **FR18.1 Post-approval corrections via adjustment entry.** An approved run is never edited in place. A correction creates a **reversing/adjustment `transactions` entry** linked to the original payout, with reason + actor, leaving the original snapshot intact (full audit trail). The driver's view and ledger reflect the net of original + adjustments.
- **FR19** Export the run (Excel/PDF payout slips), consistent with existing billing-document styling.

### 6.7 Driver Self-View (mobile)
- **FR20** A driver sees their own per-cycle compensation breakdown (base, incentives, deductions, net) read-only, only after the run is approved/published.
- **FR21** Driver cannot see other drivers' data (enforced by Firestore rules, mirroring existing driver scoping).

## 7. Non-Functional Requirements
- **N1 i18n** — all UI strings in EN + TH (`context/locales/en|th/*`).
- **N2 Auditability** — approved payouts immutable; every override logged with actor + timestamp.
- **N3 Accuracy** — money handled without float drift; all line items and net **rounded to whole THB**; recompute is idempotent; net pay never negative (FR15.1).
- **N4 Security** — driver self-view via Firestore rules; config/approve gated by capability (RBAC) — new capabilities in `lib/capabilities.ts` / `lib/roles.ts`.
- **N5 Performance** — a full-cycle run for all drivers completes server-side without timeout (batch, like billing backfill).
- **N6 Consistency** — if compute logic is shared web/functions, keep `lib/*` and `functions/src/core/*` in sync (project rule).

## 8. Data Model (high level)
- New collection `driver_compensation_config` (effective-dated rates/tiers/SSO/penalty types).
- New collection `driver_payouts` (one doc per driver per round): period, round, line items, deductions, net, status (draft/approved), snapshot, computedAt, approvedBy.
- New collection `driver_penalties` (or embedded): type, amount, reason, evidence URL, date.
- `drivers` additions: `employmentType`, `ssoBase`, `birthDate`, `hireDate` (FR14).
- Posts to existing `transactions` on approval.
- Detailed schema → `addendum.md` (TBD).

## 9. Integration with existing system
| Need | Existing source |
|------|-----------------|
| Completed trips, weekday/holiday | `trip_records` (delivered) + `working_holiday_calendar` |
| Standby exclusion | `standby_records` |
| Fuel km/L | existing fuel page logic (per truck) |
| Driver ↔ truck | `drivers.currentAssignment` |
| Ledger posting | `transactions` |
| RBAC | `lib/capabilities.ts`, `lib/roles.ts` |
| Money docs styling | `lib/billingDocument.ts` |

## 10. Phasing
- **Phase 1:** config + base pay + fuel & trip incentives + SSO + manual penalties + 2-round generation + review/approve + export + transactions posting.
- **Phase 2:** driver mobile self-view.
- **Phase 3:** automatic SLA/late-delivery detection from submit timestamp.

## 11. Open Questions
_Resolved: OQ1 fuel boundary (≥14=1800, <10=0); OQ3 holiday (Sunday + public holidays); OQ4 SSO (5% configurable; existing = hired <2026, new = 2026+; probation 3 months); OQ7 fuel gate (>5 refuels gates the whole incentive); OQ9 multi-stop = NOT paid (excluded entirely); negative net (installment deductions, FR15.1); post-approval (adjustment entry, FR18.1)._
- OQ2 km/L attribution when a driver uses multiple trucks in a month.
- OQ5 Trip-volume incentive counted over full month or per round? [ASSUMPTION: full month]
- OQ6 Payout slip format — reuse billing-document PDF/Excel style?
- OQ8 Probation "no issues / passed evaluation" — manual admin flag per driver, or derived? [ASSUMPTION: manual flag]
- OQ10 Mid-cycle hire/termination — pro-rating of SSO / partial-period handling? [ASSUMPTION: SSO charged in any round the driver is active & past probation]

## 12. Risks
| Risk | Mitigation |
|------|-----------|
| Wrong pay erodes driver trust | Review/override step + immutable approved snapshot + audit log |
| km/L mis-attribution (truck swaps) | Flag conflicts for manual review (FR10) |
| Money/float errors | Decimal handling + idempotent recompute (N3) |
| Scope creep into full payroll/tax | Phase boundaries; SLA-auto + tax explicitly out of v1 |
