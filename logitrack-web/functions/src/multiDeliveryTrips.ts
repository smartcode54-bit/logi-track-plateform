/**
 * Cloud Functions for multi-delivery trip handling.
 */
import * as admin from "firebase-admin";
import { onCall, HttpsError } from "firebase-functions/v2/https";
import { logger } from "firebase-functions";

const COL_TRIP_RECORDS = "trip_records";
const COL_TASKS = "tasks";

interface DeliveryStopPhotoPayload {
    url: string;
    type: string; // e.g., "stop_1_pre_open", "stop_1_opening", "stop_1_empty_container"
    geocoding?: {
        lat?: number;
        lng?: number;
        address?: string;
        timestamp?: string;
    };
}

interface SubmitDeliveryStopProgressRequest {
    tripId: string;
    stopIndex: number;
    destination: string;
    photos: DeliveryStopPhotoPayload[];
    deliveredLat: number;
    deliveredLng: number;
    deliveredAt?: string; // ISO timestamp (optional, server will use now if not provided)
}

interface SubmitDeliveryStopProgressResponse {
    ok: boolean;
    message?: string;
    deliveryStopsProgress?: Array<{
        index: number;
        destination: string;
        status: string;
    }>;
}

/**
 * Callable: Submit delivery progress for a single stop in a multi-delivery trip.
 * Called by mobile driver after delivering to one of multiple stops.
 *
 * Appends to trip_record.deliveryStopsProgress array (idempotent: only if not already marked delivered).
 * If all stops are now delivered, marks task as "Completed".
 */
export const submitDeliveryStopProgress = onCall(
    { enforceAppCheck: true, consumeAppCheckToken: true },
    async (request): Promise<SubmitDeliveryStopProgressResponse> => {
        if (!request.auth) {
            throw new HttpsError("unauthenticated", "User must be authenticated");
        }

        const data = request.data as SubmitDeliveryStopProgressRequest;

        // Validate required fields
        if (!data.tripId) {
            throw new HttpsError("invalid-argument", "tripId is required");
        }
        if (typeof data.stopIndex !== "number" || data.stopIndex < 1) {
            throw new HttpsError("invalid-argument", "stopIndex must be a positive number");
        }
        if (!data.destination) {
            throw new HttpsError("invalid-argument", "destination is required");
        }
        if (!Array.isArray(data.photos) || data.photos.length === 0) {
            throw new HttpsError("invalid-argument", "at least 1 photo is required");
        }
        if (typeof data.deliveredLat !== "number" || typeof data.deliveredLng !== "number") {
            throw new HttpsError("invalid-argument", "deliveredLat and deliveredLng are required");
        }

        const db = admin.firestore();
        const tripRef = db.collection(COL_TRIP_RECORDS).doc(data.tripId);

        try {
            const tripSnap = await tripRef.get();
            if (!tripSnap.exists) {
                throw new HttpsError("not-found", "Trip not found");
            }

            const trip = tripSnap.data() as Record<string, unknown>;
            const isMultiDelivery = trip.isMultiDelivery === true;

            if (!isMultiDelivery) {
                throw new HttpsError(
                    "failed-precondition",
                    "Trip is not multi-delivery; use regular delivery flow"
                );
            }

            const currentProgress = Array.isArray(trip.deliveryStopsProgress)
                ? (trip.deliveryStopsProgress as Array<Record<string, unknown>>)
                : [];

            // Check if this stop is already marked delivered
            const existingStop = currentProgress.find((s) => s.index === data.stopIndex);
            if (existingStop && existingStop.status === "delivered") {
                logger.warn(
                    "[submitDeliveryStopProgress] Stop already delivered (idempotent)",
                    { tripId: data.tripId, stopIndex: data.stopIndex }
                );
                return {
                    ok: true,
                    deliveryStopsProgress: currentProgress as any,
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

            logger.info("[submitDeliveryStopProgress] Stop progress updated", {
                tripId: data.tripId,
                stopIndex: data.stopIndex,
            });

            // Check if all stops are now delivered
            const taskId = typeof trip.taskId === "string" ? trip.taskId.trim() : "";
            if (!taskId) {
                return {
                    ok: true,
                    message: "Stop progress saved, but task not found for completion check",
                    deliveryStopsProgress: updatedProgress as any,
                };
            }

            const taskSnap = await db.collection(COL_TASKS).doc(taskId).get();
            if (!taskSnap.exists) {
                return {
                    ok: true,
                    message: "Stop progress saved, but task not found for completion check",
                    deliveryStopsProgress: updatedProgress as any,
                };
            }

            const task = taskSnap.data() as Record<string, unknown>;
            const totalStops =
                Array.isArray(task.deliveryStops) && task.deliveryStops.length > 0
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

                logger.info("[submitDeliveryStopProgress] All stops delivered - task completed", {
                    tripId: data.tripId,
                    taskId,
                    totalStops,
                });

                // Trigger billing computation asynchronously (non-blocking)
                // Fetch updated trip and compute billing
                try {
                    const billingRef = db.collection(COL_TRIP_RECORDS).doc(data.tripId);
                    const billingTrip = await billingRef.get();
                    if (billingTrip.exists) {
                        const billingData = billingTrip.data() as Record<string, unknown>;
                        if (billingData.status === "delivered" && typeof billingData.billingEstimateThb !== "number") {
                            // Call the billing computation function via admin SDK
                            const billingComputation = require("./tripBillingOnDelivered");
                            // Since we can't directly invoke Cloud Functions from another function,
                            // we'll write a helper that can be reused
                            // For now, logging that billing should be computed
                            logger.info("[submitDeliveryStopProgress] Billing should be computed for trip", {
                                tripId: data.tripId,
                            });
                        }
                    }
                } catch (billingErr) {
                    logger.warn("[submitDeliveryStopProgress] Error triggering billing", billingErr);
                    // Don't fail the overall operation if billing fails
                }
            }

            return {
                ok: true,
                deliveryStopsProgress: updatedProgress as any,
            };
        } catch (err) {
            if (err instanceof HttpsError) {
                throw err;
            }
            logger.error("[submitDeliveryStopProgress] Error", err);
            throw new HttpsError("internal", "Failed to submit delivery stop progress");
        }
    }
);
