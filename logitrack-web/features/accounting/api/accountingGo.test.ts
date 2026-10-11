// T42: the Go side of the accounting pages, the requests and the cache. The acceptance criteria of issue
// #62 this file pins: a rate-card write invalidates only the keys it touched (no seven-collection reload),
// the income list filters the dates on the server (no 500-row window), the audit's status filter is a
// server filter, documents are the stored ones (filename and WHT from the statement), and the pure helpers
// the pages render with.
import { afterEach, describe, expect, it, vi } from "vitest";

import { getQueryClient } from "@/lib/queryClient";
import { queryKeys } from "@/lib/queryKeys";
import { fakeWeb, goErr, json } from "@/test-utils/fakeWeb";
import { probeGoDownload } from "@/lib/goDownload";

import {
    billingRowsQuery,
    canMoveStatement,
    createStatement,
    DEFAULT_ROWS_PARAMS,
    DOCUMENT_POLL_LIMIT_MS,
    DOCUMENT_POLL_MS,
    documentPath,
    documentsPending,
    rowsParamsComplete,
    statementQuery,
    statementsQuery,
} from "./billing.go";
import { PartialCreateError, UploadRefusedError } from "@/lib/goUpload";
import { walkComplete } from "../components/go/IncomePage";
import { isLegacyXls } from "../components/go/TollImportDialog";
import { computeTripBilling, startBillingJob } from "./billingWrites.go";
import { expensePatchOf } from "../components/go/ExpenseDetailDialog";
import { auditFilter } from "../components/go/ExpenseAuditPage";
import { effectiveStatus, statementStats } from "../components/go/BillingResultPage";
import { fuelAdjustmentInput } from "../components/go/rateCard/FuelAdjustmentsTab";
import { parseRateSheet } from "../components/go/rateCard/RateImportDialog";
import {
    attachExpenseFiles,
    bulkExpenseStatus,
    BulkStatusError,
    createExpense,
    EXPENSES_BULK_MAX,
    expensesQuery,
    fuelEfficiency,
    importTolls,
    normalizeExpenseFilter,
    setExpenseStatus,
    statusCountsOf,
    type ExpenseDTO,
} from "./expenses.go";
import { fuelHistoryTable, matchesFuelType, type FuelSnapshotDTO } from "./fuel.go";
import { incomeRows, incomeStandbyQuery, incomeSummary, incomeTripsQuery, standbyIncomeRow, tripIncomeRow, unpricedIncomeRows, type IncomeTripDTO } from "./income.go";
import { filterRateEntries, rateEntriesQuery, voidRateEntry, type RateEntryDTO } from "./rateCard.go";
import { rateCardChangedKeys } from "./rateCardKeys";

afterEach(() => {
    vi.unstubAllGlobals();
    getQueryClient().clear();
});

/** Runs a query's `queryFn` once, as TanStack would for its first page. */
async function runQueryFn(options: { queryKey: readonly unknown[]; queryFn?: unknown }, pageParam?: string) {
    const fn = options.queryFn as (ctx: unknown) => Promise<unknown>;
    return fn({ queryKey: options.queryKey, signal: new AbortController().signal, pageParam, client: getQueryClient(), meta: undefined });
}

function query(url: string): URLSearchParams {
    return new URL(url, "http://web.test").searchParams;
}

