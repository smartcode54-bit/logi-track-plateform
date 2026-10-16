/**
 * The dashboard over Go (T54R, issue #74; T53 routes, Appendix B §B.2.19; developer-spec.md §10.13;
 * Appendix E §E.2.1, §E.6):
 *
 * - `['dashboard','summary',{from,to}]`: `GET /v1/dashboard/summary?from&to`, the whole page in one
 *   request (W8, TW6): trips, activity histogram, incidents, drivers, expenses, chats, compliance and the
 *   recent-updates feed as SQL aggregates under RLS, each block present only for a principal holding its
 *   capability. Replaces about 10.7k document reads per visit (`DashboardStats.tsx:111-149`,
 *   `ActivityChart.tsx:45-50`, `RecentUpdates.tsx:51-77`, `ComplianceSummary.tsx:32-34`). 60 s / 5 min,
 *   polled every 60 s while the tab is visible.
 * - The period is Bangkok calendar days, both ends included: the week is the last 7 days ending today,
 *   the month the last 30 (the two tabs of the old activity chart). The analytics figures (the
 *   histogram, `reporting:view_analytics`) and the parcels of the trips card (the old "packages") are
 *   blocks of this one answer: `/app/analytics` and `/app/packages` have no page (developer-spec.md
 *   §10.5).
 *
 * The dashboard summary describes the active tenant. Platform user counts use the explicit
 * platform statistics endpoint through the shared users-by-role hook.
 */
import { keepPreviousData, useQuery } from "@tanstack/react-query";

import { bangkokDateStr } from "@/lib/billingDate";
import { goQueryFn } from "@/lib/goQuery";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";
import { POLL_INTERVAL_MS, pollWhileVisible } from "@/lib/realtime/polling";

export const DASHBOARD_SUMMARY_PATH = "/v1/dashboard/summary";
/** The summary's poll while the tab is visible (Appendix E §E.6). */
export const DASHBOARD_POLL_MS = POLL_INTERVAL_MS;

export type DashboardPeriodName = "week" | "month";

/** Days of each period, today included. */
export const PERIOD_DAYS: Record<DashboardPeriodName, number> = { week: 7, month: 30 };

/** `?from&to`: Bangkok calendar days (`yyyy-MM-dd`), both included. */
export interface DashboardRange {
    from: string;
    to: string;
}

/** `yyyy-MM-dd` plus `days` (negative: earlier). Pure calendar arithmetic, no time zone involved. */
export function addDays(date: string, days: number): string {
    const [y, m, d] = date.split("-").map(Number);
    const t = new Date(Date.UTC(y, m - 1, d + days));
    return t.toISOString().slice(0, 10);
}

/** The range of a period ending today in Bangkok (`now` for tests). */
export function periodRange(period: DashboardPeriodName, now: Date = new Date()): DashboardRange {
    const to = bangkokDateStr(now);
    return { from: addDays(to, -(PERIOD_DAYS[period] - 1)), to };
}

// --- the body of GET /v1/dashboard/summary (Go dashboard.Summary) ----------------------------------

export interface SummaryPeriodDTO {
    from: string;
    to: string;
    days: number;
}

/** `operations:view_driver_monitor`: delivered trips of the period, a delivered trip with an incident is delayed. */
export interface TripsBlockDTO {
    delivered: number;
    parcels: number;
    withIncident: number;
    /** One decimal; null without a delivered trip. */
    onTimePct: number | null;
    delayPct: number | null;
}

/** `reporting:view_analytics`: one entry per Bangkok day of the period, zero days included. */
export interface ActivityBlockDTO {
    days: { date: string; delivered: number }[];
    total: number;
    previousTotal: number;
    /** Whole percent against the window of the same length before; null when there is nothing to compare. */
    trendPct: number | null;
}

/** `drivers:view`: drivers by status (the card shows active + onDuty, total as the fallback). */
export interface DriversBlockDTO {
    active: number;
    onDuty: number;
    inactive: number;
    total: number;
}

/** `fleet:view_trucks`: the due tax, insurance and service items of the tenant's active trucks. */
export interface ComplianceItemDTO {
    truckId: string;
    licensePlate: string;
    kind: "tax" | "insurance" | "service_date" | "service_km";
    dueDate?: string;
    /** Negative once overdue. */
    daysRemaining?: number;
    kmRemaining?: number;
}

export interface ComplianceBlockDTO {
    taxDue: number;
    insuranceDue: number;
    serviceDateDue: number;
    serviceKmDue: number;
    /** The most urgent first, at most 50; `itemsTotal` counts them all. */
    items: ComplianceItemDTO[];
    itemsTotal: number;
}

/** One entry of the recent-updates feed, newest first; the web builds the text from the raw fields. */
export interface RecentItemDTO {
    type: "delivered" | "driver" | "incident" | "maintenance";
    id: string;
    at: string;
    plate?: string;
    place?: string;
    name?: string;
    text?: string;
}

export interface DashboardSummaryDTO {
    period: SummaryPeriodDTO;
    trips?: TripsBlockDTO;
    activity?: ActivityBlockDTO;
    /** `operations:view_incidents` */
    incidents?: { inPeriod: number };
    drivers?: DriversBlockDTO;
    /** `accounting:view_fuel` / `accounting:view_other`, each type for its key. */
    expenses?: { pendingFuel?: number; pendingOther?: number };
    /** `chat:view` */
    chats?: { unassigned: number; urgent: number };
    compliance?: ComplianceBlockDTO;
    recent: RecentItemDTO[];
}

export function dashboardSummaryKey(range: DashboardRange) {
    return queryKeys.dashboard.summary({ from: range.from, to: range.to });
}

type SummaryKey = ReturnType<typeof dashboardSummaryKey>;

/** `['dashboard','summary',{from,to}]`, polled every 60 s while visible; the previous period stays on screen while another loads. */
export function useDashboardSummary(range: DashboardRange, enabled = true) {
    return useQuery({
        queryKey: dashboardSummaryKey(range),
        queryFn: goQueryFn<DashboardSummaryDTO, SummaryKey>(DASHBOARD_SUMMARY_PATH, { query: ([, , p]) => ({ from: p.from, to: p.to }) }),
        ...QUERY_POLICY.list,
        ...pollWhileVisible(DASHBOARD_POLL_MS),
        placeholderData: keepPreviousData,
        enabled,
    });
}

/** The active-drivers figure of the card: active + on duty, or every driver when none is either (the old rule). */
export function activeDrivers(d: DriversBlockDTO): number {
    return d.active + d.onDuty || d.total;
}
