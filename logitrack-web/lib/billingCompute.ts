/**
 * Pure per-trip billing math (no Firebase). Used by web admin and Cloud Functions.
 * Keep `functions/src/core/billingCompute.ts` in sync when changing this file.
 */

export interface BillingRateEntry {
    id: string;
    customerId: string;
    importId: string;
    hubId: string;
    destinationCode: string;
    vehicleClass: string;
    rateThb: number;
    effectiveFromMs: number;
    /** หลัก/เสริม. Missing = "PRIMARY" (legacy rows). See ADR-0005. */
    jobCategory?: "PRIMARY" | "SUPPLEMENTARY";
}

export type JobCategory = "PRIMARY" | "SUPPLEMENTARY";

export interface FuelRateAdjustment {
    id: string;
    customerId: string;
    effectiveFromMs: number;
    rateMultiplier: number;
    addThbPerTrip: number;
}

export interface TripBillingComputed {
    customerId: string;
    baseRateThb: number;
    finalRateThb: number;
    rateImportId: string;
    lookupHubId: string;
    lookupDestination: string;
    fuelAdjustmentId?: string;
    rateMultiplier: number;
    addThbPerTrip: number;
    effectiveFromDateStr?: string;
}

export interface TripBillingTimestamps {
    deliveredTimestamp?: unknown;
    createdAt?: unknown;
}

export interface TaskBillingInput {
    sourceHub?: string | null;
    destination?: string | null;
    truckType?: string | null;
    sourceHubLinkedCustomerId?: string | null;
    destinationLinkedCustomerId?: string | null;
}

function normalizeCode(v: string | null | undefined): string {
    return (v ?? "").trim().toUpperCase();
}

export function extractHubId(sourceHub: string | null | undefined): string {
    const raw = (sourceHub ?? "").trim();
    if (!raw) return "";
    const code = raw.split(" - ")[0]?.trim() ?? raw;
    return normalizeCode(code);
}

export function normalizeDestinationCode(destination: string | null | undefined): string {
    const u = normalizeCode(destination);
    if (!u) return "";
    if (u.startsWith("SOCE")) return "SOCE";
    if (u.startsWith("SOCN")) return "SOCN";
    if (u.startsWith("SOCW")) return "SOCW";
    // SPK codes: extract just the code part (e.g., "SPK890103 - ลาดกระบัง26" or "SPK890103-ลาดกระบัง26" → "SPK890103")
    const dashIdx = u.indexOf("-");
    if (dashIdx > 0) {
        return u.slice(0, dashIdx).trim();
    }
    return u;
}

export function normalizeVehicleClass(vehicleClass: string | null | undefined): string {
    const u = normalizeCode(vehicleClass || "4WJ");
    if (!u) return "4WJ";
    // Map full truck type names to short codes
    const mapping: Record<string, string> = {
        "4 WHEELS JUMBO": "4WJ",
        "6 WHEELS": "6W",
        "10 WHEELS": "10W",
        "2 WHEELS": "2W",
    };
    return mapping[u] ?? u;
}

/** Milliseconds from Firestore Timestamp, Date, number, or similar. */
export function timestampLikeToMillis(val: unknown): number {
    if (!val) return 0;
    if (typeof val === "number" && Number.isFinite(val)) return val;
    if (val instanceof Date) return val.getTime();
    if (typeof (val as { toMillis?: () => number }).toMillis === "function") {
        return (val as { toMillis: () => number }).toMillis();
    }
    if (typeof (val as { toDate?: () => Date }).toDate === "function") {
        return (val as { toDate: () => Date }).toDate().getTime();
    }
    return 0;
}

export function getTripBillingDateMs(trip: TripBillingTimestamps): number {
    const delivered = timestampLikeToMillis(trip.deliveredTimestamp);
    if (delivered > 0) return delivered;
    const created = timestampLikeToMillis(trip.createdAt);
    if (created > 0) return created;
    return Date.now();
}

