// UI2C (follow-up of issue #142) and UI8 (issue #184): customer names by id instead of every page of
// `GET /v1/customers`. One cached `['customers','ref',id]` query per distinct id (a customer id or a billing party
// id), whatever the row count; a customer a picker page brought is named without any further request; the ids no
// page brought are resolved together, one `GET /v1/customers?fields=minimal&ids=` per 100 distinct ids, and no
// read of every customer is left (the `['customers','options']` directory is gone).
import React from "react";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClientProvider, type QueryClient } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";

import { createQueryClient } from "@/lib/queryClient";
import { customerDirectoryReads, customerLookups, customersReply, fakeWeb, goErr } from "@/test-utils/fakeWeb";
import { CUSTOMER_LOOKUP_MAX, customerSearchQuery, fetchCustomersByCode } from "./pickers.go";
import { distinctRefs, fetchCustomerLookup, useCustomerLookup } from "./useCustomerLookup";

const CUSTOMERS = [
    { id: "c1", billingPartyId: "p1", code: "CJSF", name: "ซีเจ ซุปเปอร์เฟรช" },
    { id: "c2", billingPartyId: "p2", code: "TTP", name: "TTP Co" },
    { id: "c3", billingPartyId: null, code: "NOPARTY", name: "No party" },
];