describe("rate card: one key per table, a write invalidates only what it touched (AC 1)", () => {
    it("names the customer's key and the table's all view, or the small table's one key", () => {
        expect(rateCardChangedKeys("entries", "c1")).toEqual([queryKeys.rateCard.entries("c1"), queryKeys.rateCard.entries("all")]);
        expect(rateCardChangedKeys("fuelAdj", "c1")).toEqual([queryKeys.rateCard.fuelAdj("c1"), queryKeys.rateCard.fuelAdj("all")]);
        expect(rateCardChangedKeys("serviceFees", "c1")).toEqual([queryKeys.rateCard.serviceFees()]);
        expect(rateCardChangedKeys("standby", null)).toEqual([queryKeys.rateCard.standby()]);
        expect(rateCardChangedKeys("entries", null)).toEqual([["rateCard", "entries"]]);
    });

    it("voiding an entry refetches that customer's entries and nothing else of the rate card", async () => {
        const web = fakeWeb();
        web.on("POST", "/api/go/v1/billing/rate-entries/e1/void", () => json(200, { data: { id: "e1", customerId: "c1", voided: true } }));
        const client = getQueryClient();
        const keys = [
            queryKeys.rateCard.entries("c1"),
            queryKeys.rateCard.entries("all"),
            queryKeys.rateCard.entries("c2"),
            queryKeys.rateCard.fuelAdj("c1"),
            queryKeys.rateCard.serviceFees(),
            queryKeys.rateCard.standby(),
            queryKeys.fuel.monthly(),
            queryKeys.hubs.all(),
            queryKeys.customers.list({ fields: "minimal" }),
        ];
        for (const k of keys) client.setQueryData(k, { cached: true });
        await voidRateEntry({ id: "e1", customerId: "c1" }, "wrong price");
        const stale = keys.filter((k) => client.getQueryState(k)?.isInvalidated).map((k) => JSON.stringify(k));
        expect(stale.sort()).toEqual([JSON.stringify(queryKeys.rateCard.entries("all")), JSON.stringify(queryKeys.rateCard.entries("c1"))].sort());
        expect(web.calls[0].body).toEqual({ reason: "wrong price" });
    });

    it("pages the entries on the server by customer, voided rows included", async () => {
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/billing/rate-entries", () => json(200, { data: [], nextCursor: "n2" }));
        await runQueryFn(rateEntriesQuery("c1"));
        await runQueryFn(rateEntriesQuery("all"), "n2");
        const [a, b] = web.calls.map((c) => query(c.url));
        expect(Object.fromEntries(a)).toEqual({ customerId: "c1", includeVoided: "true", limit: "100" });
        expect(b.get("customerId")).toBeNull();
        expect(b.get("cursor")).toBe("n2");
    });

    it("filters the loaded entries (source, destination, class, search, voided)", () => {
        const e = (over: Partial<RateEntryDTO>): RateEntryDTO =>
            ({ id: "x", hubId: "SPX-A", rawHubName: "SPX-A - Bang Na", destinationCode: "SOCE", vehicleClass: "4WJ", importId: "imp1", voided: false, ...over }) as RateEntryDTO;
        const rows = [e({ id: "1" }), e({ id: "2", voided: true }), e({ id: "3", hubId: "SPK-GW", rawHubName: null }), e({ id: "4", vehicleClass: "6WH" })];
        expect(filterRateEntries(rows, {}).map((r) => r.id)).toEqual(["1", "3", "4"]);
        expect(filterRateEntries(rows, { showVoided: true }).map((r) => r.id)).toEqual(["1", "2", "3", "4"]);
        expect(filterRateEntries(rows, { hubId: "SPK-GW" }).map((r) => r.id)).toEqual(["3"]);
        expect(filterRateEntries(rows, { vehicleClass: "6WH" }).map((r) => r.id)).toEqual(["4"]);
        expect(filterRateEntries(rows, { q: "bang na" }).map((r) => r.id)).toEqual(["1", "4"]);
    });

    it("parses a template sheet: unknown sources and bad numbers are warnings, the rest are rows", () => {
        const sheet = [["header"], ["SPX-A - Bang Na", "", "soce", "1,250.50", "12"], ["NOPE - x", "", "SOCE", 1], ["SPX-A - Bang Na", "", "", 5], ["SPX-A", "", "SOCW", "abc"], []];
        const out = parseRateSheet(sheet, new Set(["SPX-A"]));
        expect(out.rows).toEqual([{ hubId: "SPX-A", rawHubName: "SPX-A - Bang Na", destinationCode: "SOCE", vehicleClass: "4WJ", rateThb: 1250.5, distanceKm: 12 }]);
        expect(out.warnings.map((w) => w.key)).toEqual([
            "accounting.rateCard.import.warningUnknownHub",
            "accounting.rateCard.import.warningMissingDestination",
            "accounting.rateCard.import.warningInvalidRate",
        ]);
    });

    it("builds a fuel announcement per mode, refusing an incomplete one", () => {
        const base = { customerId: "c1", effectiveFrom: "2026-10-01", pct: "5", add: "12", reference: "", baseline: "41", perBaht: "10", note: " n " };
        expect(fuelAdjustmentInput({ ...base, mode: "percent" })).toEqual({
            customerId: "c1",
            effectiveFromDateStr: "2026-10-01",
            mode: "percent",
            pct: 5,
            referenceFuelPriceThbPerLitre: null,
            announcementNote: "n",
        });
        expect(fuelAdjustmentInput({ ...base, mode: "fixed_thb" })?.addThbPerTrip).toBe(12);
        expect(fuelAdjustmentInput({ ...base, mode: "band" })).toBeNull(); // the band needs a reference price
        expect(fuelAdjustmentInput({ ...base, mode: "band", reference: "42.5" })).toMatchObject({ mode: "band", referenceFuelPriceThbPerLitre: 42.5, fuelBandBaselineFuelFloor: 41 });
        expect(fuelAdjustmentInput({ ...base, mode: "percent", customerId: "" })).toBeNull();
    });
});

