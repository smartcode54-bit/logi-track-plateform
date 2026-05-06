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
exports.addDeliveryStop = exports.submitDeliveryStopProgress = void 0;
/**
 * Cloud Functions for multi-delivery trip handling.
 */
const admin = __importStar(require("firebase-admin"));
const https_1 = require("firebase-functions/v2/https");
const firebase_functions_1 = require("firebase-functions");
const COL_TRIP_RECORDS = "trip_records";
const COL_TASKS = "tasks";
const COL_DRIVERS = "drivers";
/** Drivers may append stops only to tasks assigned to them (driverId matches auth UID or drivers/{id}.authId). */
async function callerOwnsAssignedTask(db, uid, task) {
    const driverId = task.driverId;
    // If task has driverId, check it
    if (driverId && typeof driverId === "string") {
        if (driverId === uid) {
            return true;
        }
        const snap = await db.collection(COL_DRIVERS).doc(driverId).get();
        if (snap.exists && snap.data()?.authId === uid) {
            return true;
        }
    }
    // Fallback: check if current driver (via authId) is assigned to this task via driverPhone or licensePlate
    const currentDriver = await db.collection(COL_DRIVERS)
        .where("authId", "==", uid)
        .limit(1)
        .get();
    if (currentDriver.empty) {
        return false;
    }
    const curDriver = currentDriver.docs[0].data();
    const driverPhone = task.driverPhone;
    const licensePlate = task.licensePlate;
    // If task has driverPhone or licensePlate matching current driver's record, allow it
    if (driverPhone && driverPhone === curDriver.phone) {
        return true;
    }
    if (licensePlate && licensePlate === curDriver.licensePlate) {
        return true;
    }
    return false;
}
function normalizeDestination(raw) {
    return typeof raw === "string" ? raw.trim().toUpperCase() : "";
}
function primaryStopRowFromTask(task, primaryDestination) {
    const row = {
        index: 1,
        destination: primaryDestination,
        status: "pending",
        sequence: 0,
    };
    const id = task.destinationLinkedCustomerId;
    if (typeof id === "string" && id.trim()) {
        row.destinationLinkedCustomerId = id.trim();
    }
    const name = task.destinationLinkedCustomerName;
    if (typeof name === "string" && name.trim()) {
        row.destinationLinkedCustomerName = name.trim();
    }
    const code = task.destinationLinkedCustomerCode;
    if (typeof code === "string" && code.trim()) {
        row.destinationLinkedCustomerCode = code.trim();
    }
    const kind = task.destinationCustomerLinkKind;
    if (kind === "customer" || kind === "partner") {
        row.destinationCustomerLinkKind = kind;
    }
    return row;
}
function nextStopIndex(currentStops) {
    const maxIx = currentStops.reduce((m, s) => {
        const ix = typeof s.index === "number" ? s.index : 0;
        return Math.max(m, ix);
    }, 0);
    return maxIx > 0 ? maxIx + 1 : currentStops.length + 1;
}
function destinationsConflict(stops, normalizedNew) {
    return stops.some((s) => normalizeDestination(s.destination) === normalizedNew);
}
/**
 * Callable: Submit delivery progress for a single stop in a multi-delivery trip.
 * Called by mobile driver after delivering to one of multiple stops.
 *
 * Appends to trip_record.deliveryStopsProgress array (idempotent: only if not already marked delivered).
 * If all stops are now delivered, marks task as "Completed".
 */
