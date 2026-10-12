/**
 * Standby records over Go (T34, P2; routes of T30 and T31, Appendix B §B.2.13; developer-spec.md §10.7 row
 * `['standby', …]`; Appendix E §E.2.1 row standby-records, §E.5 rows 15-17; TW8 M2).
 *
 * `['standby',{from,to}]` is one keyset list over `GET /v1/standby` (newest first on `created_at`, 100 per page,
 * "load more"): the date range is the server's filter, so a record older than the newest 300 is never silently
 * missing (the Firestore page filtered dates in the browser over that window, `standby-records/page.tsx:233-256`),
 * and each row carries its driver's name and its two photos signed, so the 300-driver listener is gone. The
 * backfill dialog's pickers are plain fetches while it is open (`['drivers',{fields:'minimal',q}]`,
 * `['tasks',{driverId,view:'backfill'}]`, `['trips',{driverId,view:'backfill'}]`), never listeners.
 *
 * SSE `standby.completed`, `standby.priced` (tenant:{tid}:trips) make `['standby']` stale in every tab.
 */
import { infiniteQueryOptions, queryOptions, type QueryClient } from "@tanstack/react-query";
import { goFetch } from "@/lib/goFetch";
import { goInfiniteQueryFn, goNextPageParam } from "@/lib/goQuery";
import { assertUploadable, uploadToGo } from "@/lib/goUpload";
import { getQueryClient } from "@/lib/queryClient";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";
import type { FileRefDTO, TaskDTO } from "@/features/tasks/api/tasks.go";

export const STANDBY_PATH = "/v1/standby";
export const STANDBY_PAGE_SIZE = 100;

export interface StandbyPhotoDTO extends FileRefDTO {
    type: "customer_worksheet" | "site_photo" | string;
    overlayText: string | null;
}

/** `GET /v1/standby` item (internal/operations `StandbyListView` for staff). */
export interface StandbyDTO {
    id: string;
    tenantId: string;
    driverId: string | null;
    driverName: string | null;
    taskId: string | null;
    tripId: string | null;
    jobCategory: string | null;
    startLocation: string | null;
    endLocation: string | null;
    startedAt: string | null;
    endedAt: string | null;
    durationMinutes: number | null;
    note: string | null;
    status: string;
    lat: number | null;
    lng: number | null;
    truckId: string | null;
    truckLicensePlate: string | null;
    photos: StandbyPhotoDTO[];
    createdAt: string;
    backfilledByAdmin?: boolean;
}

export interface StandbyFilter {
    /** Bangkok days `yyyy-MM-dd` (both inclusive), or "" for no bound. */
    from: string;
    to: string;
}

type StandbyKey = readonly ["standby", StandbyFilter];

export function standbyQuery(filter: StandbyFilter, enabled = true) {
    return infiniteQueryOptions({
        queryKey: queryKeys.standby({ from: filter.from, to: filter.to }) as StandbyKey,
        queryFn: goInfiniteQueryFn<StandbyDTO, StandbyKey>(STANDBY_PATH, {
            query: ([, f]) => ({ from: f.from || undefined, to: f.to || undefined, limit: STANDBY_PAGE_SIZE }),
        }),
        initialPageParam: undefined as string | undefined,
        getNextPageParam: goNextPageParam,
        ...QUERY_POLICY.live,
        enabled,
    });
}

/** The backfill dialog's task picker: a driver's latest tasks (both types the caller reads). */
export function driverTasksQuery(driverId: string, enabled = true) {
    return queryOptions({
        queryKey: queryKeys.tasks.list({ driverId, view: "backfill" }),
        queryFn: ({ signal }) => goFetch<TaskDTO[]>("/v1/tasks", { signal, query: { driverId, limit: 50 } }),
        ...QUERY_POLICY.live,
        enabled: enabled && Boolean(driverId),
    });
}

export interface DriverTripPick {
    id: string;
    tripNo: string;
    status: string;
    taskId: string | null;
    origin: string | null;
    destination: string | null;
    createdAt: string;
}

/** The backfill dialog's trip picker: a driver's latest trips. */
export function driverTripsQuery(driverId: string, enabled = true) {
    return queryOptions({
        queryKey: queryKeys.trips.list({ driverId, view: "backfill" }),
        queryFn: ({ signal }) => goFetch<DriverTripPick[]>("/v1/trips", { signal, query: { axis: "created", driverId, limit: 50 } }),
        ...QUERY_POLICY.live,
        enabled: enabled && Boolean(driverId),
    });
}

/** `POST /v1/standby/backfill` (operations:create_standby). */
export interface StandbyBackfillInput {
    driverId: string;
    taskId?: string;
    tripId?: string;
    startLocation: string;
    endLocation: string;
    /** RFC 3339 instants. */
    startedAt: string;
    endedAt: string;
    note?: string;
    jobCategory?: "PRIMARY" | "SUPPLEMENTARY";
    truckId?: string;
}

export interface StandbyFiles {
    worksheet?: File | null;
    site?: File | null;
}

/**
 * Uploads the two photos first (purpose `standby_photo`, under a fresh id: Go commits each key to the record it
 * creates, so the key need not name it), then writes the record, its task completed and its trip `standby` in one
 * transaction. A photo Go would refuse throws before anything is uploaded.
 */
export async function backfillStandby(input: StandbyBackfillInput, files: StandbyFiles, client: QueryClient = getQueryClient()): Promise<StandbyDTO> {
    assertUploadable([
        { file: files.worksheet, purpose: "standby_photo" },
        { file: files.site, purpose: "standby_photo" },
    ]);
    const entityId = crypto.randomUUID();
    const photos: { type: string; key: string }[] = [];
    if (files.worksheet) photos.push({ type: "customer_worksheet", key: await uploadToGo(files.worksheet, { purpose: "standby_photo", entityId, variant: "customer_worksheet" }) });
    if (files.site) photos.push({ type: "site_photo", key: await uploadToGo(files.site, { purpose: "standby_photo", entityId, variant: "site_photo" }) });
    const body = Object.fromEntries(Object.entries({ ...input, photos: photos.length ? photos : undefined }).filter(([, v]) => v !== undefined && v !== ""));
    const out = await goFetch<StandbyDTO>(`${STANDBY_PATH}/backfill`, { method: "POST", body, idempotencyKey: true });
    await Promise.all([
        client.invalidateQueries({ queryKey: queryKeys.standby() }),
        client.invalidateQueries({ queryKey: queryKeys.tasks.all() }),
        client.invalidateQueries({ queryKey: ["trips", "monitor"] }),
    ]);
    return out;
}
