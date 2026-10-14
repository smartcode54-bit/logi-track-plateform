// UI2C (follow-up of issue #142): customer names by id instead of every page of `GET /v1/customers` per page.
// One cached `['customers','ref',id]` query per distinct id (a customer id or a billing party id), whatever the row
// count; a customer a picker page brought is named without any further request; an id no picker page brought goes
// through the master-data directory once (the stopgap until Go serves an id lookup, see `customerRefQuery`).
import React from "react";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClientProvider, type QueryClient } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";

import { createQueryClient } from "@/lib/queryClient";
import { fakeWeb, json, type FakeCall } from "@/test-utils/fakeWeb";
import { customerSearchQuery } from "./pickers.go";
import { distinctRefs, fetchCustomerLookup, useCustomerLookup } from "./useCustomerLookup";

const CUSTOMERS = [
    { id: "c1", billingPartyId: "p1", code: "CJSF", name: "ซีเจ ซุปเปอร์เฟรช" },
    { id: "c2", billingPartyId: "p2", code: "TTP", name: "TTP Co" },
    { id: "c3", billingPartyId: null, code: "NOPARTY", name: "No party" },
];

const params = (call: FakeCall) => new URL(call.url, "http://web.test").searchParams;

function setup() {
    const web = fakeWeb();
    web.on("GET", "/api/go/v1/customers", (call) => {
        const q = (params(call).get("q") ?? "").toLowerCase();
        return json(200, { data: CUSTOMERS.filter((c) => !q || `${c.code} ${c.name}`.toLowerCase().includes(q)) });
    });
    const client = createQueryClient();
    const wrapper = ({ children }: { children: React.ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
    const directoryReads = () => web.calls.filter((c) => c.url.startsWith("/api/go/v1/customers") && !params(c).has("q") && params(c).get("limit") === "100");
    return { web, client, wrapper, directoryReads };
}

afterEach(() => vi.unstubAllGlobals());

describe("useCustomerLookup", () => {
    it("asks once per distinct id, finds a customer by its id or its billing party, and says what it cannot name", async () => {
        const { wrapper, directoryReads, client } = setup();
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
        // One query per distinct id; the directory behind them is read once for all of them.
        expect(client.getQueryCache().findAll({ queryKey: ["customers", "ref"] }).map((q) => q.queryKey[2]).sort()).toEqual(["c1", "p2", "unknown-id"]);
        expect(directoryReads()).toHaveLength(1);
        expect(distinctRefs(refs)).toEqual(["c1", "p2", "unknown-id"]);
    });

    it("serves a later page from the cache (no request) and keeps its result while the ids do not change", async () => {
        const { web, wrapper } = setup();
        const first = renderHook(() => useCustomerLookup(["c1", "p2"]), { wrapper });
        await waitFor(() => expect(first.result.current.pending).toBe(false));
        const requests = web.calls.length;

        const second = renderHook(({ refs }) => useCustomerLookup(refs), { wrapper, initialProps: { refs: ["p2", "c1", "c1"] } });
        expect(second.result.current.pending).toBe(false);
        expect(second.result.current.label("c1")).toBe("CJSF — ซีเจ ซุปเปอร์เฟรช");
        const before = second.result.current;
        second.rerender({ refs: ["c1", "p2"] });
        expect(second.result.current).toBe(before);
        expect(web.calls.length).toBe(requests);
    });

    it("names a customer a picker page brought without reading the directory", async () => {
        const { client, wrapper, directoryReads } = setup();
        await client.fetchQuery(customerSearchQuery("ttp"));
        const { result } = renderHook(() => useCustomerLookup(["c2", "p2"]), { wrapper });
        expect(result.current.pending).toBe(false);
        expect(result.current.label("c2")).toBe("TTP — TTP Co");
        expect(result.current.label("p2")).toBe("TTP — TTP Co");
        expect(directoryReads()).toHaveLength(0);
    });

    it("fetchCustomerLookup resolves every id before an export writes its file", async () => {
        const { client } = setup();
        const names = await fetchCustomerLookup(client as QueryClient, ["c1", "p2", "c1", null, "nope"]);
        expect(names.pending).toBe(false);
        expect([names.label("c1"), names.label("p2"), names.label("nope"), names.get("c1")?.code]).toEqual([
            "CJSF — ซีเจ ซุปเปอร์เฟรช",
            "TTP — TTP Co",
            "nope",
            "CJSF",
        ]);
    });
});
