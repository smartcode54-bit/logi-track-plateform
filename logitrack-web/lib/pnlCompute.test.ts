import { describe, expect, it } from "vitest";
import { pnlFixedCostInputSchema, type PnlFixedCostVersion, type PnlRateRow } from "@/validate/pnlSettingsSchema";
import { computePnlYear, yearTotal } from "./pnl/year";
import {
    buildDistanceIndex,
    buildPnlScenario,
    computeBreakEven,
    recomputePnlScenario,
    round2,
    computePnl,
    fixedCostMonthlyAmount,
    maintenanceBillDateKey,
    pnlDateKeyForTrip,
    renewalMonthlyAmounts,
    resolveJobDistanceKm,
    selectPnlRate,
    standbyJobDistanceKm,
    truckFuelWindow,
    type PnlDistanceRow,
    type PnlFuelRowInput,
    type PnlInputs,
    type PnlStandbyInput,
    type PnlTripInput,
} from "./pnlCompute";

/** Bangkok wall-clock → instant. */
const bkk = (y: number, m: number, d: number, h = 12, min = 0) => new Date(Date.UTC(y, m - 1, d, h - 7, min));

let seq = 0;
const rate = (kind: PnlRateRow["kind"], amountThb: number, effectiveFrom: string, extra: Partial<PnlRateRow> = {}): PnlRateRow => ({
    id: `r${++seq}`, kind, amountThb, effectiveFrom, voided: false, createdAt: new Date(2026, 0, 1, 0, 0, seq), ...extra,
});
const baseRates = (): PnlRateRow[] => [
    rate("driver_trip", 250, "2026-10-01"),
    rate("helper_day", 400, "2026-10-01"),
    rate("maint_service_km", 0.14, "2026-10-01"),
    rate("maint_tyre_km", 0.3, "2026-10-01"),
];

const distances: PnlDistanceRow[] = [
    { table: "hub_soc", hubId: "h1", socId: "SOCN", distanceKm: 120.5 },
    { table: "soc_hub", hubId: "H1", socId: "SOCN", distanceKm: 118 },
    { table: "hub_soc", hubId: "H2", socId: "SOCE", distanceKm: 125 },
    { table: "soc_hub", hubId: "H2", socId: "SOCE", distanceKm: 125 },
    { table: "hub_soc", hubId: "H1", socId: "S1", distanceKm: 40 },
    { table: "soc_hub", hubId: "H1", socId: "S1", distanceKm: 40 },
    { table: "hub_soc", hubId: "H1", socId: "S2", distanceKm: 70 },
    { table: "soc_hub", hubId: "H1", socId: "S2", distanceKm: 70 },
];
const index = buildDistanceIndex(distances);

const trucks = [
    { id: "T1", plate: "70-1111", ownershipType: "own", truckStatus: "active", insurancePremium: 24000, taxExpense: 3600 },
    { id: "T2", plate: "70-2222", ownershipType: "own", truckStatus: "active" },
    { id: "SUB", plate: "80-9999", ownershipType: "subcontractor", subcontractorId: "p1", truckStatus: "active" },
];

const trip = (id: string, extra: Partial<PnlTripInput> = {}): PnlTripInput => ({
    id, status: "delivered", truckId: "T1", revenueThb: 2000, checkInAt: bkk(2026, 10, 2, 8), deliveredAt: bkk(2026, 10, 2, 14),
    originCode: "H2", destinationCodes: ["SOCE"], isMultiDrop: false, hasTask: true, helperDriverIds: [], ...extra,
});

const fuel = (id: string, date: Date, amount: number, odometer?: number, extra: Partial<PnlFuelRowInput> = {}): PnlFuelRowInput => ({
    id, truckId: "T1", date, amount, odometer, status: "APPROVED", ...extra,
});
const t1Fuel = (): PnlFuelRowInput[] => [
    fuel("f0", bkk(2026, 9, 29), 2500, 100000),
    fuel("f1", bkk(2026, 10, 5), 3000, 100800),
    fuel("f2", bkk(2026, 10, 20), 3600, 101700),
    fuel("f3", bkk(2026, 10, 30), 3400, 102500),
    fuel("f4", bkk(2026, 10, 31), 2000),
];

