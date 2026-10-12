/**
 * Hub master data for the web: `['hubs']` over one `HubDTO[]` and `['hubs','maps']` over the
 * name/code maps (developer-spec.md §10.6, §10.7, §10.9; Appendix B `GET /v1/hubs`,
 * `GET /v1/hubs/maps`; Appendix E §E.3.1, §E.4 row "hubs").
 *
 * Every page that needs hubs reads this one cache entry (10 min stale, 60 min gc), so an ops session
 * (dashboard -> monitor -> job-assign -> first-mile -> billing) reads the collection once per stale
 * time instead of about seven times. Pages adapt the list with the `select` functions of
 * `./selectors.ts`; plain async functions (billing, income) use `fetchHubsCached`, which goes
 * through the same cache. Hub writes call `invalidateHubs`.
 *
 * Source: `GET /v1/hubs` (T21). Hub writes, the hub import, the distance reads and the Distance
 * Matrix job are Go routes too (T25: `POST/PATCH /v1/hubs`, `POST /v1/hubs/import`,
 * `GET /v1/distances[/meta]`, `POST /v1/jobs/distances.compute`). TW7 removed the Firestore adapter
 * and the `masterdata` flag switch; the fields marked legacy below are no longer filled or sent.
 */
import { queryOptions, type QueryClient } from "@tanstack/react-query";
import { goFetch, goPath } from "@/lib/goFetch";
import { fetchAllPages } from "@/lib/goPages";
import { goQueryFn } from "@/lib/goQuery";
import { getQueryClient } from "@/lib/queryClient";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";

export const HUBS_PATH = "/v1/hubs";
export const HUB_MAPS_PATH = "/v1/hubs/maps";

/**
 * One hub or SOC (the single DTO of `GET /v1/hubs`, Appendix B). `stationType` is `HUB` or `SOC`
 * from Go; the Firestore adapter passes the stored value through (legacy rows hold `FM_HUB`,
 * `RETURN_CENTER`, ...), which the selectors normalise exactly as the pages did.
 */
export interface HubDTO {
    /** Firestore document id in P0; the PostgreSQL uuid from P1. */
    id: string;
    sourceId: string;
    sourceNameTh: string | null;
    sourceNameEn: string | null;
    latitude: number | null;
    longitude: number | null;
    stationType: string | null;
    linkedCustomerId: string | null;
    linkedCustomerName: string | null;
    /** `customer` or `partner`. */
    linkedCustomerKind: string | null;
    createdByDriver: boolean;
    /** Go only (T21): `SPX` | `SPK`, the linked billing party, the last change. */
    network?: string | null;
    linkedPartyId?: string | null;
    updatedAt?: string | null;
    /**
     * P0 only: the stored Firestore document. Legacy rows spell the same fact under older field
     * names (`hubId`, `hubCode`, `hubName`, `hubTHName`, ...), and some pages key their maps by
     * several of them; `hubRecord` hands those pages the document so their output does not change.
     * Go's DTO has no such field: ETL folds the old names into the fields above.
     */
    legacyDoc?: Readonly<Record<string, unknown>>;
}

/** `GET /v1/hubs/maps`: two objects, never merged (`.vibe-rules.md` billing rule, R53). */
export interface HubMaps {
    nameToCode: Record<string, string>;
    codeToName: Record<string, string>;
}

/**
 * The name/code maps of a hub list, as the server builds them (`buildHubMaps`,
 * `functions/src/tripBillingOnDelivered.ts:124-141`): every Thai / English / legacy name to its
 * code (a name equal to its code is skipped, so a code never maps to itself), and each code to its
 * Thai name. Display and filters only: pricing resolves codes on the server (R53).
 */
export function buildHubMaps(hubs: readonly HubDTO[]): HubMaps {
    const nameToCode: Record<string, string> = {};
    const codeToName: Record<string, string> = {};
    for (const hub of hubs) {
        const data = hub.legacyDoc;
        const code = String(data ? (data.source_id ?? data.hubId ?? "") : hub.sourceId).trim();
        if (!code) continue;
        const names = data ? [data.source_name_th, data.source_name_en, data.hubName] : [hub.sourceNameTh, hub.sourceNameEn];
        for (const raw of names) {
            const name = typeof raw === "string" ? raw.trim() : "";
            if (name && name !== code && !(name in nameToCode)) nameToCode[name] = code;
        }
        const th = data ? data.source_name_th : hub.sourceNameTh;
        const display = typeof th === "string" ? th.trim() : "";
        if (display && !(code in codeToName)) codeToName[code] = display;
    }
    return { nameToCode, codeToName };
}

export const hubsQueryOptions = queryOptions({
    queryKey: queryKeys.hubs.all(),
    queryFn: goQueryFn<HubDTO[]>(HUBS_PATH),
    ...QUERY_POLICY.masterData,
    refetchOnWindowFocus: true,
});

export const hubMapsQueryOptions = queryOptions({
    queryKey: queryKeys.hubs.maps(),
    queryFn: goQueryFn<HubMaps>(HUB_MAPS_PATH),
    ...QUERY_POLICY.masterData,
    refetchOnWindowFocus: true,
});

/**
 * The hub list from the tab's cache, fetched only when missing or stale: for async functions that
 * cannot use a hook (billing, income). Same entry as `useHubs`.
 */
export function fetchHubsCached(client: QueryClient = getQueryClient()): Promise<HubDTO[]> {
    return client.fetchQuery(hubsQueryOptions);
}

/** After a hub write: `['hubs']` and `['hubs','maps']` refetch where observed, the rest on next use. */
export function invalidateHubs(client: QueryClient = getQueryClient()): Promise<void> {
    return client.invalidateQueries({ queryKey: queryKeys.hubs.all() });
}

