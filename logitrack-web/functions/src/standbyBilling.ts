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
import {
    loadBillingPeriodLocks,
    type BillingPeriodLocks,
} from "./core/billingPeriodLock";

const COL_STANDBY_RECORDS = "standby_records";
const COL_STANDBY_RATES = "standby_rate_entries";
const COL_TASKS = "tasks";
const COL_SERVICE_FEES = "customer_service_fees";

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
 * Build a fallback map of customerId → standby fee (THB) from customer_service_fees
 * (feeType "standby"). Used when no standby_rate_entries match the customer.
 */
function buildStandbyServiceFeeMap(snap: admin.firestore.QuerySnapshot): Map<string, number> {
    const map = new Map<string, number>();
    snap.docs.forEach((d) => {
        const data = d.data();
        if (data.feeType !== "standby") return;
        const cid = String(data.customerId ?? "").trim();
        const amt = Number(data.amountThb ?? 0);
        if (cid && Number.isFinite(amt) && amt >= 0) map.set(cid, amt);
    });
    return map;
}

function normalizeJobCategory(value: unknown): "PRIMARY" | "SUPPLEMENTARY" | undefined {
    return value === "SUPPLEMENTARY" ? "SUPPLEMENTARY" : value === "PRIMARY" ? "PRIMARY" : undefined;
}

/**
 * Resolve customerId + jobCategory for a standby record in a single task read.
 *
 * customerId:
 *   1. Use record.customerId if already set
 *   2. Else look up through record.taskId → task.sourceHubLinkedCustomerId / destinationLinkedCustomerId
 *
 * jobCategory (ADR 0010): the record's own value wins, else the authoritative task value. Neither
 * present ⇒ undefined — left ABSENT rather than guessed PRIMARY, so the Billing Document shows the
 * loud "unverified" marker instead of silently labelling หลัก. It is a denormalized label only:
 * standby is billed at a flat per-event rate that never depends on หลัก/เสริม.
 */
async function resolveStandbyTaskContext(
    db: admin.firestore.Firestore,
    data: Record<string, unknown>
): Promise<{ customerId: string; jobCategory?: "PRIMARY" | "SUPPLEMENTARY" }> {
    const recordCustomerId = typeof data.customerId === "string" ? data.customerId.trim() : "";
    const recordCategory = normalizeJobCategory(data.jobCategory);

    let taskCustomerId = "";
    let taskCategory: "PRIMARY" | "SUPPLEMENTARY" | undefined;
    const taskId = typeof data.taskId === "string" ? data.taskId.trim() : "";
    if (taskId) {
        try {
            const taskSnap = await db.collection(COL_TASKS).doc(taskId).get();
            if (taskSnap.exists) {
                const t = taskSnap.data() as Record<string, unknown>;
                taskCustomerId =
                    (typeof t.sourceHubLinkedCustomerId === "string" ? t.sourceHubLinkedCustomerId.trim() : "") ||
                    (typeof t.destinationLinkedCustomerId === "string" ? t.destinationLinkedCustomerId.trim() : "");
                taskCategory = normalizeJobCategory(t.jobCategory);
            }
        } catch {
            /* leave task-derived values empty on read failure */
        }
    }

    return {
        customerId: recordCustomerId || taskCustomerId,
        jobCategory: recordCategory ?? taskCategory,
    };
}

interface StandbyBillingResponse {
    ok: boolean;
    skipped?: boolean;
    /** True when an existing price was left alone because its period is already invoiced (ADR 0008 §5). */
    blocked?: boolean;
    /** The invoice that blocked the rewrite, for the admin-facing message. */
    invoiceNumber?: string;
    billingEstimateThb?: number;
    error?: string;
}

/**
 * The billing date of a standby is when the service completed — `endedAt` (ADR 0008 §3). This is the
 * same axis the Billing Document groups by, so the rate that priced a record is always the rate
 * effective in the period it is billed in. `createdAt` is provenance only and must NOT decide a
 * period: the admin backfill dialog writes it as `serverTimestamp()`, i.e. the day someone typed a
 * past event in, so using it would bill a June standby in August at August rates.
 */
export function standbyBillingDateMs(data: Record<string, unknown>): number {
    return (
        timestampLikeToMillis(data.endedAt) ||
        timestampLikeToMillis(data.startedAt) ||
        timestampLikeToMillis(data.createdAt)
    );
}

