// T34 (issue #54): the Go side of the operations pages, the requests and the cache. The acceptance criteria
// this file pins: the boards, the monitor, standby and incidents read Go only (each filter a server parameter, no
// window, no cap: TW8 M2), the monitor's export pages through `GET /v1/trips/monitor` with its cursor, a task
// write invalidates the boards and the monitor, the trip editor's stops reach `PATCH /v1/trips/{id}`, and the
// pure helpers the pages render with.
import { afterEach, describe, expect, it, vi } from "vitest";

import { getQueryClient } from "@/lib/queryClient";
import { queryKeys } from "@/lib/queryKeys";
import { fakeWeb, json } from "@/test-utils/fakeWeb";

import {
    boardTime,
    cancelTask,
    createTask,
    filterBoard,
    importTasks,
    patchTask,
    taskBoardQuery,
    taskTripQuery,
    type TaskDTO,
} from "./tasks.go";
import {
    activeMonitorFilters,
    buildMonitorExport,
    deliveredTimeOf,
    emptyMonitorFilter,
    fetchMonitorRows,
    monitorFacets,
    monitorQuery,
    patchTrip,
    toCsv,
    type MonitorRowDTO,
} from "@/features/drivers/api/monitor.go";
import { backfillStandby, standbyQuery } from "@/features/standby/api/standby.go";
import { incidentCause, incidentsQuery, matchesIncident, type IncidentDTO } from "@/features/incident-reports/api/incidents.go";
import { cellDay, cellTaskType, cellTime, resolveCustomer } from "../components/go/TaskImportDialog";
import { stopsPatch } from "@/features/drivers/components/go/TripEditDialog";

afterEach(() => {
    vi.unstubAllGlobals();
    getQueryClient().clear();
});

/** Runs a query's `queryFn` once, as TanStack would. */
async function runQueryFn(options: { queryKey: readonly unknown[]; queryFn?: unknown }, pageParam?: string) {
    const fn = options.queryFn as (ctx: unknown) => Promise<unknown>;
    return fn({ queryKey: options.queryKey, signal: new AbortController().signal, pageParam, client: getQueryClient(), meta: undefined });
}

const params = (url: string) => Object.fromEntries(new URL(url, "http://web.test").searchParams);

const place = (label: string, key = `code:${label}`) => ({ raw: label, key, label, resolved: true });

function task(over: Partial<TaskDTO> = {}): TaskDTO {
    return {
        id: "t1", tenantId: "o", taskNo: "FM-12102026-001", taskType: "first_mile", jobCategory: "PRIMARY", status: "assigned",
        planAt: "2026-10-11T17:00:00Z", planDate: "2026-10-12", planTime: "09:30", actualPickupAt: null, sourceHub: "ALANG-A",
        sourceHubId: "h1", source: place("ALANG-A"), destination: "SOCE", destinationHubId: null, destinationSocKey: "SOCE",
        destinationPlace: place("SOCE"), truckId: null, truckType: "6WH", licensePlate: null, driverId: null, driverName: null,
        helperDriverId: null, helperName: null, runOrder: null, checkInAt: null, checkInLat: null, checkInLng: null, isMultiDelivery: false,
        stops: [], trip: null, createdAt: "2026-10-11T00:00:00Z", updatedAt: "2026-10-11T00:00:00Z", ...over,
    };
}

function monitorRow(over: Partial<MonitorRowDTO> = {}): MonitorRowDTO {
    return {
        id: "r1", tenantId: "o", tripNo: "SPXTH-1", status: "delivered", jobType: "first_mile", jobCategory: "PRIMARY",
        createdAt: "2026-10-11T02:00:00Z", updatedAt: "2026-10-11T02:00:00Z", deliveredAt: "2026-10-11T06:00:00Z", taskId: "t1",
        taskNo: "FM-1", taskStatus: "completed", checkInAt: "2026-10-11T01:00:00Z", runOrder: 1, planAt: null, driverId: "d1",
        driverName: "สมชาย", origin: place("ALANG-A"), destination: place("SOCE"), stops: [],
        truck: { truckId: "k1", plate: "1กข-1234", key: "id:k1", vehicleClass: "6WH", vehicleClassKey: "6WH" }, partnerCode: null,
        sealCode: "S1", isMultiDelivery: false, incidentCount: 0, photoCount: 3, price: { estimateThb: 1200, unpricedReason: null,
            manualOverride: false, jobCategoryAtPricing: "PRIMARY", isMultiDelivery: false, computedAt: "2026-10-11T06:00:00Z" },
        ...over,
    };
}

