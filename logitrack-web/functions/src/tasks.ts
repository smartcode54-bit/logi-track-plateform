/**
 * Cloud Functions for task management (create, update, multi-delivery support).
 */
import * as admin from "firebase-admin";
import { onCall, HttpsError } from "firebase-functions/v2/https";
import { logger } from "firebase-functions";

const COL_TASKS = "tasks";

interface DeliveryStopPayload {
    index: number;
    destination: string;
    destinationLinkedCustomerId?: string;
    destinationLinkedCustomerName?: string;
    destinationLinkedCustomerCode?: string;
    destinationCustomerLinkKind?: "customer" | "partner";
}

interface CreateOrUpdateTaskRequest {
    id?: string; // undefined = create, otherwise update
    sourceHub: string;
    destination: string; // Primary destination (1st stop or single stop)
    /** หลัก/เสริม, chosen by admin at assign time. Optional — absent means billing derives it (ADR-0005/0006). */
    jobCategory?: "PRIMARY" | "SUPPLEMENTARY";
    date: string; // ISO date string
    time: string; // HH:MM format
    taskType: "FIRST_MILE" | "LINE_HAUL";
    truckType?: "4WH" | "4WJ" | "6WH" | "10WH" | "18WH" | "PICKUP" | "VAN";
    driverId?: string;
    /** Helper (training/assisting) Auth UIDs — at most one. See ADR-0001. */
    helperDriverIds?: string[];
    isMultiDelivery?: boolean;
    deliveryStops?: DeliveryStopPayload[]; // Only set if isMultiDelivery === true

    // Customer links (for primary/1st destination)
    sourceHubLinkedCustomerId?: string;
    sourceHubLinkedCustomerName?: string;
    sourceHubLinkedCustomerCode?: string;
    sourceHubCustomerLinkKind?: "customer" | "partner";
    destinationLinkedCustomerId?: string;
    destinationLinkedCustomerName?: string;
    destinationLinkedCustomerCode?: string;
    destinationCustomerLinkKind?: "customer" | "partner";
}

interface CreateOrUpdateTaskResponse {
    id: string;
    taskId?: string;
    isMultiDelivery: boolean;
    deliveryStopsCount?: number;
}

/**
 * Callable: Create or update a task (single or multi-delivery). Admin only.
 */