/** Shared core: compute and persist billing for one standby record (idempotent unless forced). */
async function tryWriteStandbyBillingSnapshot(
    db: admin.firestore.Firestore,
    recordId: string,
    data: Record<string, unknown>,
    docRef: admin.firestore.DocumentReference,
    rateEntries: StandbyRateEntry[],
    serviceFeeByCustomer: Map<string, number>,
    forceRecompute?: boolean,
    locks?: BillingPeriodLocks
): Promise<StandbyBillingResponse> {
    if (data.status !== "completed") {
        return { ok: true, skipped: true, error: "Standby record is not completed yet" };
    }

    const hasPrice = typeof data.billingEstimateThb === "number";
    if (!forceRecompute && hasPrice) {
        return { ok: true, skipped: true, billingEstimateThb: data.billingEstimateThb as number };
    }

    const { customerId, jobCategory } = await resolveStandbyTaskContext(db, data);
    if (!customerId) {
        logger.warn("[standbyBilling] no customerId on record", { recordId });
        return { ok: false, error: "No customerId — set customerId on the standby record or link a task" };
    }

    const billDateMs = standbyBillingDateMs(data) || Date.now();

    // ADR 0008 §5: only a draft period may be repriced. An already-priced row in a sent/paid period
    // keeps its number — the invoice is in the customer's hands and the system must not contradict
    // it. Checked against the customer it was BILLED under, which is what built that invoice.
    if (hasPrice && locks) {
        const billedCustomerId =
            (typeof data.billingCustomerId === "string" && data.billingCustomerId.trim()) || customerId;
        const lock = locks.lockFor(billedCustomerId, billDateMs);
        if (lock) {
            return {
                ok: true,
                skipped: true,
                blocked: true,
                invoiceNumber: lock.invoiceNumber,
                billingEstimateThb: data.billingEstimateThb as number,
                error: `Period already invoiced (${lock.invoiceNumber}, ${lock.status}) — cancel or credit-note it before repricing`,
            };
        }
    }

    const computed = computeStandbyBilling(billDateMs, customerId, rateEntries);
    if (computed) {
        await docRef.update({
            billingEstimateThb: computed.rateThb,
            billingCustomerId: computed.customerId,
            billingRateEntryId: computed.rateEntryId,
            billingEffectiveFromDateStr: computed.effectiveFromDateStr ?? null,
            billingComputedAt: admin.firestore.FieldValue.serverTimestamp(),
            // Denormalize หลัก/เสริม from the task (ADR 0010). Omitted when neither record nor task
            // carries one — Admin SDK rejects undefined, and absence keeps the "unverified" marker.
            ...(jobCategory ? { jobCategory } : {}),
        });
        return { ok: true, billingEstimateThb: computed.rateThb };
    }

    // Fallback: use the flat Stand By service fee from customer_service_fees
    const serviceFee = serviceFeeByCustomer.get(customerId);
    if (typeof serviceFee === "number") {
        await docRef.update({
            billingEstimateThb: serviceFee,
            billingCustomerId: customerId,
            billingRateEntryId: "service_fee",
            billingEffectiveFromDateStr: null,
            billingComputedAt: admin.firestore.FieldValue.serverTimestamp(),
            // Denormalize หลัก/เสริม from the task (ADR 0010) — see the rate-entry path above.
            ...(jobCategory ? { jobCategory } : {}),
        });
        return { ok: true, billingEstimateThb: serviceFee };
    }

    logger.warn("[standbyBilling] no matching standby rate entry or service fee", { recordId, customerId });
    return { ok: false, error: `No standby rate entry or service fee found for customer ${customerId}` };
}

// ─── Callable: compute single standby record ──────────────────────────────────

interface ComputeStandbyRequest {
    standbyId: string;
    /** Rewrite an existing price (ADR 0008 §5) — refused when the period is already invoiced. */
    forceRecompute?: boolean;
}

/**
 * HTTPS Callable: compute and persist billing for a single completed standby record.
 * Idempotent by default: if billingEstimateThb already exists, returns skipped: true. Used by the
 * Billing Document repair panel right after an admin assigns a customer to an unpriced record.
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
        const [docSnap, ratesSnap, feesSnap, locks] = await Promise.all([
            db.collection(COL_STANDBY_RECORDS).doc(standbyId).get(),
            db.collection(COL_STANDBY_RATES).get(),
            db.collection(COL_SERVICE_FEES).get(),
            loadBillingPeriodLocks(db),
        ]);

        if (!docSnap.exists) {
            throw new HttpsError("not-found", "Standby record not found");
        }

        const rateEntries = ratesSnap.docs.map(mapStandbyRateDoc);
        const serviceFeeByCustomer = buildStandbyServiceFeeMap(feesSnap);
        const data = docSnap.data() as Record<string, unknown>;
        return tryWriteStandbyBillingSnapshot(
            db, standbyId, data, docSnap.ref, rateEntries, serviceFeeByCustomer,
            request.data?.forceRecompute === true, locks
        );
    }
);

// ─── Scheduled: auto-compute for recently completed standby records ───────────

/**
 * Scheduled: scan standby_records that ENDED in the last 30 minutes and have no billing yet.
 * Runs every 15 minutes. Scans by `endedAt` — the same axis everything else uses (ADR 0008 §3).
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

        const [snap, ratesSnap, feesSnap] = await Promise.all([
            db.collection(COL_STANDBY_RECORDS)
                .where("status", "==", "completed")
                .where("endedAt", ">=", since)
                .limit(100)
                .get(),
            db.collection(COL_STANDBY_RATES).get(),
            db.collection(COL_SERVICE_FEES).get(),
        ]);

        const rateEntries = ratesSnap.docs.map(mapStandbyRateDoc);
        const serviceFeeByCustomer = buildStandbyServiceFeeMap(feesSnap);
        let written = 0;
        let failed = 0;

        for (const doc of snap.docs) {
            const data = doc.data() as Record<string, unknown>;
            if (typeof data.billingEstimateThb === "number") continue;

            const result = await tryWriteStandbyBillingSnapshot(db, doc.id, data, doc.ref, rateEntries, serviceFeeByCustomer);
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
    /** Rewrite prices that already exist (ADR 0008 §5) — still refused for invoiced periods. */
    forceRecompute?: boolean;
}