const inputs = (extra: Partial<PnlInputs> = {}): PnlInputs => ({
    year: 2026, month: 10, now: bkk(2026, 11, 16), trips: [], standbys: [], helperTasks: [], trucks, distances,
    fuelRows: t1Fuel(), otherExpenses: [], maintenance: [], renewals: [], rates: baseRates(), fixedCosts: [],
    driverIdentity: {}, planBasisCustomerIds: [], ...extra,
});

describe("settings — rates and fixed costs (AC1, AC2)", () => {
    it("picks the latest effective rate, honours void and ties", () => {
        const rates = [rate("driver_trip", 250, "2026-10-01"), rate("driver_trip", 270, "2026-10-16")];
        expect(selectPnlRate(rates, "driver_trip", "2026-10-15")?.amountThb).toBe(250);
        expect(selectPnlRate(rates, "driver_trip", "2026-10-16")?.amountThb).toBe(270);
        rates[1].voided = true;
        expect(selectPnlRate(rates, "driver_trip", "2026-10-16")?.amountThb).toBe(250);
        expect(selectPnlRate(rates, "driver_trip", "2026-09-30")).toBeNull();
        const tie = [rate("driver_trip", 250, "2026-10-01"), rate("driver_trip", 260, "2026-10-01")];
        expect(selectPnlRate(tie, "driver_trip", "2026-10-05")?.amountThb).toBe(260);
    });

    it("resolves monthly/yearly, versions, stop and end month", () => {
        const v = (extra: Partial<PnlFixedCostVersion>): PnlFixedCostVersion => ({
            id: `v${++seq}`, itemId: "gps", name: "GPS", category: "gps", scope: "company", frequency: "yearly",
            amountThb: 2400, effectiveFromMonth: "2026-01", voided: false, createdAt: new Date(2026, 0, 1, 0, 0, seq), ...extra,
        });
        const gps = [v({})];
        expect(fixedCostMonthlyAmount(gps, "2026-10")).toBe(200);
        gps.push(v({ amountThb: 3600, effectiveFromMonth: "2026-07" }));
        expect(fixedCostMonthlyAmount(gps, "2026-06")).toBe(200);
        expect(fixedCostMonthlyAmount(gps, "2026-10")).toBe(300);
        gps.push(v({ isStop: true, effectiveFromMonth: "2026-11" }));
        expect(fixedCostMonthlyAmount(gps, "2026-11")).toBe(0);
        const installment = [v({ itemId: "inst", category: "installment", frequency: "monthly", amountThb: 20000, effectiveFromMonth: "2025-06", effectiveToMonth: "2028-05" })];
        expect(fixedCostMonthlyAmount(installment, "2028-05")).toBe(20000);
        expect(fixedCostMonthlyAmount(installment, "2028-06")).toBe(0);
        expect(fixedCostMonthlyAmount(installment, "2025-05")).toBe(0);
    });
});