export const createOrUpdateTask = onCall(
    { enforceAppCheck: false }, // Must auth check manually (admin only)
    async (request): Promise<CreateOrUpdateTaskResponse> => {
        // Auth check
        if (!request.auth) {
            throw new HttpsError("unauthenticated", "User must be authenticated");
        }
        if (request.auth.token.admin !== true) {
            throw new HttpsError("permission-denied", "Only admins can create/update tasks");
        }

        const data = request.data as CreateOrUpdateTaskRequest;
        const db = admin.firestore();

        // Validate required fields
        if (!data.sourceHub) {
            throw new HttpsError("invalid-argument", "sourceHub is required");
        }
        if (!data.destination) {
            throw new HttpsError("invalid-argument", "destination is required");
        }
        if (!data.date) {
            throw new HttpsError("invalid-argument", "date is required");
        }
        if (!data.time) {
            throw new HttpsError("invalid-argument", "time is required");
        }
        if (!data.taskType) {
            throw new HttpsError("invalid-argument", "taskType is required");
        }

        // Validate multi-delivery
        const isMultiDelivery = data.isMultiDelivery === true;
        if (isMultiDelivery) {
            if (!data.deliveryStops || data.deliveryStops.length < 2) {
                throw new HttpsError(
                    "invalid-argument",
                    "Multi-delivery requires 2 or more delivery stops"
                );
            }

            // Check for duplicate destinations
            const dests = data.deliveryStops.map((s) => s.destination.toUpperCase());
            const uniqueDests = new Set(dests);
            if (uniqueDests.size !== dests.length) {
                throw new HttpsError("invalid-argument", "Duplicate destinations in delivery stops");
            }
        }

        // Compute runOrder if assigning to driver
        let runOrder: number | undefined;
        if (data.driverId) {
            try {
                const existing = await db
                    .collection(COL_TASKS)
                    .where("driverId", "==", data.driverId)
                    .orderBy("runOrder", "desc")
                    .limit(1)
                    .get();

                const maxRunOrder =
                    existing.docs.length > 0 ? (existing.docs[0].data().runOrder || 0) : 0;
                runOrder = maxRunOrder + 1;
            } catch (err) {
                logger.error("[createOrUpdateTask] Failed to compute runOrder", { driverId: data.driverId, err });
                throw new HttpsError("internal", "Failed to compute runOrder for driver");
            }
        }

        // Build task document
        const taskDoc: Record<string, unknown> = {
            sourceHub: data.sourceHub.trim().toUpperCase(),
            destination: data.destination.trim().toUpperCase(),
            jobCategory: data.jobCategory === "SUPPLEMENTARY" ? "SUPPLEMENTARY" : "PRIMARY",
            date: new Date(data.date),
            time: data.time.trim(),
            taskType: data.taskType,
            truckType: data.truckType,
            driverId: data.driverId,
            // At most one helper; persist as an array for array-contains queries.
            helperDriverIds: Array.isArray(data.helperDriverIds)
                ? data.helperDriverIds.filter(Boolean).slice(0, 1)
                : [],
            driverName: undefined, // Will be fetched separately if needed
            status: data.driverId ? "Assigned" : "Pending",
            isMultiDelivery,
            updatedAt: admin.firestore.FieldValue.serverTimestamp(),

            // Customer links
            sourceHubLinkedCustomerId: data.sourceHubLinkedCustomerId,
            sourceHubLinkedCustomerName: data.sourceHubLinkedCustomerName,
            sourceHubLinkedCustomerCode: data.sourceHubLinkedCustomerCode,
            sourceHubCustomerLinkKind: data.sourceHubCustomerLinkKind,
            destinationLinkedCustomerId: data.destinationLinkedCustomerId,
            destinationLinkedCustomerName: data.destinationLinkedCustomerName,
            destinationLinkedCustomerCode: data.destinationLinkedCustomerCode,
            destinationCustomerLinkKind: data.destinationCustomerLinkKind,
        };

        if (runOrder !== undefined) {
            taskDoc.runOrder = runOrder;
        }

        // Add delivery stops for multi-delivery
        if (isMultiDelivery && data.deliveryStops) {
            taskDoc.deliveryStops = data.deliveryStops.map((stop) => ({
                index: stop.index,
                destination: stop.destination.trim().toUpperCase(),
                destinationLinkedCustomerId: stop.destinationLinkedCustomerId,
                destinationLinkedCustomerName: stop.destinationLinkedCustomerName,
                destinationLinkedCustomerCode: stop.destinationLinkedCustomerCode,
                destinationCustomerLinkKind: stop.destinationCustomerLinkKind,
                status: "pending",
            }));
        }

        try {
            let taskId: string;
            if (data.id) {
                // Update existing task
                await db.collection(COL_TASKS).doc(data.id).update(taskDoc);
                taskId = data.id;
                logger.info("[createOrUpdateTask] Task updated", { taskId, isMultiDelivery });
            } else {
                // Create new task
                taskDoc.createdAt = admin.firestore.FieldValue.serverTimestamp();
                const docRef = db.collection(COL_TASKS).doc();
                await docRef.set(taskDoc);
                taskId = docRef.id;
                logger.info("[createOrUpdateTask] Task created", { taskId, isMultiDelivery });
            }

            return {
                id: taskId,
                isMultiDelivery,
                deliveryStopsCount: isMultiDelivery ? data.deliveryStops!.length : undefined,
            };
        } catch (err) {
            logger.error("[createOrUpdateTask] Failed to create/update task", err);
            throw new HttpsError("internal", "Failed to create/update task");
        }
    }
);

/**
 * Callable: Get next runOrder for a driver (used by client to pre-fill).
 */
export const getNextRunOrderForDriver = onCall(
    { enforceAppCheck: false },
    async (request): Promise<{ runOrder: number }> => {
        if (!request.auth) {
            throw new HttpsError("unauthenticated", "User must be authenticated");
        }

        const driverId = request.data?.driverId as string;
        if (!driverId) {
            throw new HttpsError("invalid-argument", "driverId is required");
        }

        try {
            const db = admin.firestore();
            const existing = await db
                .collection(COL_TASKS)
                .where("driverId", "==", driverId)
                .orderBy("runOrder", "desc")
                .limit(1)
                .get();

            const maxRunOrder = existing.docs.length > 0 ? (existing.docs[0].data().runOrder || 0) : 0;
            const nextRunOrder = maxRunOrder + 1;

            return { runOrder: nextRunOrder };
        } catch (err) {
            logger.error("[getNextRunOrderForDriver] Failed", { driverId, err });
            throw new HttpsError("internal", "Failed to compute runOrder");
        }
    }
);
