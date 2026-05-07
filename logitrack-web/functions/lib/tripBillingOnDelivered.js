"use strict";
var __createBinding = (this && this.__createBinding) || (Object.create ? (function(o, m, k, k2) {
    if (k2 === undefined) k2 = k;
    var desc = Object.getOwnPropertyDescriptor(m, k);
    if (!desc || ("get" in desc ? !m.__esModule : desc.writable || desc.configurable)) {
      desc = { enumerable: true, get: function() { return m[k]; } };
    }
    Object.defineProperty(o, k2, desc);
}) : (function(o, m, k, k2) {
    if (k2 === undefined) k2 = k;
    o[k2] = m[k];
}));
var __setModuleDefault = (this && this.__setModuleDefault) || (Object.create ? (function(o, v) {
    Object.defineProperty(o, "default", { enumerable: true, value: v });
}) : function(o, v) {
    o["default"] = v;
});
var __importStar = (this && this.__importStar) || (function () {
    var ownKeys = function(o) {
        ownKeys = Object.getOwnPropertyNames || function (o) {
            var ar = [];
            for (var k in o) if (Object.prototype.hasOwnProperty.call(o, k)) ar[ar.length] = k;
            return ar;
        };
        return ownKeys(o);
    };
    return function (mod) {
        if (mod && mod.__esModule) return mod;
        var result = {};
        if (mod != null) for (var k = ownKeys(mod), i = 0; i < k.length; i++) if (k[i] !== "default") __createBinding(result, mod, k[i]);
        __setModuleDefault(result, mod);
        return result;
    };
})();
Object.defineProperty(exports, "__esModule", { value: true });
exports.normalizeRateEntryVehicleClasses = exports.backfillTripBillingSnapshots = exports.computeTripBillingSnapshot = void 0;
const admin = __importStar(require("firebase-admin"));
const firebase_functions_1 = require("firebase-functions");
const https_1 = require("firebase-functions/v2/https");
const billingCompute_1 = require("./core/billingCompute");
const COL_TASKS = "tasks";
const COL_RATE_ENTRIES = "customer_rate_entries";
const COL_FUEL_ADJ = "customer_fuel_rate_adjustments";
const COL_TRIP_RECORDS = "trip_records";
function normalizeStoredCode(v) {
    return (v ?? "").trim().toUpperCase();
}
function mapRateDoc(customerId, doc) {
    const d = doc.data();
    return {
        id: doc.id,
        customerId,
        importId: String(d.importId ?? ""),
        hubId: normalizeStoredCode(String(d.hubId ?? "")),
        destinationCode: (0, billingCompute_1.normalizeDestinationCode)(String(d.destinationCode ?? "")),
        vehicleClass: normalizeStoredCode(String(d.vehicleClass ?? "4WJ")),
        rateThb: Number(d.rateThb ?? 0),
        effectiveFromMs: (0, billingCompute_1.timestampLikeToMillis)(d.effectiveFrom),
    };
}
function mapFuelDoc(customerId, doc) {
    const d = doc.data();
    return {
        id: doc.id,
        customerId,
        effectiveFromMs: (0, billingCompute_1.timestampLikeToMillis)(d.effectiveFrom),
        rateMultiplier: Number(d.rateMultiplier ?? 1),
        addThbPerTrip: Number(d.addThbPerTrip ?? 0),
    };
}
/** Shared core: persist billing snapshot from already-read trip fields (idempotent). */
async function tryWriteBillingSnapshotFromTripData(db, tripId, data, tripRef) {
    if (data.status !== "delivered") {
        return { ok: true, skipped: true, error: "Trip is not delivered yet" };
    }
    if (typeof data.billingEstimateThb === "number") {
        return { ok: true, skipped: true, billingEstimateThb: data.billingEstimateThb };
    }
    const taskId = typeof data.taskId === "string" ? data.taskId.trim() : "";
    if (!taskId) {
        firebase_functions_1.logger.warn("[billingSnapshot] delivered trip missing taskId", { tripId });
        return { ok: false, error: "Trip missing taskId" };
    }
    let taskSnap;
    try {
        taskSnap = await db.collection(COL_TASKS).doc(taskId).get();
    }
    catch (e) {
        firebase_functions_1.logger.error("[billingSnapshot] task load failed", e);
        return { ok: false, error: "Failed to load task" };
    }
    if (!taskSnap.exists) {
        firebase_functions_1.logger.warn("[billingSnapshot] task not found", { taskId, tripId });
        return { ok: false, error: "Task not found" };
    }
    const t = taskSnap.data();
    const taskInput = {
        sourceHub: typeof t.sourceHub === "string" ? t.sourceHub : undefined,
        destination: typeof t.destination === "string" ? t.destination : undefined,
        truckType: typeof t.truckType === "string" ? t.truckType : undefined,
        sourceHubLinkedCustomerId: typeof t.sourceHubLinkedCustomerId === "string" ? t.sourceHubLinkedCustomerId : undefined,
        destinationLinkedCustomerId: typeof t.destinationLinkedCustomerId === "string" ? t.destinationLinkedCustomerId : undefined,
    };
    const customerId = taskInput.sourceHubLinkedCustomerId?.trim() ||
        taskInput.destinationLinkedCustomerId?.trim() ||
        "";
    if (!customerId) {
        firebase_functions_1.logger.warn("[billingSnapshot] task has no linked customer", { taskId, tripId });
        return { ok: false, error: "Task has no linked customer" };
    }
    const [rateSnap, fuelSnap] = await Promise.all([
        db.collection(COL_RATE_ENTRIES).where("customerId", "==", customerId).get(),
        db.collection(COL_FUEL_ADJ).where("customerId", "==", customerId).get(),
    ]);
    const rateEntries = rateSnap.docs.map((d) => mapRateDoc(customerId, d));
    const fuelAdjustments = fuelSnap.docs.map((d) => mapFuelDoc(customerId, d));
    const tripParts = {
        deliveredTimestamp: data.deliveredTimestamp,
        createdAt: data.createdAt,
    };
    // Check if this is a multi-delivery trip
    const isMultiDelivery = data.isMultiDelivery === true;
    const deliveryStopsProgress = Array.isArray(data.deliveryStopsProgress) ? data.deliveryStopsProgress : [];
    if (isMultiDelivery && deliveryStopsProgress.length >= 3) {
        // Multi-delivery billing: charge stop 3+ only
        const stops = deliveryStopsProgress
            .filter((stop) => stop.destination && stop.status === "delivered")
            .map((stop) => ({
            index: typeof stop.index === "number" ? stop.index : 1,
            destination: String(stop.destination ?? ""),
        }));
        if (stops.length < 3) {
            firebase_functions_1.logger.warn("[billingSnapshot] multi-delivery trip has < 3 delivered stops", {
                tripId,
                taskId,
                delivered: stops.length,
            });
            return { ok: false, error: "Multi-delivery trip has < 3 delivered stops" };
        }
        const multiComputed = (0, billingCompute_1.computeMultiDeliveryBilling)(tripParts, taskInput, stops, (0, billingCompute_1.normalizeVehicleClass)(taskInput.truckType || "4WJ"), rateEntries, fuelAdjustments);
        if (!multiComputed) {
            firebase_functions_1.logger.warn("[billingSnapshot] could not compute multi-delivery billing", {
                tripId,
                taskId,
                customerId,
                stops: stops.length,
            });
            return { ok: false, error: "Could not compute multi-delivery billing" };
        }
        await tripRef.update({
            billingEstimateThb: multiComputed.totalBillingThb,
            billingBaseRateThb: multiComputed.baseRateThb,
            billingStopChargeThb: multiComputed.stopChargeThb,
            billingIsMultiDelivery: true,
            billingMultiDeliveryBreakdown: multiComputed.stopBreakdown.map((stop) => ({
                stopIndex: stop.stopIndex,
                destination: stop.destination,
                baseRateThb: stop.baseRateThb,
                finalRateThb: stop.finalRateThb,
            })),
            billingFuelAdjustmentId: multiComputed.fuelAdjustmentId ?? null,
            billingRateMultiplier: multiComputed.rateMultiplier,
            billingCustomerId: multiComputed.customerId,
            updatedAt: admin.firestore.FieldValue.serverTimestamp(),
        });
        return { ok: true, billingEstimateThb: multiComputed.totalBillingThb };
    }
    else {
        // Single-delivery billing (existing logic)
        const computed = (0, billingCompute_1.computeTripBillingFromParts)(tripParts, taskInput, rateEntries, fuelAdjustments);
        if (!computed) {
            const hubId = (0, billingCompute_1.extractHubId)(taskInput.sourceHub);
            const destination = (0, billingCompute_1.normalizeDestinationCode)(taskInput.destination);
            const vehicleClass = normalizeStoredCode(taskInput.truckType || "4WJ");
            const billDateMs = (0, billingCompute_1.getTripBillingDateMs)(tripParts);
            firebase_functions_1.logger.warn("[billingSnapshot] could not compute billing (no matching rate row)", {
                tripId,
                taskId,
                customerId,
                hubId,
                destination,
                vehicleClass,
                billDateMs,
                rateRowsForCustomer: rateEntries.length,
                rawTaskSourceHub: taskInput.sourceHub,
                rawTaskDestination: taskInput.destination,
            });
            return { ok: false, error: "No matching rate entry found" };
        }
        await tripRef.update({
            billingEstimateThb: computed.finalRateThb,
            billingBaseRateThb: computed.baseRateThb,
            billingRateImportId: computed.rateImportId,
            billingLookupHubId: computed.lookupHubId,
            billingLookupDestination: computed.lookupDestination,
            billingFuelAdjustmentId: computed.fuelAdjustmentId ?? null,
            billingRateMultiplier: computed.rateMultiplier,
            billingAddThbPerTrip: computed.addThbPerTrip,
            billingEffectiveFromDateStr: computed.effectiveFromDateStr ?? null,
            billingCustomerId: computed.customerId,
            updatedAt: admin.firestore.FieldValue.serverTimestamp(),
        });
        return { ok: true, billingEstimateThb: computed.finalRateThb };
    }
}
/**
 * HTTPS Callable: compute and persist billing snapshot for a delivered trip.
 *
 * Call from mobile after marking trip as `delivered`, or from web admin backfill.
 * Idempotent: if `billingEstimateThb` already exists, returns `skipped: true`.
 */
