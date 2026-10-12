/**
 * The driver monitor over Go (T34, P2; routes of T30, T31 and T38, Appendix B §B.2.12; developer-spec.md §10.7 row
 * `['trips','monitor', …]`, §10.9 W8; Appendix E §E.2.1 row driver-monitor, §E.5 rows 24-27; TW6 and TW8 M2).
 *
 * `['trips','monitor',{from,to,filters}]` is one keyset list over `GET /v1/trips/monitor` (200 per page, newest
 * first): the server joins each trip with its task (check-in, round), its driver's name, its incident and photo
 * counts, the resolved places and stops and, for `accounting:view_income`, the stored price; every filter runs in
 * SQL, and the first page carries the filter options of the range with their counts (`meta.facets`). It replaces
 * the unbounded trips listener, the drivers, incidents and tasks side listeners (with their 30-id chunk reads), the
 * rate-card and fuel-adjustment fan-out on every change and the per-row `computeTripBillingSnapshot` calls of
 * `useDriverMonitor.ts:443-840`: the page reads no rate card and prices nothing (price is the server's, at delivery).
 *
 * SSE: `trip.*`, `standby.*`, `incident.created` (tenant:{tid}:trips) and `task.*` (tenant:{tid}:tasks) make the
 * key stale through the 2 s window (lib/realtime/eventMap.ts), so 50 trip events cost at most one refetch per 2 s.
 *
 * The Excel export pages through the same route with its cursor (`facets=false`) and loads xlsx lazily.
 */
import { infiniteQueryOptions, queryOptions, type QueryClient } from "@tanstack/react-query";
import { goFetch, goFetchEnvelope, goPath, type QueryParams } from "@/lib/goFetch";
import { goInfiniteQueryFn, goNextPageParam, type GoPage } from "@/lib/goQuery";
import type { PlaceFilterOption } from "@/lib/placeFilter";
import { getQueryClient } from "@/lib/queryClient";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";
import type { PlateFilterOption } from "@/lib/truckPlate";
import type { FileRefDTO, PlaceDTO } from "@/features/tasks/api/tasks.go";

export const MONITOR_PATH = "/v1/trips/monitor";
/** Go's page size of the monitor (AC of T30: at most 200). */
export const MONITOR_PAGE_SIZE = 200;
/** Go's widest monitor range (internal/operations `MonitorMaxSpan`). */
export const MONITOR_MAX_DAYS = 366;
export const MONITOR_DEFAULT_DAYS = 30;
/** The none bucket of a filter (`__none__`, lib/truckPlate.ts, lib/placeFilter.ts). */
export const FILTER_NONE = "__none__";

export const TRIP_STATUSES = ["in_transit", "incident", "delivered", "standby", "cancelled"] as const;
export type TripStatus = (typeof TRIP_STATUSES)[number];

export interface MonitorStopDTO {
    index: number;
    place: PlaceDTO;
    status: string;
    deliveredAt: string | null;
    completedSeq: number | null;
}

export interface PricePreviewDTO {
    estimateThb: number | null;
    unpricedReason: string | null;
    manualOverride: boolean;
    jobCategoryAtPricing: string | null;
    isMultiDelivery: boolean;
    computedAt: string;
}

/** `GET /v1/trips/monitor` item (internal/operations `MonitorRow`). */
export interface MonitorRowDTO {
    id: string;
    tenantId: string;
    tripNo: string;
    status: TripStatus | string;
    jobType: "first_mile" | "line_haul" | string;
    jobCategory: string | null;
    createdAt: string;
    updatedAt: string;
    deliveredAt: string | null;
    taskId: string | null;
    taskNo: string | null;
    taskStatus: string | null;
    checkInAt: string | null;
    runOrder: number | null;
    planAt: string | null;
    driverId: string | null;
    driverName: string | null;
    origin: PlaceDTO;
    destination: PlaceDTO;
    stops: MonitorStopDTO[];
    truck: { truckId: string | null; plate: string | null; key: string; vehicleClass: string | null; vehicleClassKey: string };
    partnerCode: string | null;
    sealCode: string | null;
    isMultiDelivery: boolean;
    incidentCount: number;
    photoCount: number;
    // staff only
    deliveredVia?: string | null;
    needsAdminReview?: boolean;
    reviewStatus?: string | null;
    /** `accounting:view_income` only: the stored snapshot, never computed on read. */
    price?: PricePreviewDTO;
}