// --- writes (T25) ------------------------------------------------------------------------------

/** The body of `POST /v1/hubs` / `PATCH /v1/hubs/{id}` (Appendix B §B.2.9). */
export interface HubInput {
    sourceId: string;
    sourceNameTh: string;
    sourceNameEn: string | null;
    latitude: number | null;
    longitude: number | null;
    stationType: "HUB" | "SOC";
    /** A customer (`customer`) or a carrier tenant (`partner`); `null` unlinks. */
    linkedCustomerId: string | null;
    linkedCustomerKind: "customer" | "partner" | null;
    /** Legacy (the denormalised `CODE — Name` of the retired Firestore source): never sent to Go. */
    linkedCustomerName?: string | null;
}

/** One row of the pickup-location import (`POST /v1/hubs/import`, rows upserted by `sourceId`). */
export interface HubImportRow {
    sourceId: string;
    sourceNameTh?: string | null;
    sourceNameEn?: string | null;
    latitude?: number | null;
    longitude?: number | null;
    stationType?: "HUB" | "SOC";
    linkedCustomerId?: string | null;
    linkedCustomerKind?: "customer" | "partner" | null;
    /** Legacy (the template's first column, the retired Firestore document id): never sent to Go. */
    firestoreDocId?: string;
    /** Legacy (whether the sheet had a Thai name column): never sent to Go. */
    hasThaiColumn?: boolean;
}

export interface HubImportResult {
    created: number;
    updated: number;
    unchanged: number;
}

/** The Go body: the legacy-only name field is left out. */
function goHubBody({ linkedCustomerName: _n, ...input }: HubInput): Record<string, unknown> {
    void _n;
    return input as unknown as Record<string, unknown>;
}

/** Creates a hub or SOC; a taken code is `409 already_exists` (`details.field = sourceId`). */
export async function createHub(input: HubInput): Promise<string> {
    return (await goFetch<HubDTO>(HUBS_PATH, { method: "POST", body: goHubBody(input) })).id;
}

export async function updateHub({ id, input }: { id: string; input: HubInput }): Promise<void> {
    await goFetch(goPath`/v1/hubs/${id}`, { method: "PATCH", body: goHubBody(input) });
}

/** Upserts rows by code in one transaction, all or nothing. */
export function importHubs({ rows }: { rows: HubImportRow[] }): Promise<HubImportResult> {
    const body = {
        rows: rows.map(({ firestoreDocId: _d, hasThaiColumn: _h, ...row }) => {
            void _d;
            void _h;
            return row;
        }),
    };
    return goFetch<HubImportResult>(`${HUBS_PATH}/import`, { method: "POST", body });
}

// --- distances (T25) ---------------------------------------------------------------------------

export const DISTANCES_PATH = "/v1/distances";

/** `GET /v1/distances/meta`: when the Distance Matrix last ran. */
export interface DistancesMeta {
    lastCalculatedAt: string | null;
    pairs?: number;
}

/** One hub-SOC distance as the map popup shows it. */
export interface DistanceRow {
    socId: string;
    /** The hub's code (the other end of a SOC's row). */
    destId: string;
    distanceKm: number;
    durationMinutes: number;
}

/** `GET /v1/distances` item (internal/hubs `Distance`). */
interface GoDistance {
    hubSourceId: string;
    socKey: string;
    direction: "hub_to_soc" | "soc_to_hub";
    distanceKm: number;
    durationMinutes: number;
}

export type DistanceViewMode = "HUB_SOC" | "SOC_HUB";

export interface DistanceQuery {
    /** The selected point's code (a hub code, or a SOC code/key). */
    code: string;
    isSoc: boolean;
    mode: DistanceViewMode;
}

/** `['distances','meta']`. */
export const distancesMetaQuery = queryOptions({
    queryKey: queryKeys.distances.meta(),
    queryFn: goQueryFn<DistancesMeta>(`${DISTANCES_PATH}/meta`),
    ...QUERY_POLICY.masterData,
});

/** `['distances', {code, isSoc, mode}]`: the rows of one point in one direction (fetched on click). */
export function distanceRowsQuery(q: DistanceQuery) {
    return queryOptions({
        queryKey: queryKeys.distances.list({ code: q.code, isSoc: q.isSoc, mode: q.mode }),
        queryFn: async ({ signal }): Promise<DistanceRow[]> => {
            const rows = await fetchAllPages<GoDistance>(
                DISTANCES_PATH,
                { hubId: q.code, direction: q.mode === "HUB_SOC" ? "hub_to_soc" : "soc_to_hub" },
                { signal }
            );
            return rows.map((r) => ({ socId: r.socKey, destId: r.hubSourceId, distanceKm: r.distanceKm, durationMinutes: r.durationMinutes }));
        },
        ...QUERY_POLICY.masterData,
        enabled: q.code !== "",
    });
}

/** The rows of one point from the tab's cache (the map popup, outside React). */
export function fetchDistanceRows(q: DistanceQuery, client: QueryClient = getQueryClient()): Promise<DistanceRow[]> {
    return client.fetchQuery(distanceRowsQuery(q));
}

/** The answer of a Distance Matrix run: a Go job to follow (`done` was the retired callable's answer). */
export type ComputeDistancesResult =
    | { kind: "job"; jobId: string }
    | { kind: "done"; written: number; hubsCount: number; socsCount: number; calculatedAt?: string };

/** Starts the Distance Matrix: Go `POST /v1/jobs/distances.compute` (`202 {jobId}`). */
export async function computeDistances(_variables: Record<string, never> = {}): Promise<ComputeDistancesResult> {
    void _variables;
    const { jobId } = await goFetch<{ jobId: string }>("/v1/jobs/distances.compute", { method: "POST", body: {} });
    return { kind: "job", jobId };
}