describe("income: the date range is a server filter (AC 2)", () => {
    it("asks Go for the delivered axis of the range, by billing party, 200 per page", async () => {
        const web = fakeWeb();
        // Mirrors Go's ListTrips: `status` is a created-axis filter, any other axis answers 422
        // (internal/operations/trips.go `not_on_axis`; Appendix B §B.2.12).
        web.on("GET", "/api/go/v1/trips", (c) => {
            const q = Object.fromEntries(query(c.url));
            if (q.status !== undefined && (q.axis ?? "created") !== "created") {
                return goErr(422, "invalid_argument", { fields: [{ field: "status", reason: "not_on_axis" }] });
            }
            return json(200, { data: [] });
        });
        web.on("GET", "/api/go/v1/standby", () => json(200, { data: [] }));
        await runQueryFn(incomeTripsQuery({ from: "2026-01-01", to: "2026-03-31", party: "p1" }));
        await runQueryFn(incomeStandbyQuery({ from: "2026-01-01", to: "2026-03-31", party: "all" }));
        // toEqual: no `status` rides along on the delivered axis.
        expect(Object.fromEntries(query(web.calls[0].url))).toEqual({
            axis: "delivered",
            from: "2026-01-01",
            to: "2026-03-31",
            customerId: "p1",
            limit: "200",
        });
        expect(Object.fromEntries(query(web.calls[1].url))).toEqual({ sort: "ended", from: "2026-01-01", to: "2026-03-31", limit: "200" });
    });

    it("keys each list by its range, party and kind, so a new range is a new request", () => {
        const a = incomeTripsQuery({ from: "2026-01-01", to: "2026-01-31", party: "all" }).queryKey;
        const b = incomeTripsQuery({ from: "2025-01-01", to: "2025-01-31", party: "all" }).queryKey;
        expect(a).toEqual(["income", { from: "2026-01-01", to: "2026-01-31", party: "all", kind: "trips" }]);
        expect(a).not.toEqual(b);
    });

    it("renders stored prices only and sums the priced rows", () => {
        const trip = (over: Partial<IncomeTripDTO>): IncomeTripDTO =>
            ({
                id: "t1",
                tripNo: "TR-1",
                taskId: "k1",
                isMultiDelivery: false,
                jobCategory: "PRIMARY",
                originPlace: { raw: "SPX-A", key: "SPX-A", label: "Bang Na", resolved: true },
                destinationPlace: { raw: "SOCE", key: "SOCE", label: "SOC East", resolved: true },
                deliveredAt: "2026-01-02T03:00:00Z",
                billing: { customerId: "c1", billingPartyId: "p1", estimateThb: 1000, priced: true, isMultiDelivery: false, manualOverride: false },
                ...over,
            }) as IncomeTripDTO;
        const rows = [
            tripIncomeRow(trip({})),
            tripIncomeRow(trip({ id: "t2", isMultiDelivery: true, billing: { customerId: "c1", estimateThb: 500, priced: true, isMultiDelivery: true } as never })),
            tripIncomeRow(trip({ id: "t3", billing: { customerId: "c2", estimateThb: null, priced: false, unpricedReason: "no_rate" } as never })),
            standbyIncomeRow({ id: "s1", endedAt: "2026-01-03T00:00:00Z", customerPartyId: "p2", billing: { estimateThb: 300, partyId: "p2" } } as never, (p) => (p === "p2" ? "c2" : null)),
        ];
        expect(rows.map((r) => r.recordType)).toEqual(["trip", "multi_drop", "trip", "standby"]);
        expect(rows[0]).toMatchObject({ origin: "Bang Na", destination: "SOC East", estimateThb: 1000, customerId: "c1" });
        expect(rows[2].unpricedReason).toBe("no_rate");
        const s = incomeSummary(rows);
        expect(s).toMatchObject({ total: 1800, count: 3, average: 600 });
        expect(s.byCustomer).toEqual([
            { customerId: "c1", count: 2, total: 1500 },
            { customerId: "c2", count: 1, total: 300 },
        ]);
    });
});

