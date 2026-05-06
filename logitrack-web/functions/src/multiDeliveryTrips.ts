/**
 * Cloud Functions for multi-delivery trip handling.
 */
import * as admin from "firebase-admin";
import { onCall, HttpsError } from "firebase-functions/v2/https";
import { logger } from "firebase-functions";

const COL_TRIP_RECORDS = "trip_records";
const COL_TASKS = "tasks";
const COL_DRIVERS = "drivers";

/** Drivers may append stops only to tasks assigned to them (driverId matches auth UID or drivers/{id}.authId). */
async function callerOwnsAssignedTask(
    db: admin.firestore.Firestore,
    uid: string,
    task: Record<string, unknown>
): Promise<boolean> {
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

function normalizeDestination(raw: unknown): string {
    return typeof raw === "string" ? raw.trim().toUpperCase() : "";
}

function primaryStopRowFromTask(
    task: Record<string, unknown>,
    primaryDestination: string
): Record<string, unknown> {
    const row: Record<string, unknown> = {
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

function nextStopIndex(currentStops: Array<Record<string, unknown>>): number {
    const maxIx = currentStops.reduce((m, s) => {
        const ix = typeof s.index === "number" ? s.index : 0;
        return Math.max(m, ix);
    }, 0);
    return maxIx > 0 ? maxIx + 1 : currentStops.length + 1;
}

function destinationsConflict(
    stops: Array<Record<string, unknown>>,
    normalizedNew: string
): boolean {
    return stops.some(
        (s) => normalizeDestination(s.destination) === normalizedNew
    );
}

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

interface AddDeliveryStopRequest {
    taskId: string;
    destination: string;
    sourceId?: string;
    isCustom?: boolean;
    destinationLinkedCustomerId?: string;
    destinationLinkedCustomerName?: string;
    destinationCustomerLinkKind?: string; // "customer" | "partner"
}

interface AddDeliveryStopResponse {
    ok: boolean;
    message?: string;
    deliveryStops?: Array<{
        destination: string;
        sourceId?: string;
        isCustom: boolean;
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
    { enforceAppCheck: false },
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

                // Billing will be computed via:
                // 1. Mobile app calling computeTripBillingSnapshot() after delivery complete
                // 2. Admin backfill function backfillTripBillingSnapshots() for missed trips
                logger.info("[submitDeliveryStopProgress] All stops delivered, billing should be computed separately", {
                    tripId: data.tripId,
                });
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

/**
 * Callable: Add a new delivery stop to an ongoing task.
 * Called by mobile driver when a new delivery point is discovered/assigned mid-trip.
 *
 * Upserts task.deliveryStops; upgrades a single-stop task to isMultiDelivery when the
 * driver adds the first extra stop (seeds the primary destination as stop 1).
 */
export const addDeliveryStop = onCall(
    { enforceAppCheck: false }, // Disable App Check to allow local/dev testing
    async (request): Promise<AddDeliveryStopResponse> => {
        if (!request.auth) {
            throw new HttpsError("unauthenticated", "User must be authenticated");
        }
        const uid = request.auth.uid;

        const data = request.data as AddDeliveryStopRequest;

        // Validate required fields
        if (!data.taskId) {
            throw new HttpsError("invalid-argument", "taskId is required");
        }
        if (!data.destination) {
            throw new HttpsError("invalid-argument", "destination is required");
        }

        const db = admin.firestore();
        const taskRef = db.collection(COL_TASKS).doc(data.taskId);

        try {
            const taskSnap = await taskRef.get();
            if (!taskSnap.exists) {
                throw new HttpsError("not-found", "Task not found");
            }

            const task = taskSnap.data() as Record<string, unknown>;

            if (!(await callerOwnsAssignedTask(db, uid, task))) {
                throw new HttpsError("permission-denied", "Not authorized to edit this task");
            }

            let currentStops: Array<Record<string, unknown>> = Array.isArray(
                task.deliveryStops
            )
                ? ([...(task.deliveryStops as Array<Record<string, unknown>>)] as Array<
                      Record<string, unknown>
                  >)
                : [];

            const primary = normalizeDestination(task.destination);
            const destNorm = normalizeDestination(data.destination.trim());
            if (!destNorm) {
                throw new HttpsError("invalid-argument", "destination is required");
            }

            // Bootstrap deliveryStops from task.destination when absent (single-stop task → multi).
            if (currentStops.length === 0) {
                if (!primary) {
                    throw new HttpsError(
                        "failed-precondition",
                        "Task has no primary destination — cannot add delivery stops"
                    );
                }
                currentStops = [primaryStopRowFromTask(task, primary)];
            }

            if (destinationsConflict(currentStops, destNorm)) {
                return {
                    ok: false,
                    message: "Delivery stop with same destination already exists",
                    deliveryStops: currentStops as any,
                };
            }

            const nextIndex = nextStopIndex(currentStops);
            const sequence = currentStops.length;
            const newStop: Record<string, unknown> = {
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

            logger.info("[addDeliveryStop] New delivery stop added", {
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
                deliveryStops: updatedStops as any,
            };
        } catch (err) {
            if (err instanceof HttpsError) {
                throw err;
            }
            logger.error("[addDeliveryStop] Error", err);
            throw new HttpsError("internal", "Failed to add delivery stop");
        }
    }
);
