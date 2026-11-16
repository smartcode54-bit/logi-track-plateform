import { resolveHubOrSocDisplay } from "@/lib/hubDisplay";
import type { PnlWarningCode } from "@/lib/pnlCompute";

export function fmtThb(n: number | null | undefined): string {
    if (n == null || !Number.isFinite(n)) return "—";
    // Cost lines are shown negated, so an empty line would print "-0.00".
    const v = Math.abs(n) < 0.005 ? 0 : n;
    return v.toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 });
}

export function fmtNum(n: number | null | undefined, digits = 1): string {
    if (n == null || !Number.isFinite(n)) return "—";
    return n.toLocaleString("en-US", { maximumFractionDigits: digits });
}

export function fmtPct(n: number | null | undefined): string {
    if (n == null || !Number.isFinite(n)) return "—";
    return `${n.toFixed(1)}%`;
}

export function hubLabel(code: string | undefined, hubLabels: Record<string, string>): string {
    if (!code) return "?";
    return resolveHubOrSocDisplay(code, hubLabels);
}

/** "Origin → Dest" or "Origin → A + B" for multi-drop. */
export function routeLabel(origin: string | undefined, destinations: string[], hubLabels: Record<string, string>): string {
    const dests = destinations.length > 0 ? destinations.map((d) => hubLabel(d, hubLabels)).join(" + ") : "?";
    return `${hubLabel(origin, hubLabels)} → ${dests}`;
}

/** Display name of a fixed-cost category: the owner's name for a custom one, else the i18n label. */
export function fixedCategoryLabel(
    category: string,
    customLabel: string | undefined,
    t: (key: string, params?: Record<string, string | number>) => string,
): string {
    return category === "custom" && customLabel ? customLabel : t(`pnl.settings.fixed.category.${category}`);
}

/** Warning codes that exclude a row vs. ones that only inform. */
export const EXCLUDING_CODES: PnlWarningCode[] = ["unpriced", "noJobDistance", "noTruck", "missingRate", "noFuelRate"];

export function monthOptions(): Array<{ value: string; year: number; month: number }> {
    const now = new Date();
    const out: Array<{ value: string; year: number; month: number }> = [];
    for (let i = 0; i < 24; i++) {
        const d = new Date(now.getFullYear(), now.getMonth() - i, 1);
        const year = d.getFullYear();
        const month = d.getMonth() + 1;
        out.push({ value: `${year}-${String(month).padStart(2, "0")}`, year, month });
    }
    return out;
}
