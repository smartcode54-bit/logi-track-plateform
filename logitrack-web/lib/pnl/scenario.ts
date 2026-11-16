/**
 * What-if scenario for the HTML export (spec R21, owner 2026-11-16).
 *
 * The export freezes a computed month and lets the reader change ONLY the four P&L rates. Every
 * figure a rate does not touch (revenue, fuel, fixed costs, which rows are excluded) is carried as
 * computed; the figures a rate does touch are rebuilt by `recomputePnlScenario`.
 *
 * `recomputePnlScenario` is embedded in the exported file through `Function.prototype.toString()`,
 * so it MUST stay self-contained: no imports, no module-level helpers, no closures — and no object
 * or array spread / for…of, which an older bundler target could turn into injected helpers. The test
 * suite runs it both directly and re-created from its source text, and checks that with no override
 * it reproduces `computePnl` exactly.
 */
import type { PnlRateKind, PnlRateRow } from "@/validate/pnlSettingsSchema";
import { selectPnlRate } from "./settings";
import type { PnlResult } from "./types";

export interface PnlScenarioRates {
    driverTrip: number | null;
    helperDay: number | null;
    maintServiceKm: number | null;
    maintTyreKm: number | null;
}
export type PnlScenarioOverrides = Partial<PnlScenarioRates>;

export interface PnlScenarioRow {
    id: string;
    kind: "trip" | "standby";
    truckId?: string;
    laneKey: string;
    counted: boolean;
    supplementary: boolean;
    jobKm: number | null;
    revenue: number | null;
    fuel: number | null;
    driverPay: number | null;
    helperShare: number | null;
    maintAccrual: number | null;
    contribution: number | null;
    margin: number | null;
    svcRate: number | null;
    tyreRate: number | null;
    helperDayFraction: number;
}

export interface PnlScenarioTruck {
    truckId: string;
    insurance: number;
    tax: number;
    fixedItems: number;
}

/** Month figures no rate touches. */
export interface PnlScenarioFixed {
    revenue: number;
    tripFuel: number;
    offPlanFuel: number;
    fuelReconciliation: number;
    helperNoTrip: number;
    helperNoTripDays: number;
    toll: number;
    otherVehicle: number;
    maintActual: number;
    insurance: number;
    vehicleTax: number;
    fixedItems: number;
    fixedCosts: number;
}

export interface PnlScenario {
    monthKey: string;
    /** Rate in force at the end of the month — prefilled in the file's inputs. */
    baseline: PnlScenarioRates;
    /** The rate changed during the month: until edited, each trip keeps its own rate. */
    mixed: Record<keyof PnlScenarioRates, boolean>;
    rows: PnlScenarioRow[];
    trucks: PnlScenarioTruck[];
    laneKeys: string[];
    fixed: PnlScenarioFixed;
}

export interface PnlScenarioRowResult {
    driverPay: number | null;
    helperShare: number | null;
    maintAccrual: number | null;
    contribution: number | null;
    margin: number | null;
}

export interface PnlScenarioLaneResult {
    key: string;
    trips: number;
    primaryTrips: number;
    supplementaryTrips: number;
    jobKmPerTrip: number;
    revenue: number;
    contribution: number;
    avgRevenue: number;
    avgContribution: number;
    margin: number | null;
}

export interface PnlScenarioResult {
    /** Same order as `scenario.rows`. */
    rows: PnlScenarioRowResult[];
    /** Sorted by average contribution, highest first. */
    lanes: PnlScenarioLaneResult[];
    /** Same order as `scenario.trucks`. */
    trucks: Array<{ truckId: string; contribution: number; maintAccrual: number; afterFixed: number }>;
    statement: {
        revenue: number;
        tripFuel: number;
        driverPay: number;
        helperShare: number;
        maintAccrual: number;
        contribution: number;
        offPlanFuel: number;
        fuelReconciliation: number;
        helperNoTrip: number;
        toll: number;
        otherVehicle: number;
        maintVariance: number;
        monthLines: number;
        afterMonthLines: number;
        insurance: number;
        vehicleTax: number;
        fixedItems: number;
        fixedCosts: number;
        operatingProfit: number;
    };
    breakEven: { primaryTrips: number | null; withMonthLinesTrips: number | null; actualTrips: number; avgContribution: number | null };
}

const KIND_BY_KEY: Record<keyof PnlScenarioRates, PnlRateKind> = {
    driverTrip: "driver_trip",
    helperDay: "helper_day",
    maintServiceKm: "maint_service_km",
    maintTyreKm: "maint_tyre_km",
};

