# PRD Quality Review — Driver Compensation (คิดค่าตอบแทนคนขับ)

**Reviewer:** Senior product/engineering reviewer
**Date:** 2026-06-18
**Artifact reviewed:** `prd.md` (status: final, Fast path) + `.decision-log.md`
**Gate verdict:** **NEEDS-WORK**

This is a payroll-domain feature where calculation correctness is the entire value proposition, yet the PRD leaves several money-affecting edge cases unspecified and contains at least one hard contradiction with its own decision log. The structure, scope discipline, and brownfield reuse mapping are genuinely good — but the gaps below are the kind that produce wrong pay, and the PRD's own SM1/SM2 and Risk #1 ("wrong pay erodes driver trust") make these blocking, not cosmetic. Several would block story writing for the payout-generation epic.

---

## 1. Completeness — missing requirements & edge cases

These are the highest-value findings for a payroll calc.

### C1 [CRITICAL] Negative net pay / deductions exceeding earnings — unspecified
R2 settles **all** deductions (SSO + penalties) against R2 trip pay + incentives. A 3,000 THB SLA penalty (FR15) plus 600–750 SSO can easily exceed a low-volume driver's R2 earnings, producing **negative net**. The PRD never says what happens: clamp to zero? carry the shortfall to next cycle? allow negative and let it post to `transactions` as a debt? This directly affects ledger correctness (FR18) and is unanswerable by any downstream story. Must be specified.

### C2 [CRITICAL] R1 advance vs R2 reconciliation is undefined
§4 + FR40-assumption calls R1 "a trip-pay advance" but R2 "Includes... Trip base pay (2nd half)". This implies R1 pays first-half trips and R2 pays second-half trips independently — i.e. **not** an advance/reconciliation model at all, just two disjoint windows. The word "advance" is misleading and contradicts the table. If R1 is truly an advance, R2 must reconcile (net out what R1 already paid); if it's just first-half pay, say so and drop "advance". As written, an implementer cannot tell whether R2 nets against R1. **Money-affecting ambiguity.**

