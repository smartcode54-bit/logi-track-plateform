/**
 * Truck fuel cost per km, fill-to-fill (ADR 0031 §2, spec R9).
 *
 * The only odometer history is the refuel receipt (Cartrack keeps the current reading only), so a
 * truck-month is measured from the last reading BEFORE the month (anchor A) to the last reading IN
 * the month (E): rate = approved fuel bought after A up to E ÷ (E.odometer − A.odometer).
 */
import { KMPL_MAX, KMPL_MIN, normalizePlateKey } from "@/lib/fuelEfficiency";
import type { PnlFuelRowInput, PnlTruckInput } from "./types";

export type FuelWindowReason = "noReadings" | "singleReading" | "nonPositiveKm" | "kmplOutOfRange";

export interface TruckFuelWindow {
    usable: boolean;
    reason?: FuelWindowReason;
    odoKm: number;
    spendThb: number;
    liters: number;
    rateThbPerKm: number | null;
    noAnchorBeforeMonth: boolean;
    rollbackIds: string[];
}

/**
 * @param rows APPROVED fuel rows of ONE truck, covering [monthStart − look-back, monthEnd).
 */
export function truckFuelWindow(rows: PnlFuelRowInput[], monthStart: Date, monthEnd: Date): TruckFuelWindow {
    const s = monthStart.getTime();
    const e = monthEnd.getTime();
    const sorted = [...rows].sort((a, b) => a.date.getTime() - b.date.getTime() || a.id.localeCompare(b.id));

    // Odometer chain in time order; a reading below the running max is a rollback (bad entry or
    // wrong truck) and is dropped rather than allowed to shrink the km.
    const readings: PnlFuelRowInput[] = [];
    const rollbackIds: string[] = [];
    let maxOdo = -Infinity;
    for (const r of sorted) {
        if (r.odometer == null || !Number.isFinite(r.odometer)) continue;
        if (r.odometer < maxOdo) {
            rollbackIds.push(r.id);
            continue;
        }
        maxOdo = r.odometer;
        readings.push(r);
    }

    const before = readings.filter((r) => r.date.getTime() < s);
    const inMonth = readings.filter((r) => r.date.getTime() >= s && r.date.getTime() < e);
    const empty = (reason: FuelWindowReason, noAnchor: boolean): TruckFuelWindow => ({
        usable: false,
        reason,
        odoKm: 0,
        spendThb: 0,
        liters: 0,
        rateThbPerKm: null,
        noAnchorBeforeMonth: noAnchor,
        rollbackIds,
    });

    if (inMonth.length === 0) return empty("noReadings", before.length === 0);
    const noAnchorBeforeMonth = before.length === 0;
    const anchor = noAnchorBeforeMonth ? inMonth[0] : before[before.length - 1];
    const end = inMonth[inMonth.length - 1];
    if (anchor === end) return empty("singleReading", noAnchorBeforeMonth);

    const odoKm = (end.odometer as number) - (anchor.odometer as number);
    if (!(odoKm > 0)) return empty("nonPositiveKm", noAnchorBeforeMonth);

    const z = end.date.getTime();
    // Fuel bought after the anchor fill, up to and including the end fill (rows without an
    // odometer count — the fuel was still burnt in this window).
    const windowRows = sorted.slice(sorted.indexOf(anchor) + 1).filter((r) => r.date.getTime() <= z);
    const spendThb = windowRows.reduce((sum, r) => sum + (r.amount > 0 ? r.amount : 0), 0);
    const litersComplete = windowRows.every((r) => (r.volumeLiters ?? 0) > 0);
    const liters = windowRows.reduce((sum, r) => sum + (r.volumeLiters ?? 0), 0);

    const window: TruckFuelWindow = {
        usable: true,
        odoKm,
        spendThb,
        liters,
        rateThbPerKm: spendThb / odoKm,
        noAnchorBeforeMonth,
        rollbackIds,
    };
    if (litersComplete && liters > 0) {
        const kmpl = odoKm / liters;
        if (kmpl < KMPL_MIN || kmpl > KMPL_MAX) {
            return { ...window, usable: false, reason: "kmplOutOfRange", rateThbPerKm: null };
        }
    }
    return window;
}

/** Resolve the truck an expense row belongs to: truckId, else the normalized plate. */
export function makeTruckResolver(trucks: PnlTruckInput[]): (row: { truckId?: string; plate?: string }) => PnlTruckInput | undefined {
    const byId = new Map(trucks.map((t) => [t.id, t]));
    const byPlate = new Map<string, PnlTruckInput>();
    for (const t of trucks) {
        const k = normalizePlateKey(t.plate);
        if (k) byPlate.set(k, t);
    }
    return (row) => {
        if (row.truckId && byId.has(row.truckId)) return byId.get(row.truckId);
        const k = normalizePlateKey(row.plate);
        return k ? byPlate.get(k) : undefined;
    };
}

/** Own truck = not a subcontractor truck (schema default ownershipType is "own"). */
export function isOwnTruck(t: PnlTruckInput): boolean {
    return t.ownershipType !== "subcontractor" && !t.subcontractorId;
}