exports.submitDeliveryStopProgress = (0, https_1.onCall)({ enforceAppCheck: false }, async (request) => {
    if (!request.auth) {
        throw new https_1.HttpsError("unauthenticated", "User must be authenticated");
    }
    const data = request.data;
    // Validate required fields
    if (!data.tripId) {
        throw new https_1.HttpsError("invalid-argument", "tripId is required");
    }
    if (typeof data.stopIndex !== "number" || data.stopIndex < 1) {
        throw new https_1.HttpsError("invalid-argument", "stopIndex must be a positive number");
    }
    if (!data.destination) {
        throw new https_1.HttpsError("invalid-argument", "destination is required");
    }
    if (!Array.isArray(data.photos) || data.photos.length === 0) {
        throw new https_1.HttpsError("invalid-argument", "at least 1 photo is required");
    }
    if (typeof data.deliveredLat !== "number" || typeof data.deliveredLng !== "number") {
        throw new https_1.HttpsError("invalid-argument", "deliveredLat and deliveredLng are required");
    }
    const db = admin.firestore();
    const tripRef = db.collection(COL_TRIP_RECORDS).doc(data.tripId);
    try {
        const tripSnap = await tripRef.get();
        if (!tripSnap.exists) {
            throw new https_1.HttpsError("not-found", "Trip not found");
        }
        const trip = tripSnap.data();
        const isMultiDelivery = trip.isMultiDelivery === true;
        if (!isMultiDelivery) {
            throw new https_1.HttpsError("failed-precondition", "Trip is not multi-delivery; use regular delivery flow");
        }
        const currentProgress = Array.isArray(trip.deliveryStopsProgress)
            ? trip.deliveryStopsProgress
            : [];
        // Check if this stop is already marked delivered
        const existingStop = currentProgress.find((s) => s.index === data.stopIndex);
        if (existingStop && existingStop.status === "delivered") {
            firebase_functions_1.logger.warn("[submitDeliveryStopProgress] Stop already delivered (idempotent)", { tripId: data.tripId, stopIndex: data.stopIndex });
            return {
                ok: true,
                deliveryStopsProgress: currentProgress,
            };
        }
        // Build stop progress entry
        const stopProgress = {
            index: data.stopIndex,
            destination: data.destination.trim().toUpperCase(),
            status: "delivered",
            deliveredAt: data.deliveredAt
                ? new Date(data.deliveredAt)
                : admin.firestore.Timestamp.now(),
            deliveredLat: data.deliveredLat,
            deliveredLng: data.deliveredLng,
            photos: data.photos.map((p) => ({
                url: p.url,
                type: p.type,
                geocoding: p.geocoding
                    ? {
                        lat: p.geocoding.lat,
                        lng: p.geocoding.lng,
                        address: p.geocoding.address,
                        timestamp: p.geocoding.timestamp,
                    }
                    : undefined,
            })),
        };
        // Update trip: replace or append stop progress
        const updatedProgress = [
            ...currentProgress.filter((s) => s.index !== data.stopIndex),
            stopProgress,
        ];
        await tripRef.update({
            deliveryStopsProgress: updatedProgress,
            updatedAt: admin.firestore.FieldValue.serverTimestamp(),
        });
        firebase_functions_1.logger.info("[submitDeliveryStopProgress] Stop progress updated", {
            tripId: data.tripId,
            stopIndex: data.stopIndex,
        });
        // Check if all stops are now delivered
        const taskId = typeof trip.taskId === "string" ? trip.taskId.trim() : "";
        if (!taskId) {
            return {
                ok: true,
                message: "Stop progress saved, but task not found for completion check",
                deliveryStopsProgress: updatedProgress,
            };
        }
        const taskSnap = await db.collection(COL_TASKS).doc(taskId).get();
        if (!taskSnap.exists) {
            return {
                ok: true,
                message: "Stop progress saved, but task not found for completion check",
                deliveryStopsProgress: updatedProgress,
            };
        }
        const task = taskSnap.data();
        const totalStops = Array.isArray(task.deliveryStops) && task.deliveryStops.length > 0
            ? task.deliveryStops.length
            : 1;
        const deliveredCount = updatedProgress.filter((s) => s.status === "delivered").length;
        // If all stops delivered, mark task as Completed
        if (deliveredCount >= totalStops) {
            await db.collection(COL_TASKS).doc(taskId).update({
                status: "Completed",
                updatedAt: admin.firestore.FieldValue.serverTimestamp(),
            });
            // Update trip to "delivered" status and set final timestamp
            await tripRef.update({
                status: "delivered",
                deliveredTimestamp: admin.firestore.FieldValue.serverTimestamp(),
            });
            firebase_functions_1.logger.info("[submitDeliveryStopProgress] All stops delivered - task completed", {
                tripId: data.tripId,
                taskId,
                totalStops,
            });
            // Billing will be computed via:
            // 1. Mobile app calling computeTripBillingSnapshot() after delivery complete
            // 2. Admin backfill function backfillTripBillingSnapshots() for missed trips
            firebase_functions_1.logger.info("[submitDeliveryStopProgress] All stops delivered, billing should be computed separately", {
                tripId: data.tripId,
            });
        }
        return {
            ok: true,
            deliveryStopsProgress: updatedProgress,
        };
    }
    catch (err) {
        if (err instanceof https_1.HttpsError) {
            throw err;
        }
        firebase_functions_1.logger.error("[submitDeliveryStopProgress] Error", err);
        throw new https_1.HttpsError("internal", "Failed to submit delivery stop progress");
    }
});
/**
 * Callable: Add a new delivery stop to an ongoing task.
 * Called by mobile driver when a new delivery point is discovered/assigned mid-trip.
 *
 * Upserts task.deliveryStops; upgrades a single-stop task to isMultiDelivery when the
 * driver adds the first extra stop (seeds the primary destination as stop 1).
 */