describe("renewals (AC10)", () => {
    it("spreads to the next renewal or 12 months", () => {
        const normal = renewalMonthlyAmounts([
            { id: "a", truckId: "T1", type: "insurance", amount: 24000, date: "2025-11-10" },
            { id: "b", truckId: "T1", type: "insurance", amount: 25000, date: "2026-11-05" },
        ], "T1", "insurance");
        expect(normal.byMonth.get("2026-10")).toBe(2000);
        expect(normal.byMonth.get("2026-11")).toBeCloseTo(25000 / 12);
        const early = renewalMonthlyAmounts([
            { id: "a", truckId: "T1", type: "insurance", amount: 24000, date: "2025-11-10" },
            { id: "b", truckId: "T1", type: "insurance", amount: 24000, date: "2026-09-01" },
        ], "T1", "insurance");
        expect(early.byMonth.get("2025-11")).toBe(2400);
        expect(early.byMonth.get("2026-08")).toBe(2400);
        expect(early.byMonth.get("2026-09")).toBe(2000);
    });

    it("falls back to the truck doc with a warning when no renewal covers the month", () => {
        const r = computePnl(inputs({ trips: [trip("a")] }));
        const t1 = r.trucks.find((t) => t.truckId === "T1")!;
        expect(t1.vehicleTaxThb).toBe(300);
        expect(t1.insuranceThb).toBe(2000);
        expect(r.warnings.find((w) => w.code === "noRenewalHistory")?.count).toBe(4); // T1 + T2, insurance + tax
    });
});

describe("month membership (AC4)", () => {
    it("uses check-in, falls back to delivery, ignores trip_records standby", () => {
        expect(pnlDateKeyForTrip(bkk(2026, 10, 31, 23, 30), bkk(2026, 11, 1, 2))).toBe("2026-10-31");
        const standby: PnlStandbyInput = {
            id: "s1", status: "completed", truckId: "T1", revenueThb: 500, startedAt: bkk(2026, 10, 31, 20),
            endedAt: bkk(2026, 11, 1, 1), taskRoute: { originCode: "H2", destinationCodes: ["SOCE"] }, helperDriverIds: [],
        };
        const r = computePnl(inputs({
            trips: [
                trip("late", { checkInAt: bkk(2026, 10, 31, 23, 30), deliveredAt: bkk(2026, 11, 1, 2) }),
                trip("noTask", { hasTask: false, checkInAt: undefined, deliveredAt: bkk(2026, 10, 15) }),
                trip("sb", { status: "standby" }),
                trip("nov", { checkInAt: bkk(2026, 11, 1, 1) }),
            ],
            standbys: [standby],
        }));
        expect(r.trips.map((t) => t.id).sort()).toEqual(["late", "noTask", "s1"]);
        expect(r.trips.find((t) => t.id === "noTask")?.notes).toContain("noTask");
    });
});

describe("job distance (AC5, AC5b)", () => {
    it("round trip, case-insensitive hub ids, missing legs, farthest stop", () => {
        expect(resolveJobDistanceKm("H1", ["SOCN"], index)).toMatchObject({ km: 238.5, oneWayKm: 120.5 });
        const noBack = buildDistanceIndex(distances.filter((d) => !(d.table === "soc_hub" && d.socId === "SOCN")));
        expect(resolveJobDistanceKm("H1", ["SOCN"], noBack)).toEqual({ missingLeg: ["SOCN", "H1"] });
        expect(resolveJobDistanceKm("H1", ["S1", "S2"], index)).toMatchObject({ km: 140, oneWayKm: 70, farthest: "S2" });
        const noS2Back = buildDistanceIndex(distances.filter((d) => !(d.table === "soc_hub" && d.socId === "S2")));
        expect(resolveJobDistanceKm("H1", ["S1", "S2"], noS2Back)).toEqual({ missingLeg: ["S2", "H1"] });
    });

    it("standby = previous trip on the same truck × 2, else its task route", () => {
        const prev = trip("prev", { originCode: "H1", destinationCodes: ["SOCN"], checkInAt: bkk(2026, 10, 10, 8) });
        const otherTruck = trip("other", { truckId: "T2", checkInAt: bkk(2026, 10, 10, 12) });
        const s: PnlStandbyInput = {
            id: "s", status: "completed", truckId: "T1", startedAt: bkk(2026, 10, 10, 18),
            taskRoute: { originCode: "H2", destinationCodes: ["SOCE"] }, helperDriverIds: [],
        };
        expect(standbyJobDistanceKm(s, [prev, otherTruck], index)).toMatchObject({ km: 241, source: "previousTrip" });
        const old = { ...prev, checkInAt: bkk(2026, 10, 6, 8) };
        expect(standbyJobDistanceKm(s, [old], index)).toMatchObject({ km: 250, source: "standbyTask" });
        expect(standbyJobDistanceKm({ ...s, taskRoute: null }, [], index)).toHaveProperty("missingLeg");
    });
});

