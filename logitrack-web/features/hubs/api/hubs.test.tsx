// TW4 (developer-spec.md §10.6, §10.7; Appendix E §E.3.1, §E.4 row "hubs"): one ['hubs'] cache for
// every page, the Firestore adapter to the GET /v1/hubs DTO, the per-page selectors and the maps.
//
// Acceptance: "Hubs read once per session across dashboard -> monitor -> job-assign -> first-mile ->
// billing" (#84). The session test drives each consumer the way its page does and counts the reads
// of the hubs collection; the source guard fails when any other page lists the collection itself.
import React from "react";
import { readdirSync, readFileSync, statSync } from "fs";
import path from "path";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClientProvider, QueryObserver } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const store = vi.hoisted(() => ({ hubs: [] as Array<{ id: string; data: Record<string, unknown> }>, reads: [] as string[] }));

vi.mock("@/firebase/client", () => ({ db: {}, auth: {}, functions: {}, storage: {} }));
vi.mock("firebase/firestore", () => ({
    collection: (_db: unknown, name: string) => ({ name }),
    query: (ref: { name: string }) => ref,
    where: () => ({}),
    orderBy: () => ({}),
    getDocs: async (ref: { name: string }) => {
        store.reads.push(ref.name);
        const docs = ref.name === "hubs" ? store.hubs : [];
        return {
            docs: docs.map((d) => ({ id: d.id, data: () => d.data })),
            forEach: (fn: (d: { id: string; data: () => Record<string, unknown> }) => void) =>
                docs.forEach((d) => fn({ id: d.id, data: () => d.data })),
            size: docs.length,
        };
    },
    getCountFromServer: vi.fn(),
}));

const { buildHubMaps, fetchHubsCached, hubMapsQueryOptions, hubsQueryOptions, invalidateHubs } = await import("./hubs");
const { hubFromFirestore } = await import("./hubs.firestore");
const selectors = await import("./selectors");
const { useHubMaps, useHubs } = await import("./useHubs");
const { taskService } = await import("@/features/tasks/services/taskService");
const { getQueryClient, resetQueryClientForTests } = await import("@/lib/queryClient");
const { webFlagsQueryOptions } = await import("@/features/platform/api/webFlags");
const { useWebFlagsSync } = await import("@/features/platform/api/useWebFlags");

const FIXTURE: Array<{ id: string; data: Record<string, unknown> }> = [
    {
        id: "doc-a",
        data: {
            source_id: "ALANG-A",
            source_name_th: "อลัง",
            source_name_en: "Alang",
            latitude: 13.1,
            longitude: 100.2,
            station_type: "HUB",
            linkedCustomerId: "cust-1",
            linkedCustomerName: "CJ — CJ Express",
            customerLinkKind: "customer",
        },
    },
    // A legacy row: old field names, no Thai name, lat/lng spelling, FM_HUB station type.
    { id: "doc-b", data: { hubId: "spk-gw", hubName: "SPK-GW", lat: 13.5, lng: 100.9, station_type: "FM_HUB" } },
    { id: "doc-c", data: { source_id: "SOCE", source_name_th: "SOC ตะวันออก", station_type: "SOC" } },
    // Both spellings present: pages keyed maps by each of them.
    { id: "doc-d", data: { source_id: "SPK890103-ลาดกระบัง26", hubCode: "SPK890103", hubTHName: "ลาดกระบัง", hubName: "Lat Krabang", station_type: "RETURN_CENTER" } },
    { id: "doc-e", data: { hubCode: "0STANDBY", station_type: "SOC", createdByDriver: true } },
];

beforeEach(() => {
    store.hubs = FIXTURE;
    store.reads = [];
    resetQueryClientForTests();
    // Every domain still on Firestore (the P0 flags).
    vi.stubGlobal(
        "fetch",
        vi.fn(async (input: RequestInfo | URL) => {
            if (String(input) === "/api/go/v1/config/web-flags") {
                return new Response(JSON.stringify({ data: { domains: {} } }), { status: 200 });
            }
            throw new Error(`unexpected ${String(input)}`);
        })
    );
});

afterEach(() => {
    vi.unstubAllGlobals();
});