interface BackfillStandbyResponse {
    scanned: number;
    eligible: number;
    written: number;
    skipped: number;
    failed: number;
    /** Rows left untouched because their period is already invoiced. */
    blocked: number;
    blockedInvoices: string[];
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
 * Admin-only: scan completed standby_records by `endedAt` — the axis the Billing Document groups by
 * (ADR 0008 §3-4) — and write billing. With `forceRecompute` it also rewrites existing prices,
 * except in periods that already carry a sent/paid invoice.
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
        const forceRecompute = request.data?.forceRecompute === true;

        const db = admin.firestore();
        const { start, end } = bangkokBoundsToTimestamps(fromDateStr, toDateStr);

        // Surface the real Firestore error instead of a bare INTERNAL (ADR 0008 §8). The
        // `status == completed` + `endedAt` range query needs the composite index that the old
        // `createdAt`-only scan never did, so a missing/​building index is the expected failure here —
        // and Firestore's message carries the console URL that creates it.
        let snap: admin.firestore.QuerySnapshot;
        let ratesSnap: admin.firestore.QuerySnapshot;
        let feesSnap: admin.firestore.QuerySnapshot;
        let locks: BillingPeriodLocks;
        try {
            [snap, ratesSnap, feesSnap, locks] = await Promise.all([
                // No explicit orderBy: the range on `endedAt` already implies ascending order on it,
                // which is served by the SAME composite index the Billing Document's own standby
                // query uses (status + endedAt) — so this needs no new index deployment. An explicit
                // `orderBy(endedAt, "desc")` would ask for a differently-ordered index.
                db.collection(COL_STANDBY_RECORDS)
                    .where("status", "==", "completed")
                    .where("endedAt", ">=", start)
                    .where("endedAt", "<=", end)
                    .limit(maxScan)
                    .get(),
                db.collection(COL_STANDBY_RATES).get(),
                db.collection(COL_SERVICE_FEES).get(),
                loadBillingPeriodLocks(db),
            ]);
        } catch (e) {
            const message = e instanceof Error ? e.message : String(e);
            logger.error("[standbyBilling] backfill query failed", { message });
            throw new HttpsError("failed-precondition", `Standby backfill query failed: ${message}`);
        }

        const rateEntries = ratesSnap.docs.map(mapStandbyRateDoc);
        const serviceFeeByCustomer = buildStandbyServiceFeeMap(feesSnap);
        let eligible = 0;
        let written = 0;
        let skipped = 0;
        let failed = 0;
        let blocked = 0;
        let attempted = 0;
        const blockedInvoices = new Set<string>();
        const failures: Array<{ standbyId: string; error?: string }> = [];

        for (const doc of snap.docs) {
            const data = doc.data() as Record<string, unknown>;
            // status is already constrained by the query; keep the guard for defence in depth.
            if (data.status !== "completed") continue;
            if (!forceRecompute && typeof data.billingEstimateThb === "number") continue;
            eligible++;

            if (written >= maxWrite) continue;

            attempted++;
            const result = await tryWriteStandbyBillingSnapshot(
                db, doc.id, data, doc.ref, rateEntries, serviceFeeByCustomer, forceRecompute, locks
            );
            if (result.blocked === true) {
                blocked++;
                if (result.invoiceNumber) blockedInvoices.add(result.invoiceNumber);
            } else if (result.ok === true && result.skipped !== true && result.billingEstimateThb != null) {
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
            blocked,
            blockedInvoices: [...blockedInvoices],
            failures,
            capped: eligible > attempted,
        };
    }
);
