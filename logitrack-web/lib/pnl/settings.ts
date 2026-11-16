/**
 * Dates, effective-dated settings and fixed costs for the P&L (ADR 0031, spec §3 R1/R2/R15).
 * Pure — no Firestore, no clock.
 */
import { bangkokDateKey } from "@/lib/compensationCompute";
import type { PnlFixedCostVersion, PnlRateKind, PnlRateRow } from "@/validate/pnlSettingsSchema";
import type { PnlRenewalInput } from "./types";

const BKK_OFFSET_MS = 7 * 60 * 60 * 1000;
export const DAY_MS = 24 * 60 * 60 * 1000;

/** Round to satang. Every money component is rounded before it is summed (spec §3). */
export function round2(n: number): number {
    return Math.round((n + Number.EPSILON) * 100) / 100;
}

export function toMonthKey(year: number, month: number): string {
    return `${year}-${String(month).padStart(2, "0")}`;
}

/** [start, end) instants of a Bangkok calendar month. */
export function bangkokMonthBounds(year: number, month: number): { start: Date; end: Date } {
    return {
        start: new Date(Date.UTC(year, month - 1, 1) - BKK_OFFSET_MS),
        end: new Date(Date.UTC(year, month, 1) - BKK_OFFSET_MS),
    };
}

/** Bangkok 12:00 of the calendar day an instant falls on — the helper-window fallback anchor. */
export function bangkokNoonOf(d: Date): Date {
    const [y, m, day] = bangkokDateKey(d).split("-").map(Number);
    return new Date(Date.UTC(y, m - 1, day, 12) - BKK_OFFSET_MS);
}

/** P&L date of a trip: the task's check-in (actual work date), falling back to delivery (R5). */
export function pnlDateKeyForTrip(checkInAt?: Date, deliveredAt?: Date): string | null {
    const at = checkInAt ?? deliveredAt;
    return at ? bangkokDateKey(at) : null;
}

/** P&L date of a standby: startedAt → endedAt → createdAt (R6). */
export function pnlDateKeyForStandby(s: { startedAt?: Date; endedAt?: Date; createdAt?: Date }): string | null {
    const at = s.startedAt ?? s.endedAt ?? s.createdAt;
    return at ? bangkokDateKey(at) : null;
}

export function addMonths(monthKey: string, n: number): string {
    const [y, m] = monthKey.split("-").map(Number);
    const idx = y * 12 + (m - 1) + n;
    return toMonthKey(Math.floor(idx / 12), (idx % 12) + 1);
}

export function monthsBetween(fromMonthKey: string, toMonthKeyExclusive: string): number {
    const [ya, ma] = fromMonthKey.split("-").map(Number);
    const [yb, mb] = toMonthKeyExclusive.split("-").map(Number);
    return yb * 12 + mb - (ya * 12 + ma);
}

function createdMs(row: { createdAt?: Date | null }): number {
    return row.createdAt instanceof Date ? row.createdAt.getTime() : 0;
}

/**
 * The rate of `kind` in force on `dateKey` (R1): non-voided rows with effectiveFrom <= dateKey,
 * the latest effectiveFrom wins, ties go to the most recently created row.
 */
export function selectPnlRate(rates: PnlRateRow[], kind: PnlRateKind, dateKey: string): PnlRateRow | null {
    let best: PnlRateRow | null = null;
    for (const r of rates) {
        if (r.voided || r.kind !== kind || r.effectiveFrom > dateKey) continue;
        if (
            !best ||
            r.effectiveFrom > best.effectiveFrom ||
            (r.effectiveFrom === best.effectiveFrom && createdMs(r) > createdMs(best))
        ) {
            best = r;
        }
    }
    return best;
}

/** The version of ONE fixed-cost item in force in `monthKey` (R2), or null. */
export function activeFixedCostVersion(versions: PnlFixedCostVersion[], monthKey: string): PnlFixedCostVersion | null {
    let best: PnlFixedCostVersion | null = null;
    for (const v of versions) {
        if (v.voided || v.effectiveFromMonth > monthKey) continue;
        if (
            !best ||
            v.effectiveFromMonth > best.effectiveFromMonth ||
            (v.effectiveFromMonth === best.effectiveFromMonth && createdMs(v) > createdMs(best))
        ) {
            best = v;
        }
    }
    return best;
}

/** Monthly amount of ONE fixed-cost item in `monthKey`: 0 when stopped, ended or not yet started. */
export function fixedCostMonthlyAmount(versions: PnlFixedCostVersion[], monthKey: string): number {
    const v = activeFixedCostVersion(versions, monthKey);
    if (!v || v.isStop) return 0;
    if (v.effectiveToMonth && monthKey > v.effectiveToMonth) return 0;
    return v.frequency === "yearly" ? v.amountThb / 12 : v.amountThb;
}

/** Group fixed-cost versions by item. */
export function groupFixedCostItems(versions: PnlFixedCostVersion[]): Map<string, PnlFixedCostVersion[]> {
    const items = new Map<string, PnlFixedCostVersion[]>();
    for (const v of versions) {
        const list = items.get(v.itemId) ?? [];
        list.push(v);
        items.set(v.itemId, list);
    }
    return items;
}

export interface RenewalSpread {
    /** monthKey → ฿ for that month */
    byMonth: Map<string, number>;
    /** monthKeys covered by a renewal whose amount is 0 (F15) */
    zeroAmountMonths: Set<string>;
}

/**
 * Spread one truck's insurance or tax renewals over the months they cover (R15): renewal i covers
 * its own month up to the month before renewal i+1, capped at 12 months, and its amount is divided
 * evenly over those months — so every baht is recognised even when a renewal comes early.
 */
export function renewalMonthlyAmounts(
    renewals: PnlRenewalInput[],
    truckId: string,
    type: "insurance" | "tax",
): RenewalSpread {
    const list = renewals
        .filter((r) => r.truckId === truckId && r.type === type && /^\d{4}-\d{2}/.test(r.date))
        .sort((a, b) => (a.date === b.date ? a.id.localeCompare(b.id) : a.date < b.date ? -1 : 1));
    const byMonth = new Map<string, number>();
    const zeroAmountMonths = new Set<string>();
    list.forEach((r, i) => {
        const start = r.date.slice(0, 7);
        const next = list[i + 1];
        const months = next ? Math.max(1, Math.min(12, monthsBetween(start, next.date.slice(0, 7)))) : 12;
        const perMonth = r.amount > 0 ? r.amount / months : 0;
        for (let k = 0; k < months; k++) {
            const mk = addMonths(start, k);
            byMonth.set(mk, (byMonth.get(mk) ?? 0) + perMonth);
            if (!(r.amount > 0)) zeroAmountMonths.add(mk);
        }
    });
    return { byMonth, zeroAmountMonths };
}