export function resolveTaskCustomerId(task: TaskBillingInput | null | undefined): string {
    return (
        task?.sourceHubLinkedCustomerId?.trim() ||
        task?.destinationLinkedCustomerId?.trim() ||
        ""
    );
}

export function selectBillingRateEntry(
    customerId: string,
    hubId: string,
    destinationCode: string,
    vehicleClass: string,
    billDateMs: number,
    rateEntries: BillingRateEntry[],
    jobCategory: JobCategory = "PRIMARY"
): BillingRateEntry | null {
    const normalizedVehicleClass = normalizeVehicleClass(vehicleClass);
    const candidates = rateEntries.filter(
        (entry) =>
            entry.customerId === customerId &&
            entry.hubId === hubId &&
            entry.destinationCode === destinationCode &&
            normalizeVehicleClass(entry.vehicleClass) === normalizedVehicleClass &&
            (entry.jobCategory ?? "PRIMARY") === jobCategory
    );
    if (candidates.length === 0) return null;

    // Primary: effective on or before trip date (newest first)
    const effective = candidates
        .filter((e) => e.effectiveFromMs <= billDateMs)
        .sort((a, b) => b.effectiveFromMs - a.effectiveFromMs);
    if (effective.length > 0) return effective[0];

    // Fallback: trip is before all effective dates → use oldest rate card
    const oldest = candidates.sort((a, b) => a.effectiveFromMs - b.effectiveFromMs);
    return oldest[0];
}

export function selectFuelAdjustmentForBillingDate(
    customerId: string,
    billDateMs: number,
    fuelAdjustments: FuelRateAdjustment[]
): FuelRateAdjustment | null {
    const matched = fuelAdjustments
        .filter((adj) => adj.customerId === customerId && adj.effectiveFromMs <= billDateMs)
        .sort((a, b) => b.effectiveFromMs - a.effectiveFromMs);
    return matched[0] ?? null;
}

/** Final trip rate in THB, rounded to 2 decimal places (same as legacy billing snapshot). */
export function computeFinalRateThb(baseRateThb: number, rateMultiplier: number, addThbPerTrip: number): number {
    return Math.round((baseRateThb * rateMultiplier + addThbPerTrip) * 100) / 100;
}

export interface DeliveryStopForBilling {
    index: number;
    destination: string;
}

export interface MultiDeliveryStopBilling {
    stopIndex: number;
    destination: string;
    baseRateThb: number;
    finalRateThb: number;
}

export interface MultiDeliveryBillingResult {
    customerId: string;
    baseRateThb: number; // Rate of stop 1
    stopChargeThb: number; // Sum of rates for stop 3+
    totalBillingThb: number; // baseRateThb + stopChargeThb
    stopBreakdown: MultiDeliveryStopBilling[];
    rateMultiplier: number;
    fuelAdjustmentId?: string;
}

/**
 * Compute billing for multi-delivery task.
 * Rule: stop[0] = planned destination (base rate from rate card), stop[1+] = extra stops.
 * Origin (source hub) is never in deliveryStops — it is already accounted for in the base rate.
 *
 * Extra stop charging:
 *   - If `extraStopFeeThb` is provided (>= 0): each extra stop is charged this flat service fee
 *     (from customer_service_fees, feeType "extra_stop"). Fuel multiplier is NOT applied to it.
 *   - If `extraStopFeeThb` is undefined: legacy behaviour — each extra stop is looked up in the
 *     rate card per route (with fuel multiplier applied).
 */
