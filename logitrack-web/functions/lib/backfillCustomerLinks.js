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
exports.backfillTaskCustomerLinks = void 0;
const admin = __importStar(require("firebase-admin"));
const firebase_functions_1 = require("firebase-functions");
const https_1 = require("firebase-functions/v2/https");
const COL_TASKS = "tasks";
const DEFAULT_CUSTOMER_ID = "7gbnX0Tv9xNQgTKrgp0F";
/**
 * Backfill tasks: add sourceHubLinkedCustomerId and destinationLinkedCustomerId
 * to old tasks that are missing these fields.
 *
 * Only admin can call this.
 */
exports.backfillTaskCustomerLinks = (0, https_1.onCall)({ region: "asia-southeast1", enforceAppCheck: false }, async (request) => {
    // Admin check
    if (!request.auth) {
        throw new https_1.HttpsError("unauthenticated", "User must be logged in");
    }
    const db = admin.firestore();
    const user = await admin.auth().getUser(request.auth.uid);
    const customClaims = user.customClaims || {};
    if (!customClaims.admin) {
        throw new https_1.HttpsError("permission-denied", "Only admins can backfill customer links");
    }
    const stats = {
        totalProcessed: 0,
        updated: 0,
        alreadyComplete: 0,
        errors: 0,
        errorDetails: [],
    };
    try {
        // Query all tasks that are missing either customer link field
        const tasksSnapshot = await db
            .collection(COL_TASKS)
            .where("status", "!=", null) // Get all tasks
            .get();
        firebase_functions_1.logger.info(`[backfillTaskCustomerLinks] Found ${tasksSnapshot.size} total tasks`);
        // Process in batches
        const batch = db.batch();
        let batchCount = 0;
        const BATCH_SIZE = 100;
        for (const doc of tasksSnapshot.docs) {
            const data = doc.data();
            stats.totalProcessed++;
            const hasSourceLink = !!data.sourceHubLinkedCustomerId?.trim();
            const hasDestLink = !!data.destinationLinkedCustomerId?.trim();
            if (hasSourceLink && hasDestLink) {
                stats.alreadyComplete++;
                continue;
            }
            try {
                const updates = {};
                if (!hasSourceLink) {
                    updates.sourceHubLinkedCustomerId = DEFAULT_CUSTOMER_ID;
                    updates.sourceHubCustomerLinkKind = "customer";
                }
                if (!hasDestLink) {
                    updates.destinationLinkedCustomerId = DEFAULT_CUSTOMER_ID;
                    updates.destinationCustomerLinkKind = "customer";
                }
                batch.update(doc.ref, updates);
                stats.updated++;
                batchCount++;
                // Commit batch every BATCH_SIZE updates
                if (batchCount >= BATCH_SIZE) {
                    await batch.commit();
                    batchCount = 0;
                    firebase_functions_1.logger.info(`[backfillTaskCustomerLinks] Committed batch at ${stats.totalProcessed}`);
                }
            }
            catch (e) {
                stats.errors++;
                const msg = e instanceof Error ? e.message : String(e);
                stats.errorDetails.push(`Task ${doc.id}: ${msg}`);
                firebase_functions_1.logger.warn(`[backfillTaskCustomerLinks] Error updating task ${doc.id}:`, e);
            }
        }
        // Final batch commit
        if (batchCount > 0) {
            await batch.commit();
        }
        firebase_functions_1.logger.info("[backfillTaskCustomerLinks] Complete", stats);
        return {
            success: true,
            ...stats,
        };
    }
    catch (e) {
        firebase_functions_1.logger.error("[backfillTaskCustomerLinks] Fatal error:", e);
        throw new https_1.HttpsError("internal", `Backfill failed: ${e instanceof Error ? e.message : String(e)}`);
    }
});
//# sourceMappingURL=backfillCustomerLinks.js.map