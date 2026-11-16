/**
 * Per-trip P&L — contribution model (ADR 0031, spec shared-docs/specs/per-trip-pnl.md).
 *
 * Pure: computed on read in the browser from the source collections, nothing stored. A trip's
 * contribution = revenue − fuel on its job distance − driver pay − helper share − maintenance accrual.
 * Fixed costs are NEVER allocated to trips; they, the off-plan km fuel and the maintenance variance
 * live at month level. Rows that cannot be computed are listed, excluded from totals and counted
 * (fail loud) — never silently dropped.
 */
import { PNL_FIXED_COST_CATEGORIES, fixedCostCategoryKey, type PnlFixedCostCategory } from "@/validate/pnlSettingsSchema";
import { WarningBag, buildLanes, computeBreakEven, laneKeyFor } from "./pnl/aggregate";
import { buildDistanceIndex, resolveJobDistanceKm, standbyJobDistanceKm } from "./pnl/distance";
import { isOwnTruck, makeTruckResolver, truckFuelWindow, type TruckFuelWindow } from "./pnl/fuel";
import { helperShares, taskWindowAnchor, type HelperUnit } from "./pnl/helpers";
import {
    isCompletedMaintenance,
    isInsuranceClaim,
    isMaintenanceNotClosed,
    maintenanceBillDateKey,
    openMaintenanceDateKey,
} from "./pnl/maintenance";
import {
    bangkokMonthBounds,
    fixedCostMonthlyAmount,
    groupFixedCostItems,
    activeFixedCostVersion,
    pnlDateKeyForStandby,
    pnlDateKeyForTrip,
    renewalMonthlyAmounts,
    round2,
    selectPnlRate,
    toMonthKey,
} from "./pnl/settings";
import type {
    PnlFixedItemLine,
    PnlInputs,
    PnlMonthStatement,
    PnlResult,
    PnlTripRow,
    PnlTruckInput,
    PnlTruckRow,
    PnlWarningCode,
} from "./pnl/types";

export * from "./pnl/types";
export { buildDistanceIndex, legKm, resolveJobDistanceKm, standbyJobDistanceKm } from "./pnl/distance";
export { truckFuelWindow, isOwnTruck } from "./pnl/fuel";
export { helperShares } from "./pnl/helpers";
export { maintenanceBillDateKey } from "./pnl/maintenance";
export {
    activeFixedCostVersion,
    bangkokMonthBounds,
    fixedCostMonthlyAmount,
    pnlDateKeyForStandby,
    pnlDateKeyForTrip,
    renewalMonthlyAmounts,
    round2,
    selectPnlRate,
    toMonthKey,
} from "./pnl/settings";
export { computeBreakEven, laneKeyFor } from "./pnl/aggregate";
export * from "./pnl/scenario";

const ACTIVE_TRUCK_STATUSES = new Set(["active", "maintenance", "insurance-claim"]);
const sum = (xs: number[]) => round2(xs.reduce((a, b) => a + b, 0));

interface DraftRow {
    row: PnlTripRow;
    truck?: PnlTruckInput;
    revenue?: number;
}

