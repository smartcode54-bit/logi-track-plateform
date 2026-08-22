# ADR 0013 — Payroll line items carry a self-contained breakdown

- **Status:** Accepted (2026-06-26)
- **Deciders:** Samart Kas (product owner), Claude
- **Area:** Driver Compensation (logitrack-web, Cloud Functions)

> **Provenance.** Originally `ADR-0003-payroll-lineitem-breakdown` in the BMAD driver-compensation
> planning pipeline. Migrated into the canonical set on 2026-08-09 when the BMAD output folder was
> retired — see [0017](0017-retire-bmad-wds-tooling.md). Content preserved; only cross-references and
> the metadata block were adapted.

## Context

The payroll detail view (`PayrollReviewDialog`, opened from the `/app/payroll` kebab → "View details") lists each line item as **name + amount** only. The orchestrator emits lump-sum line items (`Trip pay`, `Helper/training pay`, `Fuel incentive`, `Trip-volume incentive`, `Social security`, `Penalty`) with no `quantity`, `unitRate`, or `description` — so a driver/admin cannot see *how* a number was reached (how many trips, at what rate, how many helper-days, which installment of a penalty).

The 2026-06-26 grilling session decided the detail view must **itemize every pay and deduction with its breakdown**, and that the breakdown must be **stored on the payroll document at generation time** so it stays correct even if the compensation config later changes (a payroll is a historical record).

### Decisions locked in the grilling session

1. **Show every line item** that exists on the payroll (earnings and deductions, including SSO and each penalty). Penalties and SSO are already emitted as `DEDUCTION` line items — the gap is breakdown detail, not presence.
2. **Breakdown is stored on the line item at generate time** (self-contained), not recomputed lazily in the UI. Rejected the lazy-recompute alternative: it is slower and can disagree with the figure that was actually paid if source data changed.
3. **Trip pay is split** into separate weekday and holiday line items (or one line carrying both quantities) so the weekday/holiday counts and rates are visible. `computeBasePay` already knows this split internally; it must surface it.
4. Penalty line items should expose **installment progress** (this installment, paid-so-far, remaining) so the detail view can show "งวด 2/4, หักงวดนี้ 500, คงเหลือ 1,000".

## Decision

Extend `PayrollLineItem` with optional, presentation-ready breakdown fields:

```ts
type PayrollLineItem = {
  id?: string;
  type: "EARNING" | "DEDUCTION";
  category: string;        // TRIP_COMMISSION | HELPER_PAY | FUEL_INCENTIVE | ... | SOCIAL_SECURITY | PENALTY | CASH_ADVANCE
  name: string;
  amount: number;          // THB, the settled amount for this line
  // NEW — all optional, populated at generation time:
  quantity?: number;       // e.g. trip count, helper-day count
  unitRate?: number;       // e.g. THB per trip / per helper-day
  description?: string;    // human-readable, pre-localised fallback
  meta?: Record<string, unknown>; // structured extras (weekday/holiday counts, installment x/y, remaining, sso base/percent)
}
```

- `quantity × unitRate` should reconcile to `amount` where it makes sense (trip pay, helper pay); `meta` carries anything that doesn't fit the quantity/rate shape.
- **Trip pay** is emitted as **two** line items when both apply: weekday (`quantity` = weekday trips, `unitRate` = `weekdayRateThb`) and holiday (`quantity` = holiday trips, `unitRate` = `holidayRateThb`). `computeBasePay` returns the counts; the orchestrator splits the line.
- **Helper pay**: `quantity` = eligible helper-days, `unitRate` = `helperDayRateThb`.
- **SSO**: `meta` = `{ basePercent, baseThb }`.
- **Penalty**: `meta` = `{ installmentIndex, installmentsTotal, remainingThb, totalThb }`.
- `PayrollReviewDialog` renders `name` + (`quantity` × `unitRate`) + `description`/`meta` under each row; falls back to name+amount when the new fields are absent (older payrolls).

## Consequences

- **Positive:** the detail view is a faithful, frozen record of how each figure was computed; no recompute drift; older payrolls still render (fields optional).
- **Negative / follow-ups:**
  - The line-item type widens; all writers must be updated to populate the new fields and all readers must treat them as optional.
  - Splitting trip pay into two lines changes the line count — any code that assumed a single `TRIP_COMMISSION` line must handle two.

## Alternatives considered

- **Recompute the breakdown lazily in the UI** from the current config — rejected (decision 2): slower, and disagrees with the figure actually paid if source data changed after the payout was generated. A payroll is a historical record.

## Related

- Glossary: [../glossary.md](../glossary.md) — *Line-item breakdown*, *Trip-pay split*, *Penalty breakdown*.
- Driver-compensation epics/stories: [../driver-compensation/epics.md](../driver-compensation/epics.md).
- Retirement of the BMAD pipeline that authored this: [0017](0017-retire-bmad-wds-tooling.md).