describe("income: the export and the missing-billing fallback", () => {
    it("writes only a range walked to its end without an error", () => {
        const done = { status: "success", isError: false, hasNextPage: false };
        expect(walkComplete(done, true)).toBe(true);
        expect(walkComplete({ ...done, hasNextPage: true }, true)).toBe(false); // the walk's bound
        expect(walkComplete({ status: "error", isError: true, hasNextPage: true }, true)).toBe(false); // a page failed
        expect(walkComplete({ status: "pending", isError: false, hasNextPage: false }, true)).toBe(false);
        expect(walkComplete({ status: "pending", isError: false, hasNextPage: false }, false)).toBe(true); // not wanted
    });

    it("lists the loaded rows without a price for a user who cannot read the billing rows", () => {
        const rows = [{ id: "a", estimateThb: 100 }, { id: "b", estimateThb: null, unpricedReason: "no_rate" }] as never[];
        expect(unpricedIncomeRows(rows).map((r: { id: string }) => r.id)).toEqual(["b"]);
    });
});

describe("income rows", () => {
    it("merges the loaded trips and standby newest first and narrows to one record type", () => {
        const trips = [{ data: [{ id: "t1", tripNo: "A", deliveredAt: "2026-01-01T00:00:00Z", isMultiDelivery: false }, { id: "t2", tripNo: "B", deliveredAt: "2026-01-03T00:00:00Z", isMultiDelivery: true }] }] as never;
        const standby = [{ data: [{ id: "s1", endedAt: "2026-01-02T00:00:00Z", customerPartyId: "p1" }] }] as never;
        const all = incomeRows(trips, standby, { trips: true, standby: true, recordType: "all", customerOfParty: () => "c1" });
        expect(all.map((r) => r.id)).toEqual(["t2", "standby_s1", "t1"]);
        expect(incomeRows(trips, standby, { trips: true, standby: true, recordType: "multi_drop", customerOfParty: () => null }).map((r) => r.id)).toEqual(["t2"]);
        expect(incomeRows(trips, standby, { trips: false, standby: true, recordType: "all", customerOfParty: () => "c1" })[0].customerId).toBe("c1");
    });
});

