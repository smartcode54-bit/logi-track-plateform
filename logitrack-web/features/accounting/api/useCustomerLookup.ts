"use client";

/**
 * Customer names by id for tables, exports and picker values (UI2C, issue #142), instead of every page of
 * `GET /v1/customers` per page: one cached `['customers','ref',id]` query per distinct id (`customerRefQuery`,
 * 10 min), whatever the row count. An id is a customer id or a billing party id (standby records and tasks
 * hold the party), and either finds the customer. The picker pages seed the cache, so a customer picked on a
 * page is labelled without a request; see `customerRefQuery` for what an unseeded id costs until Go serves
 * an id lookup.
 */
import { useCallback, useMemo } from "react";
import { useQueries, type QueryClient, type UseQueryResult } from "@tanstack/react-query";

import { customerLabel, customerRefQuery, type CustomerPick } from "./pickers.go";

export interface CustomerLookup {
    /** The customer a customer id or a billing party id names; undefined while unknown. */
    get(ref: string | null | undefined): CustomerPick | undefined;
    /** `CODE — Name`; `…` while the id resolves; the id itself when Go knows no such customer; `—` for none. */
    label(ref: string | null | undefined): string;
    /** Some id is still resolving. */
    pending: boolean;
}

/** The distinct non-empty ids, sorted (the queries of a lookup, so their order never depends on the rows). */
export function distinctRefs(refs: Iterable<string | null | undefined>): string[] {
    const out = new Set<string>();
    for (const r of refs) if (r) out.add(r);
    return [...out].sort();
}

/** A lookup over resolved ids: each customer is found by the id asked for, its customer id and its billing party. */
export function makeCustomerLookup(found: Iterable<readonly [string, CustomerPick | null | undefined]>, pendingRefs: ReadonlySet<string> = new Set()): CustomerLookup {
    const by = new Map<string, CustomerPick>();
    for (const [ref, c] of found) {
        if (!c) continue;
        by.set(ref, c);
        by.set(c.id, c);
        if (c.billingPartyId) by.set(c.billingPartyId, c);
    }
    const get = (ref: string | null | undefined) => (ref ? by.get(ref) : undefined);
    return {
        get,
        label: (ref) => {
            if (!ref) return "—";
            const c = get(ref);
            if (c) return customerLabel(c);
            return pendingRefs.has(ref) ? "…" : ref;
        },
        pending: pendingRefs.size > 0,
    };
}

/**
 * The customers of `refs` (ids of the rows on screen, a picker's value). The result keeps its identity while
 * no query changes, so it can sit in a memo's dependencies.
 */
export function useCustomerLookup(refs: Iterable<string | null | undefined>): CustomerLookup {
    const key = distinctRefs(refs).join("\n");
    const list = useMemo(() => (key ? key.split("\n") : []), [key]);
    const combine = useCallback(
        (results: UseQueryResult<CustomerPick | null>[]) =>
            makeCustomerLookup(
                list.map((ref, i) => [ref, results[i]?.data] as const),
                new Set(list.filter((_, i) => results[i]?.isPending))
            ),
        [list]
    );
    return useQueries({ queries: list.map((ref) => customerRefQuery(ref)), combine });
}

/** The same lookup with every id resolved first: for an export, which must name each row's customer. */
export async function fetchCustomerLookup(client: QueryClient, refs: Iterable<string | null | undefined>): Promise<CustomerLookup> {
    const found = await Promise.all(
        distinctRefs(refs).map(async (ref) => [ref, await client.fetchQuery(customerRefQuery(ref)).catch(() => null)] as const)
    );
    return makeCustomerLookup(found);
}