describe("fuel ฿/km fill-to-fill (AC6)", () => {
    it("anchors before the month and counts fuel up to the last reading", () => {
        const w = truckFuelWindow(t1Fuel(), bkk(2026, 10, 1, 0), bkk(2026, 11, 1, 0));
        expect(w).toMatchObject({ usable: true, odoKm: 2500, spendThb: 10000, rateThbPerKm: 4, noAnchorBeforeMonth: false });
    });

    it("drops rollbacks and rejects implausible km/L", () => {
        const rows = [...t1Fuel(), fuel("bad", bkk(2026, 10, 25), 100, 90000)];
        expect(truckFuelWindow(rows, bkk(2026, 10, 1, 0), bkk(2026, 11, 1, 0)).rollbackIds).toEqual(["bad"]);
        const thirsty = [fuel("a", bkk(2026, 9, 30), 0, 1000, { volumeLiters: 10 }), fuel("b", bkk(2026, 10, 10), 30000, 1100, { volumeLiters: 500 })];
        expect(truckFuelWindow(thirsty, bkk(2026, 10, 1, 0), bkk(2026, 11, 1, 0))).toMatchObject({ usable: false, reason: "kmplOutOfRange" });
    });

    it("uses the fleet average for a truck with one reading, and warns on pending fuel", () => {
        const r = computePnl(inputs({
            trips: [trip("t2", { truckId: "T2" })],
            fuelRows: [
                ...t1Fuel(),
                fuel("x", bkk(2026, 10, 12), 3000, 50000, { truckId: "T2" }),
                fuel("p", bkk(2026, 10, 13), 1500, undefined, { status: "PENDING" }),
            ],
        }));
        const t2 = r.trips.find((t) => t.id === "t2")!;
        expect(t2.fuelRateThbPerKm).toBe(4);
        expect(t2.notes).toContain("fleetAvgFuelRate");
        expect(r.warnings.find((w) => w.code === "fleetAvgFuelRate")?.refs).toEqual(["70-2222"]);
        expect(r.warnings.find((w) => w.code === "pendingFuel")).toMatchObject({ count: 1, amountThb: 1500 });
    });

    it("excludes every trip when no truck has a usable window (F6)", () => {
        const r = computePnl(inputs({ trips: [trip("a")], fuelRows: [] }));
        expect(r.trips[0].reasons).toContain("noFuelRate");
    });
});

