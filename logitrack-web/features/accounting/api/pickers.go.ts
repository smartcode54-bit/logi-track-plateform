/**
 * Go-only pickers of the accounting and maintenance pages (T42). They read PostgreSQL ids directly under
 * their own keys (T42 kept them apart from the then `masterdata`-flagged option keys of T25, whose
 * Firestore side TW7 removed):
 * - `['customers',{fields:'minimal',q,view:'picker'}]`: customers searched on the server (UI2, below);
 * - `['customers','ref',id]`: one customer by a customer id or a billing party id, the label of a row or a
 *   picker value (UI2C, `customerRefQuery`; the hook is `./useCustomerLookup.ts`);
 * - `['drivers',{fields:'minimal',q}]`: drivers searched on the server (the users page's key, 20 rows;
 *   `{view:'picker',...}` with a status filter or the full rows);
 * - `['trucks',{view:'picker',q}]`: trucks searched by plate (`GET /v1/trucks?q`, one page of 50; `ownership`
 *   in the key when it filters).
 */
import { queryOptions, type QueryClient } from "@tanstack/react-query";
import { customerOptionsQuery } from "@/features/customers/api/customers";
import { isApiError } from "@/lib/apiError";
import { goFetch, goPath, goUrl } from "@/lib/goFetch";
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
 * party id (trips, rate rows and fees hold the customer id; standby records and tasks the party id). Cached per
 * id for 10 min and seeded by every picker page (`customerSearchQuery`), so a customer the user picked is
 * labelled without a request.
 *
 * Go has no id lookup the pages' users may call yet: `GET /v1/customers/{id}` needs the steward-only
 * `fleet:manage_customers`, knows customer ids only and answers the full record, and the minimal list filters
 * by `q` (code or name) only. Until Go serves one (`GET /v1/customers?fields=minimal&ids=` matching a customer id
 * or a billing party id, issue #184 UI8), an id no picker page seeded resolves through the master-data directory
 * `['customers','options']` (every page of the minimal list, one request per stale time, the key the
 * master-data pickers already read). This query function is its only reader outside the master-data pages:
 * moving it to an id lookup changes no caller.
 */
export function customerRefQuery(ref: string) {
    return queryOptions({
        queryKey: queryKeys.customers.ref(ref),
        queryFn: async ({ client }): Promise<CustomerPick | null> => {
            const all = await client.fetchQuery(customerOptionsQuery);
            return all.find((c) => c.id === ref || c.billingPartyId === ref) ?? null;
        },
        ...QUERY_POLICY.masterData,
        enabled: ref !== "",
    });
}

/** Puts customers read elsewhere (a picker page) into the `['customers','ref',…]` cache, by id and by billing party. */
export function seedCustomerRefs(client: QueryClient, customers: readonly CustomerPick[]) {
    for (const c of customers) {
        client.setQueryData(queryKeys.customers.ref(c.id), c);
        if (c.billingPartyId) client.setQueryData(queryKeys.customers.ref(c.billingPartyId), c);
    }
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