// The four shapes as the pages built them before TW4 (copied from the removed code).
function legacyTaskServiceRows() {
    return FIXTURE.map(({ id, data }) => ({
        "Hub Code": data.source_id ?? data.hubId ?? data.hubCode,
        "Hub Name": data.source_name_en ?? data.hubName,
        "Hub Name Th": (data.source_name_th ?? data.hubTHName ?? data.hub_th_name ?? data.station_name_th ?? "") || undefined,
        station_type: data.station_type ?? "",
        linkedCustomerId: data.linkedCustomerId ?? "",
        customerLinkKind: data.customerLinkKind ?? "",
        lat: data.latitude ?? data.lat,
        lng: data.longitude ?? data.lng,
        source: "custom",
        id,
    }));
}

function legacyMonitorEntries() {
    return FIXTURE.map(({ data }) => ({
        source_id: String(data.source_id ?? data.hubId ?? data.hubCode ?? ""),
        source_name_en: String(data.source_name_en ?? data.hubName ?? "") || undefined,
        source_name_th: String(data.source_name_th ?? data.hubTHName ?? data.hub_th_name ?? "").trim() || undefined,
        station_type: data.station_type === "SOC" ? "SOC" : "HUB",
        linkedCustomerId: data.linkedCustomerId,
        linkedCustomerName: data.linkedCustomerName,
        customerLinkKind: data.customerLinkKind,
    }));
}

function legacyRateCardOptions() {
    return FIXTURE.map(({ data }) => {
        const id = String(data.hubId ?? data.source_id ?? "").trim().toUpperCase();
        const name = String(data.source_name_en ?? data.source_name_th ?? data.hubName ?? "").trim();
        return { id, name: name || undefined };
    }).filter((x) => x.id);
}

function legacyServerMaps() {
    const nameToCode = new Map<string, string>();
    const codeToName = new Map<string, string>();
    for (const { data } of FIXTURE) {
        const code = String(data.source_id ?? data.hubId ?? "").trim();
        if (!code) continue;
        for (const nameField of [data.source_name_th, data.source_name_en, data.hubName]) {
            const name = typeof nameField === "string" ? nameField.trim() : "";
            if (name && name !== code && !nameToCode.has(name)) nameToCode.set(name, code);
        }
        const displayName = typeof data.source_name_th === "string" ? data.source_name_th.trim() : "";
        if (displayName && !codeToName.has(code)) codeToName.set(code, displayName);
    }
    return { nameToCode: Object.fromEntries(nameToCode), codeToName: Object.fromEntries(codeToName) };
}

const dtos = () => FIXTURE.map(({ id, data }) => hubFromFirestore(id, data));
// The same hubs as the Go DTO (no legacyDoc).
const goDtos = () =>
    dtos().map((hub) => {
        const dto = { ...hub };
        delete dto.legacyDoc;
        return dto;
    });

describe("Firestore adapter -> GET /v1/hubs DTO", () => {
    it("maps the stored fields, legacy spellings included", () => {
        const [a, b, , d, e] = dtos();
        expect(a).toMatchObject({
            id: "doc-a",
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
        });
        expect(b).toMatchObject({ sourceId: "spk-gw", sourceNameTh: null, sourceNameEn: "SPK-GW", latitude: 13.5, longitude: 100.9, stationType: "FM_HUB" });
        expect(d).toMatchObject({ sourceId: "SPK890103-ลาดกระบัง26", sourceNameTh: "ลาดกระบัง", sourceNameEn: "Lat Krabang" });
        expect(e).toMatchObject({ sourceId: "0STANDBY", createdByDriver: true, latitude: null });
        expect(a.legacyDoc).toBe(FIXTURE[0].data);
    });
});