describe("boards: GET /v1/tasks by plan day (rows 5, 9, 11)", () => {
    it("sends the type and the day; job-assign sends no type; no day lists the newest tasks", async () => {
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/tasks", () => json(200, { data: [], nextCursor: "c2" }));
        await runQueryFn(taskBoardQuery({ date: "2026-10-12", type: "first_mile" }));
        await runQueryFn(taskBoardQuery({ date: "2026-10-12", type: "all" }), "c2");
        await runQueryFn(taskBoardQuery({ date: "", type: "line_haul" }));
        const [a, b, c] = web.calls.map((x) => params(x.url));
        expect(a).toEqual({ taskType: "first_mile", date: "2026-10-12", limit: "200" });
        expect(b).toEqual({ date: "2026-10-12", limit: "200", cursor: "c2" });
        expect(c).toEqual({ taskType: "line_haul", limit: "200" });
        expect(taskBoardQuery({ date: "d", type: "all" }).queryKey).toEqual(["tasks", { date: "d", type: "all" }]);
    });

    it("a task's trip is null when Go answers 404", async () => {
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/tasks/t1/trip", () => json(404, { error: { code: "not_found", message: "x" } }));
        expect(await runQueryFn(taskTripQuery("t1"))).toBeNull();
    });

    it("filters the loaded day by SOC key and source, and shows the plan time", () => {
        const rows = [task(), task({ id: "t2", destinationSocKey: "SOCN", destinationPlace: place("SOCN") }), task({ id: "t3", sourceHub: "X" })];
        expect(filterBoard(rows, "all", "all").map((r) => r.id)).toEqual(["t1", "t2", "t3"]);
        expect(filterBoard(rows, "SOCN", "all").map((r) => r.id)).toEqual(["t2"]);
        expect(filterBoard(rows, "all", "X").map((r) => r.id)).toEqual(["t3"]);
        expect(boardTime(task({ planTime: "09:30:00" }))).toBe("09:30");
        expect(boardTime(task({ planTime: null, actualPickupAt: null }))).toBe("");
    });
});

describe("task writes: one Idempotency-Key per create, the boards and the monitor stale (no notifyTaskUpdate)", () => {
    it("create, patch and cancel reach their routes and invalidate ['tasks'] and ['trips','monitor']", async () => {
        const web = fakeWeb();
        web.on("POST", "/api/go/v1/tasks", () => json(201, { data: task({ taskNo: "FM-12102026-007" }) }));
        web.on("PATCH", "/api/go/v1/tasks/t1", () => json(200, { data: task() }));
        web.on("POST", "/api/go/v1/tasks/t1/cancel", () => json(200, { data: task({ status: "cancelled" }) }));
        const client = getQueryClient();
        const keys = [queryKeys.tasks.list({ date: "2026-10-12", type: "first_mile" }), queryKeys.trips.monitor({ from: "a" }), queryKeys.hubs.all()];
        for (const k of keys) client.setQueryData(k, { cached: true });
        const out = await createTask({ taskType: "first_mile", date: "2026-10-12", sourceHub: "ALANG-A", destination: "SOCE",
            billingPartyId: "p1", jobCategory: "PRIMARY", truckId: undefined, driverId: "" });
        expect(out.taskNo).toBe("FM-12102026-007");
        expect(web.calls[0].body).toEqual({ taskType: "first_mile", date: "2026-10-12", sourceHub: "ALANG-A", destination: "SOCE",
            billingPartyId: "p1", jobCategory: "PRIMARY" });
        expect(web.calls[0].headers["idempotency-key"]).toMatch(/^[0-9a-f-]{36}$/);
        expect(keys.map((k) => Boolean(client.getQueryState(k)?.isInvalidated))).toEqual([true, true, false]);
        await patchTask("t1", { helperDriverId: null });
        expect(web.calls[1].body).toEqual({ helperDriverId: null });
        await cancelTask("t1", undefined);
        expect(web.calls[2].body).toEqual({});
        expect(web.calls.some((c) => c.url.includes("notifyTaskUpdate"))).toBe(false);
    });

    it("the import is a dry run first, then the write (one request per type)", async () => {
        const web = fakeWeb();
        web.on("POST", "/api/go/v1/tasks/import", (c) =>
            json(200, { data: { created: [{ row: 1, id: (c.body as { dryRun: boolean }).dryRun ? undefined : "n1", taskNo: "LH-1" }], errors: [], dryRun: (c.body as { dryRun: boolean }).dryRun } }));
        const rows = [{ date: "2026-10-12", sourceHub: "SOCE", destination: "HUB", billingPartyId: "p1", jobCategory: "เสริม", plate: "" }];
        const dry = await importTasks("line_haul", rows, true);
        expect(dry.dryRun).toBe(true);
        expect(web.calls[0].headers["idempotency-key"]).toBeUndefined();
        await importTasks("line_haul", rows, false);
        expect(web.calls[1].body).toEqual({ taskType: "line_haul", dryRun: false,
            rows: [{ date: "2026-10-12", sourceHub: "SOCE", destination: "HUB", billingPartyId: "p1", jobCategory: "เสริม" }] });
        expect(web.calls[1].headers["idempotency-key"]).toBeDefined();
    });

    it("reads the import cells as the legacy dialog did", () => {
        const serial = () => ({ y: 2026, m: 10, d: 12 });
        expect(cellDay(46307, serial)).toBe("2026-10-12");
        expect(cellDay("12/10/2026")).toBe("2026-10-12");
        expect(cellDay("2026-10-12T00:00:00")).toBe("2026-10-12");
        expect(cellDay("")).toBe("");
        expect(cellTime(0.5)).toBe("12:00");
        expect(cellTime("9:05")).toBe("09:05");
        expect(cellTaskType("LH", "first_mile")).toBe("line_haul");
        expect(cellTaskType("Line haul", "first_mile")).toBe("line_haul");
        expect(cellTaskType("fm", "line_haul")).toBe("first_mile");
        expect(cellTaskType("", "line_haul")).toBe("line_haul");
        const list = [{ id: "c1", billingPartyId: "p1", code: "CJSF", name: "CJ" }, { id: "c2", billingPartyId: "p2", code: "SPX", name: "Shopee" }];
        expect(resolveCustomer("cjsf", list)?.id).toBe("c1");
        expect(resolveCustomer("shopee", list)?.id).toBe("c2");
        expect(resolveCustomer("", list)).toBeUndefined();
    });
});

