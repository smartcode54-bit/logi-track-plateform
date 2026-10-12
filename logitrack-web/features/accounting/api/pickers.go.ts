/**
 * Go-only pickers of the accounting and maintenance pages (T42). They read PostgreSQL ids directly under
 * their own keys (T42 kept them apart from the then `masterdata`-flagged option keys of T25, whose
 * Firestore side TW7 removed):
 * - `['customers',{fields:'minimal'}]`: every customer `{id, billingPartyId, code, name}` (the key and
 *   request of the users page's picker, T18; 10 min);
 * - `['drivers',{fields:'minimal',q}]`: drivers searched on the server (the users page's key, 20 rows);
 * - `['trucks',{view:'picker',q}]`: trucks searched by plate (`GET /v1/trucks?q`, one page of 50).
 */
import { queryOptions } from "@tanstack/react-query";
import { goFetch } from "@/lib/goFetch";
import { fetchAllPages } from "@/lib/goPages";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";

/** `GET /v1/customers?fields=minimal` item. */
export interface CustomerPick {
    id: string;
    billingPartyId: string | null;
    code: string;
    name: string;
}

export const customerPicksQuery = queryOptions({
    queryKey: queryKeys.customers.list({ fields: "minimal" }),
    queryFn: ({ signal }) => fetchAllPages<CustomerPick>("/v1/customers", { fields: "minimal" }, { signal }),
    ...QUERY_POLICY.masterData,
});

export function customerLabel(c: Pick<CustomerPick, "code" | "name"> | undefined | null): string {
    if (!c) return "";
    return c.code ? `${c.code} — ${c.name}` : c.name;
}

/** Lookups of the customer list: by customer id and by billing party id. */
export function customerMaps(list: CustomerPick[] | undefined) {
    const byId = new Map<string, CustomerPick>();
    const byParty = new Map<string, CustomerPick>();
    for (const c of list ?? []) {
        byId.set(c.id, c);
        if (c.billingPartyId) byParty.set(c.billingPartyId, c);
    }
    return { byId, byParty };
}

/** `GET /v1/drivers?fields=minimal` item. */
export interface DriverPick {
    id: string;
    displayName: string | null;
}

export function driverPicksQuery(q: string) {
    const term = q.trim();
    return queryOptions({
        queryKey: queryKeys.drivers.list({ fields: "minimal", q: term }),
        queryFn: ({ signal }) => goFetch<DriverPick[]>("/v1/drivers", { signal, query: { fields: "minimal", q: term || undefined, limit: 20 } }),
        staleTime: 5 * 60_000,
    });
}

/** The `GET /v1/trucks` fields a picker shows. */
export interface TruckPick {
    id: string;
    licensePlate: string;
    type?: string | null;
}

export function truckPicksQuery(q: string) {
    const term = q.trim();
    return queryOptions({
        queryKey: queryKeys.trucks.list({ view: "picker", q: term }),
        queryFn: ({ signal }) => goFetch<TruckPick[]>("/v1/trucks", { signal, query: { q: term || undefined, limit: 50 } }),
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
}

/**
 * `['hubs',{view:'go'}]`: every hub and SOC from Go (one request, 10 min); `hubs.changed` reaches it
 * through the `['hubs']` prefix. Not the `masterdata`-flagged `['hubs']` of the master-data pages.
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