export interface MonitorFacetsDTO {
    total: number;
    statusCounts: Record<string, number>;
    incident: number;
    origins: PlaceFilterOption[];
    destinations: PlaceFilterOption[];
    plates: PlateFilterOption[];
    vehicleClasses: { value: string; label: string; count: number }[];
    partners: { value: string; label: string; count: number }[];
}

/** The monitor's filters: every one is a server parameter and part of the key (W6). */
export type MonitorFilter = {
    /** Bangkok days `yyyy-MM-dd`, both inclusive. */
    from: string;
    to: string;
    status: string;
    jobType: string;
    driverId: string;
    partner: string;
    plate: string;
    vehicleClass: string;
    origin: string;
    destination: string;
    q: string;
};

export const MONITOR_ALL = "all";

export function emptyMonitorFilter(from: string, to: string): MonitorFilter {
    return { from, to, status: MONITOR_ALL, jobType: MONITOR_ALL, driverId: "", partner: MONITOR_ALL, plate: MONITOR_ALL,
        vehicleClass: MONITOR_ALL, origin: MONITOR_ALL, destination: MONITOR_ALL, q: "" };
}

/** The query of `GET /v1/trips/monitor` for a filter ("all" and "" are left out). */
export function monitorQueryParams(f: MonitorFilter): QueryParams {
    const v = (x: string) => (x && x !== MONITOR_ALL ? x : undefined);
    return {
        from: f.from,
        to: f.to,
        status: v(f.status),
        jobType: v(f.jobType),
        driverId: v(f.driverId),
        partner: v(f.partner),
        plate: v(f.plate),
        vehicleClass: v(f.vehicleClass),
        origin: v(f.origin),
        destination: v(f.destination),
        q: f.q.trim() || undefined,
    };
}

/** How many filters besides the range are set (the "clear all" button). */
export function activeMonitorFilters(f: MonitorFilter): number {
    return [f.status, f.jobType, f.partner, f.plate, f.vehicleClass, f.origin, f.destination].filter((x) => x !== MONITOR_ALL).length +
        (f.driverId ? 1 : 0) + (f.q.trim() ? 1 : 0);
}

type MonitorKey = readonly ["trips", "monitor", MonitorFilter];

export function monitorQuery(filter: MonitorFilter, enabled = true) {
    return infiniteQueryOptions({
        queryKey: queryKeys.trips.monitor(filter) as MonitorKey,
        queryFn: goInfiniteQueryFn<MonitorRowDTO, MonitorKey>(MONITOR_PATH, {
            query: ([, , f]) => ({ ...monitorQueryParams(f), limit: MONITOR_PAGE_SIZE }),
        }),
        initialPageParam: undefined as string | undefined,
        getNextPageParam: goNextPageParam,
        ...QUERY_POLICY.live,
        enabled: enabled && Boolean(filter.from && filter.to),
    });
}

/** The facets of the first page (`meta.facets`), when it carried them. */
export function monitorFacets(pages: GoPage<MonitorRowDTO>[] | undefined): MonitorFacetsDTO | null {
    const meta = pages?.[0]?.meta as { facets?: MonitorFacetsDTO } | undefined;
    return meta?.facets ?? null;
}

/**
 * Every row of a filter, page by page through the cursor (`facets=false`), for the export: no window, no cap
 * (the Firestore page re-read Firestore in batches of 500, `DriverMonitorDashboard.tsx:327-350`).
 */