describe("monitor: one aggregate request per page (TW6 M2, TW8 M2: 200 per page)", () => {
    it("sends every filter to the server and nothing for 'all'", async () => {
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/trips/monitor", () => json(200, { data: [], meta: { facets: { total: 3 } } }));
        const f = { ...emptyMonitorFilter("2026-10-01", "2026-10-31"), status: "incident", plate: "id:k1", origin: "raw:ลาน", driverId: "d1", q: " SPX " };
        await runQueryFn(monitorQuery(f));
        expect(params(web.calls[0].url)).toEqual({ from: "2026-10-01", to: "2026-10-31", status: "incident", plate: "id:k1",
            origin: "raw:ลาน", driverId: "d1", q: "SPX", limit: "200" });
        expect(activeMonitorFilters(f)).toBe(5);
        expect(activeMonitorFilters(emptyMonitorFilter("a", "b"))).toBe(0);
        expect(monitorQuery(f).queryKey[0]).toBe("trips");
        expect(monitorQuery(f).queryKey[1]).toBe("monitor");
    });

    it("reads the facets of the first page", () => {
        expect(monitorFacets([{ data: [], meta: { facets: { total: 7 } } }])?.total).toBe(7);
        expect(monitorFacets([{ data: [] }])).toBeNull();
        expect(monitorFacets(undefined)).toBeNull();
    });

    it("the export pages through the cursor with facets=false until the last page", async () => {
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/trips/monitor", (c) => {
            const cur = params(c.url).cursor;
            if (!cur) return json(200, { data: [monitorRow({ id: "a" })], nextCursor: "p2" });
            if (cur === "p2") return json(200, { data: [monitorRow({ id: "b" })], nextCursor: "p3" });
            return json(200, { data: [monitorRow({ id: "c" })] });
        });
        const seen: number[] = [];
        const rows = await fetchMonitorRows(emptyMonitorFilter("2026-10-01", "2026-10-31"), undefined, (n) => seen.push(n));
        expect(rows.map((r) => r.id)).toEqual(["a", "b", "c"]);
        expect(seen).toEqual([1, 2, 3]);
        expect(web.calls.map((c) => params(c.url).facets)).toEqual(["false", "false", "false"]);
        expect(web.calls.map((c) => params(c.url).cursor ?? "")).toEqual(["", "p2", "p3"]);
    });

    it("builds the sheet: one row per stop, a destination filter keeps the matching stops, the price once", () => {
        const multi = monitorRow({ id: "m", stops: [
            { index: 1, place: place("SOCE"), status: "delivered", deliveredAt: "2026-10-11T03:00:00Z", completedSeq: 1 },
            { index: 2, place: place("SOCN"), status: "pending", deliveredAt: null, completedSeq: null },
        ] });
        const labels = { headers: ["h"], status: (s: string) => s, stopStatus: (s: string) => s, jobType: (j: string) => j, yes: "Y", no: "N",
            formatDateTime: (iso: string | null | undefined) => iso ?? "" };
        const all = buildMonitorExport([monitorRow(), multi], "all", labels);
        expect(all).toHaveLength(4);
        expect(all[1].slice(10)).toEqual([1200, "N", 1, "", "2026-10-11T06:00:00Z"]);
        expect(all[2][6]).toBe("SOCE");
        expect(all[2][10]).toBe(1200);
        expect(all[3][10]).toBeNull();
        const narrowed = buildMonitorExport([multi], "code:SOCN", labels);
        expect(narrowed).toHaveLength(2);
        expect(narrowed[1][6]).toBe("SOCN");
        expect(narrowed[1][10]).toBe(1200);
        expect(toCsv([["a", 'b"c'], [1, null]])).toBe('﻿a,"b""c"\r\n1,');
        expect(deliveredTimeOf({ deliveredAt: null, stops: multi.stops })).toBe("2026-10-11T03:00:00Z");
    });

    it("the trip editor sends the stops only when they changed (PATCH /v1/trips/{id} stops[], T34)", async () => {
        const original = [{ id: "s1", index: 1, destination: "SOCE", destinationHubId: null, place: place("SOCE"), sourceId: null,
            status: "delivered", deliveredAt: null, deliveredLat: null, deliveredLng: null, completedSeq: 1 }];
        expect(stopsPatch(original, [{ destination: "soce", status: "delivered", locked: true }])).toBeUndefined();
        expect(stopsPatch(original, [{ destination: "SOCE", status: "delivered", locked: true }, { destination: " socn ", status: "delivered", locked: false }]))
            .toEqual([{ destination: "SOCE", status: "delivered" }, { destination: "SOCN", status: "delivered" }]);
        const web = fakeWeb();
        web.on("PATCH", "/api/go/v1/trips/r1", () => json(200, { data: { id: "r1" } }));
        const client = getQueryClient();
        client.setQueryData(queryKeys.trips.monitor({ from: "x" }), { cached: true });
        await patchTrip("r1", { stops: [{ destination: "SOCE" }], origin: "SOCN" });
        expect(web.calls[0].body).toEqual({ stops: [{ destination: "SOCE" }], origin: "SOCN" });
        expect(client.getQueryState(queryKeys.trips.monitor({ from: "x" }))?.isInvalidated).toBe(true);
    });
});

