/**
 * Helper-day pay split over trips (ADR 0031 §5, spec R11).
 *
 * One helper-day = one ADR 0012 work window (12:00 D → 11:59 D+1 Bangkok, keyed D). Its P&L rate is
 * divided equally over the own-truck trips + standbys the helper rode in that window. A window in
 * which the helper drove their own job pays nothing as helper (payroll's no-double-pay rule). A
 * helper-day with no own-truck trip is a month-level line.
 */
import { helperWindowKey } from "@/lib/compensationCompute";
import type { PnlRateRow } from "@/validate/pnlSettingsSchema";
import { bangkokNoonOf, selectPnlRate } from "./settings";
import type { PnlHelperTaskInput, PnlTripInput } from "./types";

/** An own-truck trip or standby that may carry helpers. */
export interface HelperUnit {
    rowId: string;
    helperDriverIds: string[];
    /** Window anchor: task check-in → task date at Bangkok noon → the row's own time. */
    anchor?: Date;
}

export interface HelperShareResult {
    /**
     * shareThb = Σ rate ÷ trips over the row's eligible helper-days; dayFraction = Σ 1 ÷ trips (the
     * rate-free part, so a what-if helper rate R gives share = R × dayFraction).
     */
    byRowId: Map<string, { shareThb: number; dayFraction: number; missingRate: boolean }>;
    noTripHelperDays: Array<{ helperUid: string; windowKey: string; amountThb: number }>;
    /** Helper-days with no trip in the month whose helper_day rate is missing. */
    noTripMissingRate: number;
}

export function taskWindowAnchor(checkInAt?: Date, date?: Date, fallback?: Date): Date | undefined {
    return checkInAt ?? (date ? bangkokNoonOf(date) : undefined) ?? fallback;
}

export function helperShares(params: {
    units: HelperUnit[];
    helperTasks: PnlHelperTaskInput[];
    trips: PnlTripInput[];
    driverIdentity: Record<string, string[]>;
    rates: PnlRateRow[];
    monthKey: string;
}): HelperShareResult {
    const { units, helperTasks, trips, driverIdentity, rates, monthKey } = params;

    // Windows in which each driver key drove their own job (own tasks + delivered trips).
    const drivingWindows = new Map<string, Set<string>>();
    const addDriving = (driverId: string | undefined, at: Date | undefined) => {
        if (!driverId || !at) return;
        const set = drivingWindows.get(driverId) ?? new Set<string>();
        set.add(helperWindowKey(at));
        drivingWindows.set(driverId, set);
    };
    for (const t of helperTasks) addDriving(t.driverId, taskWindowAnchor(t.checkInAt, t.date));
    for (const t of trips) if (t.status === "delivered") addDriving(t.driverId, t.deliveredAt ?? t.checkInAt);
    const drove = (helperUid: string, windowKey: string) =>
        (driverIdentity[helperUid] ?? [helperUid]).some((k) => drivingWindows.get(k)?.has(windowKey));

    // Group own-truck rows by helper × window.
    const groups = new Map<string, { helperUid: string; windowKey: string; rowIds: string[] }>();
    for (const u of units) {
        if (!u.anchor || u.helperDriverIds.length === 0) continue;
        const windowKey = helperWindowKey(u.anchor);
        for (const h of new Set(u.helperDriverIds)) {
            const k = `${h}|${windowKey}`;
            const g = groups.get(k) ?? { helperUid: h, windowKey, rowIds: [] };
            g.rowIds.push(u.rowId);
            groups.set(k, g);
        }
    }

    const byRowId: HelperShareResult["byRowId"] = new Map();
    const bump = (rowId: string, share: number, fraction: number, missingRate: boolean) => {
        const cur = byRowId.get(rowId) ?? { shareThb: 0, dayFraction: 0, missingRate: false };
        byRowId.set(rowId, {
            shareThb: cur.shareThb + share,
            dayFraction: cur.dayFraction + fraction,
            missingRate: cur.missingRate || missingRate,
        });
    };
    for (const g of groups.values()) {
        if (drove(g.helperUid, g.windowKey)) continue;
        const rate = selectPnlRate(rates, "helper_day", g.windowKey);
        const n = g.rowIds.length;
        for (const rowId of g.rowIds) bump(rowId, rate ? rate.amountThb / n : 0, 1 / n, !rate);
    }

    // Helper-days in this month with no own-truck trip → month line.
    const noTripHelperDays: HelperShareResult["noTripHelperDays"] = [];
    let noTripMissingRate = 0;
    const seen = new Set<string>();
    for (const t of helperTasks) {
        if (t.helperDriverIds.length === 0) continue;
        const anchor = taskWindowAnchor(t.checkInAt, t.date);
        if (!anchor) continue;
        const windowKey = helperWindowKey(anchor);
        if (!windowKey.startsWith(monthKey)) continue;
        for (const h of t.helperDriverIds) {
            const k = `${h}|${windowKey}`;
            if (seen.has(k) || groups.has(k) || drove(h, windowKey)) continue;
            seen.add(k);
            const rate = selectPnlRate(rates, "helper_day", windowKey);
            if (!rate) {
                noTripMissingRate++;
                continue;
            }
            noTripHelperDays.push({ helperUid: h, windowKey, amountThb: rate.amountThb });
        }
    }
    return { byRowId, noTripHelperDays, noTripMissingRate };
}