exports.computeTripBillingSnapshot = (0, https_1.onCall)({
    region: "asia-southeast1",
    enforceAppCheck: true,
    consumeAppCheckToken: true,
}, async (request) => {
    const tripId = request.data?.tripId?.trim();
    if (!tripId) {
        throw new https_1.HttpsError("invalid-argument", "tripId is required");
    }
    const db = admin.firestore();
    const tripRef = db.collection(COL_TRIP_RECORDS).doc(tripId);
    const tripSnap = await tripRef.get();
    if (!tripSnap.exists) {
        throw new https_1.HttpsError("not-found", "Trip not found");
    }
    const data = tripSnap.data();
    return tryWriteBillingSnapshotFromTripData(db, tripId, data, tripRef);
});
function bangkokBoundsToTimestamps(fromDateStr, toDateStr) {
    const start = new Date(`${fromDateStr.trim()}T00:00:00+07:00`);
    const end = new Date(`${toDateStr.trim()}T23:59:59.999+07:00`);
    if (Number.isNaN(start.getTime()) || Number.isNaN(end.getTime()) || start > end) {
        throw new https_1.HttpsError("invalid-argument", "Invalid fromDateStr / toDateStr");
    }
    return {
        start: admin.firestore.Timestamp.fromDate(start),
        end: admin.firestore.Timestamp.fromDate(end),
    };
}
/**
 * Admin-only: scan trip_records by createdAt (Bangkok calendar range), write billing snapshots
 * for delivered trips missing billingEstimateThb (same rules as computeTripBillingSnapshot).
 */