export async function fetchMonitorRows(filter: MonitorFilter, signal?: AbortSignal, onPage?: (n: number) => void): Promise<MonitorRowDTO[]> {
    const out: MonitorRowDTO[] = [];
    let cursor: string | undefined;
    for (;;) {
        const page = await goFetchEnvelope<MonitorRowDTO[]>(MONITOR_PATH, {
            signal,
            query: { ...monitorQueryParams(filter), limit: MONITOR_PAGE_SIZE, facets: false, cursor },
        });
        out.push(...(page.data ?? []));
        onPage?.(out.length);
        cursor = page.nextCursor || undefined;
        if (!cursor) return out;
    }
}

// --- the trip detail -----------------------------------------------------------------------------------

export interface TripStopDTO {
    id: string;
    index: number;
    destination: string;
    destinationHubId: string | null;
    place: PlaceDTO;
    sourceId: string | null;
    status: "pending" | "delivered" | "failed" | string;
    deliveredAt: string | null;
    deliveredLat: number | null;
    deliveredLng: number | null;
    completedSeq: number | null;
}

export interface TripPhotoDTO {
    id: string;
    type: string;
    known: boolean;
    stopId: string | null;
    fileId: string;
    key: string | null;
    url: string | null;
    geocoding: { lat: number | null; lng: number | null; address: string | null; at: string | null } | null;
    rank: number;
    createdAt: string;
}

/** `GET /v1/trips/{id}` (internal/operations `TripDetailView`, staff). */
export interface TripDetailDTO {
    id: string;
    tenantId: string;
    tripNo: string;
    status: string;
    jobType: string;
    jobCategory: "PRIMARY" | "SUPPLEMENTARY" | null;
    taskId: string | null;
    driverId: string | null;
    driverName: string | null;
    origin: string | null;
    originHubId: string | null;
    originPlace: PlaceDTO;
    destination: string | null;
    destinationHubId: string | null;
    destinationPlace: PlaceDTO;
    sealCode: string | null;
    partnerCode: string | null;
    truckId: string | null;
    truckLicensePlate: string | null;
    vehicleClass: string | null;
    deliveredAt: string | null;
    isMultiDelivery: boolean;
    createdAt: string;
    updatedAt: string;
    deliveredVia?: string | null;
    needsAdminReview?: boolean;
    reviewStatus?: string | null;
    reviewReason?: string | null;
    ocrData?: Record<string, unknown> | null;
    billing?: { estimateThb: number | null; unpricedReason: string | null; manualOverride: boolean } | null;
    stops: TripStopDTO[];
    photos: TripPhotoDTO[];
    renames?: { oldTripNo: string; newTripNo: string; renamedAt: string }[];
}

/** `['trips','detail',id]` (`enabled` while its dialog is open): photos signed for 15 min. */
export function tripDetailQuery(id: string, enabled = true) {
    return queryOptions({
        queryKey: queryKeys.trips.detail(id),
        queryFn: ({ signal }) => goFetch<TripDetailDTO>(goPath`/v1/trips/${id}`, { signal }),
        ...QUERY_POLICY.live,
        enabled: enabled && Boolean(id),
    });
}

/** An incident of a trip (`GET /v1/incidents?tripId`), for the detail's report box. */
export interface TripIncidentDTO {
    id: string;
    delayCause: string | null;
    description: string | null;
    createdAt: string;
    mapPhoto: FileRefDTO | null;
    situation1Photo: FileRefDTO | null;
    situation2Photo: FileRefDTO | null;
}

export function tripIncidentsQuery(tripId: string, enabled = true) {
    return queryOptions({
        queryKey: queryKeys.incidents({ tripId }),
        queryFn: ({ signal }) => goFetch<TripIncidentDTO[]>("/v1/incidents", { signal, query: { tripId, limit: 50 } }),
        ...QUERY_POLICY.live,
        enabled: enabled && Boolean(tripId),
    });
}

// --- writes ---------------------------------------------------------------------------------------------

/** One stop of `PATCH /v1/trips/{id}` stops[] (T34): position = index - 1. */
export interface TripStopPatch {
    destination: string;
    status?: "pending" | "delivered" | "failed";
    deliveredAt?: string | null;
    lat?: number | null;
    lng?: number | null;
}

