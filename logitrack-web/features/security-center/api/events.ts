/**
 * `['security','events',{...}]`: the security events feed over Go (T54S; `GET /v1/security-events`,
 * T51, Appendix B §B.2.6; developer-spec.md §10.7, §10.10; Appendix E §E.5 row 31). Replaces the
 * `onSnapshot` of 150 documents (`hooks/useSecurityEventsFeed.ts`) and the audit page's 500-row
 * `getDocs` (`audit/page.tsx:94-130`): a keyset list, newest first, 100 per page with "load more",
 * the type, severity and date range in the key and in the request (RLS decides the rows). Polled every
 * 30 s while the tab is visible (no web topic carries `security.event` to tenant staff, Appendix B
 * §B.4.2; a platform principal's `platform:security` stream has no producer yet), only while one page is
 * loaded (`pollHeadWhileVisible`), and every 5 min for a platform principal (`reachPollMs`).
 *
 * A platform principal reads with `X-Act-On-Tenant: *`, so each of its requests adds one
 * `platform_cross_tenant_access` warning row that the next read returns (the row commits before the
 * handler reads). `isOwnCrossTenantRead` names those rows of the viewer, so the pages can hide them by
 * default behind a visible toggle with a count (never a silent drop); the server counters of
 * `GET /v1/security/overview` still include them (developer-spec.md §19.2 question 20).
 */
import { useInfiniteQuery } from "@tanstack/react-query";

import { neutraliseCsvFormula } from "@/lib/csv";
import type { GoPage } from "@/lib/goQuery";
import { goInfiniteQueryFn, goNextPageParam } from "@/lib/goQuery";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";
import { pollHeadWhileVisible } from "@/lib/realtime/polling";
import { reachHeaders, reachPollMs, type SecurityReach } from "./reach";

export const SECURITY_EVENTS_PATH = "/v1/security-events";
/** Rows per page of the audit list (developer-spec.md §10.10). */
export const SECURITY_EVENTS_PAGE_SIZE = 100;
/** The overview's "recent events" card. */
export const RECENT_EVENTS_LIMIT = 8;
/**
 * The rows the overview reads for that card for a platform principal: enough that the 8 newest
 * events other than the viewer's own cross-tenant reads are usually among them.
 */
export const RECENT_EVENTS_FETCH_PLATFORM = 50;
/** Poll while visible (Appendix E §E.5 row 31). */
export const SECURITY_EVENTS_POLL_MS = 30_000;

export type Severity = "info" | "warning" | "critical";

/** One row of `GET /v1/security-events` (Go `security.Row`). */
export interface SecurityEventDTO {
    id: string;
    createdAt: string;
    /** snake_case, `meta.types` lists the catalogue; a legacy row may carry another type. */
    type: string;
    severity: Severity | string;
    summary: string;
    details: Record<string, unknown> | null;
    actorUserId: string | null;
    /** The actor's address when the event was written (older rows are named on read). */
    actorEmail: string | null;
    /** A legacy actor uid that no user matched at ETL. */
    actorLegacyUid: string | null;
    targetUserId: string | null;
    tenantId: string | null;
    requestId: string | null;
}

export interface SecurityEventsFilter {
    /** One type, or a comma list of up to 20; "" = every type. */
    type: string;
    /** `info`, `warning`, `critical`; "" = every severity. */
    severity: string;
    /** RFC 3339: `createdAt >= from`; "" = no lower bound. */
    from: string;
    /** RFC 3339: `createdAt < to`; "" = no upper bound. */
    to: string;
}

export const NO_EVENT_FILTER: SecurityEventsFilter = { type: "", severity: "", from: "", to: "" };

export function securityEventsKey(filter: SecurityEventsFilter, reach: SecurityReach, limit: number) {
    return queryKeys.security.events({ type: filter.type, severity: filter.severity, from: filter.from, to: filter.to, limit, reach });
}

type EventsKey = ReturnType<typeof securityEventsKey>;

/** The query of `GET /v1/security-events` (only the documented parameters, empty ones left out). */
export function securityEventsQuery(p: SecurityEventsFilter & { limit: number }) {
    return {
        type: p.type || undefined,
        severity: p.severity || undefined,
        from: p.from || undefined,
        to: p.to || undefined,
        limit: p.limit,
    };
}

export interface UseSecurityEventsOptions {
    pageSize?: number;
    enabled?: boolean;
}

/**
 * The keyset feed; disabled until the reach is known. It polls only while it holds its first page
 * (TanStack refetches every loaded page, and for a platform principal each request is an audit row):
 * every 30 s for a tenant principal, every 5 min (poll and focus) for a platform principal.
 */
export function useSecurityEvents(
    filter: SecurityEventsFilter,
    reach: SecurityReach | undefined,
    { pageSize = SECURITY_EVENTS_PAGE_SIZE, enabled = true }: UseSecurityEventsOptions = {}
) {
    return useInfiniteQuery({
        queryKey: securityEventsKey(filter, reach ?? "tenant", pageSize),
        queryFn: goInfiniteQueryFn<SecurityEventDTO, EventsKey>(SECURITY_EVENTS_PATH, {
            query: ([, , p]) => securityEventsQuery(p),
            headers: ([, , p]) => reachHeaders(p.reach),
        }),
        initialPageParam: undefined as string | undefined,
        getNextPageParam: goNextPageParam,
        gcTime: QUERY_POLICY.live.gcTime,
        staleTime: reachPollMs(reach, QUERY_POLICY.live.staleTime),
        ...pollHeadWhileVisible(reachPollMs(reach, SECURITY_EVENTS_POLL_MS)),
        enabled: enabled && reach !== undefined,
    });
}