describe("expenses: type and status are server filters (AC 3)", () => {
    it("the audit's status tab and type filter go into the request and the key", async () => {
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/expenses", () => json(200, { data: [], meta: { statusCounts: { pending: 3, approved: 7 } } }));
        const pending = expensesQuery(auditFilter("pending", "all"));
        const tolls = expensesQuery(auditFilter("approved", "other:toll"));
        await runQueryFn(pending);
        await runQueryFn(tolls);
        expect(pending.queryKey).toEqual(["expenses", { status: "pending" }]);
        expect(tolls.queryKey).toEqual(["expenses", { status: "approved", type: "other", category: "toll" }]);
        expect(Object.fromEntries(query(web.calls[0].url))).toEqual({ status: "pending", limit: "100" });
        expect(Object.fromEntries(query(web.calls[1].url))).toEqual({ status: "approved", type: "other", category: "toll", limit: "100" });
        expect(expensesQuery(auditFilter("all", "fuel")).queryKey).toEqual(["expenses", { type: "fuel" }]);
    });

    it("reads the tab counts from the first page's meta and drops empty filters from the key", () => {
        expect(statusCountsOf([{ data: [], meta: { statusCounts: { pending: 2 } } }])).toEqual({ pending: 2 });
        expect(statusCountsOf(undefined)).toEqual({});
        expect(normalizeExpenseFilter({ type: "fuel", driverId: "", truckId: undefined, from: "2026-01-01" })).toEqual({ type: "fuel", from: "2026-01-01" });
    });

    it("an approval invalidates the expense lists and the badges, and the maintenance list when a PM was booked", async () => {
        const web = fakeWeb();
        web.on("POST", "/api/go/v1/expenses/x1/status", () => json(200, { data: { id: "x1", pmCheck: { created: true, maintenanceId: "m1", outcome: "booked" } } }));
        const client = getQueryClient();
        for (const k of [queryKeys.expenses({ status: "pending" }), queryKeys.badges(), queryKeys.maintenance({}), queryKeys.rateCard.standby()]) client.setQueryData(k, 1);
        const out = await setExpenseStatus("x1", "approved", "ok", client);
        expect(out.pmCheck?.created).toBe(true);
        expect(web.calls[0].body).toEqual({ status: "approved", adminNote: "ok" });
        expect(client.getQueryState(queryKeys.expenses({ status: "pending" }))?.isInvalidated).toBe(true);
        expect(client.getQueryState(queryKeys.badges())?.isInvalidated).toBe(true);
        expect(client.getQueryState(queryKeys.maintenance({}))?.isInvalidated).toBe(true);
        expect(client.getQueryState(queryKeys.rateCard.standby())?.isInvalidated).toBe(false);
    });

    it("sends the toll sheet as a form upload with the driver, the truck and dryRun", async () => {
        fakeWeb();
        let form: FormData | undefined;
        let url = "";
        const fetchMock = globalThis.fetch as ReturnType<typeof vi.fn>;
        fetchMock.mockImplementationOnce(async (u: string, init: RequestInit) => {
            url = u;
            form = init.body as FormData;
            return json(200, { data: { created: 2, skipped: 1, skippedRows: [{ row: 4, reason: "not_toll" }], dryRun: true } });
        });
        const out = await importTolls({ file: new File(["x"], "tolls.xlsx"), driverId: "d1", truckId: "tr1", dryRun: true });
        expect(out.created).toBe(2);
        expect(form?.get("driverId")).toBe("d1");
        expect(form?.get("truckId")).toBe("tr1");
        expect(form?.get("dryRun")).toBe("true");
        expect((form?.get("file") as File).name).toBe("tolls.xlsx");
        expect(url).toBe("/api/go/v1/expenses/toll-import");
    });

    it("patches only the changed fields of the type", () => {
        const e = { id: "x", type: "fuel", amountThb: 100, volumeLiters: 10, pricePerLiter: 10, odometerKm: 5, distanceKm: null, stationTaxId: null, taxInvId: null, category: null, description: null, note: null, adminNote: null, truckId: "t1", licensePlate: "70-1" } as unknown as ExpenseDTO;
        const form = { amountThb: "120", volumeLiters: "10", pricePerLiter: "10.456", odometerKm: "", distanceKm: "", stationTaxId: "", taxInvId: "", category: "toll", description: "", note: "", adminNote: "" };
        expect(expensePatchOf(e, form, { id: "t2", label: "70-2" })).toEqual({ amountThb: 120, pricePerLiter: 10.46, odometerKm: null, truckId: "t2" });
    });

    it("chains km/L per truck in odometer order and flags the impossible ones", () => {
        const f = (id: string, truckId: string, odo: number | null, liters: number) => ({ id, truckId, licensePlate: null, odometerKm: odo, volumeLiters: liters, date: `2026-01-0${id}T00:00:00Z` }) as unknown as ExpenseDTO;
        const out = fuelEfficiency([f("3", "A", 1300, 50), f("1", "A", 1000, 40), f("2", "A", 1100, 10), f("4", "B", 500, 10), f("5", "A", null, 10)]);
        expect(out.get("1")).toEqual({ distanceKm: null, kmPerLiter: null, flag: "chain-start" });
        expect(out.get("2")).toEqual({ distanceKm: 100, kmPerLiter: 10, flag: null });
        expect(out.get("3")).toEqual({ distanceKm: 200, kmPerLiter: 4, flag: null });
        expect(out.get("4")?.flag).toBe("chain-start");
        expect(out.get("5")?.flag).toBe("no-odometer");
        expect(fuelEfficiency([f("1", "A", 1000, 1), f("2", "A", 1100, 1)]).get("2")?.flag).toBe("out-of-range");
    });
});