export interface TripPhotoPatch {
    type: string;
    key: string;
}

/** `PATCH /v1/trips/{id}` (operations:edit_trip_details). */
export interface TripPatch {
    sealCode?: string | null;
    partnerCode?: string | null;
    deliveredAt?: string | null;
    origin?: string | null;
    destination?: string | null;
    needsAdminReview?: boolean;
    reviewStatus?: "pending_review" | null;
    reviewReason?: string | null;
    photos?: TripPhotoPatch[];
    stops?: TripStopPatch[];
}

/** What a trip write makes stale: the monitor, the trip's own keys, the boards (their trip column) and income. */
export async function invalidateTrips(client: QueryClient = getQueryClient(), id?: string): Promise<void> {
    await Promise.all([
        client.invalidateQueries({ queryKey: ["trips", "monitor"] }),
        id ? client.invalidateQueries({ queryKey: queryKeys.trips.detail(id) }) : null,
        client.invalidateQueries({ queryKey: queryKeys.tasks.all() }),
        client.invalidateQueries({ queryKey: ["income"] }),
    ]);
}

export async function patchTrip(id: string, patch: TripPatch, client: QueryClient = getQueryClient()): Promise<TripDetailDTO> {
    const out = await goFetch<TripDetailDTO>(goPath`/v1/trips/${id}`, { method: "PATCH", body: patch });
    await invalidateTrips(client, id);
    return out;
}

export async function deliverTrip(
    id: string,
    body: { deliveredAt?: string; photos?: TripPhotoPatch[] },
    client: QueryClient = getQueryClient()
): Promise<TripDetailDTO> {
    const out = await goFetch<TripDetailDTO>(goPath`/v1/trips/${id}/deliver`, { method: "POST", body, idempotencyKey: true });
    await invalidateTrips(client, id);
    return out;
}

export async function cancelTrip(id: string, client: QueryClient = getQueryClient()): Promise<TripDetailDTO> {
    const out = await goFetch<TripDetailDTO>(goPath`/v1/trips/${id}/cancel`, { method: "POST", body: {}, idempotencyKey: true });
    await invalidateTrips(client, id);
    return out;
}

export interface BulkCancelResultDTO {
    cancelled: string[];
    failed: { id: string; reason: string; status?: string }[];
}

export async function bulkCancelTrips(ids: string[], client: QueryClient = getQueryClient()): Promise<BulkCancelResultDTO> {
    const out = await goFetch<BulkCancelResultDTO>("/v1/trips/bulk-cancel", { method: "POST", body: { ids }, idempotencyKey: true });
    await invalidateTrips(client);
    return out;
}

export async function renameTrip(id: string, newTripNo: string, client: QueryClient = getQueryClient()): Promise<TripDetailDTO> {
    const out = await goFetch<TripDetailDTO>(goPath`/v1/trips/${id}/rename`, { method: "POST", body: { newTripNo }, idempotencyKey: true });
    await invalidateTrips(client, id);
    return out;
}

/** `POST /v1/trips/{id}/job-category` (T38, accounting:edit_rate_card): the explicit reprice of หลัก/เสริม. */
export async function setTripJobCategory(id: string, jobCategory: "PRIMARY" | "SUPPLEMENTARY", client: QueryClient = getQueryClient()) {
    const out = await goFetch<{ ok: boolean; billingEstimateThb?: number }>(goPath`/v1/trips/${id}/job-category`, {
        method: "POST",
        body: { jobCategory },
        idempotencyKey: true,
    });
    await invalidateTrips(client, id);
    return out;
}

/**
 * `POST /v1/trips/{id}/billing/compute` (T38): prices a delivered trip whose route or stops an admin corrected
 * (the dialog's parity call, `EditTripDetailsDialog.tsx:640-675`; a priced trip is left as it is without force).
 */
export async function computeTripBilling(id: string, client: QueryClient = getQueryClient()) {
    const out = await goFetch<{ ok: boolean; skipped?: boolean }>(goPath`/v1/trips/${id}/billing/compute`, {
        method: "POST",
        body: {},
        idempotencyKey: true,
    });
    await invalidateTrips(client, id);
    return out;
}

