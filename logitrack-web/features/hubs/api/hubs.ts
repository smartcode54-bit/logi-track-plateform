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
 * Source per fetch (`domainQueryFn`, flag `masterdata`): the Firestore adapter
 * (`./hubs.firestore.ts`) until P1, then `GET /v1/hubs` (T21) without a key change.
 */
import { queryOptions, type QueryClient } from "@tanstack/react-query";
import { goQueryFn } from "@/lib/goQuery";
import { getQueryClient } from "@/lib/queryClient";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";
import { domainQueryFn } from "@/features/platform/api/webFlags";
import { fetchHubsFromFirestore } from "./hubs.firestore";

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
    queryFn: domainQueryFn<HubDTO[]>("masterdata", { firestore: fetchHubsFromFirestore, go: goQueryFn<HubDTO[]>(HUBS_PATH) }),
    ...QUERY_POLICY.masterData,
    refetchOnWindowFocus: true,
});

export const hubMapsQueryOptions = queryOptions({
    queryKey: queryKeys.hubs.maps(),
    queryFn: domainQueryFn<HubMaps>("masterdata", {
        // Derived from the cached list: no second read of the collection.
        firestore: async ({ client }) => buildHubMaps(await client.fetchQuery(hubsQueryOptions)),
        go: goQueryFn<HubMaps>(HUB_MAPS_PATH),
    }),
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
