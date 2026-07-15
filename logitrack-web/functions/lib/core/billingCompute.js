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
exports.selectStandbyRateEntry = selectStandbyRateEntry;
exports.computeStandbyBilling = computeStandbyBilling;
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
    // SPK codes: extract just the code part (e.g., "SPK890103 - ลาดกระบัง26" or "SPK890103-ลาดกระบัง26" → "SPK890103")
    const dashIdx = u.indexOf("-");
    if (dashIdx > 0) {
        return u.slice(0, dashIdx).trim();
    }
    return u;
}
/**
 * Folds every spelling a vehicle class was ever stored in onto the class a task carries today
 * (TASK_TRUCK_TYPE_ENUM: 4W, 4WJ, 6WH, 10WH, 18WH, VAN).
 *
 * Both sides of the rate lookup pass through here, so a rate entry only matches a trip when the two
 * land on the same code. Rate cards written from the old truck-master dropdown ("Pickup", "6 Wheels")
 * and from an earlier normalize pass ("6W", "10W") therefore matched nothing at all — the codes they
 * produced are not in the enum. They are mapped here rather than left to rot, since an unmatched
 * rate card shows up as "No rate" on a trip that does have a price agreed.
 *
 * PICKUP and 4WH are the pre-2026-07 names for 4W.
 */
function normalizeVehicleClass(vehicleClass) {
    const u = normalizeCode(vehicleClass || "4WJ");
    if (!u)
        return "4WJ";
    const mapping = {
        "PICKUP": "4W",
        "4WH": "4W",
        "4 WHEELS": "4WJ",
        "4 WHEELS JUMBO": "4WJ",
        "6 WHEELS": "6WH",
        "6W": "6WH",
        "10 WHEELS": "10WH",
        "10W": "10WH",
        "18 WHEELS": "18WH",
        "18W": "18WH",
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
function selectBillingRateEntry(customerId, hubId, destinationCode, vehicleClass, billDateMs, rateEntries, jobCategory = "PRIMARY") {
    const normalizedVehicleClass = normalizeVehicleClass(vehicleClass);
    const candidates = rateEntries.filter((entry) => entry.customerId === customerId &&
        entry.hubId === hubId &&
        entry.destinationCode === destinationCode &&
        normalizeVehicleClass(entry.vehicleClass) === normalizedVehicleClass &&
        (entry.jobCategory ?? "PRIMARY") === jobCategory);
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
function computeMultiDeliveryBilling(trip, task, deliveryStops, vehicleClass, rateEntries, fuelAdjustments, extraStopFeeThb, jobCategory = "PRIMARY") {
    if (!task || deliveryStops.length < 2)
        return null;
    const customerId = resolveTaskCustomerId(task);
    if (!customerId)
        return null;
    const hubId = extractHubId(task.sourceHub);
    const normalizedVehicleClass = normalizeVehicleClass(vehicleClass || "4WJ");
    const billDateMs = getTripBillingDateMs(trip);
    // Supplementary (เสริม) rate cards are fixed, separately-agreed prices (ADR-0005) —
    // they never move with primary fuel-rate adjustments.
    const matchedAdjustment = jobCategory === "SUPPLEMENTARY"
        ? null
        : selectFuelAdjustmentForBillingDate(customerId, billDateMs, fuelAdjustments);
    const rateMultiplier = matchedAdjustment?.rateMultiplier ?? 1;
    const addThbPerTrip = matchedAdjustment?.addThbPerTrip ?? 0;
    const useFlatExtraStopFee = typeof extraStopFeeThb === "number" && extraStopFeeThb >= 0;
    let baseRateThb = 0;
    let stopChargeThb = 0;
    const stopBreakdown = [];
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
                if (m) {
                    baseMatch = m;
                    baseIdx = i;
                    baseDest = normStops[i];
                    break;
                }
            }
        }
        if (!baseMatch)
            return null; // no rate card for any stop → cannot bill
        const baseFinal = computeFinalRateThb(baseMatch.rateThb, rateMultiplier, addThbPerTrip);
        baseRateThb = baseFinal;
        stopBreakdown.push({ stopIndex: 1, destination: baseDest, baseRateThb: baseMatch.rateThb, finalRateThb: baseFinal });
        // Exclude exactly one delivered stop (the base) from the extra-stop charges.
        const excludeIdx = baseIdx >= 0 ? baseIdx : 0;
        let extraSeq = 2;
        deliveryStops.forEach((_stop, idx) => {
            if (idx === excludeIdx)
                return;
            stopChargeThb += extraStopFeeThb;
            stopBreakdown.push({
                stopIndex: extraSeq++,
                destination: normStops[idx],
                baseRateThb: extraStopFeeThb,
                finalRateThb: extraStopFeeThb,
            });
        });
    }
    else {
        // Legacy per-route mode: stop[0] = base rate, stop[1+] = per-route rate-card lookup.
        deliveryStops.forEach((stop, idx) => {
            const destination = normalizeDestinationCode(stop.destination);
            const matchedRate = selectBillingRateEntry(customerId, hubId, destination, normalizedVehicleClass, billDateMs, rateEntries, jobCategory);
            if (!matchedRate)
                return;
            const baseRate = matchedRate.rateThb;
            const finalRate = computeFinalRateThb(baseRate, rateMultiplier, addThbPerTrip);
            if (idx === 0) {
                baseRateThb = finalRate;
            }
            else {
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
/** Select the standby rate effective on or before billDateMs; fallback to oldest if none match. */
function selectStandbyRateEntry(customerId, billDateMs, rateEntries) {
    const candidates = rateEntries.filter((e) => e.customerId === customerId);
    if (candidates.length === 0)
        return null;
    const effective = candidates
        .filter((e) => e.effectiveFromMs <= billDateMs)
        .sort((a, b) => b.effectiveFromMs - a.effectiveFromMs);
    if (effective.length > 0)
        return effective[0];
    // Fallback: use oldest rate if standby happened before all effective dates
    return candidates.sort((a, b) => a.effectiveFromMs - b.effectiveFromMs)[0];
}
/**
 * Compute standby billing for a completed standby record.
 * Fixed rate per event — does not depend on duration.
 */
function computeStandbyBilling(billDateMs, customerId, rateEntries) {
    if (!customerId)
        return null;
    const matched = selectStandbyRateEntry(customerId, billDateMs, rateEntries);
    if (!matched)
        return null;
    return {
        customerId,
        rateThb: matched.rateThb,
        rateEntryId: matched.id,
        effectiveFromDateStr: new Date(matched.effectiveFromMs).toISOString().slice(0, 10),
    };
}
function computeTripBillingFromParts(trip, task, rateEntries, fuelAdjustments, jobCategory = "PRIMARY") {
    if (!task)
        return null;
    const customerId = resolveTaskCustomerId(task);
    if (!customerId)
        return null;
    const hubId = extractHubId(task.sourceHub);
    const destination = normalizeDestinationCode(task.destination);
    const vehicleClass = normalizeVehicleClass(task.truckType || "4WJ");
    const billDateMs = getTripBillingDateMs(trip);
    const matchedRate = selectBillingRateEntry(customerId, hubId, destination, vehicleClass, billDateMs, rateEntries, jobCategory);
    if (!matchedRate)
        return null;
    // Supplementary (เสริม) rate cards are fixed, separately-agreed prices (ADR-0005) —
    // they never move with primary fuel-rate adjustments.
    const matchedAdjustment = jobCategory === "SUPPLEMENTARY"
        ? null
        : selectFuelAdjustmentForBillingDate(customerId, billDateMs, fuelAdjustments);
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