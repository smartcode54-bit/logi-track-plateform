import * as admin from "firebase-admin";
import { logger } from "firebase-functions";
import { onCall, HttpsError } from "firebase-functions/v2/https";

const COL_TASKS = "tasks";
const DEFAULT_CUSTOMER_ID = "7gbnX0Tv9xNQgTKrgp0F";

interface BackfillStats {
    totalProcessed: number;
    updated: number;
    alreadyComplete: number;
    errors: number;
    errorDetails: string[];
}

/**
 * Backfill tasks: add sourceHubLinkedCustomerId and destinationLinkedCustomerId
 * to old tasks that are missing these fields.
 *
 * Only admin can call this.
 */
export const backfillTaskCustomerLinks = onCall(
    { region: "asia-southeast1", enforceAppCheck: false },
    async (request) => {
        // Admin check
        if (!request.auth) {
            throw new HttpsError("unauthenticated", "User must be logged in");
        }

        const db = admin.firestore();
        const user = await admin.auth().getUser(request.auth.uid);
        const customClaims = (user.customClaims as { admin?: boolean } | null) || {};

        if (!customClaims.admin) {
            throw new HttpsError("permission-denied", "Only admins can backfill customer links");
        }

        const stats: BackfillStats = {
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

            logger.info(`[backfillTaskCustomerLinks] Found ${tasksSnapshot.size} total tasks`);

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
                    const updates: Record<string, unknown> = {};

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
                        logger.info(`[backfillTaskCustomerLinks] Committed batch at ${stats.totalProcessed}`);
                    }
                } catch (e) {
                    stats.errors++;
                    const msg = e instanceof Error ? e.message : String(e);
                    stats.errorDetails.push(`Task ${doc.id}: ${msg}`);
                    logger.warn(`[backfillTaskCustomerLinks] Error updating task ${doc.id}:`, e);
                }
            }

            // Final batch commit
            if (batchCount > 0) {
                await batch.commit();
            }

            logger.info("[backfillTaskCustomerLinks] Complete", stats);
            return {
                success: true,
                ...stats,
            };
        } catch (e) {
            logger.error("[backfillTaskCustomerLinks] Fatal error:", e);
            throw new HttpsError("internal", `Backfill failed: ${e instanceof Error ? e.message : String(e)}`);
        }
    }
);

