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
exports.backfillTripTruckData = void 0;
const functions = __importStar(require("firebase-functions/v2/https"));
const admin = __importStar(require("firebase-admin"));
const db = admin.firestore();
/**
 * Backfill truckId / truckLicensePlate / truckType into trip_records that lack them.
 * Resolves from the linked task's licensePlate/truckType snapshot (written at check-in time)
 * — more reliable than currentAssignment since the task snapshot is immutable.
 *
 * Admin-only callable.
 */
exports.backfillTripTruckData = functions.onCall({ enforceAppCheck: false }, async (request) => {
    if (!request.auth)
        throw new functions.HttpsError("unauthenticated", "Auth required");
    const isAdmin = request.auth.token.admin === true ||
        request.auth.token["role"] === "admin";
    if (!isAdmin)
        throw new functions.HttpsError("permission-denied", "Admin only");
    const tripSnap = await db
        .collection("trip_records")
        .where("truckLicensePlate", "==", null)
        .limit(500)
        .get();
    // Also catch docs where field simply doesn't exist (null query won't catch missing fields)
    const missingSnap = await db.collection("trip_records").limit(500).get();
    const allDocs = [
        ...tripSnap.docs,
        ...missingSnap.docs.filter((d) => d.data().truckLicensePlate === undefined),
    ];
    // dedup
    const seen = new Set();
    const docs = allDocs.filter((d) => {
        if (seen.has(d.id))
            return false;
        seen.add(d.id);
        return true;
    });
    let updated = 0;
    let skipped = 0;
    let errors = 0;
    const batches = [];
    let batch = db.batch();
    let batchCount = 0;
    for (const doc of docs) {
        const data = doc.data();
        if (data.truckLicensePlate) {
            skipped++;
            continue;
        }
        const taskId = data.taskId;
        if (!taskId) {
            skipped++;
            continue;
        }
        try {
            const taskDoc = await db.collection("tasks").doc(taskId).get();
            if (!taskDoc.exists) {
                skipped++;
                continue;
            }
            const task = taskDoc.data();
            const licensePlate = task.licensePlate;
            const truckType = task.truckType;
            if (!licensePlate) {
                skipped++;
                continue;
            }
            batch.update(doc.ref, {
                truckLicensePlate: licensePlate,
                ...(truckType ? { truckType } : {}),
                updatedAt: admin.firestore.FieldValue.serverTimestamp(),
            });
            updated++;
            batchCount++;
            if (batchCount >= 100) {
                batches.push(batch);
                batch = db.batch();
                batchCount = 0;
            }
        }
        catch {
            errors++;
        }
    }
    if (batchCount > 0)
        batches.push(batch);
    await Promise.all(batches.map((b) => b.commit()));
    return { totalScanned: docs.length, updated, skipped, errors };
});
//# sourceMappingURL=backfillTripTruckData.js.map