function setup(customers = CUSTOMERS) {
    const web = fakeWeb();
    web.on("GET", "/api/go/v1/customers", customersReply(customers));
    const client = createQueryClient();
    const wrapper = ({ children }: { children: React.ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
    return { web, client, wrapper, lookups: () => customerLookups(web.calls), directoryReads: () => customerDirectoryReads(web.calls) };
}

const refKeys = (client: QueryClient) =>
    client
        .getQueryCache()
        .findAll({ queryKey: ["customers", "ref"] })
        .map((q) => q.queryKey[2])
        .sort();

afterEach(() => vi.unstubAllGlobals());

describe("useCustomerLookup", () => {
    it("asks once for every distinct id, finds a customer by its id or its billing party, and says what it cannot name", async () => {
        const { wrapper, lookups, directoryReads, client } = setup();
        // Many rows, few customers: c1 four times, p2 (c2's party) twice, an id Go does not know, blanks.
        const refs = ["c1", "c1", null, "p2", "c1", "", "unknown-id", "p2", undefined, "c1"];
        const { result } = renderHook(() => useCustomerLookup(refs), { wrapper });
        expect(result.current.label("c1")).toBe("…");
        expect(result.current.pending).toBe(true);
        await waitFor(() => expect(result.current.pending).toBe(false));

        expect(result.current.label("c1")).toBe("CJSF — ซีเจ ซุปเปอร์เฟรช");
        expect(result.current.label("p2")).toBe("TTP — TTP Co");
        // A party's customer is found by the customer id too (an income row names the customer of a standby party).
        expect(result.current.get("p2")?.id).toBe("c2");
        expect(result.current.get("c2")?.billingPartyId).toBe("p2");
        expect(result.current.label("unknown-id")).toBe("unknown-id");
        expect(result.current.label(null)).toBe("—");
        // One request for the three distinct ids, never the directory; each id still has its own cache entry, and
        // the other id of each customer found is cached too (a later row holding it asks nothing).
        expect(lookups()).toEqual([["c1", "p2", "unknown-id"]]);
        expect(directoryReads()).toEqual([]);
        expect(refKeys(client)).toEqual(["c1", "c2", "p1", "p2", "unknown-id"]);
        expect(distinctRefs(refs)).toEqual(["c1", "p2", "unknown-id"]);
    });

    it("joins the ids of every table mounted together into one request; a later page asks only for the new ids", async () => {
        const { web, wrapper, lookups } = setup();
        const a = renderHook(() => useCustomerLookup(["c1", "p2"]), { wrapper });
        const b = renderHook(() => useCustomerLookup(["p2", "c3"]), { wrapper });
        await waitFor(() => expect(a.result.current.pending || b.result.current.pending).toBe(false));
        expect(lookups()).toEqual([["c1", "c3", "p2"]]);
        expect(b.result.current.label("c3")).toBe("NOPARTY — No party");
        const requests = web.calls.length;

        // From the cache, by either id; the result keeps its identity while the ids do not change.
        const second = renderHook(({ refs }) => useCustomerLookup(refs), { wrapper, initialProps: { refs: ["p1", "c2", "c2"] } });
        expect(second.result.current.pending).toBe(false);
        expect(second.result.current.label("p1")).toBe("CJSF — ซีเจ ซุปเปอร์เฟรช");
        const before = second.result.current;
        second.rerender({ refs: ["c2", "p1"] });
        expect(second.result.current).toBe(before);
        expect(web.calls.length).toBe(requests);

        second.rerender({ refs: ["c2", "p1", "nope"] });
        await waitFor(() => expect(second.result.current.pending).toBe(false));
        expect(lookups()).toEqual([["c1", "c3", "p2"], ["nope"]]);
    });

    it("splits more than 100 unresolved ids into requests of at most 100", async () => {
        const many = Array.from({ length: 230 }, (_, i) => ({ id: `c-${i}`, billingPartyId: `p-${i}`, code: `C${String(i).padStart(3, "0")}`, name: `Customer ${i}` }));
        const { wrapper, lookups } = setup(many);
        const refs = many.map((c, i) => (i % 2 ? c.billingPartyId : c.id));
        const { result } = renderHook(() => useCustomerLookup([...refs, ...refs]), { wrapper });
        await waitFor(() => expect(result.current.pending).toBe(false));
        expect(lookups().map((ids) => ids.length)).toEqual([CUSTOMER_LOOKUP_MAX, CUSTOMER_LOOKUP_MAX, 30]);
        expect(new Set(lookups().flat()).size).toBe(230);
        expect(result.current.label("p-229")).toBe("C229 — Customer 229");
    });

    it("names a customer a picker page brought without a lookup", async () => {
        const { client, wrapper, lookups } = setup();
        await client.fetchQuery(customerSearchQuery("ttp"));
        const { result } = renderHook(() => useCustomerLookup(["c2", "p2"]), { wrapper });
        expect(result.current.pending).toBe(false);
        expect(result.current.label("c2")).toBe("TTP — TTP Co");
        expect(result.current.label("p2")).toBe("TTP — TTP Co");
        expect(lookups()).toEqual([]);
    });

    it("a failed lookup leaves the ids unnamed (the id itself), not pending", async () => {
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/customers", () => goErr(503, "unavailable"));
        const client = createQueryClient();
        client.setDefaultOptions({ queries: { retry: false } });
        const wrapper = ({ children }: { children: React.ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
        const { result } = renderHook(() => useCustomerLookup(["c1", "p2"]), { wrapper });
        await waitFor(() => expect(result.current.pending).toBe(false));
        expect([result.current.label("c1"), result.current.label("p2")]).toEqual(["c1", "p2"]);
        expect(customerLookups(web.calls)).toEqual([["c1", "p2"]]);
    });

    it("fetchCustomerLookup resolves every id in one request before an export writes its file", async () => {
        const { client, lookups } = setup();
        const names = await fetchCustomerLookup(client as QueryClient, ["c1", "p2", "c1", null, "nope"]);
        expect(names.pending).toBe(false);
        expect([names.label("c1"), names.label("p2"), names.label("nope"), names.get("c1")?.code]).toEqual([
            "CJSF — ซีเจ ซุปเปอร์เฟรช",
            "TTP — TTP Co",
            "nope",
            "CJSF",
        ]);
        expect(lookups()).toEqual([["c1", "nope", "p2"]]);
    });
});

describe("fetchCustomersByCode", () => {
    it("resolves codes by the server search, exact and case-insensitive, one search per distinct code", async () => {
        // "X" is a substring of many names: the exact code is found on a later page of its search, never by reading
        // every customer.
        const many = [
            ...Array.from({ length: 150 }, (_, i) => ({ id: `a-${i}`, billingPartyId: null, code: `A${i}`, name: `Box ${i}` })),
            { id: "x", billingPartyId: "px", code: "X", name: "X Co." },
            { id: "spx", billingPartyId: "pspx", code: "SPX", name: "Shopee" },
        ];
        const { web, client, directoryReads } = setup(many);
        const found = await fetchCustomersByCode(client, ["spx", "x", " SPX ", "", "NOPE"]);
        expect([...found.entries()].map(([code, c]) => [code, c.id]).sort()).toEqual([["SPX", "spx"], ["X", "x"]]);
        const searches = web.calls.map((c) => new URL(c.url, "http://web.test").searchParams.get("q"));
        expect(searches.filter((q) => q === "SPX")).toHaveLength(1);
        expect(searches.filter((q) => q === "NOPE")).toHaveLength(1);
        expect(searches.every((q) => q !== null)).toBe(true);
        expect(directoryReads()).toEqual([]);
    });
});