export function computePnl(inputs: PnlInputs): PnlResult {
    const monthKey = toMonthKey(inputs.year, inputs.month);
    const { start, end } = bangkokMonthBounds(inputs.year, inputs.month);
    const inMonthKey = (dk: string | null | undefined): dk is string => !!dk && dk.startsWith(monthKey);
    const inMonthDate = (d: Date) => d.getTime() >= start.getTime() && d.getTime() < end.getTime();
    const warnings = new WarningBag();
    const index = buildDistanceIndex(inputs.distances);
    const resolveTruck = makeTruckResolver(inputs.trucks);
    const truckById = new Map(inputs.trucks.map((t) => [t.id, t]));
    const ownTrucks = inputs.trucks.filter(isOwnTruck);

    // ── Fuel: fill-to-fill window per own truck, fleet average fallback (R9) ──────────────────────
    const fuelByTruck = new Map<string, typeof inputs.fuelRows>();
    const fuelCashByTruck = new Map<string, number>();
    let unattributedFuelCash = 0;
    for (const r of inputs.fuelRows) {
        const status = r.status.toUpperCase();
        const truck = resolveTruck(r);
        if (truck && !isOwnTruck(truck)) continue;
        if (status === "PENDING" && inMonthDate(r.date)) {
            warnings.add("pendingFuel", r.id, r.amount);
            continue;
        }
        if (status !== "APPROVED") continue;
        if (!truck) {
            if (inMonthDate(r.date)) {
                unattributedFuelCash += r.amount;
                warnings.add("fuelNoTruck", r.id, r.amount);
            }
            continue;
        }
        fuelByTruck.set(truck.id, [...(fuelByTruck.get(truck.id) ?? []), r]);
        if (inMonthDate(r.date)) fuelCashByTruck.set(truck.id, (fuelCashByTruck.get(truck.id) ?? 0) + r.amount);
    }
    const windows = new Map<string, TruckFuelWindow>();
    let fleetSpend = 0;
    let fleetKm = 0;
    for (const t of ownTrucks) {
        const w = truckFuelWindow(fuelByTruck.get(t.id) ?? [], start, end);
        windows.set(t.id, w);
        w.rollbackIds.forEach((id) => warnings.add("odometerRollback", `${t.plate}:${id}`));
        if (w.usable) {
            fleetSpend += w.spendThb;
            fleetKm += w.odoKm;
            if (w.noAnchorBeforeMonth) warnings.add("noAnchorBeforeMonth", t.plate);
        }
    }
    const fleetRate = fleetKm > 0 ? fleetSpend / fleetKm : null;
    const fuelRateFor = (truckId: string): { rate: number; fleetAvg: boolean } | null => {
        const w = windows.get(truckId);
        if (w?.usable && w.rateThbPerKm !== null) return { rate: w.rateThbPerKm, fleetAvg: false };
        return fleetRate === null ? null : { rate: fleetRate, fleetAvg: true };
    };

    // ── Trips + standbys of the month (and helper units across the loaded window) ────────────────
    const drafts: DraftRow[] = [];
    const units: HelperUnit[] = [];
    const subcontractor = { trips: 0, revenueThb: 0 };

    for (const t of inputs.trips) {
        if (t.status !== "delivered") continue;
        const dateKey = pnlDateKeyForTrip(t.checkInAt, t.deliveredAt);
        if (!dateKey) continue;
        const truck = t.truckId ? truckById.get(t.truckId) : undefined;
        if (truck && !isOwnTruck(truck)) {
            if (inMonthKey(dateKey)) {
                subcontractor.trips++;
                subcontractor.revenueThb = round2(subcontractor.revenueThb + (t.revenueThb ?? 0));
            }
            continue;
        }
        const rowId = `trip:${t.id}`;
        if (truck) units.push({ rowId, helperDriverIds: t.helperDriverIds, anchor: taskWindowAnchor(t.checkInAt, t.taskDate, t.deliveredAt) });
        if (!inMonthKey(dateKey)) continue;
        const dist = resolveJobDistanceKm(t.originCode, t.destinationCodes, index);
        const notes: PnlWarningCode[] = t.hasTask ? [] : ["noTask"];
        drafts.push({
            truck,
            revenue: t.revenueThb,
            row: {
                kind: "trip", id: t.id, dateKey, truckId: t.truckId, truckPlate: truck?.plate,
                driverId: t.driverId, driverName: t.driverName, customerId: t.customerId, customerName: t.customerName,
                originCode: t.originCode, destinationCodes: t.destinationCodes, isMultiDrop: t.isMultiDrop,
                vehicleClass: t.vehicleClass, jobCategory: t.jobCategory,
                jobKm: "km" in dist ? round2(dist.km) : null,
                missingLeg: "missingLeg" in dist ? dist.missingLeg : undefined,
                ...emptyMoney(), counted: false, reasons: [], notes, laneKey: "",
            },
        });
    }

    for (const s of inputs.standbys) {
        if (s.status !== "completed") continue;
        const dateKey = pnlDateKeyForStandby(s);
        if (!dateKey) continue;
        const truck = s.truckId ? truckById.get(s.truckId) : undefined;
        if (truck && !isOwnTruck(truck)) {
            if (inMonthKey(dateKey)) {
                subcontractor.trips++;
                subcontractor.revenueThb = round2(subcontractor.revenueThb + (s.revenueThb ?? 0));
            }
            continue;
        }
        const rowId = `standby:${s.id}`;
        if (truck) units.push({ rowId, helperDriverIds: s.helperDriverIds, anchor: taskWindowAnchor(s.taskCheckInAt, s.taskDate, s.startedAt ?? s.endedAt) });
        if (!inMonthKey(dateKey)) continue;
        const dist = standbyJobDistanceKm(s, inputs.trips, index);
        const notes: PnlWarningCode[] = "km" in dist && dist.source === "standbyTask" ? ["standbyDistanceFromTask"] : [];
        drafts.push({
            truck,
            revenue: s.revenueThb,
            row: {
                kind: "standby", id: s.id, dateKey, truckId: s.truckId, truckPlate: truck?.plate,
                driverId: s.driverId, driverName: s.driverName, customerId: s.customerId, customerName: s.customerName,
                originCode: s.taskRoute?.originCode, destinationCodes: s.taskRoute?.destinationCodes ?? [], isMultiDrop: false,
                jobKm: "km" in dist ? round2(dist.km) : null,
                missingLeg: "missingLeg" in dist ? dist.missingLeg : undefined,
                ...emptyMoney(), counted: false, reasons: [], notes, laneKey: "",
            },
        });
    }

    const helper = helperShares({
        units,
        helperTasks: inputs.helperTasks,
        trips: inputs.trips,
        driverIdentity: inputs.driverIdentity,
        rates: inputs.rates,
        monthKey,
    });
    warnings.addCount("missingRate", helper.noTripMissingRate);

    // ── Price each row (R10, R12, R14) ───────────────────────────────────────────────────────────
    const rows: PnlTripRow[] = drafts.map(({ row, truck, revenue }) => {
        const share = helper.byRowId.get(`${row.kind}:${row.id}`);
        const driverRate = selectPnlRate(inputs.rates, "driver_trip", row.dateKey);
        const svc = selectPnlRate(inputs.rates, "maint_service_km", row.dateKey);
        const tyre = selectPnlRate(inputs.rates, "maint_tyre_km", row.dateKey);
        const fuelRate = truck ? fuelRateFor(truck.id) : null;
        const reasons: PnlWarningCode[] = [];
        if (revenue === undefined) reasons.push("unpriced");
        if (!truck) reasons.push("noTruck");
        if (row.jobKm === null) reasons.push("noJobDistance");
        if (!driverRate || !svc || !tyre || share?.missingRate) reasons.push("missingRate");
        if (truck && !fuelRate) reasons.push("noFuelRate");
        const notes = [...row.notes];
        if (fuelRate?.fleetAvg) notes.push("fleetAvgFuelRate");

        const km = row.jobKm;
        const out: PnlTripRow = {
            ...row,
            revenueThb: revenue ?? null,
            fuelRateThbPerKm: fuelRate ? round2(fuelRate.rate) : null,
            fuelThb: km !== null && fuelRate ? round2(km * fuelRate.rate) : null,
            driverPayThb: driverRate ? round2(driverRate.amountThb) : null,
            helperShareThb: round2(share?.shareThb ?? 0),
            maintAccrualThb: km !== null && svc && tyre ? round2(km * (svc.amountThb + tyre.amountThb)) : null,
            maintServiceRateThbPerKm: svc?.amountThb ?? null,
            maintTyreRateThbPerKm: tyre?.amountThb ?? null,
            helperDayFraction: share?.dayFraction ?? 0,
            counted: reasons.length === 0,
            reasons,
            notes,
        };
        if (out.counted) {
            out.contributionThb = round2(
                (revenue as number) - (out.fuelThb as number) - (out.driverPayThb as number) - (out.helperShareThb as number) - (out.maintAccrualThb as number),
            );
            out.marginPct = (revenue as number) > 0 ? round2((out.contributionThb / (revenue as number)) * 100) : null;
        }
        out.laneKey = laneKeyFor(out);
        const ref = `${row.kind}:${row.id}`;
        reasons.forEach((code) => warnings.add(code, ref, revenue));
        notes.filter((n) => n !== "fleetAvgFuelRate").forEach((code) => warnings.add(code, ref));
        return out;
    }).sort((a, b) => a.dateKey.localeCompare(b.dateKey) || a.id.localeCompare(b.id));
    const counted = rows.filter((r) => r.counted);

    // ── Maintenance bills (R13) ──────────────────────────────────────────────────────────────────
    const maintPm = new Map<string, number>();
    const maintCm = new Map<string, number>();
    let insuranceClaimInfo = 0;
    const monthEndKey = `${monthKey}-99`;
    for (const m of inputs.maintenance) {
        const truck = m.truckId ? truckById.get(m.truckId) : undefined;
        if (!truck || !isOwnTruck(truck)) continue;
        if (isMaintenanceNotClosed(m)) {
            const openKey = openMaintenanceDateKey(m);
            if (openKey && openKey <= monthEndKey) warnings.add("maintenanceNotClosed", `${truck.plate}:${m.id}`, m.amountThb);
            continue;
        }
        if (!isCompletedMaintenance(m)) continue;
        const bill = maintenanceBillDateKey(m);
        if (!bill || !inMonthKey(bill.dateKey)) continue;
        if (bill.estimated) warnings.add("maintenanceDateEstimated", `${truck.plate}:${m.id}`);
        if (isInsuranceClaim(m)) {
            insuranceClaimInfo += m.amountThb;
            continue;
        }
        if (!(m.amountThb > 0)) {
            warnings.add("maintenanceZeroAmount", `${truck.plate}:${m.id}`);
            continue;
        }
        const target = m.type.toUpperCase() === "PM" ? maintPm : maintCm;
        target.set(truck.id, (target.get(truck.id) ?? 0) + m.amountThb);
    }

    // ── Fixed costs: the fixed-cost list (R2) + renewal history (R15) ────────────────────────────
    // A truck's ภาษีรถ (vehicle_tax) list item REPLACES its renewal-history tax for the month — never
    // both (owner 2026-11-16). Owner-named categories each get their own line.
    const items = [...groupFixedCostItems(inputs.fixedCosts).values()];
    const fixedLines = new Map<string, PnlFixedItemLine>();
    const fixedByTruck = new Map<string, number>();
    const listTaxByTruck = new Map<string, number>();
    for (const versions of items) {
        const v = activeFixedCostVersion(versions, monthKey);
        if (!v || v.isStop || (v.effectiveToMonth && monthKey > v.effectiveToMonth)) continue;
        const amount = round2(fixedCostMonthlyAmount(versions, monthKey));
        if (v.category === "vehicle_tax" && v.truckId) {
            listTaxByTruck.set(v.truckId, round2((listTaxByTruck.get(v.truckId) ?? 0) + amount));
            continue;
        }
        if (amount === 0) continue;
        const key = fixedCostCategoryKey(v);
        const line = fixedLines.get(key) ?? {
            key,
            category: v.category,
            label: v.category === "custom" ? v.categoryLabel : undefined,
            amountThb: 0,
        };
        line.amountThb = round2(line.amountThb + amount);
        fixedLines.set(key, line);
        if (v.scope === "truck" && v.truckId) fixedByTruck.set(v.truckId, (fixedByTruck.get(v.truckId) ?? 0) + amount);
    }
    const categoryOrder = (c: PnlFixedCostCategory) => PNL_FIXED_COST_CATEGORIES.indexOf(c);
    const fixedItemLines = [...fixedLines.values()].sort(
        (a, b) => categoryOrder(a.category) - categoryOrder(b.category) || (a.label ?? "").localeCompare(b.label ?? ""),
    );

    const truckRows: PnlTruckRow[] = [];
    for (const t of ownTrucks) {
        const tRows = rows.filter((r) => r.truckId === t.id);
        const tCounted = tRows.filter((r) => r.counted);
        const insurance = renewalMonthlyAmounts(inputs.renewals, t.id, "insurance");
        const tax = renewalMonthlyAmounts(inputs.renewals, t.id, "tax");
        const fuelCash = round2(fuelCashByTruck.get(t.id) ?? 0);
        const pm = round2(maintPm.get(t.id) ?? 0);
        const cm = round2(maintCm.get(t.id) ?? 0);
        const inFleet =
            tRows.length > 0 ||
            insurance.byMonth.has(monthKey) ||
            tax.byMonth.has(monthKey) ||
            fixedByTruck.has(t.id) ||
            listTaxByTruck.has(t.id) ||
            ACTIVE_TRUCK_STATUSES.has(t.truckStatus ?? "");
        if (!inFleet && fuelCash === 0 && pm === 0 && cm === 0) continue;

        const spread = (s: typeof insurance, fallback: number | undefined, label: string) => {
            if (s.zeroAmountMonths.has(monthKey)) warnings.add("renewalZeroAmount", `${t.plate} ${label}`);
            if (s.byMonth.has(monthKey)) return round2(s.byMonth.get(monthKey) as number);
            if (!inFleet) return 0;
            warnings.add("noRenewalHistory", `${t.plate} ${label}`);
            return fallback && fallback > 0 ? round2(fallback / 12) : 0;
        };
        const insuranceThb = spread(insurance, t.insurancePremium, "insurance");
        const vehicleTaxThb = listTaxByTruck.has(t.id)
            ? (listTaxByTruck.get(t.id) as number)
            : spread(tax, t.taxExpense, "tax");

        const w = windows.get(t.id);
        const fuelRate = fuelRateFor(t.id);
        // Off-plan km subtracts the job km of EVERY row with a distance (counted or not): that km was
        // driven, and only its fuel (not its km) is missing from the trip totals.
        const jobKm = round2(tRows.reduce((a, r) => a + (r.jobKm ?? 0), 0));
        const usable = !!w?.usable && w.rateThbPerKm !== null;
        const offPlanKm = usable ? round2((w as TruckFuelWindow).odoKm - jobKm) : null;
        if (!usable && fuelRate?.fleetAvg && (tRows.length > 0 || fuelCash > 0)) warnings.add("fleetAvgFuelRate", t.plate);
        const contribution = sum(tCounted.map((r) => r.contributionThb ?? 0));
        const fixedItemsThb = round2(fixedByTruck.get(t.id) ?? 0);
        truckRows.push({
            truckId: t.id,
            plate: t.plate,
            trips: tCounted.filter((r) => r.kind === "trip").length,
            standbys: tCounted.filter((r) => r.kind === "standby").length,
            revenueThb: sum(tCounted.map((r) => r.revenueThb ?? 0)),
            jobKm,
            odoKm: usable ? round2((w as TruckFuelWindow).odoKm) : null,
            offPlanKm,
            offPlanPct: offPlanKm !== null && jobKm > 0 ? round2((offPlanKm / jobKm) * 100) : null,
            fuelRateThbPerKm: fuelRate ? round2(fuelRate.rate) : null,
            fuelRateIsFleetAvg: !!fuelRate?.fleetAvg,
            tripFuelThb: sum(tCounted.map((r) => r.fuelThb ?? 0)),
            offPlanFuelThb: offPlanKm !== null ? round2(offPlanKm * (w as TruckFuelWindow).rateThbPerKm!) : null,
            fuelCashThb: fuelCash,
            maintAccrualThb: sum(tCounted.map((r) => r.maintAccrualThb ?? 0)),
            maintActualPmThb: pm,
            maintActualCmThb: cm,
            contributionThb: contribution,
            insuranceThb,
            vehicleTaxThb,
            fixedItemsThb,
            afterFixedThb: round2(contribution - insuranceThb - vehicleTaxThb - fixedItemsThb),
        });
    }
    truckRows.sort((a, b) => a.plate.localeCompare(b.plate));

    // ── Month-level lines (R16) ──────────────────────────────────────────────────────────────────
    const otherExpenseByCategory: Record<string, number> = {};
    for (const e of inputs.otherExpenses) {
        if (e.status.toUpperCase() !== "APPROVED" || !inMonthDate(e.date)) continue;
        const truck = resolveTruck(e);
        if (truck && !isOwnTruck(truck)) continue;
        const cat = e.category?.trim() || "other";
        otherExpenseByCategory[cat] = round2((otherExpenseByCategory[cat] ?? 0) + e.amount);
    }
    const tollThb = round2(otherExpenseByCategory.toll ?? 0);
    const otherVehicleExpenseThb = round2(
        Object.entries(otherExpenseByCategory).filter(([c]) => c !== "toll").reduce((a, [, v]) => a + v, 0),
    );

    const statement = buildStatement({
        counted,
        rows,
        truckRows,
        unattributedFuelCash,
        helperNoTripThb: sum(helper.noTripHelperDays.map((h) => h.amountThb)),
        helperNoTripDays: helper.noTripHelperDays.length,
        tollThb,
        otherVehicleExpenseThb,
        otherExpenseByCategory,
        insuranceClaimInfoThb: round2(insuranceClaimInfo),
        fixedItemLines,
    });

    const planBasis = new Set(inputs.planBasisCustomerIds);
    return {
        monthKey,
        trips: rows,
        lanes: buildLanes(rows),
        trucks: truckRows,
        statement,
        breakEven: computeBreakEven({
            contributionThb: statement.contributionThb,
            countedUnits: statement.countedTrips + statement.countedStandbys,
            fixedCostsThb: statement.fixedCostsThb,
            monthLinesThb: statement.monthLinesThb,
        }),
        subcontractor,
        warnings: warnings.list(),
        flags: {
            monthInProgress: inputs.now.getTime() < end.getTime(),
            hasPlanBasisCustomer: rows.some((r) => !!r.customerId && planBasis.has(r.customerId)),
        },
    };
}