/** Freeze a computed month into the data the what-if file needs. */
export function buildPnlScenario(result: PnlResult, rates: PnlRateRow[]): PnlScenario {
    const first = `${result.monthKey}-01`;
    const last = `${result.monthKey}-31`;
    const baseline = {} as PnlScenarioRates;
    const mixed = {} as Record<keyof PnlScenarioRates, boolean>;
    for (const key of Object.keys(KIND_BY_KEY) as Array<keyof PnlScenarioRates>) {
        const kind = KIND_BY_KEY[key];
        baseline[key] = selectPnlRate(rates, kind, last)?.amountThb ?? null;
        mixed[key] =
            selectPnlRate(rates, kind, first) !== null &&
            rates.some((r) => !r.voided && r.kind === kind && r.effectiveFrom > first && r.effectiveFrom <= last);
    }
    const s = result.statement;
    return {
        monthKey: result.monthKey,
        baseline,
        mixed,
        rows: result.trips.map((r) => ({
            id: `${r.kind}:${r.id}`,
            kind: r.kind,
            truckId: r.truckId,
            laneKey: r.laneKey,
            counted: r.counted,
            supplementary: r.jobCategory === "SUPPLEMENTARY",
            jobKm: r.jobKm,
            revenue: r.revenueThb,
            fuel: r.fuelThb,
            driverPay: r.driverPayThb,
            helperShare: r.helperShareThb,
            maintAccrual: r.maintAccrualThb,
            contribution: r.contributionThb,
            margin: r.marginPct,
            svcRate: r.maintServiceRateThbPerKm,
            tyreRate: r.maintTyreRateThbPerKm,
            helperDayFraction: r.helperDayFraction,
        })),
        trucks: result.trucks.map((t) => ({
            truckId: t.truckId,
            insurance: t.insuranceThb,
            tax: t.vehicleTaxThb,
            fixedItems: t.fixedItemsThb,
        })),
        laneKeys: result.lanes.map((l) => l.key),
        fixed: {
            revenue: s.revenueThb,
            tripFuel: s.tripFuelThb,
            offPlanFuel: s.offPlanFuelThb,
            fuelReconciliation: s.fuelReconciliationThb,
            helperNoTrip: s.helperNoTripThb,
            helperNoTripDays: s.helperNoTripDays,
            toll: s.tollThb,
            otherVehicle: s.otherVehicleExpenseThb,
            maintActual: s.maintActualThb,
            insurance: s.insuranceThb,
            vehicleTax: s.vehicleTaxThb,
            fixedItems: s.fixedItemsThb,
            fixedCosts: s.fixedCostsThb,
        },
    };
}

/**
 * Rebuild every rate-dependent figure. An override left empty keeps each row's own rate, so
 * `recomputePnlScenario(s, {})` equals the month as computed. SELF-CONTAINED — see file header.
 */