exports.backfillTripBillingSnapshots = (0, https_1.onCall)({
    region: "asia-southeast1",
    enforceAppCheck: false, // Admin-only function protected by auth check; App Check enforcement disabled to avoid web client token issues
}, async (request) => {
    if (request.auth?.token?.admin !== true) {
        throw new https_1.HttpsError("permission-denied", "Admin only");
    }
    const fromDateStr = request.data?.fromDateStr?.trim() ?? "";
    const toDateStr = request.data?.toDateStr?.trim() ?? "";
    if (!fromDateStr || !toDateStr) {
        throw new https_1.HttpsError("invalid-argument", "fromDateStr and toDateStr are required (yyyy-MM-dd)");
    }
    const maxScan = Math.min(Math.max(1, request.data?.maxScan ?? 500), 2000);
    const maxWrite = Math.min(Math.max(1, request.data?.maxWrite ?? 200), 500);
    const db = admin.firestore();
    const { start, end } = bangkokBoundsToTimestamps(fromDateStr, toDateStr);
    const snap = await db
        .collection(COL_TRIP_RECORDS)
        .where("createdAt", ">=", start)
        .where("createdAt", "<=", end)
        .orderBy("createdAt", "desc")
        .limit(maxScan)
        .get();
    let eligible = 0;
    let written = 0;
    let skipped = 0;
    let failed = 0;
    let attempted = 0;
    const failures = [];
    for (const doc of snap.docs) {
        const data = doc.data();
        if (data.status !== "delivered")
            continue;
        if (typeof data.billingEstimateThb === "number")
            continue;
        eligible++;
        if (written >= maxWrite)
            continue;
        attempted++;
        const result = await tryWriteBillingSnapshotFromTripData(db, doc.id, data, doc.ref);
        if (result.ok === true && result.skipped !== true && result.billingEstimateThb != null) {
            written++;
        }
        else if (result.skipped === true) {
            skipped++;
        }
        else {
            failed++;
            if (failures.length < 25) {
                failures.push({ tripId: doc.id, error: result.error });
            }
        }
    }
    return {
        scanned: snap.size,
        eligible,
        written,
        skipped,
        failed,
        failures,
        capped: eligible > attempted,
    };
});
/**
 * Admin-only: normalize vehicle class values in all customer_rate_entries.
 * Converts full names (e.g., "4 WHEELS JUMBO") to short codes (e.g., "4WJ").
 */
