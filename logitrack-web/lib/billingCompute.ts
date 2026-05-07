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
}

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
    // SPK codes: extract just the code part (e.g., "SPK890103-ลาดกระบัง26" → "SPK890103")
    const dashIdx = u.indexOf("-");
    if (dashIdx > 0) {
        return u.slice(0, dashIdx);
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
    rateEntries: BillingRateEntry[]
): BillingRateEntry | null {
    const candidates = rateEntries.filter(
        (entry) =>
            entry.customerId === customerId &&
            entry.hubId === hubId &&
            entry.destinationCode === destinationCode &&
            entry.vehicleClass === vehicleClass
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
    dropFeeThb: number;
    finalRateThb: number;
}

export interface MultiDeliveryBillingResult {
    customerId: string;
    totalBillingThb: number;
    stopBreakdown: MultiDeliveryStopBilling[];
    rateMultiplier: number;
    fuelAdjustmentId?: string;
}

/** Compute billing for multi-delivery task: per-stop rates + drop fees. */
export function computeMultiDeliveryBilling(
    trip: TripBillingTimestamps,
    task: TaskBillingInput | null | undefined,
    deliveryStops: DeliveryStopForBilling[],
    vehicleClass: string,
    dropFeeThb: number,
    rateEntries: BillingRateEntry[],
    fuelAdjustments: FuelRateAdjustment[]
): MultiDeliveryBillingResult | null {
    if (!task || deliveryStops.length < 2) return null;
    const customerId = resolveTaskCustomerId(task);
    if (!customerId) return null;

    const hubId = extractHubId(task.sourceHub);
    const normalizedVehicleClass = normalizeVehicleClass(vehicleClass);
    const billDateMs = getTripBillingDateMs(trip);
    const matchedAdjustment = selectFuelAdjustmentForBillingDate(customerId, billDateMs, fuelAdjustments);
    const rateMultiplier = matchedAdjustment?.rateMultiplier ?? 1;

    const stopBreakdown: MultiDeliveryStopBilling[] = [];
    let totalBillingThb = 0;

    deliveryStops.forEach((stop, idx) => {
        const destination = normalizeDestinationCode(stop.destination);
        const matchedRate = selectBillingRateEntry(
            customerId,
            hubId,
            destination,
            normalizedVehicleClass,
            billDateMs,
            rateEntries
        );

        if (matchedRate) {
            const baseRateThb = matchedRate.rateThb;
            const fee = idx === 0 ? 0 : dropFeeThb;
            const finalRate = computeFinalRateThb(baseRateThb + fee, rateMultiplier, 0);
            stopBreakdown.push({
                stopIndex: idx + 1,
                destination,
                baseRateThb,
                dropFeeThb: fee,
                finalRateThb: finalRate,
            });
            totalBillingThb += finalRate;
        }
    });

    if (stopBreakdown.length === 0) return null;

    return {
        customerId,
        totalBillingThb,
        stopBreakdown,
        rateMultiplier,
        fuelAdjustmentId: matchedAdjustment?.id,
    };
}

export function computeTripBillingFromParts(
    trip: TripBillingTimestamps,
    task: TaskBillingInput | null | undefined,
    rateEntries: BillingRateEntry[],
    fuelAdjustments: FuelRateAdjustment[]
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
        rateEntries
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
