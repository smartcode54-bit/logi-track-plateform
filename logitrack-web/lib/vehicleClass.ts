/**
 * Vehicle-class (truck type) filter matching — the single source of truth shared by Driver Monitor
 * (`features/drivers/`) and Billing Document (`app/app/accounting/billing-document/`).
 *
 * Unlike a licence plate (an identity — see lib/truckPlate.ts and ADR 0005), a vehicle class is a
 * NORMALISABLE enum: "6 Wheels", "6W", and "6WH" are the same class. We therefore collapse spellings
 * through `normalizeVehicleClass` — the same SSOT billing uses to pick a rate card — so one class is
 * one filter option however it was written.
 *
 * The one rule this module exists to enforce: **blank never becomes a real class.**
 * `normalizeVehicleClass` defaults blank/unknown to "4WJ" because billing must charge *something*;
 * a filter must not. A row with no class lands in an explicit "not specified" bucket, never silently
 * folded into 4WJ (ADR 0003 — an honest gap beats an invented value).
 */

import { normalizeVehicleClass } from "@/lib/billingCompute";

/** Select value meaning "no vehicle-class filter applied". */
export const VEHICLE_CLASS_FILTER_ALL = "all";

/** Select value for rows with no resolvable vehicle class (mirrors PLATE_FILTER_NONE). */
export const VEHICLE_CLASS_FILTER_NONE = "__none__";

export interface VehicleClassOption {
    /** Stable select value: a normalised class code (e.g. "6WH") or VEHICLE_CLASS_FILTER_NONE. */
    value: string;
    /** Class code for display; "" for the no-class bucket (the caller supplies an i18n label). */
    label: string;
    /** How many loaded rows this option matches. */
    count: number;
}

/**
 * Resolves a raw vehicle-class string to its canonical filter key.
 *
 * Blank → NONE (deliberately NOT the billing 4WJ default). A non-blank value is collapsed through
 * `normalizeVehicleClass`; an unknown non-blank keeps its own upper-cased key rather than being
 * invented into a known class.
 */
export function resolveVehicleClassKey(raw?: string | null): string {
    const s = typeof raw === "string" ? raw.trim() : "";
    if (!s) return VEHICLE_CLASS_FILTER_NONE;
    return normalizeVehicleClass(s);
}

/**
 * Builds the filter's options from the rows currently loaded — never from a fixed enum — so every
 * option provably matches at least one row. Sorted by class code; the no-class bucket sorts last.
 */
export function buildVehicleClassOptions(rawClasses: (string | null | undefined)[]): VehicleClassOption[] {
    const byKey = new Map<string, VehicleClassOption>();

    for (const raw of rawClasses) {
        const key = resolveVehicleClassKey(raw);
        const existing = byKey.get(key);
        if (existing) {
            existing.count += 1;
            continue;
        }
        byKey.set(key, {
            value: key,
            label: key === VEHICLE_CLASS_FILTER_NONE ? "" : key,
            count: 1,
        });
    }

    return [...byKey.values()].sort((a, b) => {
        if (a.value === VEHICLE_CLASS_FILTER_NONE) return 1;
        if (b.value === VEHICLE_CLASS_FILTER_NONE) return -1;
        return a.label.localeCompare(b.label);
    });
}

/**
 * True when a row belongs under the selected filter value. `VEHICLE_CLASS_FILTER_ALL` matches
 * everything; otherwise the row's resolved key must equal the selection.
 */
export function rowMatchesVehicleClass(raw: string | null | undefined, selected: string): boolean {
    if (!selected || selected === VEHICLE_CLASS_FILTER_ALL) return true;
    return resolveVehicleClassKey(raw) === selected;
}
