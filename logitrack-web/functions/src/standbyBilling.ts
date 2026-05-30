/**
 * Standby Billing Cloud Functions
 *
 * Computes and persists billing snapshots for completed standby records.
 * A standby record is billed at a fixed rate per event (per customer, by effective date).
 *
 * Fields written to standby_records/{id}:
 *   billingEstimateThb   number   — fixed standby rate charged
 *   billingCustomerId    string   — customer being billed
 *   billingRateEntryId   string   — source rate entry doc id
 *   billingEffectiveFromDateStr  string  — rate effective date (yyyy-MM-dd)
 *   billingComputedAt    Timestamp
 */

import * as admin from "firebase-admin";
import { logger } from "firebase-functions";
import { onCall, HttpsError } from "firebase-functions/v2/https";
import { onSchedule } from "firebase-functions/v2/scheduler";
import {
    computeStandbyBilling,
    timestampLikeToMillis,
    type StandbyRateEntry,
} from "./core/billingCompute";

const COL_STANDBY_RECORDS = "standby_records";
const COL_STANDBY_RATES = "standby_rate_entries";
const COL_TASKS = "tasks";

function mapStandbyRateDoc(doc: admin.firestore.QueryDocumentSnapshot): StandbyRateEntry {
    const d = doc.data();
    return {
        id: doc.id,
        customerId: String(d.customerId ?? "").trim(),
        rateThb: Number(d.rateThb ?? 0),
        effectiveFromMs: timestampLikeToMillis(d.effectiveFrom),
        note: typeof d.note === "string" ? d.note : undefined,
    };
}

/**
 * Resolve customerId for a standby record:
 * 1. Use record.customerId if already set
 * 2. Look up through record.taskId → task.sourceHubLinkedCustomerId / destinationLinkedCustomerId
 */
async function resolveStandbyCustomerId(
    db: admin.firestore.Firestore,
    data: Record<string, unknown>
): Promise<string> {
    // Direct field takes priority
    if (typeof data.customerId === "string" && data.customerId.trim()) {
        return data.customerId.trim();
    }

    // Try to resolve through linked task
    const taskId = typeof data.taskId === "string" ? data.taskId.trim() : "";
    if (!taskId) return "";

    try {
        const taskSnap = await db.collection(COL_TASKS).doc(taskId).get();
        if (!taskSnap.exists) return "";
        const t = taskSnap.data() as Record<string, unknown>;
        return (
            (typeof t.sourceHubLinkedCustomerId === "string" ? t.sourceHubLinkedCustomerId.trim() : "") ||
            (typeof t.destinationLinkedCustomerId === "string" ? t.destinationLinkedCustomerId.trim() : "")
        );
    } catch {
        return "";
    }
}

interface StandbyBillingResponse {
    ok: boolean;
    skipped?: boolean;
    billingEstimateThb?: number;
    error?: string;
}

/** Shared core: compute and persist billing for one standby record (idempotent). */
async function tryWriteStandbyBillingSnapshot(
    db: admin.firestore.Firestore,
    recordId: string,
    data: Record<string, unknown>,
    docRef: admin.firestore.DocumentReference,
    rateEntries: StandbyRateEntry[]
): Promise<StandbyBillingResponse> {
    if (data.status !== "completed") {
        return { ok: true, skipped: true, error: "Standby record is not completed yet" };
    }

    if (typeof data.billingEstimateThb === "number") {
        return { ok: true, skipped: true, billingEstimateThb: data.billingEstimateThb as number };
    }

    const customerId = await resolveStandbyCustomerId(db, data);
    if (!customerId) {
        logger.warn("[standbyBilling] no customerId on record", { recordId });
        return { ok: false, error: "No customerId — set customerId on the standby record or link a task" };
    }

    // Use createdAt (or startedAt) as the billing date
    const billDateMs =
        timestampLikeToMillis(data.createdAt) ||
        timestampLikeToMillis(data.startedAt) ||
        Date.now();

    const computed = computeStandbyBilling(billDateMs, customerId, rateEntries);
    if (!computed) {
        logger.warn("[standbyBilling] no matching standby rate entry", { recordId, customerId });
        return { ok: false, error: `No standby rate entry found for customer ${customerId}` };
    }

    await docRef.update({
        billingEstimateThb: computed.rateThb,
        billingCustomerId: computed.customerId,
        billingRateEntryId: computed.rateEntryId,
        billingEffectiveFromDateStr: computed.effectiveFromDateStr ?? null,
        billingComputedAt: admin.firestore.FieldValue.serverTimestamp(),
    });

    return { ok: true, billingEstimateThb: computed.rateThb };
}

// ─── Callable: compute single standby record ──────────────────────────────────

interface ComputeStandbyRequest {
    standbyId: string;
}

/**
 * HTTPS Callable: compute and persist billing for a single completed standby record.
 * Idempotent: if billingEstimateThb already exists, returns skipped: true.
 */
export const computeStandbyBillingSnapshot = onCall<ComputeStandbyRequest, Promise<StandbyBillingResponse>>(
    {
        region: "asia-southeast1",
        enforceAppCheck: false,
    },
    async (request): Promise<StandbyBillingResponse> => {
        if (!request.auth) {
            throw new HttpsError("unauthenticated", "Must be authenticated");
        }

        const standbyId = request.data?.standbyId?.trim();
        if (!standbyId) {
            throw new HttpsError("invalid-argument", "standbyId is required");
        }

        const db = admin.firestore();
        const [docSnap, ratesSnap] = await Promise.all([
            db.collection(COL_STANDBY_RECORDS).doc(standbyId).get(),
            db.collection(COL_STANDBY_RATES).get(),
        ]);

        if (!docSnap.exists) {
            throw new HttpsError("not-found", "Standby record not found");
        }

        const rateEntries = ratesSnap.docs.map(mapStandbyRateDoc);
        const data = docSnap.data() as Record<string, unknown>;
        return tryWriteStandbyBillingSnapshot(db, standbyId, data, docSnap.ref, rateEntries);
    }
);