exports.normalizeRateEntryVehicleClasses = (0, https_1.onCall)({
    region: "asia-southeast1",
    enforceAppCheck: false,
}, async (request) => {
    if (request.auth?.token?.admin !== true) {
        throw new https_1.HttpsError("permission-denied", "Admin only");
    }
    const maxScan = Math.min(Math.max(1, request.data?.maxScan ?? 5000), 10000);
    const maxUpdate = Math.min(Math.max(1, request.data?.maxUpdate ?? 500), 1000);
    const db = admin.firestore();
    const snap = await db.collection(COL_RATE_ENTRIES).limit(maxScan).get();
    let needsUpdate = 0;
    let updated = 0;
    const samples = [];
    for (const doc of snap.docs) {
        const data = doc.data();
        const old = String(data.vehicleClass ?? "4WJ").trim().toUpperCase();
        const normalized = (0, billingCompute_1.normalizeVehicleClass)(old);
        if (old !== normalized) {
            needsUpdate++;
            if (updated < maxUpdate) {
                await doc.ref.update({ vehicleClass: normalized });
                updated++;
                if (samples.length < 10) {
                    samples.push({ docId: doc.id, old, new: normalized });
                }
            }
        }
    }
    return {
        scanned: snap.size,
        needsUpdate,
        updated,
        samples,
        capped: needsUpdate > updated,
    };
});
//# sourceMappingURL=tripBillingOnDelivered.js.map