describe("expenses: photos and bulk approval", () => {
    const image = (name: string, type: string, size = 10) => new File([new Uint8Array(size)], name, { type });
    const row = { id: "x1", type: "fuel", status: "approved", date: "2026-10-01T03:00:00Z", amountThb: 3000 } as ExpenseDTO;

    it("refuses a photo Go would refuse before anything is written", async () => {
        const web = fakeWeb();
        await expect(createExpense({ type: "fuel", driverId: "d1", date: "2026-10-01", amountThb: 3000 }, { receipt: image("r.heic", "image/heic") })).rejects.toBeInstanceOf(
            UploadRefusedError
        );
        await expect(
            createExpense({ type: "fuel", driverId: "d1", date: "2026-10-01", amountThb: 3000 }, { odometer: image("o.jpg", "image/jpeg", 10 * 1024 * 1024 + 1) })
        ).rejects.toMatchObject({ reason: "too_large" });
        expect(web.calls).toHaveLength(0);
    });

    it("a photo failing after the row exists hands the row back; the retry attaches to it and never posts again", async () => {
        const web = fakeWeb();
        web.on("POST", "/api/go/v1/expenses", () => json(201, { data: row }));
        let presigns = 0;
        web.on("POST", "/api/go/v1/uploads/presign", () => {
            presigns += 1;
            return presigns === 1 ? goErr(503, "unavailable") : json(200, { data: { key: "expenses/x1/receipt-1.jpg", url: "https://s3.test/put", method: "PUT" } });
        });
        web.on("PUT", "https://s3.test/put", () => new Response(null, { status: 200 }));
        web.on("PATCH", "/api/go/v1/expenses/x1", () => json(200, { data: { ...row, receiptKey: "expenses/x1/receipt-1.jpg" } }));
        const client = getQueryClient();
        client.setQueryData(queryKeys.expenses({}), 1);
        let err: unknown;
        try {
            await createExpense({ type: "fuel", driverId: "d1", date: "2026-10-01", amountThb: 3000 }, { receipt: image("r.jpg", "image/jpeg") }, client);
        } catch (e) {
            err = e;
        }
        expect(err).toBeInstanceOf(PartialCreateError);
        expect((err as PartialCreateError<ExpenseDTO>).entity.id).toBe("x1");
        // The list shows the saved row even though its photo failed.
        expect(client.getQueryState(queryKeys.expenses({}))?.isInvalidated).toBe(true);
        const out = await attachExpenseFiles((err as PartialCreateError<ExpenseDTO>).entity, { receipt: image("r.jpg", "image/jpeg") }, client);
        expect(out.receiptKey).toBe("expenses/x1/receipt-1.jpg");
        expect(web.count("POST", "/api/go/v1/expenses")).toBe(1);
        expect(web.calls.find((c) => c.method === "PATCH")!.body).toEqual({ receiptKey: "expenses/x1/receipt-1.jpg" });
    });

    it("approves any number of rows in requests of at most 500 ids, in order", async () => {
        const web = fakeWeb();
        web.on("POST", "/api/go/v1/expenses/bulk-status", (c) => json(200, { data: (c.body as { ids: string[] }).ids.map((id) => ({ id, pmCheck: null })) }));
        const ids = Array.from({ length: 1200 }, (_, i) => `e${i}`);
        const out = await bulkExpenseStatus(ids, "approved");
        const sizes = web.calls.map((c) => (c.body as { ids: string[] }).ids.length);
        expect(sizes).toEqual([EXPENSES_BULK_MAX, EXPENSES_BULK_MAX, 200]);
        expect(out.map((r) => r.id)).toEqual(ids);
    });

    it("a failed request stops the run and says how many were approved", async () => {
        const web = fakeWeb();
        let n = 0;
        web.on("POST", "/api/go/v1/expenses/bulk-status", (c) => {
            n += 1;
            return n === 2 ? goErr(409, "failed_precondition") : json(200, { data: (c.body as { ids: string[] }).ids.map((id) => ({ id, pmCheck: null })) });
        });
        const client = getQueryClient();
        client.setQueryData(queryKeys.expenses({}), 1);
        const err = await bulkExpenseStatus(Array.from({ length: 1200 }, (_, i) => `e${i}`), "approved", undefined, client).catch((e: unknown) => e);
        expect(err).toBeInstanceOf(BulkStatusError);
        expect((err as BulkStatusError).done).toHaveLength(500);
        expect((err as BulkStatusError).total).toBe(1200);
        expect(web.calls).toHaveLength(2);
        expect(client.getQueryState(queryKeys.expenses({}))?.isInvalidated).toBe(true);
    });

    it("recognises a legacy .xls workbook by name or by its OLE2 header", async () => {
        expect(await isLegacyXls(new File(["x"], "statement.XLS"))).toBe(true);
        expect(await isLegacyXls(new File([new Uint8Array([0xd0, 0xcf, 0x11, 0xe0, 0xa1])], "statement.xlsx"))).toBe(true);
        expect(await isLegacyXls(new File([new Uint8Array([0x50, 0x4b, 0x03, 0x04])], "statement.xlsx"))).toBe(false);
        expect(await isLegacyXls(new File(["a,b\n1,2"], "statement.csv", { type: "text/csv" }))).toBe(false);
    });
});