export function recomputePnlScenario(s: PnlScenario, o: PnlScenarioOverrides): PnlScenarioResult {
    function r2(n: number): number {
        return Math.round((n + Number.EPSILON) * 100) / 100;
    }
    function isSet(v: number | null | undefined): v is number {
        return typeof v === "number" && isFinite(v) && v >= 0;
    }
    function total(xs: number[]): number {
        let acc = 0;
        for (let i = 0; i < xs.length; i++) acc += xs[i];
        return r2(acc);
    }
    function ceilTrips(n: number): number {
        return Math.max(0, Math.ceil(n - 1e-9));
    }
    const mainOverride = isSet(o.maintServiceKm) || isSet(o.maintTyreKm);

    const rows: PnlScenarioRowResult[] = [];
    const counted: number[] = [];
    for (let i = 0; i < s.rows.length; i++) {
        const row = s.rows[i];
        if (!row.counted || row.revenue === null || row.fuel === null || row.jobKm === null) {
            rows.push({ driverPay: row.driverPay, helperShare: row.helperShare, maintAccrual: row.maintAccrual, contribution: row.contribution, margin: row.margin });
            continue;
        }
        const driverPay = isSet(o.driverTrip) ? r2(o.driverTrip) : (row.driverPay as number);
        const helperShare = isSet(o.helperDay) ? r2(o.helperDay * row.helperDayFraction) : (row.helperShare as number);
        let maintAccrual = row.maintAccrual as number;
        if (mainOverride) {
            const svc = isSet(o.maintServiceKm) ? o.maintServiceKm : (row.svcRate as number);
            const tyre = isSet(o.maintTyreKm) ? o.maintTyreKm : (row.tyreRate as number);
            maintAccrual = r2(row.jobKm * (svc + tyre));
        }
        const contribution = r2(row.revenue - row.fuel - driverPay - helperShare - maintAccrual);
        rows.push({
            driverPay,
            helperShare,
            maintAccrual,
            contribution,
            margin: row.revenue > 0 ? r2((contribution / row.revenue) * 100) : null,
        });
        counted.push(i);
    }

    const pick = (field: "driverPay" | "helperShare" | "maintAccrual" | "contribution") => {
        const xs: number[] = [];
        for (let k = 0; k < counted.length; k++) xs.push(rows[counted[k]][field] as number);
        return total(xs);
    };

    // Lanes — same accumulation and rounding as lib/pnl/aggregate.ts buildLanes.
    const laneMap: Record<string, PnlScenarioLaneResult & { jobKmSum: number }> = {};
    for (let k = 0; k < counted.length; k++) {
        const row = s.rows[counted[k]];
        const res = rows[counted[k]];
        let lane = laneMap[row.laneKey];
        if (!lane) {
            lane = { key: row.laneKey, trips: 0, primaryTrips: 0, supplementaryTrips: 0, jobKmPerTrip: 0, jobKmSum: 0, revenue: 0, contribution: 0, avgRevenue: 0, avgContribution: 0, margin: null };
            laneMap[row.laneKey] = lane;
        }
        lane.trips++;
        if (row.supplementary) lane.supplementaryTrips++;
        else lane.primaryTrips++;
        lane.jobKmSum += row.jobKm ?? 0;
        lane.revenue = r2(lane.revenue + (row.revenue ?? 0));
        lane.contribution = r2(lane.contribution + (res.contribution ?? 0));
    }
    const lanes: PnlScenarioLaneResult[] = [];
    const laneKeys = Object.keys(laneMap);
    for (let k = 0; k < laneKeys.length; k++) {
        const l = laneMap[laneKeys[k]];
        lanes.push({
            key: l.key,
            trips: l.trips,
            primaryTrips: l.primaryTrips,
            supplementaryTrips: l.supplementaryTrips,
            jobKmPerTrip: r2(l.jobKmSum / l.trips),
            revenue: l.revenue,
            contribution: l.contribution,
            avgRevenue: r2(l.revenue / l.trips),
            avgContribution: r2(l.contribution / l.trips),
            margin: l.revenue > 0 ? r2((l.contribution / l.revenue) * 100) : null,
        });
    }
    lanes.sort((a, b) => b.avgContribution - a.avgContribution);

    const trucks: PnlScenarioResult["trucks"] = [];
    for (let t = 0; t < s.trucks.length; t++) {
        const tr = s.trucks[t];
        const contributions: number[] = [];
        const accruals: number[] = [];
        for (let k = 0; k < counted.length; k++) {
            if (s.rows[counted[k]].truckId !== tr.truckId) continue;
            contributions.push(rows[counted[k]].contribution as number);
            accruals.push(rows[counted[k]].maintAccrual as number);
        }
        const contribution = total(contributions);
        trucks.push({
            truckId: tr.truckId,
            contribution,
            maintAccrual: total(accruals),
            afterFixed: r2(contribution - tr.insurance - tr.tax - tr.fixedItems),
        });
    }

    const f = s.fixed;
    const driverPay = pick("driverPay");
    const helperShare = pick("helperShare");
    const maintAccrual = pick("maintAccrual");
    const contribution = pick("contribution");
    const helperNoTrip = isSet(o.helperDay) ? r2(o.helperDay * f.helperNoTripDays) : f.helperNoTrip;
    const maintVariance = r2(f.maintActual - maintAccrual);
    const monthLines = total([f.offPlanFuel, f.fuelReconciliation, helperNoTrip, f.toll, f.otherVehicle, maintVariance]);
    const afterMonthLines = r2(contribution - monthLines);

    const units = counted.length;
    const avg = units > 0 ? contribution / units : null;
    const reachable = avg !== null && avg > 0;
    return {
        rows,
        lanes,
        trucks,
        statement: {
            revenue: f.revenue,
            tripFuel: f.tripFuel,
            driverPay,
            helperShare,
            maintAccrual,
            contribution,
            offPlanFuel: f.offPlanFuel,
            fuelReconciliation: f.fuelReconciliation,
            helperNoTrip,
            toll: f.toll,
            otherVehicle: f.otherVehicle,
            maintVariance,
            monthLines,
            afterMonthLines,
            insurance: f.insurance,
            vehicleTax: f.vehicleTax,
            fixedItems: f.fixedItems,
            fixedCosts: f.fixedCosts,
            operatingProfit: r2(afterMonthLines - f.fixedCosts),
        },
        breakEven: {
            primaryTrips: reachable ? ceilTrips(f.fixedCosts / (avg as number)) : null,
            withMonthLinesTrips: reachable ? ceilTrips((f.fixedCosts + monthLines) / (avg as number)) : null,
            actualTrips: units,
            avgContribution: avg === null ? null : r2(avg),
        },
    };
}
