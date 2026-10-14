/**
 * Go-only pickers of the accounting and maintenance pages (T42). They read PostgreSQL ids directly under
 * their own keys (T42 kept them apart from the then `masterdata`-flagged option keys of T25, whose
 * Firestore side TW7 removed):
 * - `['customers',{fields:'minimal',q,view:'picker'}]`: customers searched on the server (UI2, below);
 * - `['customers','ref',id]`: one customer by a customer id or a billing party id, the label of a row or a
 *   picker value (UI2C, `customerRefQuery`; the hook is `./useCustomerLookup.ts`), resolved in batches through
 *   `GET /v1/customers?fields=minimal&ids=` (UI8);
 * - `['drivers',{fields:'minimal',q}]`: drivers searched on the server (the users page's key, 20 rows;
 *   `{view:'picker',...}` with a status filter or the full rows);
 * - `['trucks',{view:'picker',q}]`: trucks searched by plate (`GET /v1/trucks?q`, one page of 50; `ownership`
 *   in the key when it filters).
 */
import { queryOptions, type QueryClient } from "@tanstack/react-query";
import { isApiError } from "@/lib/apiError";
import { goFetch, goFetchEnvelope, goPath, goUrl } from "@/lib/goFetch";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";

/** `GET /v1/customers?fields=minimal` item. */
export interface CustomerPick {
    id: string;
    billingPartyId: string | null;
    code: string;
    name: string;
}

export function customerLabel(c: Pick<CustomerPick, "code" | "name"> | undefined | null): string {
    if (!c) return "";
    return c.code ? `${c.code} — ${c.name}` : c.name;
}

/**
 * `['customers','ref',ref]`: the customer a row or a picker value names, by its customer id or by its billing
 * party id (trips, rate rows and fees hold the customer id; standby records and tasks the party id); `null` when
 * Go knows none in the caller's reach. Cached per id for 10 min and seeded by every picker page
 * (`customerSearchQuery`), so a customer the user picked is labelled without a request.
 *
 * An unseeded id goes to `loadCustomerRef`, which batches: every ref query that starts in the same task (the
 * distinct ids of a table, a refetch of all of them on `customers.changed`, the refs of an export) is one
 * `GET /v1/customers?fields=minimal&ids=` request per `CUSTOMER_LOOKUP_MAX` ids (UI8, issue #184; any
 * authenticated principal, the reach of the minimal list), while each id keeps its own cache key.
 */
export function customerRefQuery(ref: string) {
    return queryOptions({
        queryKey: queryKeys.customers.ref(ref),
        queryFn: ({ client }): Promise<CustomerPick | null> => loadCustomerRef(client, ref),
        ...QUERY_POLICY.masterData,
        enabled: ref !== "",
    });
}

/**
 * Puts customers read elsewhere (a picker page, a lookup) into the `['customers','ref',…]` cache, by id and by
 * billing party; `skip` holds the ids whose own queries are settling.
 */
export function seedCustomerRefs(client: QueryClient, customers: readonly CustomerPick[], skip?: { has(ref: string): boolean }) {
    for (const c of customers) {
        for (const ref of [c.id, c.billingPartyId]) {
            if (ref && !skip?.has(ref)) client.setQueryData(queryKeys.customers.ref(ref), c);
        }
    }
}

/** Ids per `GET /v1/customers?ids=` request (Go's cap, `parties.MaxLookupIDs`). */
export const CUSTOMER_LOOKUP_MAX = 100;

interface RefWaiter {
    resolve: (c: CustomerPick | null) => void;
    reject: (e: unknown) => void;
}

/** The ids waiting for the next lookup, per query client (a test's client never shares a batch with another). */
const pendingRefs = new WeakMap<QueryClient, Map<string, RefWaiter[]>>();

/**
 * One customer by id, through the batch of its query client: the ids asked for in the same task go out together
 * after it (a zero-delay timer, so the queries a render starts and those of a following microtask join), each id
 * once, in chunks of `CUSTOMER_LOOKUP_MAX`. A customer found by one of its ids is also cached under the other.
 */
export function loadCustomerRef(client: QueryClient, ref: string): Promise<CustomerPick | null> {
    return new Promise((resolve, reject) => {
        let batch = pendingRefs.get(client);
        if (!batch) {
            batch = new Map();
            pendingRefs.set(client, batch);
            setTimeout(() => flushCustomerRefs(client), 0);
        }
        const waiters = batch.get(ref);
        if (waiters) waiters.push({ resolve, reject });
        else batch.set(ref, [{ resolve, reject }]);
    });
}