// ─── Scheduled: auto-compute for recently completed standby records ───────────

/**
 * Scheduled: scan standby_records completed in the last 30 minutes that have no billing yet.
 * Runs every 15 minutes.
 */
export const autoComputeStandbyBilling = onSchedule(
    {
        schedule: "every 15 minutes",
        region: "asia-southeast1",
        timeoutSeconds: 300,
    },
    async () => {
        const db = admin.firestore();
        const windowMs = 30 * 60 * 1000;
        const since = admin.firestore.Timestamp.fromMillis(Date.now() - windowMs);

        const [snap, ratesSnap] = await Promise.all([
            db.collection(COL_STANDBY_RECORDS)
                .where("status", "==", "completed")
                .where("createdAt", ">=", since)
                .limit(100)
                .get(),
            db.collection(COL_STANDBY_RATES).get(),
        ]);

        const rateEntries = ratesSnap.docs.map(mapStandbyRateDoc);
        let written = 0;
        let failed = 0;

        for (const doc of snap.docs) {
            const data = doc.data() as Record<string, unknown>;
            if (typeof data.billingEstimateThb === "number") continue;

            const result = await tryWriteStandbyBillingSnapshot(db, doc.id, data, doc.ref, rateEntries);
            if (result.ok && !result.skipped) {
                written++;
                logger.info("[autoStandbyBilling] billing written", {
                    standbyId: doc.id,
                    billingEstimateThb: result.billingEstimateThb,
                });
            } else if (!result.ok) {
                failed++;
                logger.warn("[autoStandbyBilling] billing failed", { standbyId: doc.id, error: result.error });
            }
        }

        logger.info("[autoStandbyBilling] run complete", { scanned: snap.size, written, failed });
    }
);

// ─── Admin Callable: backfill ─────────────────────────────────────────────────

interface BackfillStandbyRequest {
    fromDateStr: string;
    toDateStr: string;
    maxScan?: number;
    maxWrite?: number;
}

interface BackfillStandbyResponse {
    scanned: number;
    eligible: number;
    written: number;
    skipped: number;
    failed: number;
    failures: Array<{ standbyId: string; error?: string }>;
    capped: boolean;
}

function bangkokBoundsToTimestamps(fromDateStr: string, toDateStr: string): {
    start: admin.firestore.Timestamp;
    end: admin.firestore.Timestamp;
} {
    const start = new Date(`${fromDateStr.trim()}T00:00:00+07:00`);
    const end = new Date(`${toDateStr.trim()}T23:59:59.999+07:00`);
    if (Number.isNaN(start.getTime()) || Number.isNaN(end.getTime()) || start > end) {
        throw new HttpsError("invalid-argument", "Invalid fromDateStr / toDateStr");
    }
    return {
        start: admin.firestore.Timestamp.fromDate(start),
        end: admin.firestore.Timestamp.fromDate(end),
    };
}

/**
 * Admin-only: scan standby_records by createdAt and write billing for completed records
 * missing billingEstimateThb.
 */
export const backfillStandbyBillingSnapshots = onCall<BackfillStandbyRequest, Promise<BackfillStandbyResponse>>(
    {
        region: "asia-southeast1",
        enforceAppCheck: false,
    },
    async (request): Promise<BackfillStandbyResponse> => {
        if (request.auth?.token?.admin !== true) {
            throw new HttpsError("permission-denied", "Admin only");
        }

        const fromDateStr = request.data?.fromDateStr?.trim() ?? "";
        const toDateStr = request.data?.toDateStr?.trim() ?? "";
        if (!fromDateStr || !toDateStr) {
            throw new HttpsError("invalid-argument", "fromDateStr and toDateStr are required (yyyy-MM-dd)");
        }

        const maxScan = Math.min(Math.max(1, request.data?.maxScan ?? 500), 2000);
        const maxWrite = Math.min(Math.max(1, request.data?.maxWrite ?? 200), 500);

        const db = admin.firestore();
        const { start, end } = bangkokBoundsToTimestamps(fromDateStr, toDateStr);

        const [snap, ratesSnap] = await Promise.all([
            db.collection(COL_STANDBY_RECORDS)
                .where("createdAt", ">=", start)
                .where("createdAt", "<=", end)
                .orderBy("createdAt", "desc")
                .limit(maxScan)
                .get(),
            db.collection(COL_STANDBY_RATES).get(),
        ]);

        const rateEntries = ratesSnap.docs.map(mapStandbyRateDoc);
        let eligible = 0;
        let written = 0;
        let skipped = 0;
        let failed = 0;
        let attempted = 0;
        const failures: Array<{ standbyId: string; error?: string }> = [];

        for (const doc of snap.docs) {
            const data = doc.data() as Record<string, unknown>;
            if (data.status !== "completed") continue;
            if (typeof data.billingEstimateThb === "number") continue;
            eligible++;

            if (written >= maxWrite) continue;

            attempted++;
            const result = await tryWriteStandbyBillingSnapshot(db, doc.id, data, doc.ref, rateEntries);
            if (result.ok === true && result.skipped !== true && result.billingEstimateThb != null) {
                written++;
            } else if (result.skipped === true) {
                skipped++;
            } else {
                failed++;
                if (failures.length < 25) {
                    failures.push({ standbyId: doc.id, error: result.error });
                }
            }
        }

        return {
            scanned: snap.size,
            eligible,
            written,
            skipped,
            failed,
            failures,
            capped: eligible > attempted,
        };
    }
);
