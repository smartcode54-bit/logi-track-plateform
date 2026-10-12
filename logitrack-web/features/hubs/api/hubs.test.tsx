// TW4 / TW7 (developer-spec.md §10.6, §10.7; Appendix E §E.3.1, §E.4 row "hubs"): one ['hubs'] cache for
// every page over `GET /v1/hubs`, the per-page selectors and `['hubs','maps']` over `GET /v1/hubs/maps`.
// TW7 removed the Firestore adapter and the `masterdata` flag, so Go is the only source.
//
// Acceptance: "Hubs read once per session across dashboard -> monitor -> job-assign -> first-mile ->
// billing" (#84). The session test drives each consumer the way its page does and counts the requests.
import React from "react";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClientProvider, useQuery } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { getQueryClient, resetQueryClientForTests } from "@/lib/queryClient";
import { buildHubMaps, fetchHubsCached, hubMapsQueryOptions, hubsQueryOptions, invalidateHubs, type HubDTO } from "./hubs";
import * as selectors from "./selectors";

const HUBS: HubDTO[] = [
    {
        id: "0192b3a4-0000-7000-8000-00000000000a",
        sourceId: "ALANG-A",
        sourceNameTh: "อลัง",
        sourceNameEn: "Alang",
        latitude: 13.1,
        longitude: 100.2,
        stationType: "HUB",
        linkedCustomerId: "cust-1",
        linkedCustomerName: "CJ — CJ Express",
        linkedCustomerKind: "customer",
        createdByDriver: false,
        network: "SPK",
    },
    {
        id: "0192b3a4-0000-7000-8000-00000000000b",
        sourceId: "SPK-GW",
        sourceNameTh: null,
        sourceNameEn: "SPK-GW",
        latitude: 13.5,
        longitude: 100.9,
        stationType: "HUB",
        linkedCustomerId: null,
        linkedCustomerName: null,
        linkedCustomerKind: null,
        createdByDriver: false,
    },
    {
        id: "0192b3a4-0000-7000-8000-00000000000c",
        sourceId: "SOCE",
        sourceNameTh: "SOC ตะวันออก",
        sourceNameEn: null,
        latitude: null,
        longitude: null,
        stationType: "SOC",
        linkedCustomerId: null,
        linkedCustomerName: null,
        linkedCustomerKind: null,
        createdByDriver: false,
    },
];

let urls: string[] = [];

beforeEach(() => {
    urls = [];
    resetQueryClientForTests();
    vi.stubGlobal(
        "fetch",
        vi.fn(async (input: RequestInfo | URL) => {
            const url = String(input);
            urls.push(url);
            if (url === "/api/go/v1/hubs") return new Response(JSON.stringify({ data: HUBS }), { status: 200 });
            if (url === "/api/go/v1/hubs/maps") return new Response(JSON.stringify({ data: buildHubMaps(HUBS) }), { status: 200 });
            throw new Error(`unexpected ${url}`);
        })
    );
});

afterEach(() => {
    vi.unstubAllGlobals();
});

describe("selectors over the Go DTO", () => {
    it("task boards and dialogs (the rows of the retired taskService.fetchHubs)", () => {
        const rows = selectors.selectHubRows(HUBS);
        expect(rows[0]).toMatchObject({ "Hub Code": "ALANG-A", "Hub Name": "Alang", "Hub Name Th": "อลัง", lat: 13.1, lng: 100.2, id: HUBS[0].id });
        expect(rows[0].linkedCustomerName).toBe("CJ — CJ Express");
        expect(rows[1]["Hub Name Th"]).toBeUndefined();
    });

    it("display entries and picker options", () => {
        expect(selectors.selectHubDisplayEntries(HUBS)[2]).toMatchObject({ source_id: "SOCE", station_type: "SOC" });
        expect(selectors.selectHubOptions(HUBS)).toEqual([
            { id: "ALANG-A", name: "Alang" },
            { id: "SPK-GW", name: "SPK-GW" },
            { id: "SOCE", name: "SOC ตะวันออก" },
        ]);
    });

    it("labels and the source-id index", () => {
        const [a, b] = HUBS;
        expect(selectors.selectHubLabel(a)).toBe("อลัง");
        expect(selectors.selectBillingHubLabel(a)).toBe("Alang");
        expect(selectors.selectHubLabel(b)).toBe("SPK-GW");
        expect(selectors.selectHubBySourceId(HUBS).get("SOCE")?.id).toBe(HUBS[2].id);
    });
});

describe("buildHubMaps", () => {
    it("mirrors the server's maps: two objects, never merged", () => {
        const maps = buildHubMaps(HUBS);
        expect(maps.nameToCode).toEqual({ อลัง: "ALANG-A", Alang: "ALANG-A", "SOC ตะวันออก": "SOCE" });
        expect(maps.codeToName).toEqual({ "ALANG-A": "อลัง", SOCE: "SOC ตะวันออก" });
        // A code never maps to itself, and the two directions stay apart.
        expect(maps.nameToCode["SPK-GW"]).toBeUndefined();
        expect(maps.codeToName["อลัง"]).toBeUndefined();
    });
});

function wrapper({ children }: { children: React.ReactNode }) {
    return <QueryClientProvider client={getQueryClient()}>{children}</QueryClientProvider>;
}

describe("one read per session (#84 acceptance)", () => {
    it("dashboard -> monitor -> job-assign -> first-mile -> billing reads GET /v1/hubs once, with no web flags request", async () => {
        const useHubRows = () => useQuery({ ...hubsQueryOptions, select: selectors.selectHubRows });
        const monitor = renderHook(() => useQuery({ ...hubsQueryOptions, select: selectors.selectHubDisplayEntries }), { wrapper });
        await waitFor(() => expect(monitor.result.current.data).toHaveLength(HUBS.length));
        monitor.unmount();
        const jobAssign = renderHook(useHubRows, { wrapper });
        await waitFor(() => expect(jobAssign.result.current.data).toHaveLength(HUBS.length));
        jobAssign.unmount();
        const firstMile = renderHook(() => [useHubRows(), useHubRows()], { wrapper });
        await waitFor(() => expect(firstMile.result.current[1].data).toHaveLength(HUBS.length));
        firstMile.unmount();
        await fetchHubsCached();
        await fetchHubsCached().then(selectors.selectHubOptions);
        const maps = renderHook(() => useQuery(hubMapsQueryOptions), { wrapper });
        await waitFor(() => expect(maps.result.current.data?.codeToName["SOCE"]).toBe("SOC ตะวันออก"));

        expect(urls).toEqual(["/api/go/v1/hubs", "/api/go/v1/hubs/maps"]);
        expect(hubsQueryOptions.staleTime).toBe(600_000);
        expect(hubsQueryOptions.gcTime).toBe(3_600_000);
        expect(hubMapsQueryOptions.queryKey).toEqual(["hubs", "maps"]);
    });

    it("a hub write invalidates the list and the maps for every page", async () => {
        await fetchHubsCached();
        await getQueryClient().fetchQuery(hubMapsQueryOptions);
        await invalidateHubs();
        expect(getQueryClient().getQueryState(["hubs"])?.isInvalidated).toBe(true);
        expect(getQueryClient().getQueryState(["hubs", "maps"])?.isInvalidated).toBe(true);
        await fetchHubsCached();
        expect(urls.filter((u) => u === "/api/go/v1/hubs")).toHaveLength(2);
    });
});