describe("billing aggregates and statements", () => {
    it("puts every parameter that changes the answer in the rows key and the request", async () => {
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/billing/rows", () => json(200, { data: { rows: [] } }));
        const q = billingRowsQuery("c1", 2026, 9, { ...DEFAULT_ROWS_PARAMS, types: ["standby", "trip"], plate: "70-1" });
        expect(q.queryKey).toEqual([
            "billing",
            "rows",
            "c1",
            2026,
            9,
            { types: ["standby", "trip"], categories: ["PRIMARY", "SUPPLEMENTARY", "unverified"], plate: "70-1", vehicleClass: "", reviewMonth: "" },
        ]);
        await runQueryFn(q);
        expect(Object.fromEntries(query(web.calls[0].url))).toEqual({
            customerId: "c1",
            year: "2026",
            month: "9",
            types: "standby,trip",
            categories: "PRIMARY,SUPPLEMENTARY,unverified",
            plate: "70-1",
        });
    });

    it("never asks Go for an empty type or category list (Go answers 422 required; absent means all)", () => {
        expect(rowsParamsComplete(DEFAULT_ROWS_PARAMS)).toBe(true);
        expect(rowsParamsComplete({ types: [], categories: ["PRIMARY"] })).toBe(false);
        expect(rowsParamsComplete({ types: ["trip"], categories: [] })).toBe(false);
        expect(billingRowsQuery("c1", 2026, 9, { ...DEFAULT_ROWS_PARAMS, types: [] }).enabled).toBe(false);
        expect(billingRowsQuery("c1", 2026, 9, { ...DEFAULT_ROWS_PARAMS, categories: [] }).enabled).toBe(false);
        expect(billingRowsQuery("c1", 2026, 9, { ...DEFAULT_ROWS_PARAMS, types: ["standby"] }).enabled).toBe(true);
    });

    it("a new statement invalidates the statements and the month's billing keys only", async () => {
        const web = fakeWeb();
        web.on("POST", "/api/go/v1/billing/statements", () => json(201, { data: { id: "s1", invoiceNumber: "INV-1" } }));
        const client = getQueryClient();
        const month = queryKeys.billing.rows("c1", 2026, 9, { types: [] });
        const other = queryKeys.billing.rows("c1", 2026, 8, { types: [] });
        for (const k of [month, other, queryKeys.statements.list({}), queryKeys.income({})]) client.setQueryData(k, 1);
        await createStatement({ customerId: "c1", year: 2026, month: 9, types: ["trip"], categories: ["PRIMARY"], showActualPickup: true }, client);
        expect(web.calls[0].body).toEqual({ customerId: "c1", year: 2026, month: 9, types: ["trip"], categories: ["PRIMARY"], showActualPickup: true });
        expect(client.getQueryState(month)?.isInvalidated).toBe(true);
        expect(client.getQueryState(queryKeys.statements.list({}))?.isInvalidated).toBe(true);
        expect(client.getQueryState(other)?.isInvalidated).toBe(false);
        expect(client.getQueryState(queryKeys.income({}))?.isInvalidated).toBe(false);
    });

    it("filters the statements' period on the server", async () => {
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/billing/statements", () => json(200, { data: [] }));
        await runQueryFn(statementsQuery({ customerId: "c1", status: "all", year: 2026, month: 9 }));
        await runQueryFn(statementsQuery({ customerId: "all", status: "paid", year: 0, month: 9 }));
        expect(Object.fromEntries(query(web.calls[0].url))).toEqual({ customerId: "c1", year: "2026", month: "9", limit: "50" });
        expect(Object.fromEntries(query(web.calls[1].url))).toEqual({ status: "paid", limit: "50" });
    });

    it("polls a statement every 5 s while a document is still to come and the stream is closed, for at most 2 min", () => {
        const ready = (kind: string) => ({ kind, status: "ready" });
        const invoiceSet = [ready("bundle_zip"), ready("invoice_summary_pdf"), ready("invoice_detail_xlsx")];
        const interval = (streamOpen: boolean, openedAt: number, documents: { kind?: string; status: string }[], status = "draft") =>
            (statementQuery("s1", streamOpen, openedAt).refetchInterval as (q: unknown) => number | false)({ state: { data: { status, documents } } });
        const now = Date.now();
        expect(interval(false, now, [{ kind: "bundle_zip", status: "rendering" }])).toBe(DOCUMENT_POLL_MS);
        expect(interval(true, now, [{ kind: "bundle_zip", status: "rendering" }])).toBe(false);
        expect(interval(false, now, invoiceSet)).toBe(false);
        expect(interval(false, now - DOCUMENT_POLL_LIMIT_MS - 1, [{ kind: "bundle_zip", status: "pending" }])).toBe(false);
        // Go writes the document rows only when the render job starts: a new statement answers `documents: []`.
        expect(interval(false, now, [])).toBe(DOCUMENT_POLL_MS);
        // Marked paid: the receipt is still to come; a failed kind has its row and is not waited for.
        expect(interval(false, now, invoiceSet, "paid")).toBe(DOCUMENT_POLL_MS);
        expect(interval(false, now, [...invoiceSet, { kind: "receipt_pdf", status: "failed" }], "paid")).toBe(false);
        expect(documentsPending({ status: "cancelled", documents: [] })).toBe(false);
    });

    it("offers only the statement moves Go's state machine allows", () => {
        expect(canMoveStatement("draft", "sent")).toBe(true);
        expect(canMoveStatement("draft", "paid")).toBe(false);
        expect(canMoveStatement("sent", "paid")).toBe(true);
        expect(canMoveStatement("sent", "cancelled")).toBe(true);
        expect(canMoveStatement("paid", "cancelled")).toBe(false);
        expect(canMoveStatement("cancelled", "sent")).toBe(false);
    });

    it("counts the loaded statements without the cancelled ones and marks the overdue", () => {
        const s = (status: string, totalAmount: number, dueDate: string | null = null) => ({ status, totalAmount, dueDate }) as never;
        const now = new Date("2026-10-12T00:00:00Z");
        expect(effectiveStatus(s("sent", 1, "2026-10-01"), now)).toBe("overdue");
        expect(effectiveStatus(s("paid", 1, "2026-10-01"), now)).toBe("paid");
        expect(statementStats([s("draft", 100), s("sent", 50, "2026-10-01"), s("paid", 25), s("cancelled", 999)], now)).toEqual({
            total: 175,
            counted: 3,
            cancelled: 1,
            draft: 1,
            sent: 1,
            paid: 1,
            overdue: 1,
        });
    });

    it("downloads a stored document: a redirect is ready, a 409 is the Go error, a 401 refreshes once", async () => {
        const web = fakeWeb();
        const path = documentPath("s1", "bundle_zip");
        expect(path).toBe("/v1/billing/statements/s1/documents/bundle_zip");
        let n = 0;
        web.on("GET", "/api/go/v1/billing/statements/s1/documents/bundle_zip", () => {
            n += 1;
            return n === 1 ? goErr(401, "token_expired") : new Response(null, { status: 302, headers: { Location: "https://media.test/x" } });
        });
        web.on("GET", "/api/go/v1/me", () => json(200, { data: {} }));
        web.on("POST", "/api/auth/refresh", () => new Response(null, { status: 204 }));
        await expect(probeGoDownload(path)).resolves.toEqual({ kind: "ready", href: "/api/go/v1/billing/statements/s1/documents/bundle_zip" });
        web.on("GET", "/api/go/v1/billing/statements/s2/documents/receipt_pdf", () => goErr(409, "failed_precondition", { status: "rendering" }));
        await expect(probeGoDownload(documentPath("s2", "receipt_pdf"))).rejects.toMatchObject({ status: 409, code: "failed_precondition" });
        web.on("GET", "/api/go/v1/billing/shopee-report.pdf", () => json(202, { data: { jobId: "j1" } }));
        await expect(probeGoDownload("/v1/billing/shopee-report.pdf", { customerId: "c1" })).resolves.toMatchObject({ kind: "accepted", jobId: "j1" });
    });

    it("recomputes through the T38 route and starts billing jobs with only the given fields", async () => {
        const web = fakeWeb();
        web.on("POST", "/api/go/v1/trips/t1/billing/compute", () => json(200, { data: { ok: true, billingEstimateThb: 900 } }));
        web.on("POST", "/api/go/v1/jobs/billing.backfill-trips", () => json(202, { data: { jobId: "j1" } }));
        expect(await computeTripBilling("t1", { forceRecompute: true, month: { customerId: "c1", year: 2026, month: 9 } })).toEqual({ ok: true, billingEstimateThb: 900 });
        expect(web.calls[0].body).toEqual({ forceRecompute: true });
        expect(await startBillingJob("billing.backfill-trips", { fromDateStr: "2026-09-01", toDateStr: "2026-09-30", customerId: undefined, forceRecompute: undefined })).toEqual({ jobId: "j1" });
        expect(web.calls[1].body).toEqual({ fromDateStr: "2026-09-01", toDateStr: "2026-09-30" });
    });
});

describe("fuel price history", () => {
    it("builds one column per fuel name of the type and each day's change from the day before", () => {
        const day = (dayKey: string, prices: Record<string, number>): FuelSnapshotDTO =>
            ({ dayKey, items: Object.entries(prices).map(([nameTh, price]) => ({ nameTh, nameEn: nameTh, price, unit: "L" })) }) as FuelSnapshotDTO;
        const days = [day("2026-10-02", { "ไฮดีเซล S": 31.5, แก๊สโซฮอล์95: 36 }), day("2026-10-01", { "ไฮดีเซล S": 31 })];
        const table = fuelHistoryTable(days, "diesel");
        expect(table.names).toEqual(["ไฮดีเซล S"]);
        expect(table.rows[0].diff("ไฮดีเซล S")).toBe(0.5);
        expect(table.rows[1].diff("ไฮดีเซล S")).toBeNull();
        expect(fuelHistoryTable(days, "all").names).toHaveLength(2);
        expect(matchesFuelType("แก๊สโซฮอล์95", "gasohol")).toBe(true);
    });
});
