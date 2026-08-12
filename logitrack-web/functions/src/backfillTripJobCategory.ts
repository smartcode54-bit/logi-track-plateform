import * as admin from "firebase-admin";
import { logger } from "firebase-functions";
import { onCall, HttpsError } from "firebase-functions/v2/https";

const COL_TRIP_RECORDS = "trip_records";
const COL_TASKS = "tasks";
const PAGE_SIZE = 400;
const WRITE_BATCH_SIZE = 400;
const TASK_IN_CHUNK = 30; // Firestore `in` query cap

type JobCategory = "PRIMARY" | "SUPPLEMENTARY";

interface BackfillStats {
    totalProcessed: number;
    alreadySet: number;
    updated: number;
    copiedFromTask: number;
    defaultedPrimary: number;
    taskMissing: number;
    errors: number;
    errorDetails: string[];
}

function normalizeJobCategory(value: unknown): JobCategory | undefined {
    return value === "SUPPLEMENTARY" ? "SUPPLEMENTARY" : value === "PRIMARY" ? "PRIMARY" : undefined;
}

/**
 * Backfill `trip_records.jobCategory` by copying the linked task's value (ADR 0010 R7).
 *
 * Replaces the unsafe local `scripts/backfill-job-category.mjs`, which set PRIMARY blindly and
 * could not run without local Firestore access. jobCategory's source of truth is the *task*; a
 * trip that never got a value (billing skipped/failed, or a legacy trip billed before the field
 * existed) inherits its task's category here. Only when the task is missing or has no category do
 * we default PRIMARY.
 *
 * Idempotent: trips already carrying PRIMARY/SUPPLEMENTARY are skipped. Admin only.
 */
export const backfillTripJobCategoryFromTask = onCall(
    { region: "asia-southeast1", enforceAppCheck: false, timeoutSeconds: 540 },
    async (request) => {
        if (!request.auth) {
            throw new HttpsError("unauthenticated", "User must be logged in");
        }
        const user = await admin.auth().getUser(request.auth.uid);
        const customClaims = (user.customClaims as { admin?: boolean } | null) || {};
        if (!customClaims.admin) {
            throw new HttpsError("permission-denied", "Only admins can backfill trip job category");
        }

        const db = admin.firestore();
        const stats: BackfillStats = {
            totalProcessed: 0,
            alreadySet: 0,
            updated: 0,
            copiedFromTask: 0,
            defaultedPrimary: 0,
            taskMissing: 0,
            errors: 0,
            errorDetails: [],
        };

        // Cache so a task shared by several trips is read once. Key presence = looked up.
        const taskCategoryCache = new Map<string, JobCategory | undefined>();
        const taskFound = new Set<string>();

        async function loadTaskCategories(taskIds: string[]): Promise<void> {
            const missing = [...new Set(taskIds)].filter((id) => id && !taskCategoryCache.has(id));
            for (let i = 0; i < missing.length; i += TASK_IN_CHUNK) {
                const chunk = missing.slice(i, i + TASK_IN_CHUNK);
                if (chunk.length === 0) continue;
                const snap = await db
                    .collection(COL_TASKS)
                    .where(admin.firestore.FieldPath.documentId(), "in", chunk)
                    .get();
                snap.forEach((d) => {
                    taskFound.add(d.id);
                    taskCategoryCache.set(d.id, normalizeJobCategory(d.data().jobCategory));
                });
                // Ids not returned don't exist — record so we don't re-query them.
                for (const id of chunk) {
                    if (!taskCategoryCache.has(id)) taskCategoryCache.set(id, undefined);
                }
            }
        }

        try {
            let lastDoc: admin.firestore.QueryDocumentSnapshot | null = null;

            while (true) {
                let q = db
                    .collection(COL_TRIP_RECORDS)
                    .orderBy(admin.firestore.FieldPath.documentId())
                    .limit(PAGE_SIZE);
                if (lastDoc) q = q.startAfter(lastDoc);
                const snap = await q.get();
                if (snap.empty) break;

                // Trips in this page that still need a value, with their taskId.
                const needing: { ref: admin.firestore.DocumentReference; taskId: string }[] = [];
                for (const doc of snap.docs) {
                    stats.totalProcessed++;
                    lastDoc = doc;
                    const data = doc.data();
                    if (normalizeJobCategory(data.jobCategory)) {
                        stats.alreadySet++;
                        continue;
                    }
                    const taskId = typeof data.taskId === "string" ? data.taskId.trim() : "";
                    needing.push({ ref: doc.ref, taskId });
                }

                await loadTaskCategories(needing.map((n) => n.taskId).filter(Boolean));

                let batch = db.batch();
                let batchCount = 0;
                for (const { ref, taskId } of needing) {
                    try {
                        let resolved: JobCategory;
                        if (!taskId || !taskFound.has(taskId)) {
                            // No linked task (or task doc deleted) — safest default is PRIMARY.
                            resolved = "PRIMARY";
                            stats.defaultedPrimary++;
                            stats.taskMissing++;
                        } else {
                            const taskCat = taskCategoryCache.get(taskId);
                            if (taskCat) {
                                resolved = taskCat;
                                stats.copiedFromTask++;
                            } else {
                                // Task exists but carries no category — default PRIMARY.
                                resolved = "PRIMARY";
                                stats.defaultedPrimary++;
                            }
                        }
                        batch.update(ref, {
                            jobCategory: resolved,
                            updatedAt: admin.firestore.FieldValue.serverTimestamp(),
                        });
                        stats.updated++;
                        batchCount++;
                        if (batchCount >= WRITE_BATCH_SIZE) {
                            await batch.commit();
                            batch = db.batch();
                            batchCount = 0;
                        }
                    } catch (e) {
                        stats.errors++;
                        const msg = e instanceof Error ? e.message : String(e);
                        stats.errorDetails.push(`Trip ${ref.id}: ${msg}`);
                        logger.warn(`[backfillTripJobCategoryFromTask] Error updating trip ${ref.id}:`, e);
                    }
                }
                if (batchCount > 0) await batch.commit();

                if (snap.size < PAGE_SIZE) break;
            }

            logger.info("[backfillTripJobCategoryFromTask] Complete", stats);
            return { success: true, ...stats };
        } catch (e) {
            logger.error("[backfillTripJobCategoryFromTask] Fatal error:", e);
            throw new HttpsError("internal", `Backfill failed: ${e instanceof Error ? e.message : String(e)}`);
        }
    }
);