describe("contribution and the month statement (AC7, AC9)", () => {
    const eightTrips = () => Array.from({ length: 8 }, (_, i) => trip(`t${i}`, { checkInAt: bkk(2026, 10, 2 + i, 8), deliveredAt: bkk(2026, 10, 2 + i, 14) }));

    it("prices a trip and ties fuel to cash paid", () => {
        const r = computePnl(inputs({ trips: eightTrips() }));
        expect(r.trips[0]).toMatchObject({ jobKm: 250, fuelThb: 1000, driverPayThb: 250, helperShareThb: 0, maintAccrualThb: 110, contributionThb: 640, counted: true });
        expect(r.statement).toMatchObject({ tripFuelThb: 8000, offPlanFuelThb: 2000, fuelCashThb: 12000, fuelReconciliationThb: 2000, contributionThb: 5120 });
        const t1 = r.trucks.find((t) => t.truckId === "T1")!;
        expect(t1).toMatchObject({ odoKm: 2500, jobKm: 2000, offPlanKm: 500, offPlanPct: 25 });
    });

    it("dates bills by completion and reports the variance", () => {
        const m = (id: string, extra: object) => ({ id, truckId: "T1", type: "CM", status: "completed", amountThb: 0, ...extra });
        const r = computePnl(inputs({
            trips: eightTrips(),
            maintenance: [
                m("pm", { type: "PM", endDate: "2026-10-28", amountThb: 6000 }),
                m("cm", { checkOutAt: bkk(2026, 10, 3), startDate: "2026-09-29", amountThb: 3000 }),
                m("claim", { endDate: "2026-10-10", amountThb: 5000, paymentMethod: "insurance_claim" }),
                m("open", { status: "In-Progress", driverSubmitted: true, checkOutAt: bkk(2026, 10, 20), amountThb: 2500 }),
                m("sept", { endDate: "2026-09-30", amountThb: 9999 }),
            ],
        }));
        expect(r.statement).toMatchObject({ maintActualThb: 9000, maintAccrualThb: 880, maintVarianceThb: 8120, insuranceClaimInfoThb: 5000 });
        expect(r.warnings.find((w) => w.code === "maintenanceNotClosed")).toMatchObject({ count: 1, amountThb: 2500 });
        expect(r.warnings.find((w) => w.code === "maintenanceDateEstimated")?.count).toBe(1);
        expect(maintenanceBillDateKey({ id: "x", type: "PM", status: "completed", amountThb: 1, startDate: "2026-10-02" })).toEqual({ dateKey: "2026-10-02", estimated: true });
    });

    it("fails loud on unpriced, missing truck, missing distance and missing rates", () => {
        const r = computePnl(inputs({
            trips: [
                trip("unpriced", { revenueThb: undefined }),
                trip("noTruck", { truckId: undefined }),
                trip("noDist", { destinationCodes: ["NOWHERE"] }),
                trip("sept", { checkInAt: bkk(2026, 10, 1, 8) }),
            ],
            rates: [rate("driver_trip", 250, "2026-10-02"), rate("helper_day", 400, "2026-10-01"), rate("maint_service_km", 0.14, "2026-10-01"), rate("maint_tyre_km", 0.3, "2026-10-01")],
        }));
        const reasons = Object.fromEntries(r.trips.map((t) => [t.id, t.reasons]));
        expect(reasons.unpriced).toContain("unpriced");
        expect(reasons.noTruck).toContain("noTruck");
        expect(reasons.noDist).toContain("noJobDistance");
        expect(reasons.sept).toContain("missingRate");
        expect(r.statement.countedTrips).toBe(0);
        expect(r.statement.excludedCount).toBe(4);
        expect(r.statement.excludedRevenueThb).toBe(6000);
        expect(r.trips.find((t) => t.id === "noDist")?.missingLeg).toEqual(["H2", "NOWHERE"]);
    });

    it("keeps subcontractor trucks out of the P&L", () => {
        const r = computePnl(inputs({ trips: [trip("own"), trip("sub", { truckId: "SUB", revenueThb: 3000 })] }));
        expect(r.trips.map((t) => t.id)).toEqual(["own"]);
        expect(r.subcontractor).toEqual({ trips: 1, revenueThb: 3000 });
    });
});