export function computeMultiDeliveryBilling(
    trip: TripBillingTimestamps,
    task: TaskBillingInput | null | undefined,
    deliveryStops: DeliveryStopForBilling[],
    vehicleClass: string,
    rateEntries: BillingRateEntry[],
    fuelAdjustments: FuelRateAdjustment[],
    extraStopFeeThb?: number,
    jobCategory: JobCategory = "PRIMARY"
): MultiDeliveryBillingResult | null {
    if (!task || deliveryStops.length < 2) return null;
    const customerId = resolveTaskCustomerId(task);
    if (!customerId) return null;

    const hubId = extractHubId(task.sourceHub);
    const normalizedVehicleClass = normalizeVehicleClass(vehicleClass);
    const billDateMs = getTripBillingDateMs(trip);
    const matchedAdjustment = selectFuelAdjustmentForBillingDate(customerId, billDateMs, fuelAdjustments);
    const rateMultiplier = matchedAdjustment?.rateMultiplier ?? 1;
    const addThbPerTrip = matchedAdjustment?.addThbPerTrip ?? 0;
    const useFlatExtraStopFee = typeof extraStopFeeThb === "number" && extraStopFeeThb >= 0;

    let baseRateThb = 0;
    let stopChargeThb = 0;
    const stopBreakdown: MultiDeliveryStopBilling[] = [];

    if (useFlatExtraStopFee) {
        // Flat extra-stop fee mode: the trip's BASE rate comes from the PLANNED
        // destination (task.destination), independent of the order the stops were
        // delivered in. Every OTHER delivered stop is an extra stop charged the flat
        // fee (no rate-card lookup, no fuel multiplier).
        //
        // This is order-independent on purpose: deliveryStopsProgress[0] is whichever
        // stop the driver completed first, which is NOT necessarily the planned route.
        const normStops = deliveryStops.map((s) => normalizeDestinationCode(s.destination));
        const plannedDest = normalizeDestinationCode(task.destination ?? "");

        // Pick the base stop: prefer the delivered stop matching the planned
        // destination; otherwise fall back to the first delivered stop that has a
        // matching rate-card row.
        let baseIdx = plannedDest ? normStops.findIndex((d) => d === plannedDest) : -1;
        let baseDest = baseIdx >= 0 ? normStops[baseIdx] : plannedDest;
        let baseMatch = baseDest
            ? selectBillingRateEntry(customerId, hubId, baseDest, normalizedVehicleClass, billDateMs, rateEntries, jobCategory)
            : null;
        if (!baseMatch) {
            for (let i = 0; i < normStops.length; i++) {
                const m = selectBillingRateEntry(customerId, hubId, normStops[i], normalizedVehicleClass, billDateMs, rateEntries, jobCategory);
                if (m) { baseMatch = m; baseIdx = i; baseDest = normStops[i]; break; }
            }
        }
        if (!baseMatch) return null; // no rate card for any stop → cannot bill

        const baseFinal = computeFinalRateThb(baseMatch.rateThb, rateMultiplier, addThbPerTrip);
        baseRateThb = baseFinal;
        stopBreakdown.push({ stopIndex: 1, destination: baseDest, baseRateThb: baseMatch.rateThb, finalRateThb: baseFinal });

        // Exclude exactly one delivered stop (the base) from the extra-stop charges.
        const excludeIdx = baseIdx >= 0 ? baseIdx : 0;
        let extraSeq = 2;
        deliveryStops.forEach((_stop, idx) => {
            if (idx === excludeIdx) return;
            stopChargeThb += extraStopFeeThb as number;
            stopBreakdown.push({
                stopIndex: extraSeq++,
                destination: normStops[idx],
                baseRateThb: extraStopFeeThb as number,
                finalRateThb: extraStopFeeThb as number,
            });
        });
    } else {
        // Legacy per-route mode: stop[0] = base rate, stop[1+] = per-route rate-card lookup.
        deliveryStops.forEach((stop, idx) => {
            const destination = normalizeDestinationCode(stop.destination);
            const matchedRate = selectBillingRateEntry(
                customerId,
                hubId,
                destination,
                normalizedVehicleClass,
                billDateMs,
                rateEntries,
                jobCategory
            );
            if (!matchedRate) return;

            const baseRate = matchedRate.rateThb;
            const finalRate = computeFinalRateThb(baseRate, rateMultiplier, addThbPerTrip);
            if (idx === 0) {
                baseRateThb = finalRate;
            } else {
                stopChargeThb += finalRate;
            }
            stopBreakdown.push({
                stopIndex: idx + 1,
                destination,
                baseRateThb: baseRate,
                finalRateThb: finalRate,
            });
        });
    }

    if (baseRateThb === 0 || stopBreakdown.length === 0) return null;

    return {
        customerId,
        baseRateThb,
        stopChargeThb,
        totalBillingThb: baseRateThb + stopChargeThb,
        stopBreakdown,
        rateMultiplier,
        fuelAdjustmentId: matchedAdjustment?.id,
    };
}

