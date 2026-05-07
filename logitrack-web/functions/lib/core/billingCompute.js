"use strict";
/**
 * Pure per-trip billing math (no Firebase). Used by web admin and Cloud Functions.
 * Duplicated from `logitrack-web/lib/billingCompute.ts` — keep in sync when changing either file.
 */
Object.defineProperty(exports, "__esModule", { value: true });
exports.extractHubId = extractHubId;
exports.normalizeDestinationCode = normalizeDestinationCode;
exports.normalizeVehicleClass = normalizeVehicleClass;
exports.timestampLikeToMillis = timestampLikeToMillis;
exports.getTripBillingDateMs = getTripBillingDateMs;
exports.resolveTaskCustomerId = resolveTaskCustomerId;
exports.selectBillingRateEntry = selectBillingRateEntry;
exports.selectFuelAdjustmentForBillingDate = selectFuelAdjustmentForBillingDate;
exports.computeFinalRateThb = computeFinalRateThb;
exports.computeMultiDeliveryBilling = computeMultiDeliveryBilling;
exports.computeTripBillingFromParts = computeTripBillingFromParts;
function normalizeCode(v) {
    return (v ?? "").trim().toUpperCase();
}
function extractHubId(sourceHub) {
    const raw = (sourceHub ?? "").trim();
    if (!raw)
        return "";
    const code = raw.split(" - ")[0]?.trim() ?? raw;
    return normalizeCode(code);
}
function normalizeDestinationCode(destination) {
    const u = normalizeCode(destination);
    if (!u)
        return "";
    if (u.startsWith("SOCE"))
        return "SOCE";
    if (u.startsWith("SOCN"))
        return "SOCN";
    if (u.startsWith("SOCW"))
        return "SOCW";
    // SPK codes: extract just the code part (e.g., "SPK890103-ลาดกระบัง26" → "SPK890103")
    const dashIdx = u.indexOf("-");
    if (dashIdx > 0) {
        return u.slice(0, dashIdx);
    }
    return u;
}
function normalizeVehicleClass(vehicleClass) {
    const u = normalizeCode(vehicleClass || "4WJ");
    if (!u)
        return "4WJ";
    // Map full truck type names to short codes
    const mapping = {
        "4 WHEELS JUMBO": "4WJ",
        "6 WHEELS": "6W",
        "10 WHEELS": "10W",
        "2 WHEELS": "2W",
    };
    return mapping[u] ?? u;
}
/** Milliseconds from Firestore Timestamp, Date, number, or similar. */
function timestampLikeToMillis(val) {
    if (!val)
        return 0;
    if (typeof val === "number" && Number.isFinite(val))
        return val;
    if (val instanceof Date)
        return val.getTime();
    if (typeof val.toMillis === "function") {
        return val.toMillis();
    }
    if (typeof val.toDate === "function") {
        return val.toDate().getTime();
    }
    return 0;
}
function getTripBillingDateMs(trip) {
    const delivered = timestampLikeToMillis(trip.deliveredTimestamp);
    if (delivered > 0)
        return delivered;
    const created = timestampLikeToMillis(trip.createdAt);
    if (created > 0)
        return created;
    return Date.now();
}
function resolveTaskCustomerId(task) {
    return (task?.sourceHubLinkedCustomerId?.trim() ||
        task?.destinationLinkedCustomerId?.trim() ||
        "");
}
function selectBillingRateEntry(customerId, hubId, destinationCode, vehicleClass, billDateMs, rateEntries) {
    const candidates = rateEntries.filter((entry) => entry.customerId === customerId &&
        entry.hubId === hubId &&
        entry.destinationCode === destinationCode &&
        entry.vehicleClass === vehicleClass);
    if (candidates.length === 0)
        return null;
    // Primary: effective on or before trip date (newest first)
    const effective = candidates
        .filter((e) => e.effectiveFromMs <= billDateMs)
        .sort((a, b) => b.effectiveFromMs - a.effectiveFromMs);
    if (effective.length > 0)
        return effective[0];
    // Fallback: trip is before all effective dates → use oldest rate card
    const oldest = candidates.sort((a, b) => a.effectiveFromMs - b.effectiveFromMs);
    return oldest[0];
}
function selectFuelAdjustmentForBillingDate(customerId, billDateMs, fuelAdjustments) {
    const matched = fuelAdjustments
        .filter((adj) => adj.customerId === customerId && adj.effectiveFromMs <= billDateMs)
        .sort((a, b) => b.effectiveFromMs - a.effectiveFromMs);
    return matched[0] ?? null;
}
/** Final trip rate in THB, rounded to 2 decimal places (same as legacy billing snapshot). */
function computeFinalRateThb(baseRateThb, rateMultiplier, addThbPerTrip) {
    return Math.round((baseRateThb * rateMultiplier + addThbPerTrip) * 100) / 100;
}
/** Compute billing for multi-delivery task: charge stop 3+ only (A → each destination). */
function computeMultiDeliveryBilling(trip, task, deliveryStops, vehicleClass, rateEntries, fuelAdjustments) {
    if (!task || deliveryStops.length < 3)
        return null;
    const customerId = resolveTaskCustomerId(task);
    if (!customerId)
        return null;
    const hubId = extractHubId(task.sourceHub);
    const normalizedVehicleClass = normalizeVehicleClass(vehicleClass || "4WJ");
    const billDateMs = getTripBillingDateMs(trip);
    const matchedAdjustment = selectFuelAdjustmentForBillingDate(customerId, billDateMs, fuelAdjustments);
    const rateMultiplier = matchedAdjustment?.rateMultiplier ?? 1;
    const addThbPerTrip = matchedAdjustment?.addThbPerTrip ?? 0;
    let baseRateThb = 0;
    let stopChargeThb = 0;
    const stopBreakdown = [];
    deliveryStops.forEach((stop, idx) => {
        const destination = normalizeDestinationCode(stop.destination);
        const matchedRate = selectBillingRateEntry(customerId, hubId, destination, normalizedVehicleClass, billDateMs, rateEntries);
        if (!matchedRate)
            return;
        const baseRate = matchedRate.rateThb;
        const finalRate = computeFinalRateThb(baseRate, rateMultiplier, addThbPerTrip);
        if (idx === 0) {
            // Stop 1: set baseRateThb
            baseRateThb = finalRate;
            stopBreakdown.push({
                stopIndex: 1,
                destination,
                baseRateThb: baseRate,
                finalRateThb: finalRate,
            });
        }
        else if (idx >= 2) {
            // Stop 3+: add to stopChargeThb
            stopChargeThb += finalRate;
            stopBreakdown.push({
                stopIndex: idx + 1,
                destination,
                baseRateThb: baseRate,
                finalRateThb: finalRate,
            });
        }
    });
    if (baseRateThb === 0 || stopBreakdown.length === 0)
        return null;
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
function computeTripBillingFromParts(trip, task, rateEntries, fuelAdjustments) {
    if (!task)
        return null;
    const customerId = resolveTaskCustomerId(task);
    if (!customerId)
        return null;
    const hubId = extractHubId(task.sourceHub);
    const destination = normalizeDestinationCode(task.destination);
    const vehicleClass = normalizeVehicleClass(task.truckType || "4WJ");
    const billDateMs = getTripBillingDateMs(trip);
    const matchedRate = selectBillingRateEntry(customerId, hubId, destination, vehicleClass, billDateMs, rateEntries);
    if (!matchedRate)
        return null;
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
//# sourceMappingURL=billingCompute.js.map