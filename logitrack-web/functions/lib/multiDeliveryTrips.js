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
exports.submitDeliveryStopProgress = void 0;
/**
 * Cloud Functions for multi-delivery trip handling.
 */
const admin = __importStar(require("firebase-admin"));
const https_1 = require("firebase-functions/v2/https");
const firebase_functions_1 = require("firebase-functions");
const COL_TRIP_RECORDS = "trip_records";
const COL_TASKS = "tasks";
/**
 * Callable: Submit delivery progress for a single stop in a multi-delivery trip.
 * Called by mobile driver after delivering to one of multiple stops.
 *
 * Appends to trip_record.deliveryStopsProgress array (idempotent: only if not already marked delivered).
 * If all stops are now delivered, marks task as "Completed".
 */
exports.submitDeliveryStopProgress = (0, https_1.onCall)({ enforceAppCheck: true, consumeAppCheckToken: true }, async (request) => {
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
                : admin.firestore.FieldValue.serverTimestamp(),
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
//# sourceMappingURL=multiDeliveryTrips.js.map