// ─── Standby Billing ─────────────────────────────────────────────────────────

export interface StandbyRateEntry {
    id: string;
    customerId: string;
    /** Fixed THB charged per standby event (regardless of duration). */
    rateThb: number;
    effectiveFromMs: number;
    note?: string;
}

export interface StandbyBillingComputed {
    customerId: string;
    rateThb: number;
    rateEntryId: string;
    effectiveFromDateStr?: string;
}

/** Select the standby rate effective on or before billDateMs; fallback to oldest if none match. */
export function selectStandbyRateEntry(
    customerId: string,
    billDateMs: number,
    rateEntries: StandbyRateEntry[]
): StandbyRateEntry | null {
    const candidates = rateEntries.filter((e) => e.customerId === customerId);
    if (candidates.length === 0) return null;

    const effective = candidates
        .filter((e) => e.effectiveFromMs <= billDateMs)
        .sort((a, b) => b.effectiveFromMs - a.effectiveFromMs);
    if (effective.length > 0) return effective[0];

    // Fallback: use oldest rate if standby happened before all effective dates
    return candidates.sort((a, b) => a.effectiveFromMs - b.effectiveFromMs)[0];
}

/**
 * Compute standby billing for a completed standby record.
 * Fixed rate per event — does not depend on duration.
 */
export function computeStandbyBilling(
    billDateMs: number,
    customerId: string,
    rateEntries: StandbyRateEntry[]
): StandbyBillingComputed | null {
    if (!customerId) return null;
    const matched = selectStandbyRateEntry(customerId, billDateMs, rateEntries);
    if (!matched) return null;
    return {
        customerId,
        rateThb: matched.rateThb,
        rateEntryId: matched.id,
        effectiveFromDateStr: new Date(matched.effectiveFromMs).toISOString().slice(0, 10),
    };
}

export function computeTripBillingFromParts(
    trip: TripBillingTimestamps,
    task: TaskBillingInput | null | undefined,
    rateEntries: BillingRateEntry[],
    fuelAdjustments: FuelRateAdjustment[],
    jobCategory: JobCategory = "PRIMARY"
): TripBillingComputed | null {
    if (!task) return null;
    const customerId = resolveTaskCustomerId(task);
    if (!customerId) return null;

    const hubId = extractHubId(task.sourceHub);
    const destination = normalizeDestinationCode(task.destination);
    const vehicleClass = normalizeVehicleClass(task.truckType);
    const billDateMs = getTripBillingDateMs(trip);

    const matchedRate = selectBillingRateEntry(
        customerId,
        hubId,
        destination,
        vehicleClass,
        billDateMs,
        rateEntries,
        jobCategory
    );
    if (!matchedRate) return null;

    const matchedAdjustment = selectFuelAdjustmentForBillingDate(customerId, billDateMs, fuelAdjustments);

    const multiplier = matchedAdjustment?.rateMultiplier ?? 1;
    const addThbPerTrip = matchedAdjustment?.addThbPerTrip ?? 0;
    const finalRate = computeFinalRateThb(matchedRate.rateThb, multiplier, addThbPerTrip);

    return {
        customerId,
        baseRateThb: matchedRate.rateThb,
        finalRateThb: finalRate,
        rateImportId: matchedRate.importId,
        lookupHubId: hubId,
        lookupDestination: destination,
        fuelAdjustmentId: matchedAdjustment?.id,
        rateMultiplier: multiplier,
        addThbPerTrip,
        effectiveFromDateStr: matchedAdjustment
            ? new Date(matchedAdjustment.effectiveFromMs).toISOString().slice(0, 10)
            : undefined,
    };
}