### C3 [HIGH] Trip-date vs round-boundary attribution unspecified
FR5 counts "completed trips in the period." A trip can be **loaded on the 15th and delivered on the 16th**, or have a corrected `deliveredTimestamp` (per CLAUDE.md item #35, billing uses `deliveredTimestamp`, and admins can edit it inline). The PRD never states which timestamp assigns a trip to R1 vs R2, nor that the compensation period uses `deliveredTimestamp`. Given the platform already had billing bugs around exactly this, this must be pinned explicitly.

### C4 [HIGH] Corrections after approval — flow missing
FR18 locks approved runs as immutable snapshots; N2 reinforces immutability. The counter-metric explicitly tracks "post-approval corrections," so they are expected to happen — yet **no requirement describes how a correction is made** (adjustment in next cycle? reversing `transactions` entry? supplemental run?). Without this, the immutability rule and the counter-metric contradict each other operationally, and there's no story to write for the inevitable correction case.

### C5 [HIGH] Mid-cycle hire / termination / leave not addressed
- A driver **hired on the 10th** — does R1 pay only trips from the 10th (fine, trip-count handles it), but does SSO/probation logic interact correctly? Probation starts at hire; the 3-month gate (FR13) needs a defined evaluation date.
- A driver **terminated mid-month** — is a final/off-cycle payout supported, or do they wait for the normal pay date? Final SSO settlement?
- These are standard payroll scenarios; v1 can defer them but must say so. Currently silent.

### C6 [HIGH] Penalties spanning rounds / penalty timing undefined
FR15 lets admin "add penalty deductions to a driver's cycle," but penalties settle only in R2 (§4). What if an SLA breach occurs in R1's window? Is it dated to the event and always swept into the **same month's R2**, or can it land in a later cycle? What if it's added after R2 is approved (see C4)? No requirement covers penalty-to-cycle assignment.

### C7 [MEDIUM] Rounding rules unspecified
N3 says "integer/decimal THB, no float drift" but never states the **rounding policy** for SSO (5% of 12,000 = 600 is clean, but a configurable rate of e.g. 5% of 15,000 = 750, and future rates like 3% or subsidized fractions will produce decimals). Round half-up? Truncate? To satang or whole baht? Payroll needs an explicit rule; "no float drift" is not a rounding policy.

### C8 [MEDIUM] Fuel/volume incentive when a driver is on probation or has zero trips in a round
Incentives compute over the full month (R2). If a driver has trips in R1 but zero in R2 (e.g. went on leave on the 16th), do they still receive the full-month fuel/volume incentive in R2 even with no R2 earnings to net against? Interacts with C1.

### C9 [MEDIUM] Holiday classification boundary cases
FR6: Holiday = Sunday OR public holiday. Edge cases unstated: a public holiday that **falls on a Sunday** (no double pay — but confirm it's not double-counted), and whether `working_holiday_calendar` dates are matched against the trip's `deliveredTimestamp` in the project timezone (Asia/Bangkok) vs UTC. Timezone-off-by-one would misclassify holiday pay.

### C10 [LOW] "≥14 km/L = 1800" tier table gap
FR9 lists `[13,14)=1400, ≥14=1800` but the decision log/brain dump said `>13 → 1800`. FR9's banding (the 1400 band for [13,14)) is a **reasonable resolution** of the ambiguity but is a new decision not in the log — should be recorded as a decision, not silently introduced.

---

## 2. Clarity & testability

### CT1 [HIGH] FR9.1 eligibility-gate scope is self-contradictory with "final" status
FR9.1 carries an inline `[ASSUMPTION]` AND is flagged OQ7 (whole-incentive vs top-tier). The header says the PRD is "final" with OQ7 "deferred (non-blocking)." For a payroll calc this is **directly money-affecting and binary** — it changes payout amounts. It is not safely deferrable to architecture; an architect cannot guess. Either resolve it or mark FR9.1 explicitly as blocked-for-build.

### CT2 [MEDIUM] FR10 marked `[ASSUMPTION]` + "Needs confirmation (OQ2)" but is on the critical path
km/L attribution when a driver uses multiple trucks (the platform supports backup drivers swapping trucks mid-shift, per CLAUDE.md #37) is core to computing the fuel incentive. "Flag conflicts for manual review" is a reasonable v1 stance, but the **resolution rule** (which truck's km/L wins, or is it summed) is undefined, so the FR is not testable.

### CT3 [MEDIUM] FR16 "computes per-driver line items" — incomplete acceptance criteria
The FR doesn't enumerate the required line items or state that a run must include drivers with **zero pay** (so they appear in review) vs silently skipping them. Testers can't write "given/then" without knowing the expected line-item set and zero-pay handling.

### CT4 [LOW] SM3 not measurable as written
"Driver pay-related inquiries drop after mobile self-view ships" has no baseline, no measurement source, and no threshold. Either tie to a tracked support channel/number or downgrade to a qualitative goal.

### CT5 [LOW] FR2 vs FR4 phrasing
FR2 states "300/350 THB (defaults; editable)" while FR4 says amounts are "never hard-coded." Consistent in intent, but FR2's concrete numbers read as spec values; clarify these are **seed defaults** for the initial config doc, not constants.

---

## 3. Consistency with the decision log

### DL1 [CRITICAL] SSO age rule contradiction
Decision log line 44: SSO base applies **"when age ≤ 55"** and "Age > 55 → no social security." FR13/FR14 implement "Age > 55 → exempt." **Consistent on the >55 exemption.** However, the log also pairs the 12,000/15,000 base with "when age ≤ 55," whereas FR13 lists the >55 exemption as a separate bullet — fine. The real contradiction: the log's clarifying round (line 44) and the later "NO base-salary payout" decision (line 50) both survive, and FR13 correctly reflects line 50. **No contradiction here after re-read** — but the PRD should delete/supersede the stale line-42–45 "Base salary" block in the log to avoid future confusion (it still reads "Base salary + Thai social security" as a dimension). *Downgrade: this is a log-hygiene issue, MEDIUM.* (See DL2 for the genuine one.)

### DL2 [MEDIUM] Holiday default differs between log brain-dump and final
Decision log line 14 (brain dump): "default holiday = Sunday for everyone." Final decision (line 56) and FR6: "Sunday + public holidays." The PRD correctly reflects the **later** decision, but the brain-dump line is left intact and contradicts the resolution. Mark the brain-dump as superseded so a future reader doesn't reintroduce Sunday-only.

### DL3 [LOW] Fuel tier resolution recorded loosely
Log line 54 "ranges per integer band" → FR9 half-open intervals `[10,11)` etc. Good, but the 1400 band (see C10) and the exact `<10 = 0` floor should be a single explicit decision-log row matching FR9 verbatim.

---

## 4. Gaps that block downstream architecture / epics

### G1 [HIGH] No definition of "completed/delivered" trip predicate
FR5 says "completed... (delivered)" but the codebase has multi-stop trips, standby, cancelled tasks, and a known bug where "task cancel does not free driver" (MEMORY). The exact Firestore predicate (status value, multi-stop = 1 trip or N, partially-delivered multi-stop) must be defined or stories can't be written. Does a 3-stop J&T trip count as 1 trip or 3 for base pay AND for the 50/60/70 volume tiers? **This materially changes both base pay and the volume incentive** and is completely unaddressed.

### G2 [HIGH] `transactions` ledger posting contract undefined
FR18 posts "expense entries" but doesn't specify: one entry per driver per run, or per line item? What `type`/`category`? How does a payout entry relate back to `driver_payouts`? Reversal on correction (C4)? The existing ledger schema reuse needs at least the field contract or the architect is guessing.

### G3 [MEDIUM] Probation "3-month completion + pass" date model undefined
FR14 adds an "SSO eligibility / probation-passed flag." OQ8 leaves manual-vs-derived open. For story writing you need at minimum: what date SSO starts once the flag flips (the 4th month? the cycle after the flag is set?), and whether back-deduction applies. Currently underspecified.

### G4 [MEDIUM] Config effective-dating semantics for mid-cycle changes
FR1/FR4: "amounts read from the active config for the cycle being computed." If config changes `effectiveFrom` the 10th, does the whole R1 use one config or do trips split? Rate cards resolve per-trip by date; this PRD resolves "per cycle." State the resolution granularity explicitly — it affects whether a mid-period rate change is honored.

---

## 5. Scope discipline

Scope discipline is **strong** overall: SLA auto-detection (Phase 3), partner scoping, and tax-beyond-SSO are cleanly cut, and phasing is sensible.

### S1 [MEDIUM] Phase 2 (driver mobile self-view) FR20/FR21 sit in a web PRD
FR20/FR21 specify mobile-app behavior and Firestore rules, but the project is `logitrack-web` and mobile is a separate Flutter codebase. Fine to reference, but the **mobile-side work (Flutter screens, repository, i18n in `th.json/en.json`) is entirely unscoped here** — no FR describes the mobile implementation surface. Either explicitly say "mobile work tracked separately" or risk Phase 2 stories having no home.

### S2 [LOW] FR19/OQ6 export format still open in a "final" PRD
Export styling (OQ6) being open is low-risk and genuinely deferrable. Acceptable as-is.

---

## Summary of must-fix before epics/architecture
1. **C1** negative-net-pay handling (blocking).
2. **C2** R1-advance vs R2-disjoint reconciliation model (blocking).
3. **C3 / G1** trip→round attribution timestamp + multi-stop trip counting predicate (blocking for base pay AND volume incentive).
4. **CT1** FR9.1 fuel-gate scope — resolve, don't defer (money-affecting).
5. **C4 / G2** post-approval correction flow + `transactions` posting contract (blocking for the approve/post epic).
6. **DL2 / log hygiene** mark superseded brain-dump lines.

The remaining items (rounding policy, mid-cycle hire/term, probation date model, config granularity) are HIGH/MEDIUM and should be resolved or explicitly deferred-with-assumption before the relevant stories are written.
