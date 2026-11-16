/**
 * Year view: January → the latest month, one column per month (owner 2026-11-16).
 *
 * Sources are loaded ONCE for the whole range and each month is computed by the same
 * `computePnl` as the month view, so a month's column always equals that month's page. Only the
 * expense rows are cut per month — the fuel window looks back 60 days before the month and must not
 * see later months (a later odometer rollback would otherwise warn in every earlier month).
 */
import { computePnl } from "../pnlCompute";
import { DAY_MS, bangkokMonthBounds, round2 } from "./settings";
import type { PnlInputs, PnlResult } from "./types";

const FUEL_LOOKBACK_MS = 60 * DAY_MS;

export function sliceInputsForMonth(inputs: PnlInputs, month: number): PnlInputs {
    const { start, end } = bangkokMonthBounds(inputs.year, month);
    const from = start.getTime() - FUEL_LOOKBACK_MS;
    const to = end.getTime();
    const inWindow = (d: Date) => d.getTime() >= from && d.getTime() < to;
    return {
        ...inputs,
        month,
        fuelRows: inputs.fuelRows.filter((r) => inWindow(r.date)),
        otherExpenses: inputs.otherExpenses.filter((r) => inWindow(r.date)),
    };
}

export interface PnlYear {
    year: number;
    /** Index 0 = January. */
    months: PnlResult[];
}

/** Compute months 1..lastMonth of `inputs.year` from one year-wide load. */
export function computePnlYear(inputs: PnlInputs, lastMonth: number): PnlYear {
    const months: PnlResult[] = [];
    for (let m = 1; m <= lastMonth; m++) months.push(computePnl(sliceInputsForMonth(inputs, m)));
    return { year: inputs.year, months };
}

/** Year total of one figure (rounded the same way as the month totals). */
export function yearTotal(months: PnlResult[], pick: (r: PnlResult) => number): number {
    return round2(months.reduce((a, r) => a + pick(r), 0));
}