exports.addDeliveryStop = (0, https_1.onCall)({ enforceAppCheck: false }, // Disable App Check to allow local/dev testing
async (request) => {
    if (!request.auth) {
        throw new https_1.HttpsError("unauthenticated", "User must be authenticated");
    }
    const uid = request.auth.uid;
    const data = request.data;
    // Validate required fields
    if (!data.taskId) {
        throw new https_1.HttpsError("invalid-argument", "taskId is required");
    }
    if (!data.destination) {
        throw new https_1.HttpsError("invalid-argument", "destination is required");
    }
    const db = admin.firestore();
    const taskRef = db.collection(COL_TASKS).doc(data.taskId);
    try {
        const taskSnap = await taskRef.get();
        if (!taskSnap.exists) {
            throw new https_1.HttpsError("not-found", "Task not found");
        }
        const task = taskSnap.data();
        if (!(await callerOwnsAssignedTask(db, uid, task))) {
            throw new https_1.HttpsError("permission-denied", "Not authorized to edit this task");
        }
        let currentStops = Array.isArray(task.deliveryStops)
            ? [...task.deliveryStops]
            : [];
        const primary = normalizeDestination(task.destination);
        const destNorm = normalizeDestination(data.destination.trim());
        if (!destNorm) {
            throw new https_1.HttpsError("invalid-argument", "destination is required");
        }
        // Bootstrap deliveryStops from task.destination when absent (single-stop task → multi).
        if (currentStops.length === 0) {
            if (!primary) {
                throw new https_1.HttpsError("failed-precondition", "Task has no primary destination — cannot add delivery stops");
            }
            currentStops = [primaryStopRowFromTask(task, primary)];
        }
        if (destinationsConflict(currentStops, destNorm)) {
            return {
                ok: false,
                message: "Delivery stop with same destination already exists",
                deliveryStops: currentStops,
            };
        }
        const nextIndex = nextStopIndex(currentStops);
        const sequence = currentStops.length;
        const newStop = {
            destination: destNorm,
            index: nextIndex,
            sequence,
            status: "pending",
            addedAt: admin.firestore.Timestamp.now(),
        };
        if (data.sourceId) {
            newStop.sourceId = data.sourceId.trim();
        }
        if (data.isCustom === true) {
            newStop.isCustom = true;
        }
        if (typeof data.destinationLinkedCustomerId === "string" && data.destinationLinkedCustomerId.trim()) {
            newStop.destinationLinkedCustomerId = data.destinationLinkedCustomerId.trim();
        }
        if (typeof data.destinationLinkedCustomerName === "string" && data.destinationLinkedCustomerName.trim()) {
            newStop.destinationLinkedCustomerName = data.destinationLinkedCustomerName.trim();
        }
        if (data.destinationCustomerLinkKind === "customer" || data.destinationCustomerLinkKind === "partner") {
            newStop.destinationCustomerLinkKind = data.destinationCustomerLinkKind;
        }
        const updatedStops = [...currentStops, newStop];
        const wasMultiFlag = task.isMultiDelivery === true;
        await taskRef.update({
            deliveryStops: updatedStops,
            isMultiDelivery: true,
            updatedAt: admin.firestore.FieldValue.serverTimestamp(),
        });
        firebase_functions_1.logger.info("[addDeliveryStop] New delivery stop added", {
            taskId: data.taskId,
            destination: destNorm,
            upgradedFromSingle: !wasMultiFlag,
            isCustom: data.isCustom,
        });
        return {
            ok: true,
            message: wasMultiFlag
                ? "Delivery stop added successfully"
                : "Task upgraded to multi-delivery; stop added",
            deliveryStops: updatedStops,
        };
    }
    catch (err) {
        if (err instanceof https_1.HttpsError) {
            throw err;
        }
        firebase_functions_1.logger.error("[addDeliveryStop] Error", err);
        throw new https_1.HttpsError("internal", "Failed to add delivery stop");
    }
});
//# sourceMappingURL=multiDeliveryTrips.js.map