describe("helper shares (AC8)", () => {
    it("splits a helper-day over the window's trips and standbys, skips own-driving windows", () => {
        const h = "helperUid";
        const r = computePnl(inputs({
            trips: [
                trip("a", { helperDriverIds: [h], checkInAt: bkk(2026, 10, 5, 13) }),
                trip("b", { helperDriverIds: [h], checkInAt: bkk(2026, 10, 6, 2) }), // same window (keyed 10-05)
                trip("c", { helperDriverIds: [h], checkInAt: bkk(2026, 10, 8, 13) }),
                trip("own", { truckId: "T2", driverId: "helperDoc", checkInAt: bkk(2026, 10, 8, 14), deliveredAt: bkk(2026, 10, 8, 18) }),
                trip("d", { helperDriverIds: [h], checkInAt: bkk(2026, 10, 12, 13) }),
            ],
            standbys: [{
                id: "s", status: "completed", truckId: "T1", revenueThb: 600, startedAt: bkk(2026, 10, 12, 18),
                taskCheckInAt: bkk(2026, 10, 12, 16), taskRoute: { originCode: "H2", destinationCodes: ["SOCE"] }, helperDriverIds: [h],
            }],
            helperTasks: [{ id: "cancelled", helperDriverIds: [h], checkInAt: bkk(2026, 10, 20, 13) }],
            driverIdentity: { [h]: [h, "helperDoc"], helperDoc: [h, "helperDoc"] },
        }));
        const share = Object.fromEntries(r.trips.map((t) => [t.id, t.helperShareThb]));
        expect(share).toMatchObject({ a: 200, b: 200, c: 0, d: 200, s: 200 });
        expect(r.statement.helperNoTripThb).toBe(400);
    });
});

describe("break-even (AC11)", () => {
    it("rounds up, adds month lines, and is unreachable when average ≤ 0", () => {
        expect(computeBreakEven({ contributionThb: 640 * 100, countedUnits: 100, fixedCostsThb: 100000, monthLinesThb: 12500 }))
            .toMatchObject({ primaryTrips: 157, withMonthLinesTrips: 176, avgContributionThb: 640 });
        expect(computeBreakEven({ contributionThb: 64000, countedUnits: 100, fixedCostsThb: 112000, monthLinesThb: 0 }).primaryTrips).toBe(175);
        expect(computeBreakEven({ contributionThb: -10, countedUnits: 5, fixedCostsThb: 100, monthLinesThb: 0 }).primaryTrips).toBeNull();
    });
});

