/**
 * Month review-filter matching for the Billing Document (ADR 0005 — review filters narrow the preview,
 * never the invoice). Mirrors lib/vehicleClass.ts: options are built from the rows actually loaded,
 * so every option provably matches at least one row, and a row with no date lands in an explicit
 * "not specified" bucket instead of being silently dropped.
 *
 * Months are read on the **Bangkok calendar** — the same calendar the billing period and the period
 * locks use — so a trip at 23:30 on the 30th (ICT) is September, not whatever month the viewer's
 * browser timezone or UTC would say.
 */

import { bangkokDateStr } from "@/lib/billingDate";

/** Select value meaning "no month filter applied". */
export const MONTH_FILTER_ALL = "all";

/** Select value for rows with no date on the filtered axis. */
export const MONTH_FILTER_NONE = "__none__";

export interface MonthFilterOption {
    /** `yyyy-MM` (Bangkok calendar) or MONTH_FILTER_NONE. */
    value: string;
    /** Calendar year; 0 for the no-date bucket. */
    year: number;
    /** 1-12; 0 for the no-date bucket. */
    month: number;
    /** How many loaded rows this option matches. */
    count: number;
}

/** `yyyy-MM` of a date on the Bangkok calendar; no (valid) date → MONTH_FILTER_NONE. */
export function monthFilterKey(value?: Date | null): string {
    if (!value || Number.isNaN(value.getTime())) return MONTH_FILTER_NONE;
    return bangkokDateStr(value).slice(0, 7);
}

/** Options from the loaded rows' dates, oldest month first; the no-date bucket sorts last. */
export function buildMonthFilterOptions(dates: (Date | null | undefined)[]): MonthFilterOption[] {
    const byKey = new Map<string, MonthFilterOption>();
    for (const d of dates) {
        const key = monthFilterKey(d);
        const existing = byKey.get(key);
        if (existing) {
            existing.count += 1;
            continue;
        }
        const [y, m] = key === MONTH_FILTER_NONE ? [0, 0] : key.split("-").map(Number);
        byKey.set(key, { value: key, year: y, month: m, count: 1 });
    }
    return [...byKey.values()].sort((a, b) => {
        if (a.value === MONTH_FILTER_NONE) return 1;
        if (b.value === MONTH_FILTER_NONE) return -1;
        return a.value.localeCompare(b.value);
    });
}

/** True when a row's date belongs under the selected option. MONTH_FILTER_ALL matches everything. */
export function rowMatchesMonthFilter(value: Date | null | undefined, selected: string): boolean {
    if (!selected || selected === MONTH_FILTER_ALL) return true;
    return monthFilterKey(value) === selected;
}