/** A driver's in-transit trips (the "clear every stuck trip" option of the resolve dialog). */
export async function driverInTransitTrips(driverId: string, signal?: AbortSignal): Promise<{ id: string; tripNo: string }[]> {
    const out: { id: string; tripNo: string }[] = [];
    let cursor: string | undefined;
    for (let i = 0; i < 10; i++) {
        const page = await goFetchEnvelope<{ id: string; tripNo: string }[]>("/v1/trips", {
            signal,
            query: { axis: "created", driverId, status: "in_transit", limit: 200, cursor },
        });
        out.push(...(page.data ?? []));
        cursor = page.nextCursor || undefined;
        if (!cursor) break;
    }
    return out;
}

// --- display helpers ----------------------------------------------------------------------------------

/** The delivered-time column: the trip's delivery, else (multi-drop) the last stop delivered. */
export function deliveredTimeOf(row: Pick<MonitorRowDTO, "deliveredAt" | "stops">): string | null {
    if (row.deliveredAt) return row.deliveredAt;
    let last: string | null = null;
    for (const s of row.stops ?? []) if (s.deliveredAt && (!last || s.deliveredAt > last)) last = s.deliveredAt;
    return last;
}

/** The label of a place, its raw text, else "—". */
export function placeLabel(p: PlaceDTO | null | undefined): string {
    if (!p) return "—";
    return p.label || p.raw || "—";
}

// --- export -----------------------------------------------------------------------------------------------

export type ExportCell = string | number | null;

export interface MonitorExportLabels {
    headers: string[];
    status: (s: string) => string;
    stopStatus: (s: string) => string;
    jobType: (j: string) => string;
    yes: string;
    no: string;
    formatDateTime: (iso: string | null | undefined) => string;
}

/**
 * The sheet of the export (the Firestore page's columns, `DriverMonitorDashboard.tsx:383-500`): one row per trip,
 * one per stop of a multi-drop trip. With a destination filter a multi-drop trip lists only the stops that match
 * it (ADR 0006 §6), and its stored price goes on the first row it lists, so a summed column counts each trip once.
 */
export function buildMonitorExport(rows: MonitorRowDTO[], destinationFilter: string, l: MonitorExportLabels): ExportCell[][] {
    const out: ExportCell[][] = [l.headers];
    for (const r of rows) {
        const price = r.price?.estimateThb ?? null;
        const base: ExportCell[] = [
            r.tripNo,
            l.formatDateTime(r.checkInAt ?? r.createdAt),
            r.driverName ?? "",
            r.truck?.plate ?? "",
            l.jobType(r.jobType),
            placeLabel(r.origin),
            "",
            r.sealCode ?? "",
            r.partnerCode ?? "",
            l.status(r.status),
            null,
        ];
        const stops = r.stops ?? [];
        if (stops.length > 0) {
            const emitted =
                destinationFilter && destinationFilter !== MONITOR_ALL ? stops.filter((s) => s.place?.key === destinationFilter) : stops;
            emitted.forEach((s, i) => {
                const row = [...base];
                row[6] = placeLabel(s.place);
                row[10] = i === 0 ? price : null;
                row.push(l.yes, s.index, l.stopStatus(s.status), l.formatDateTime(s.deliveredAt));
                out.push(row);
            });
            if (emitted.length > 0) continue;
        }
        const row = [...base];
        row[6] = placeLabel(r.destination);
        row[10] = price;
        row.push(l.no, 1, "", l.formatDateTime(deliveredTimeOf(r)));
        out.push(row);
    }
    return out;
}

/** CSV of the sheet (UTF-8 with a BOM, CRLF), each cell quoted when it must be. */
export function toCsv(sheet: ExportCell[][]): string {
    const esc = (v: ExportCell) => {
        const s = v == null ? "" : String(v);
        return /[",\r\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
    };
    return "﻿" + sheet.map((r) => r.map(esc).join(",")).join("\r\n");
}