describe("what-if scenario for the HTML export (AC17)", () => {
    const h = "helperUid";
    const rich = () =>
        inputs({
            trips: [
                ...Array.from({ length: 8 }, (_, i) => trip(`t${i}`, { checkInAt: bkk(2026, 10, 2 + i, 8), deliveredAt: bkk(2026, 10, 2 + i, 14) })),
                trip("ha", { helperDriverIds: [h], checkInAt: bkk(2026, 10, 20, 13), customerId: "c2", destinationCodes: ["SOCE"] }),
                trip("hb", { helperDriverIds: [h], checkInAt: bkk(2026, 10, 21, 2), customerId: "c2", jobCategory: "SUPPLEMENTARY" }),
                trip("x", { revenueThb: undefined }),
            ],
            helperTasks: [{ id: "cancelled", helperDriverIds: [h], checkInAt: bkk(2026, 10, 25, 13) }],
            maintenance: [{ id: "pm", truckId: "T1", type: "PM", status: "completed", endDate: "2026-10-28", amountThb: 6000 }],
            fixedCosts: [{
                id: "f1", itemId: "inst", name: "Installment", category: "installment", scope: "truck", truckId: "T1",
                frequency: "monthly", amountThb: 20000, effectiveFromMonth: "2025-01", voided: false,
            }],
        });

    it("reproduces computePnl exactly when no rate is changed", () => {
        const result = computePnl(rich());
        const scenario = buildPnlScenario(result, baseRates());
        const r = recomputePnlScenario(scenario, {});
        const s = result.statement;
        expect(r.statement).toMatchObject({
            revenue: s.revenueThb, driverPay: s.driverPayThb, helperShare: s.helperShareThb, maintAccrual: s.maintAccrualThb,
            contribution: s.contributionThb, helperNoTrip: s.helperNoTripThb, maintVariance: s.maintVarianceThb,
            monthLines: s.monthLinesThb, afterMonthLines: s.afterMonthLinesThb, fixedCosts: s.fixedCostsThb, operatingProfit: s.operatingProfitThb,
        });
        expect(r.breakEven).toEqual({
            primaryTrips: result.breakEven.primaryTrips,
            withMonthLinesTrips: result.breakEven.withMonthLinesTrips,
            actualTrips: result.breakEven.actualTrips,
            avgContribution: result.breakEven.avgContributionThb,
        });
        expect(r.rows.map((x) => x.contribution)).toEqual(result.trips.map((x) => x.contributionThb));
        expect(r.lanes.map((l) => [l.key, l.avgContribution, l.trips])).toEqual(result.lanes.map((l) => [l.key, l.avgContributionThb, l.trips]));
        expect(r.trucks.map((t) => t.afterFixed)).toEqual(result.trucks.map((t) => t.afterFixedThb));
        expect(scenario.baseline).toEqual({ driverTrip: 250, helperDay: 400, maintServiceKm: 0.14, maintTyreKm: 0.3 });
    });

    it("applies overridden rates to every counted row and the month lines", () => {
        const result = computePnl(rich());
        const scenario = buildPnlScenario(result, baseRates());
        const t0 = scenario.rows.findIndex((x) => x.id === "trip:t0");
        const ha = scenario.rows.findIndex((x) => x.id === "trip:ha");
        const excluded = scenario.rows.findIndex((x) => x.id === "trip:x");

        const driver = recomputePnlScenario(scenario, { driverTrip: 300 });
        expect(driver.rows[t0].contribution).toBe(590);
        expect(driver.statement.contribution).toBe(round2(result.statement.contributionThb - 50 * 10));
        expect(driver.rows[excluded].contribution).toBeNull();

        const helper = recomputePnlScenario(scenario, { helperDay: 500 });
        expect(helper.rows[ha].helperShare).toBe(250);
        expect(helper.statement.helperNoTrip).toBe(500);

        const tyre = recomputePnlScenario(scenario, { maintTyreKm: 0.4 });
        expect(tyre.rows[t0].maintAccrual).toBe(135);
        expect(tyre.statement.maintVariance).toBe(round2(6000 - 135 * 10));
    });

    it("still runs when re-created from its source text (as the HTML file embeds it)", () => {
        const result = computePnl(rich());
        const scenario = buildPnlScenario(result, baseRates());
        const rebuilt = new Function(`return (${recomputePnlScenario.toString()})`)() as typeof recomputePnlScenario;
        expect(rebuilt(scenario, { driverTrip: 300, helperDay: 500 })).toEqual(recomputePnlScenario(scenario, { driverTrip: 300, helperDay: 500 }));
    });

    it("flags a rate that changed during the month", () => {
        const result = computePnl(rich());
        const scenario = buildPnlScenario(result, [...baseRates(), rate("driver_trip", 270, "2026-10-16")]);
        expect(scenario.mixed.driverTrip).toBe(true);
        expect(scenario.mixed.helperDay).toBe(false);
        expect(scenario.baseline.driverTrip).toBe(270);
    });
});