describe("selectors reproduce the legacy page shapes", () => {
    it("task boards, task dialogs and import dialogs (taskService.fetchHubs)", () => {
        const rows = selectors.selectHubRows(dtos());
        expect(
            rows.map((row) => {
                const rest = { ...row };
                delete rest.linkedCustomerName;
                return rest;
            })
        ).toEqual(legacyTaskServiceRows());
        // first-mile / line-haul / job-assign also carried linkedCustomerName for display.
        expect(rows[0].linkedCustomerName).toBe("CJ — CJ Express");
    });

    it("driver monitor (useDriverMonitor.ts:505-521)", () => {
        expect(selectors.selectHubDisplayEntries(dtos())).toEqual(legacyMonitorEntries());
    });

    it("rate card (rate-card/page.tsx:420-427)", () => {
        expect(selectors.selectHubOptions(dtos())).toEqual(legacyRateCardOptions());
    });

    it("labels and the source-id index", () => {
        const [a, b] = dtos();
        expect(selectors.selectHubLabel(a)).toBe("อลัง");
        expect(selectors.selectBillingHubLabel(a)).toBe("Alang");
        expect(selectors.selectHubLabel(b)).toBe("SPK-GW");
        expect(selectors.selectHubBySourceId(dtos()).get("SOCE")?.id).toBe("doc-c");
    });

    it("work the same over the Go DTO for canonical rows", () => {
        const fromGo = selectors.selectHubRows(goDtos());
        expect(fromGo[0]).toMatchObject({ "Hub Code": "ALANG-A", "Hub Name": "Alang", "Hub Name Th": "อลัง", lat: 13.1, lng: 100.2 });
        expect(selectors.selectHubDisplayEntries(goDtos())[2]).toMatchObject({ source_id: "SOCE", station_type: "SOC" });
    });
});

describe("['hubs','maps']", () => {
    it("mirrors the server's buildHubMaps: two objects, never merged", () => {
        const maps = buildHubMaps(dtos());
        expect(maps).toEqual(legacyServerMaps());
        // A code never maps to itself, and the two directions stay apart.
        expect(maps.nameToCode["ALANG-A"]).toBeUndefined();
        expect(maps.codeToName["อลัง"]).toBeUndefined();
    });
});

function wrapper({ children }: { children: React.ReactNode }) {
    return <QueryClientProvider client={getQueryClient()}>{children}</QueryClientProvider>;
}