function flushCustomerRefs(client: QueryClient) {
    const batch = pendingRefs.get(client);
    pendingRefs.delete(client);
    if (!batch) return;
    const refs = [...batch.keys()].sort(); // a stable request for the same ids
    for (let i = 0; i < refs.length; i += CUSTOMER_LOOKUP_MAX) {
        const chunk = refs.slice(i, i + CUSTOMER_LOOKUP_MAX);
        const settle = (each: (w: RefWaiter, ref: string) => void) => {
            for (const ref of chunk) for (const w of batch.get(ref) ?? []) each(w, ref);
        };
        goFetch<CustomerPick[]>("/v1/customers", { query: { fields: "minimal", ids: chunk.join(",") } }).then(
            (found) => {
                // The other id of each customer, for a later row that holds it (the asked ids settle below).
                seedCustomerRefs(client, found, batch);
                settle((w, ref) => w.resolve(found.find((c) => c.id === ref || c.billingPartyId === ref) ?? null));
            },
            (e: unknown) => settle((w) => w.reject(e))
        );
    }
}

/** Pages of one code search before a code counts as unknown (100 rows each). */
const CODE_SEARCH_MAX_PAGES = 5;
/** Code searches in flight at once (an import sheet may name many customers). */
const CODE_SEARCH_CONCURRENCY = 4;
/** Longer text is no customer code (Go: at most 32 characters; `q` takes at most 100). */
const CODE_MAX_LENGTH = 64;

/**
 * The customers of `codes` (exact, case-insensitive), for a form or an import that holds codes rather than ids
 * (the driver form's `customerDriverIds`, the hub import's customer column): per distinct code, the server search
 * `GET /v1/customers?fields=minimal&q=<code>` until the page holding that code (Go orders by code), so no page
 * reads every customer (UI8). Keyed by the upper-case code; a code Go does not know is absent.
 */
export async function fetchCustomersByCode(client: QueryClient, codes: Iterable<string>, signal?: AbortSignal): Promise<Map<string, CustomerPick>> {
    const queue = [...new Set([...codes].map((c) => c.trim().toUpperCase()).filter((c) => c !== "" && c.length <= CODE_MAX_LENGTH))];
    const out = new Map<string, CustomerPick>();
    const find = async (code: string) => {
        let cursor: string | undefined;
        for (let i = 0; i < CODE_SEARCH_MAX_PAGES; i++) {
            const page = await goFetchEnvelope<CustomerPick[]>("/v1/customers", { signal, query: { fields: "minimal", q: code, limit: 100, cursor } });
            const hit = (page.data ?? []).find((c) => c.code.toUpperCase() === code);
            if (hit) {
                out.set(code, hit);
                seedCustomerRefs(client, [hit]);
                return;
            }
            cursor = page.nextCursor;
            if (!cursor) return;
        }
    };
    await Promise.all(
        Array.from({ length: Math.min(CODE_SEARCH_CONCURRENCY, queue.length) }, async () => {
            for (let code = queue.shift(); code !== undefined; code = queue.shift()) await find(code);
        })
    );
    return out;
}

/**
 * `GET /v1/drivers?fields=minimal` item. The full rows (`details`) also carry the licence type, the status
 * and the home truck (`currentAssignment`, null when the driver holds none).
 */
export interface DriverPick {
    id: string;
    displayName: string | null;
    licenseType?: string | null;
    status?: string;
    currentAssignment?: { truckPlate: string | null } | null;
}

export interface DriverPickFilter {
    /** One driver status, a server filter (`active`, `inactive`, `on_duty`). */
    status?: string;
    /** The full rows (licence type, status, home truck) instead of `fields=minimal`. */
    details?: boolean;
}

/**
 * `['drivers',{fields:'minimal',q}]` (the users page's key), or `['drivers',{view:'picker',q,details,status}]`
 * for a filtered picker or its full rows (the truck-assignment page).
 */
export function driverPicksQuery(q: string, filter: DriverPickFilter = {}) {
    const term = q.trim();
    const { status, details } = filter;
    return queryOptions({
        queryKey:
            details || status
                ? queryKeys.drivers.list({ view: "picker", q: term, details: !!details, status: status ?? "" })
                : queryKeys.drivers.list({ fields: "minimal", q: term }),
        queryFn: ({ signal }) =>
            goFetch<DriverPick[]>("/v1/drivers", { signal, query: { fields: details ? undefined : "minimal", status, q: term || undefined, limit: 20 } }),
        staleTime: 5 * 60_000,
    });
}

/** The `GET /v1/trucks` fields a picker shows. */
export interface TruckPick {
    id: string;
    licensePlate: string;
    type?: string | null;
    model?: string | null;
    status?: string | null;
}

export interface TruckPickFilter {
    /** `own` (the own fleet's trucks) or `subcontractor`, a server filter. */
    ownership?: string;
}

export function truckPicksQuery(q: string, filter: TruckPickFilter = {}) {
    const term = q.trim();
    const { ownership } = filter;
    return queryOptions({
        queryKey: queryKeys.trucks.list(ownership ? { view: "picker", q: term, ownership } : { view: "picker", q: term }),
        queryFn: ({ signal }) => goFetch<TruckPick[]>("/v1/trucks", { signal, query: { ownership, q: term || undefined, limit: 50 } }),
        staleTime: 5 * 60_000,
    });
}