describe("fixed-cost categories: พรบ., ภาษีรถ, owner-named (owner 2026-11-16)", () => {
    const item = (extra: Partial<PnlFixedCostVersion>): PnlFixedCostVersion => ({
        id: `fc${++seq}`, itemId: `i${seq}`, name: "x", category: "other", scope: "truck", truckId: "T1",
        frequency: "yearly", amountThb: 1200, effectiveFromMonth: "2026-01", voided: false, ...extra,
    });

    it("a ภาษีรถ item replaces the renewal-history tax of that truck — never both", () => {
        const renewals = [{ id: "tx", truckId: "T1", type: "tax" as const, amount: 3600, date: "2026-03-01" }];
        const withoutItem = computePnl(inputs({ trips: [trip("a")], renewals }));
        expect(withoutItem.trucks.find((t) => t.truckId === "T1")?.vehicleTaxThb).toBe(300);

        const r = computePnl(inputs({ trips: [trip("a")], renewals, fixedCosts: [item({ category: "vehicle_tax", amountThb: 4800 })] }));
        const t1 = r.trucks.find((t) => t.truckId === "T1")!;
        expect(t1.vehicleTaxThb).toBe(400);
        expect(t1.fixedItemsThb).toBe(0); // not counted a second time as a fixed item
        expect(r.statement.fixedItemLines.some((l) => l.category === "vehicle_tax")).toBe(false);
        expect(r.warnings.find((w) => w.code === "noRenewalHistory")?.refs ?? []).not.toContain("70-1111 tax");
    });

    it("พรบ. gets its own line; owner-named categories group by name, case-insensitively", () => {
        const r = computePnl(inputs({
            trips: [trip("a")],
            fixedCosts: [
                item({ category: "cmi", amountThb: 2400 }),
                item({ category: "custom", categoryLabel: "Parking", scope: "company", truckId: undefined, frequency: "monthly", amountThb: 1000 }),
                item({ category: "custom", categoryLabel: " parking ", scope: "company", truckId: undefined, frequency: "monthly", amountThb: 500 }),
                item({ category: "custom", categoryLabel: "Phone", scope: "company", truckId: undefined, frequency: "monthly", amountThb: 300 }),
            ],
        }));
        const lines = Object.fromEntries(r.statement.fixedItemLines.map((l) => [l.key, l.amountThb]));
        expect(lines).toEqual({ cmi: 200, "custom:parking": 1500, "custom:phone": 300 });
        expect(r.statement.fixedItemsThb).toBe(2000);
        expect(r.trucks.find((t) => t.truckId === "T1")?.fixedItemsThb).toBe(200);
    });

    it("validates the new categories", () => {
        const base = { name: "x", scope: "truck", truckId: "T1", frequency: "yearly", amountThb: 1, effectiveFromMonth: "2026-01" } as const;
        expect(pnlFixedCostInputSchema.safeParse({ ...base, category: "cmi", scope: "company", truckId: undefined }).success).toBe(false);
        expect(pnlFixedCostInputSchema.safeParse({ ...base, category: "custom" }).success).toBe(false);
        expect(pnlFixedCostInputSchema.safeParse({ ...base, category: "custom", categoryLabel: "Parking" }).success).toBe(true);
    });
});

describe("year view (owner 2026-11-16)", () => {
    it("computes each month exactly like the month view, from one year-wide load", () => {
        const yearInputs = inputs({
            trips: [
                trip("sep", { checkInAt: bkk(2026, 9, 10, 8), deliveredAt: bkk(2026, 9, 10, 14) }),
                trip("oct", { checkInAt: bkk(2026, 10, 10, 8), deliveredAt: bkk(2026, 10, 10, 14) }),
            ],
            fuelRows: [...t1Fuel(), fuel("novBad", bkk(2026, 11, 5), 100, 90000)],
        });
        const y = computePnlYear(yearInputs, 10);
        expect(y.months.map((m) => m.monthKey)).toEqual(Array.from({ length: 10 }, (_, i) => `2026-${String(i + 1).padStart(2, "0")}`));
        const oct = computePnl({ ...yearInputs, fuelRows: t1Fuel() });
        expect(y.months[9].statement).toEqual(oct.statement);
        // A November rollback must not warn in October.
        expect(y.months[9].warnings.find((w) => w.code === "odometerRollback")).toBeUndefined();
        expect(yearTotal(y.months, (m) => m.statement.countedTrips)).toBe(1); // Sep trip has no rate (rates start Oct)
    });
});

describe("flags", () => {
    it("marks a running month and plan-basis customers", () => {
        const r = computePnl(inputs({ now: bkk(2026, 10, 20), trips: [trip("a", { customerId: "cjsf" })], planBasisCustomerIds: ["cjsf"] }));
        expect(r.flags).toEqual({ monthInProgress: true, hasPlanBasisCustomer: true });
    });
});
