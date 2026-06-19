# Decision Log — Driver Compensation PRD

> Canonical memory & audit trail for this PRD run. Every decision, change, and override is recorded here as the conversation unfolds.

| Date | Decision | Rationale |
|------|----------|-----------|
| 2026-06-18 | Started PRD run (Create intent) | New brownfield feature: driver compensation / payout calculation in LogiTrack |
| 2026-06-18 | Captured compensation model (brain dump) | See model below — base per-trip, fuel incentive, trip-volume incentive, penalties; all configurable |

## Compensation model (as described by Smart.dev, 2026-06-18)

> ⚠️ **Initial brain-dump capture — point-in-time.** Some lines below (e.g. "holiday = Sunday for everyone", fuel ">13") were refined later; the **Decisions** rows above are authoritative. Kept here for audit history.

**Base pay (per trip)**
- Weekday trip: 300 THB/trip
- Holiday trip: 350 THB/trip (default holiday = Sunday for everyone)
- Standby trips: NOT paid
- All rates configurable (future income-structure changes)

**Fuel incentive (by fuel efficiency, km/L)** — exact tier boundaries TBC
- 10 km/L → 1000
- 11 km/L → 1100
- 12 km/L → 1200
- 13 km/L → 1400
- > 13 km/L → 1800

**Trip-volume incentive (by # trips, period TBC)**
- 50 trips → +1000
- 60 trips → +1500
- 70 trips → +2000
- (stacking rule TBC: highest tier only vs additive)

**Penalties / deductions (configurable, extensible)**
- Late / SLA breach without evidence → −3000 (e.g. accepted job but failed to deliver before customer deadline)
- May need automatic delivery-time calculation from submit timestamp (scope TBC: manual vs auto)

**Open questions (to resolve before drafting):** fuel tier boundaries & <10 km/L, holiday definition (Sunday only vs + public holidays), km/L attribution per driver when switching trucks, output (slip + transactions entries), config model.

## Decisions from clarifying round (2026-06-18)

- **Pay cycle = semi-monthly (2 rounds):**
  - **Round 1 (1–15), paid on the 20th:** trip base pay ONLY.
  - **Round 2 (16–end of month), paid on the 5th of next month:** trip base pay (2nd half) + all incentives (fuel, trip-volume, computed over full month) + all deductions (social security, penalties).
- **Base salary + Thai social security (ประกันสังคม) — NEW dimension:**
  - SSO base salary: **new employees = 12,000**, **existing employees = 15,000** (when age ≤ 55).
  - **Age > 55 → no social security.**
  - [ASSUMPTION] employee SSO = standard Thai 5% of base → new 600/mo, existing 750/mo; deducted in Round 2; rate configurable.
- **Trip-volume incentive = highest tier reached only** (not additive). e.g. 70 trips ⇒ +2000.
- **Late/SLA penalty:** Phase 1 = manual entry by admin (with reason/evidence); auto-detect from submit timestamp = later phase.
- **Driver visibility:** show each driver their own compensation in the **mobile app** (in addition to web admin).
- Trip-volume incentive: highest tier only (confirmed).
| 2026-06-18 | NO base-salary payout | Clarified by Smart.dev: driver income = ค่าเที่ยว + incentives only. The 12,000/15,000 are ONLY the notional SSO contribution base for the social-security deduction, never paid as salary. Corrected FR13/FR14 + cycle note. |
| 2026-06-18 | SSO: 5% default but configurable | Govt may subsidize/adjust the rate. |
| 2026-06-18 | New vs existing by hire date | Hired before 2026 = existing (base 15,000); 2026 onward = new (base 12,000). |
| 2026-06-18 | SSO probation gate | Driver gets SSO only after completing 3-month probation with no issues / passing evaluation. |
| 2026-06-18 | Fuel tier confirmed | ≥14 km/L = 1800; <10 = 0; ranges per integer band. |
| 2026-06-18 | Fuel incentive eligibility gate | Requires >5 refuels in the month (anti-gaming / reliable km/L). Scope of gate (whole incentive vs top tier) = OQ7. |
| 2026-06-18 | Holiday = Sunday + public holidays | 350 THB rate applies to Sunday and วันหยุดนักขัตฤกษ์ (working_holiday_calendar). |
| 2026-06-18 | **PRD finalized** (Fast path, reviewer gate skipped per user) | Content complete; OQ2/5/6/7/8 deferred as non-blockers with assumptions. status: final. Next: bmad-create-epics-and-stories or bmad-create-architecture. |
| 2026-06-18 | Ran light reviewer gate (1 subagent) → NEEDS-WORK | User switched to option B. Found 3 CRITICAL + 2 HIGH (negative net, R1/R2 framing, trip predicate, fuel-gate scope, post-approval corrections). Review at review-quality.md. |
| 2026-06-18 | Negative net → installment deductions | FR15.1: deduction taken in full (if net≥0) or split into N งวด (admin choice); balance carries; net never negative. |
| 2026-06-18 | Multi-stop / J&T multi-drop = not paid under this model | FR5; excluded like standby. [CONFIRM] excluded entirely vs counts as 1 trip (OQ9). |
| 2026-06-18 | Fuel >5-refuel gate applies to whole incentive | FR9.1 (OQ7 resolved). |
| 2026-06-18 | Post-approval corrections via adjustment entry | FR18.1: reversing/adjustment transaction linked to original; snapshot immutable; full audit. |
| 2026-06-18 | Autofixes from review | R1/R2 = disjoint windows (not advance); round assignment by deliveredTimestamp; rounding to whole THB (N3); decision-log brain-dump annotated as superseded. |
| 2026-06-18 | **PRD re-finalized** after review fixes | Criticals/highs resolved; OQ2/5/6/8/9/10 deferred (non-blocking, assumptions logged). status: final. |
| 2026-06-18 | **OQ9 resolved** — multi-stop NOT paid | Confirmed by Smart.dev: multi-stop / J&T multi-drop trips are excluded entirely from compensation (not paid). FR5 + epics Story 2.1 unblocked. |
| 2026-06-18 | **RE-SCOPE onto existing `payroll`** | Build-time discovery: payroll collection + payrollSchema + list/review/approve UI + hr capabilities already exist (a shell, no compute/no ledger posting). Decision (Smart.dev): reuse payroll, don't create driver_payouts. Net-new = config + compute engine (wire Generate button) + driver fields + transactions posting + mobile. Architecture + epics revised (see Revision sections). |
