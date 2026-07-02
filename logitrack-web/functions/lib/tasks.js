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
exports.getNextRunOrderForDriver = exports.createOrUpdateTask = void 0;
/**
 * Cloud Functions for task management (create, update, multi-delivery support).
 */
const admin = __importStar(require("firebase-admin"));
const https_1 = require("firebase-functions/v2/https");
const firebase_functions_1 = require("firebase-functions");
const COL_TASKS = "tasks";
/**
 * Callable: Create or update a task (single or multi-delivery). Admin only.
 */
exports.createOrUpdateTask = (0, https_1.onCall)({ enforceAppCheck: false }, // Must auth check manually (admin only)
async (request) => {
    // Auth check
    if (!request.auth) {
        throw new https_1.HttpsError("unauthenticated", "User must be authenticated");
    }
    if (request.auth.token.admin !== true) {
        throw new https_1.HttpsError("permission-denied", "Only admins can create/update tasks");
    }
    const data = request.data;
    const db = admin.firestore();
    // Validate required fields
    if (!data.sourceHub) {
        throw new https_1.HttpsError("invalid-argument", "sourceHub is required");
    }
    if (!data.destination) {
        throw new https_1.HttpsError("invalid-argument", "destination is required");
    }
    if (!data.date) {
        throw new https_1.HttpsError("invalid-argument", "date is required");
    }
    if (!data.time) {
        throw new https_1.HttpsError("invalid-argument", "time is required");
    }
    if (!data.taskType) {
        throw new https_1.HttpsError("invalid-argument", "taskType is required");
    }
    // Validate multi-delivery
    const isMultiDelivery = data.isMultiDelivery === true;
    if (isMultiDelivery) {
        if (!data.deliveryStops || data.deliveryStops.length < 2) {
            throw new https_1.HttpsError("invalid-argument", "Multi-delivery requires 2 or more delivery stops");
        }
        // Check for duplicate destinations
        const dests = data.deliveryStops.map((s) => s.destination.toUpperCase());
        const uniqueDests = new Set(dests);
        if (uniqueDests.size !== dests.length) {
            throw new https_1.HttpsError("invalid-argument", "Duplicate destinations in delivery stops");
        }
    }
    // Compute runOrder if assigning to driver
    let runOrder;
    if (data.driverId) {
        try {
            const existing = await db
                .collection(COL_TASKS)
                .where("driverId", "==", data.driverId)
                .orderBy("runOrder", "desc")
                .limit(1)
                .get();
            const maxRunOrder = existing.docs.length > 0 ? (existing.docs[0].data().runOrder || 0) : 0;
            runOrder = maxRunOrder + 1;
        }
        catch (err) {
            firebase_functions_1.logger.error("[createOrUpdateTask] Failed to compute runOrder", { driverId: data.driverId, err });
            throw new https_1.HttpsError("internal", "Failed to compute runOrder for driver");
        }
    }
    // Build task document
    const taskDoc = {
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
        let taskId;
        if (data.id) {
            // Update existing task
            await db.collection(COL_TASKS).doc(data.id).update(taskDoc);
            taskId = data.id;
            firebase_functions_1.logger.info("[createOrUpdateTask] Task updated", { taskId, isMultiDelivery });
        }
        else {
            // Create new task
            taskDoc.createdAt = admin.firestore.FieldValue.serverTimestamp();
            const docRef = db.collection(COL_TASKS).doc();
            await docRef.set(taskDoc);
            taskId = docRef.id;
            firebase_functions_1.logger.info("[createOrUpdateTask] Task created", { taskId, isMultiDelivery });
        }
        return {
            id: taskId,
            isMultiDelivery,
            deliveryStopsCount: isMultiDelivery ? data.deliveryStops.length : undefined,
        };
    }
    catch (err) {
        firebase_functions_1.logger.error("[createOrUpdateTask] Failed to create/update task", err);
        throw new https_1.HttpsError("internal", "Failed to create/update task");
    }
});
/**
 * Callable: Get next runOrder for a driver (used by client to pre-fill).
 */
exports.getNextRunOrderForDriver = (0, https_1.onCall)({ enforceAppCheck: false }, async (request) => {
    if (!request.auth) {
        throw new https_1.HttpsError("unauthenticated", "User must be authenticated");
    }
    const driverId = request.data?.driverId;
    if (!driverId) {
        throw new https_1.HttpsError("invalid-argument", "driverId is required");
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
    }
    catch (err) {
        firebase_functions_1.logger.error("[getNextRunOrderForDriver] Failed", { driverId, err });
        throw new https_1.HttpsError("internal", "Failed to compute runOrder");
    }
});
//# sourceMappingURL=tasks.js.map