describe("standby and incidents: keyset lists with the range on the server (rows 7, 15; TW8 M2)", () => {
    it("standby and incident lists send the range, 100 per page, no window", async () => {
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/standby", () => json(200, { data: [] }));
        web.on("GET", "/api/go/v1/incidents", () => json(200, { data: [] }));
        await runQueryFn(standbyQuery({ from: "2026-09-01", to: "2026-09-30" }));
        await runQueryFn(standbyQuery({ from: "", to: "" }));
        await runQueryFn(incidentsQuery({ from: "2026-10-12", to: "2026-10-12" }), "n");
        const [a, b, c] = web.calls.map((x) => params(x.url));
        expect(a).toEqual({ from: "2026-09-01", to: "2026-09-30", limit: "100" });
        expect(b).toEqual({ limit: "100" });
        expect(c).toEqual({ from: "2026-10-12", to: "2026-10-12", limit: "100", cursor: "n" });
    });

    it("the backfill uploads its photos first, then writes once with their keys", async () => {
        const web = fakeWeb();
        web.on("POST", "/api/go/v1/uploads/presign", (c) =>
            json(200, { data: { key: `standby/x/${(c.body as { variant: string }).variant}.jpg`, url: "https://s3.test/put", method: "PUT", headers: {} } }));
        web.on("PUT", "https://s3.test/put", () => new Response(null, { status: 200 }));
        web.on("POST", "/api/go/v1/standby/backfill", () => json(201, { data: { id: "sb1" } }));
        const ws = new File(["x"], "ws.jpg", { type: "image/jpeg" });
        await backfillStandby({ driverId: "d1", startLocation: "A", endLocation: "B", startedAt: "2026-10-10T01:00:00Z", endedAt: "2026-10-10T02:00:00Z" },
            { worksheet: ws, site: null });
        const post = web.calls.find((c) => c.url === "/api/go/v1/standby/backfill");
        expect(post?.body).toEqual({ driverId: "d1", startLocation: "A", endLocation: "B", startedAt: "2026-10-10T01:00:00Z",
            endedAt: "2026-10-10T02:00:00Z", photos: [{ type: "customer_worksheet", key: "standby/x/customer_worksheet.jpg" }] });
        expect((web.calls[0].body as { purpose: string }).purpose).toBe("standby_photo");
    });

    it("incident helpers: the cause label and the loaded-rows search", () => {
        expect(incidentCause("incident_cause_tire")).toBe("TIRE");
        expect(incidentCause(null)).toBe("—");
        const row = { tripNo: "SPXTH-9", driverName: "สมหญิง", truckLicensePlate: "1กข", description: "ยางแตก", delayCause: null } as IncidentDTO;
        expect(matchesIncident(row, "spxth")).toBe(true);
        expect(matchesIncident(row, "ยาง")).toBe(true);
        expect(matchesIncident(row, "nope")).toBe(false);
    });
});
