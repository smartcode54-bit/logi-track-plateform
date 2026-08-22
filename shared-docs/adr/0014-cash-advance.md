# ADR 0014 — Cash advance (เบิกล่วงหน้า): recorded, deducted in the next pay round

- **Status:** Accepted (2026-06-26) — implementation pending
- **Deciders:** Samart Kas (product owner), Claude
- **Area:** Driver Compensation (logitrack-web, Cloud Functions)

> **Provenance.** Originally `ADR-0004-cash-advance` in the BMAD driver-compensation planning
> pipeline. Migrated into the canonical set on 2026-08-09 when the BMAD output folder was retired —
> see [0017](0017-retire-bmad-wds-tooling.md). Content preserved; only cross-references and the
> metadata block were adapted. The `driver_advances` collection and UI described here were **not yet
> built** as of migration — this remains a decision record, not shipped work.

## Context

Drivers may request cash **before** a pay round closes. There is currently no model for this. The 2026-06-26 grilling session defined a cash-advance feature: admin/HR records the advance at the withdrawal date, and it is deducted from the driver's net pay in the **next** pay round.

### Decisions locked in the grilling session

1. **Cash advance** = the driver receives money before the pay round; it is later deducted from what the driver is owed.
2. **Deducted in the next pay round after the withdrawal date** (real "next round", not always R2):
   - Withdrawn in **R1** (day 1–15) → deducted in **R2 of the same month**.
   - Withdrawn in **R2** (day 16–end) → deducted in **R1 of the next month**.
3. **Single deduction, in full, in that one round.** No installments, no carry-over.
4. **No interest / no fee** — deduct exactly what was advanced.
5. **Cap = at most 1/2 of what the driver has earned so far**, but **admin/HR judges this manually**; the system does **not** enforce the cap. The system only records the amount and the withdrawal date and deducts it in the next round.
6. **Over-deduction cannot occur** — because the manual cap prevents advancing more than the driver has earned, the next round's net pay will always cover the advance. (`netPay` is still clamped at 0 defensively, but no carry-over balance is modelled — there is nothing to carry, per decision 3.)
7. **Admin enters the advance** in the system, recorded at the withdrawal date.

## Decision

New collection **`driver_advances`** (admin-written, like `driver_penalties`):

```ts
{
  id?: string;
  driverId: string;          // Auth UID (matches payroll.driverId)
  driverName?: string;
  amountThb: number;         // > 0
  withdrawnAt: Date;         // the withdrawal date (decides the deduction round)
  // Deduction target, computed once at creation so the orchestrator query is a simple equality:
  deductPeriod: string;      // "YYYY-MM"
  deductRound: "R1" | "R2";
  status: "pending" | "deducted" | "cancelled";
  reason?: string;
  createdAt?: Date; updatedAt?: Date; createdBy?: string;
}
```

**Deduction-target rule** (computed at creation from `withdrawnAt`, Asia/Bangkok):

```
day(withdrawnAt) ≤ 15  → deductPeriod = same month,  deductRound = "R2"
day(withdrawnAt) ≥ 16  → deductPeriod = next month,  deductRound = "R1"
```

**Orchestrator** (`generateDriverPayoutRun` for `period`+`round`):
- Query `driver_advances` where `driverId == authId`, `deductPeriod == period`, `deductRound == round`, `status == "pending"`.
- Emit a `DEDUCTION` line item per advance: `category: "CASH_ADVANCE"`, `name: "Cash advance"`, `amount: amountThb`, `meta: { withdrawnAt }`.
- This is **not** gated to R2 like SSO/penalties — a cash advance settles in whichever round its `deductRound` says (R1 is now a possible deduction round).
- Balances/status flip to `deducted` are committed at **approve** time (mirrors how penalty balances are committed at approve, not at draft), so a recompute of a DRAFT can be re-run safely.

**Admin UI:** a new page (sibling to `/app/payroll/penalties`) to record an advance: pick driver, amount, withdrawal date, reason; list/cancel pending advances. The page shows the computed deduction round so the admin sees when it will be taken.

## Consequences

- **Positive:** simple single-shot model; reuses the penalty page/API shape; deduction round is precomputed so the orchestrator query is one equality match.
- **Negative / follow-ups:**
  - **R1 can now carry deductions** — any code/UX that assumed "deductions only in R2" must be revisited.
  - No system cap means an over-advance is operationally possible if admin misjudges; the clamp at 0 protects the math but a mis-entry is a manual-process risk (acceptable per decision 5/6).
  - `withdrawnAt` near a month boundary must use the Bangkok calendar day to pick the deduction target consistently.

## Alternatives considered (and rejected)

1. **Always deduct in R2 (like SSO/penalties).** Rejected: contradicts decision 2 — an advance taken late in the month (R2) must roll to next month's R1, not the same R2 it was taken in.
2. **Installments / carry-over balance (like `driver_penalties`).** Rejected: decision 3 — advance is single-shot; the manual cap (decision 5) guarantees the next round covers it, so no `remainingThb` is needed.
3. **System-enforced cap.** Rejected: decision 5 — admin/HR assess the half-of-earnings cap by judgement; computing "earnings so far this round" at withdrawal time mid-round was considered and dropped for simplicity.

## Related

- Glossary: [../glossary.md](../glossary.md) — *Cash advance*, *Deduction round*, *Advance cap*.
- Driver-compensation epics/stories: [../driver-compensation/epics.md](../driver-compensation/epics.md).
- Retirement of the BMAD pipeline that authored this: [0017](0017-retire-bmad-wds-tooling.md).