/** The `GET /v1/hubs` fields the rate card shows and picks (a hub's code is its `sourceId`). */
export interface HubPick {
    id: string;
    sourceId: string;
    sourceNameTh: string | null;
    sourceNameEn: string | null;
    stationType: string | null;
    /** The hub's billing party (the task forms' customer prefill, T34). */
    linkedPartyId?: string | null;
    /** Who that party bills: `customer` or `partner` (a carrier); null when the caller may not read the party. */
    linkedCustomerKind?: string | null;
}

/**
 * `['hubs',{view:'go'}]`: every hub and SOC from Go (`GET /v1/hubs` without `limit`: the whole master in
 * one request, 10 min); `hubs.changed` reaches it through the `['hubs']` prefix. For readers that need the
 * whole master (the rate card's table, the boards' source-hub filter); a picker searches a page instead
 * (`hubSearchQuery`) and labels its value with `hubByCodeQuery`.
 */
export const hubPicksQuery = queryOptions({
    queryKey: [...queryKeys.hubs.all(), { view: "go" }] as const,
    queryFn: ({ signal }) => goFetch<HubPick[]>("/v1/hubs", { signal }),
    ...QUERY_POLICY.masterData,
});

/** A hub's label: Thai name, else English name, else its code. */
export function hubLabel(h: Pick<HubPick, "sourceId" | "sourceNameTh" | "sourceNameEn">): string {
    return h.sourceNameTh?.trim() || h.sourceNameEn?.trim() || h.sourceId;
}

/** Whether a station is a SOC (`SOC`, or a legacy spelling starting with it). */
export function isSocStation(h: Pick<HubPick, "stationType">): boolean {
    return String(h.stationType ?? "").trim().toUpperCase().startsWith("SOC");
}

/** A hub as the task and trip forms show it: "CODE — name", or the code alone. */
export function hubDisplay(h: Pick<HubPick, "sourceId" | "sourceNameTh" | "sourceNameEn">): string {
    const name = h.sourceNameTh?.trim() || h.sourceNameEn?.trim();
    return name && name !== h.sourceId ? `${h.sourceId} — ${name}` : h.sourceId;
}

/**
 * `['hubs',{view:'one',code}]`: one hub or SOC by its code (`GET /v1/hubs/{code}`; Go also resolves a uuid
 * and a SOC key such as `SOCE`), the label of a value a picker holds that its current page may not (UI2B).
 * A code that is not in the master (a legacy task's free text) is `null`, not an error: the picker shows it
 * as it is. 10 min, like the master; `hubs.changed` reaches it through the `['hubs']` prefix.
 */
export function hubByCodeQuery(code: string) {
    const ref = code.trim();
    return queryOptions({
        queryKey: [...queryKeys.hubs.all(), { view: "one", code: ref.toUpperCase() }] as const,
        queryFn: async ({ signal }): Promise<HubPick | null> => {
            let path: string;
            try {
                path = goPath`/v1/hubs/${ref}`;
                goUrl(path); // a code no path can carry (a "/", a dot segment) names no hub
            } catch {
                return null;
            }
            try {
                const hub = await goFetch<HubPick>(path, { signal });
                // A code that collides with a static route (`/v1/hubs/maps`) answers another payload: no hub.
                return typeof hub?.sourceId === "string" ? hub : null;
            } catch (e) {
                if (isApiError(e) && e.status === 404) return null;
                throw e;
            }
        },
        enabled: ref !== "",
        ...QUERY_POLICY.masterData,
    });
}

/**
 * Server searches of the shared pickers (UI2, `components/entity-pickers.tsx`): one page per typed term
 * (debounced by the picker), so no list of every customer or hub is read for a picker. Go folds `q` as
 * `lib/searchText.ts` does (UI2B): Thai tone marks, Latin accents and Thai digits, every word in any order.
 * - `['customers',{fields:'minimal',q,view:'picker'}]`: `GET /v1/customers?fields=minimal&q&limit=50`;
 * - `['hubs',{view:'picker',q,stationType}]`: `GET /v1/hubs?q&stationType&limit=50` (one keyset page by code).
 */
export const PICKER_PAGE_SIZE = 50;

export function customerSearchQuery(q: string) {
    const term = q.trim();
    return queryOptions({
        queryKey: queryKeys.customers.list({ fields: "minimal", q: term, view: "picker" }),
        queryFn: async ({ signal, client }) => {
            const page = await goFetch<CustomerPick[]>("/v1/customers", { signal, query: { fields: "minimal", q: term || undefined, limit: PICKER_PAGE_SIZE } });
            // Each customer of the page is labelled by id from now on (`customerRefQuery`), without a request.
            seedCustomerRefs(client, page);
            return page;
        },
        staleTime: 5 * 60_000,
    });
}

export function hubSearchQuery(q: string, stationType?: string) {
    const term = q.trim();
    return queryOptions({
        queryKey: [...queryKeys.hubs.all(), { view: "picker", q: term, stationType: stationType ?? "" }] as const,
        queryFn: ({ signal }) =>
            goFetch<HubPick[]>("/v1/hubs", { signal, query: { q: term || undefined, stationType: stationType || undefined, limit: PICKER_PAGE_SIZE } }),
        staleTime: 5 * 60_000,
    });
}