/** The audit row Go writes for each `X-Act-On-Tenant` request (Appendix C §C.3.9). */
export const CROSS_TENANT_ACCESS = "platform_cross_tenant_access";

/**
 * Whether `e` records one of the viewer's own accepted cross-tenant reads (a `GET` or `HEAD` that a
 * Security Center page of theirs sent with `X-Act-On-Tenant`). A refused, repeated or malformed
 * attempt, a write, and anyone else's access are never matched.
 */
export function isOwnCrossTenantRead(e: Pick<SecurityEventDTO, "type" | "actorUserId" | "details">, viewerId: string | null | undefined): boolean {
    if (!viewerId || e.type !== CROSS_TENANT_ACCESS || e.actorUserId !== viewerId) return false;
    const d = e.details ?? {};
    const method = typeof d.method === "string" ? d.method.toUpperCase() : "";
    return (method === "GET" || method === "HEAD") && d.outcome === "accepted";
}

/**
 * The rows a page shows: all of them when `showOwn`, else those that are not the viewer's own
 * cross-tenant reads; `own` counts the viewer's rows among `rows` either way (the toggle shows it).
 */
export function withoutOwnReads(
    rows: readonly SecurityEventDTO[],
    viewerId: string | null | undefined,
    showOwn: boolean
): { rows: SecurityEventDTO[]; own: number } {
    const others = rows.filter((r) => !isOwnCrossTenantRead(r, viewerId));
    return { rows: showOwn ? [...rows] : others, own: rows.length - others.length };
}

/** Every row of the loaded pages, in order. */
export function eventRows(pages: readonly GoPage<SecurityEventDTO>[] | undefined): SecurityEventDTO[] {
    return (pages ?? []).flatMap((p) => (Array.isArray(p.data) ? p.data : []));
}

/** The type catalogue of the feed (`meta.types` of the first page), for the type filter. */
export function eventTypesOf(pages: readonly GoPage<SecurityEventDTO>[] | undefined): string[] {
    const types = pages?.[0]?.meta?.types;
    return Array.isArray(types) ? types.filter((t): t is string => typeof t === "string") : [];
}

/** The date ranges of the audit page. */
export const EVENT_RANGES = ["last7", "last30", "last90"] as const;
export type EventRange = (typeof EVENT_RANGES)[number];

const RANGE_DAYS: Record<EventRange, number> = { last7: 7, last30: 30, last90: 90 };

/**
 * The lower bound of a range: local midnight `days` days ago (as the Firestore page did), so the key
 * stays the same all day instead of moving with every render.
 */
export function rangeStart(range: EventRange, now: Date = new Date()): string {
    const d = new Date(now);
    d.setDate(d.getDate() - RANGE_DAYS[range]);
    d.setHours(0, 0, 0, 0);
    return d.toISOString();
}

/** The person (or principal) behind an event, for display: email, else the legacy uid, else the user id. */
export function eventActor(e: Pick<SecurityEventDTO, "actorEmail" | "actorLegacyUid" | "actorUserId">): string {
    return e.actorEmail || e.actorLegacyUid || e.actorUserId || "";
}

/** The target of an event, for display: an email or name in the details, else the target user id. */
export function eventTarget(e: Pick<SecurityEventDTO, "details" | "targetUserId">): string {
    const d = e.details ?? {};
    for (const k of ["email", "targetEmail", "name", "keyPrefix", "targetUid"]) {
        const v = d[k];
        if (typeof v === "string" && v) return v;
    }
    return e.targetUserId ?? "";
}

type Translate = (key: string, fallbackOrParams?: string | Record<string, string | number>) => string;

export function eventTypeLabel(type: string, t: Translate): string {
    return t(`securityCenter.securityEvents.types.${type}`, t("securityCenter.ui4.unknownEvent"));
}

export function eventActorLabel(e: SecurityEventDTO, t: Translate): string {
    return e.actorEmail || t(e.actorUserId || e.actorLegacyUid ? "securityCenter.ui4.unknownUser" : "securityCenter.audit.system");
}

export function eventTargetLabel(e: SecurityEventDTO, t: Translate): string {
    for (const key of ["email", "targetEmail", "name", "keyPrefix"]) {
        const value = e.details?.[key];
        if (typeof value === "string" && value) return value;
    }
    return eventTarget(e) ? t("securityCenter.ui4.unknownTarget") : "—";
}

/**
 * CSV of the loaded rows (the export of the audit page): one line per event, quoted fields. A field
 * that a spreadsheet would read as a formula (`=`, `+`, `-`, `@`, tab, CR first) is prefixed with `'`:
 * summaries and details hold user-chosen text (names, emails).
 */
export function eventsCsv(rows: readonly SecurityEventDTO[], t: Translate): string {
    const q = (v: string) => `"${neutraliseCsvFormula(v).replaceAll('"', '""')}"`;
    const header = ["timestamp", "type", "severity", "actor", "actorUserId", "target", "summary", "details", "tenantId", "requestId"];
    const lines = [[...header, ...["securityCenter.ui4.csvEventLabel", "securityCenter.ui4.csvActorLabel", "securityCenter.ui4.csvTargetLabel"].map((key) => q(t(key)))].join(",")];
    for (const r of rows) {
        lines.push(
            [
                r.createdAt,
                r.type,
                r.severity,
                eventActor(r),
                r.actorUserId ?? "",
                eventTarget(r),
                r.summary,
                JSON.stringify(r.details ?? {}),
                r.tenantId ?? "",
                r.requestId ?? "",
                eventTypeLabel(r.type, t),
                eventActorLabel(r, t),
                eventTarget(r) ? eventTargetLabel(r, t) : "",
            ]
                .map((v) => q(String(v)))
                .join(",")
        );
    }
    return lines.join("\n");
}
