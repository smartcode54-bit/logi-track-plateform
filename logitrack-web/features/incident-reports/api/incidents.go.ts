/**
 * Incident reports over Go (T34, P2; `GET /v1/incidents` of T30, Appendix B §B.2.12; developer-spec.md §10.7 row
 * `['incidents', …]`; Appendix E §E.2.1 row incident-reports, §E.5 rows 7-8; TW8 M2).
 *
 * `['incidents',{from,to}]` is one keyset list (newest first, 100 per page, "load more"; no cap, the legacy listener
 * stopped at 200). Each row carries the trip number, the driver's name and the three photos signed, so the
 * 300-driver listener is gone; a customer-scope principal reads the incidents of the trips it sees (RLS and the
 * `scope_incidents` view): the 500-trip lookup and its "0 trips sees everything" path
 * (`incident-reports/page.tsx:126-146,185`) are gone. SSE `incident.created` (tenant:{tid}:trips) makes the key stale.
 */
import { infiniteQueryOptions } from "@tanstack/react-query";
import { goInfiniteQueryFn, goNextPageParam } from "@/lib/goQuery";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";
import type { FileRefDTO } from "@/features/tasks/api/tasks.go";

export const INCIDENTS_PATH = "/v1/incidents";
export const INCIDENTS_PAGE_SIZE = 100;

/** `GET /v1/incidents` item (internal/operations `IncidentView` for staff). */
export interface IncidentDTO {
    id: string;
    tenantId: string;
    tripId: string | null;
    tripNo: string | null;
    driverId: string | null;
    driverName: string | null;
    delayCause: string | null;
    description: string | null;
    lat: number | null;
    lng: number | null;
    truckId: string | null;
    truckLicensePlate: string | null;
    mapPhoto: FileRefDTO | null;
    situation1Photo: FileRefDTO | null;
    situation2Photo: FileRefDTO | null;
    reportedByKind: "driver" | "admin" | string;
    createdAt: string;
}

export interface IncidentFilter {
    /** Bangkok days `yyyy-MM-dd` (both inclusive), or "" for no bound. */
    from: string;
    to: string;
}

type IncidentKey = readonly ["incidents", IncidentFilter];

export function incidentsQuery(filter: IncidentFilter, enabled = true) {
    return infiniteQueryOptions({
        queryKey: queryKeys.incidents({ from: filter.from, to: filter.to }) as IncidentKey,
        queryFn: goInfiniteQueryFn<IncidentDTO, IncidentKey>(INCIDENTS_PATH, {
            query: ([, f]) => ({ from: f.from || undefined, to: f.to || undefined, limit: INCIDENTS_PAGE_SIZE }),
        }),
        initialPageParam: undefined as string | undefined,
        getNextPageParam: goNextPageParam,
        ...QUERY_POLICY.live,
        enabled,
    });
}

/** The cause label of a legacy `incident_cause_*` value. */
export function incidentCause(cause: string | null | undefined): string {
    if (!cause) return "—";
    return cause.replace(/^incident_cause_/, "").replace(/_/g, " ").toUpperCase();
}

/** A text search over the loaded rows (trip number, driver, plate, description, cause). */
export function matchesIncident(row: IncidentDTO, q: string): boolean {
    const term = q.trim().toLowerCase();
    if (!term) return true;
    return [row.tripNo, row.driverName, row.truckLicensePlate, row.description, row.delayCause]
        .some((v) => (v ?? "").toLowerCase().includes(term));
}