function emptyMoney() {
    return {
        revenueThb: null, fuelRateThbPerKm: null, fuelThb: null, driverPayThb: null,
        helperShareThb: null, maintAccrualThb: null, contributionThb: null, marginPct: null,
        maintServiceRateThbPerKm: null, maintTyreRateThbPerKm: null, helperDayFraction: 0,
    };
}

function buildStatement(p: {
    counted: PnlTripRow[];
    rows: PnlTripRow[];
    truckRows: PnlTruckRow[];
    unattributedFuelCash: number;
    helperNoTripThb: number;
    helperNoTripDays: number;
    tollThb: number;
    otherVehicleExpenseThb: number;
    otherExpenseByCategory: Record<string, number>;
    insuranceClaimInfoThb: number;
    fixedItemLines: PnlFixedItemLine[];
}): PnlMonthStatement {
    const { counted, rows, truckRows } = p;
    const revenueThb = sum(counted.map((r) => r.revenueThb ?? 0));
    const tripFuelThb = sum(counted.map((r) => r.fuelThb ?? 0));
    const driverPayThb = sum(counted.map((r) => r.driverPayThb ?? 0));
    const helperShareThb = sum(counted.map((r) => r.helperShareThb ?? 0));
    const maintAccrualThb = sum(counted.map((r) => r.maintAccrualThb ?? 0));
    const contributionThb = sum(counted.map((r) => r.contributionThb ?? 0));
    const offPlanFuelThb = sum(truckRows.map((t) => t.offPlanFuelThb ?? 0));
    const fuelCashThb = round2(sum(truckRows.map((t) => t.fuelCashThb)) + p.unattributedFuelCash);
    const fuelReconciliationThb = round2(fuelCashThb - tripFuelThb - offPlanFuelThb);
    const maintActualThb = sum(truckRows.map((t) => t.maintActualPmThb + t.maintActualCmThb));
    const maintVarianceThb = round2(maintActualThb - maintAccrualThb);
    const monthLinesThb = sum([offPlanFuelThb, fuelReconciliationThb, p.helperNoTripThb, p.tollThb, p.otherVehicleExpenseThb, maintVarianceThb]);
    const afterMonthLinesThb = round2(contributionThb - monthLinesThb);
    const insuranceThb = sum(truckRows.map((t) => t.insuranceThb));
    const vehicleTaxThb = sum(truckRows.map((t) => t.vehicleTaxThb));
    const fixedItemsThb = sum(p.fixedItemLines.map((l) => l.amountThb));
    const fixedCostsThb = sum([insuranceThb, vehicleTaxThb, fixedItemsThb]);
    const excluded = rows.filter((r) => !r.counted);
    return {
        revenueThb, tripFuelThb, driverPayThb, helperShareThb, maintAccrualThb, contributionThb,
        offPlanFuelThb, fuelReconciliationThb, fuelCashThb,
        helperNoTripThb: p.helperNoTripThb,
        tollThb: p.tollThb,
        otherVehicleExpenseThb: p.otherVehicleExpenseThb,
        otherExpenseByCategory: p.otherExpenseByCategory,
        maintActualThb, maintVarianceThb,
        insuranceClaimInfoThb: p.insuranceClaimInfoThb,
        monthLinesThb, afterMonthLinesThb,
        insuranceThb, vehicleTaxThb,
        fixedItemLines: p.fixedItemLines,
        fixedItemsThb, fixedCostsThb,
        operatingProfitThb: round2(afterMonthLinesThb - fixedCostsThb),
        helperNoTripDays: p.helperNoTripDays,
        countedTrips: counted.filter((r) => r.kind === "trip").length,
        countedStandbys: counted.filter((r) => r.kind === "standby").length,
        excludedCount: excluded.length,
        excludedRevenueThb: sum(excluded.map((r) => r.revenueThb ?? 0)),
    };
}