describe("one read per session (#84 acceptance)", () => {
    it("dashboard -> monitor -> job-assign -> first-mile -> billing reads the hubs collection once", async () => {
        // Driver monitor (useDriverMonitor)
        const monitor = renderHook(() => useHubs(selectors.selectHubDisplayEntries), { wrapper });
        await waitFor(() => expect(monitor.result.current.data).toHaveLength(FIXTURE.length));
        monitor.unmount();
        // Job assign board, then its import dialog (taskService.fetchHubs)
        const jobAssign = renderHook(() => useHubs(selectors.selectHubRows), { wrapper });
        await waitFor(() => expect(jobAssign.result.current.data).toHaveLength(FIXTURE.length));
        await taskService.fetchHubs();
        jobAssign.unmount();
        // First mile board + its task dialog (useFirstMileTask), both mounted together
        const firstMile = renderHook(() => [useHubs(selectors.selectHubRows), useHubs(selectors.selectHubRows)], { wrapper });
        await waitFor(() => expect(firstMile.result.current[1].data).toHaveLength(FIXTURE.length));
        firstMile.unmount();
        // Billing document (features/accounting/api/billing.ts), income (buildIncomeHubMaps), rate card
        await fetchHubsCached();
        await fetchHubsCached().then(selectors.selectHubOptions);
        // Hub maps derive from the cached list
        const maps = renderHook(() => useHubMaps(), { wrapper });
        await waitFor(() => expect(maps.result.current.data?.codeToName["SOCE"]).toBe("SOC ตะวันออก"));

        expect(store.reads.filter((c) => c === "hubs")).toEqual(["hubs"]);
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
        expect(store.reads.filter((c) => c === "hubs")).toHaveLength(2);
    });

    // The flags observer of the providers (useWebFlagsSync) starts ['webFlags'] before any domain
    // fetch, and a later fetch joins that request with the observer's retry policy, not its own: the
    // flags query itself must not retry (the 60 s poll and the focus refetch are its retry).
    it("falls back to Firestore at once when the flags cannot be read, with the flags observer mounted (no retry wait)", async () => {
        const urls: string[] = [];
        vi.stubGlobal(
            "fetch",
            vi.fn(async (input: RequestInfo | URL) => {
                urls.push(String(input));
                return new Response("<html>bad gateway</html>", { status: 502 });
            })
        );
        const stopFlags = new QueryObserver(getQueryClient(), webFlagsQueryOptions).subscribe(() => undefined);
        const started = Date.now();
        await expect(fetchHubsCached()).resolves.toHaveLength(FIXTURE.length);
        expect(Date.now() - started).toBeLessThan(500);
        expect(urls).toEqual(["/api/go/v1/config/web-flags"]);
        expect(store.reads).toEqual(["hubs"]);
        stopFlags();
    });

    it("a page's useHubs beside the providers' useWebFlagsSync falls back after one flags request", async () => {
        const urls: string[] = [];
        vi.stubGlobal(
            "fetch",
            vi.fn(async (input: RequestInfo | URL) => {
                urls.push(String(input));
                return new Response("<html>bad gateway</html>", { status: 502 });
            })
        );
        const readsBefore = store.reads.length;
        const started = Date.now();
        // The providers' order: the flags runtime is a sibling before the page.
        const { result } = renderHook(
            () => {
                useWebFlagsSync();
                return useHubs(selectors.selectHubRows);
            },
            { wrapper }
        );
        await waitFor(() => expect(result.current.data).toHaveLength(FIXTURE.length));
        expect(Date.now() - started).toBeLessThan(500);
        expect(urls).toEqual(["/api/go/v1/config/web-flags"]);
        // One flags request, then the Firestore read: no retry was waited for.
        expect(store.reads.slice(readsBefore)).toEqual(["hubs"]);
    });

    it("reads GET /v1/hubs instead once the masterdata flag is go, under the same key", async () => {
        const urls: string[] = [];
        vi.stubGlobal(
            "fetch",
            vi.fn(async (input: RequestInfo | URL) => {
                const url = String(input);
                urls.push(url);
                if (url === "/api/go/v1/config/web-flags") return new Response(JSON.stringify({ data: { domains: { masterdata: "go" } } }), { status: 200 });
                if (url === "/api/go/v1/hubs") return new Response(JSON.stringify({ data: goDtos() }), { status: 200 });
                throw new Error(`unexpected ${url}`);
            })
        );
        const hubs = await fetchHubsCached();
        expect(hubs[0]).not.toHaveProperty("legacyDoc");
        expect(urls).toEqual(["/api/go/v1/config/web-flags", "/api/go/v1/hubs"]);
        expect(store.reads).toEqual([]);
    });
});

describe("source guard", () => {
    const root = path.resolve(__dirname, "../../..");
    // Writers and the single-document duplicate probe; everything else goes through ['hubs'].
    const ALLOWED = new Set([
        "features/hubs/api/hubs.firestore.ts", // the adapter: the only list read
        "app/app/first-mile/hub-dialog.tsx", // duplicate-code probe (one where-query) and the hub write
        "app/app/sources/pickup-import-dialog.tsx", // the import write
        "lib/hubSocDistances.ts", // server-side distance batch helper with no importer in the web
    ]);
    const HUB_COLLECTION = /collection\(\s*db\s*,\s*(?:COLLECTIONS\.HUBS|["']hubs["'])\s*\)/;

    function walk(dir: string, out: string[] = []): string[] {
        for (const name of readdirSync(dir)) {
            const p = path.join(dir, name);
            if (statSync(p).isDirectory()) {
                if (name !== "node_modules" && name !== "__tests__") walk(p, out);
            } else if (/\.(ts|tsx)$/.test(name) && !/\.test\.tsx?$/.test(name)) out.push(p);
        }
        return out;
    }

    it("no page, hook or billing function lists the hubs collection itself", () => {
        const offenders = ["app", "components", "context", "features", "hooks", "lib"]
            .flatMap((d) => walk(path.join(root, d)))
            .filter((f) => HUB_COLLECTION.test(readFileSync(f, "utf8")))
            .map((f) => path.relative(root, f))
            .filter((f) => !ALLOWED.has(f));
        expect(offenders).toEqual([]);
    });
});
