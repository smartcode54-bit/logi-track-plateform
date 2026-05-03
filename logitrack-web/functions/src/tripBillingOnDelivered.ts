import * as admin from "firebase-admin";
import { logger } from "firebase-functions";
import { onCall, HttpsError } from "firebase-functions/v2/https";
import {
    computeTripBillingFromParts,
    extractHubId,
    getTripBillingDateMs,
    normalizeDestinationCode,
    timestampLikeToMillis,
    type BillingRateEntry,
    type FuelRateAdjustment,
    type TaskBillingInput,
    type TripBillingTimestamps,
} from "./core/billingCompute";

const COL_TASKS = "tasks";
const COL_RATE_ENTRIES = "customer_rate_entries";
const COL_FUEL_ADJ = "customer_fuel_rate_adjustments";
const COL_TRIP_RECORDS = "trip_records";

function normalizeStoredCode(v: string | null | undefined): string {
    return (v ?? "").trim().toUpperCase();
}

function mapRateDoc(customerId: string, doc: admin.firestore.QueryDocumentSnapshot): BillingRateEntry {
    const d = doc.data();
    return {
        id: doc.id,
        customerId,
        importId: String(d.importId ?? ""),
        hubId: normalizeStoredCode(String(d.hubId ?? "")),
        destinationCode: normalizeDestinationCode(String(d.destinationCode ?? "")),
        vehicleClass: normalizeStoredCode(String(d.vehicleClass ?? "4WJ")),
        rateThb: Number(d.rateThb ?? 0),
        effectiveFromMs: timestampLikeToMillis(d.effectiveFrom),
    };
}

function mapFuelDoc(customerId: string, doc: admin.firestore.QueryDocumentSnapshot): FuelRateAdjustment {
    const d = doc.data();
    return {
        id: doc.id,
        customerId,
        effectiveFromMs: timestampLikeToMillis(d.effectiveFrom),
        rateMultiplier: Number(d.rateMultiplier ?? 1),
        addThbPerTrip: Number(d.addThbPerTrip ?? 0),
    };
}

interface ComputeBillingResponse {
    ok: boolean;
    skipped?: boolean;
    billingEstimateThb?: number;
    error?: string;
}

/** Shared core: persist billing snapshot from already-read trip fields (idempotent). */
async function tryWriteBillingSnapshotFromTripData(
    db: admin.firestore.Firestore,
    tripId: string,
    data: Record<string, unknown>,
    tripRef: admin.firestore.DocumentReference
): Promise<ComputeBillingResponse> {
    if (data.status !== "delivered") {
        return { ok: true, skipped: true, error: "Trip is not delivered yet" };
    }

    if (typeof data.billingEstimateThb === "number") {
        return { ok: true, skipped: true, billingEstimateThb: data.billingEstimateThb as number };
    }

    const taskId = typeof data.taskId === "string" ? data.taskId.trim() : "";
    if (!taskId) {
        logger.warn("[billingSnapshot] delivered trip missing taskId", { tripId });
        return { ok: false, error: "Trip missing taskId" };
    }

    let taskSnap: admin.firestore.DocumentSnapshot;
    try {
        taskSnap = await db.collection(COL_TASKS).doc(taskId).get();
    } catch (e) {
        logger.error("[billingSnapshot] task load failed", e);
        return { ok: false, error: "Failed to load task" };
    }
    if (!taskSnap.exists) {
        logger.warn("[billingSnapshot] task not found", { taskId, tripId });
        return { ok: false, error: "Task not found" };
    }

    const t = taskSnap.data() as Record<string, unknown>;
    const taskInput: TaskBillingInput = {
        sourceHub: typeof t.sourceHub === "string" ? t.sourceHub : undefined,
        destination: typeof t.destination === "string" ? t.destination : undefined,
        truckType: typeof t.truckType === "string" ? t.truckType : undefined,
        sourceHubLinkedCustomerId:
            typeof t.sourceHubLinkedCustomerId === "string" ? t.sourceHubLinkedCustomerId : undefined,
        destinationLinkedCustomerId:
            typeof t.destinationLinkedCustomerId === "string" ? t.destinationLinkedCustomerId : undefined,
    };

    const customerId =
        taskInput.sourceHubLinkedCustomerId?.trim() ||
        taskInput.destinationLinkedCustomerId?.trim() ||
        "";
    if (!customerId) {
        logger.warn("[billingSnapshot] task has no linked customer", { taskId, tripId });
        return { ok: false, error: "Task has no linked customer" };
    }

    const [rateSnap, fuelSnap] = await Promise.all([
        db.collection(COL_RATE_ENTRIES).where("customerId", "==", customerId).get(),
        db.collection(COL_FUEL_ADJ).where("customerId", "==", customerId).get(),
    ]);

    const rateEntries: BillingRateEntry[] = rateSnap.docs.map((d) => mapRateDoc(customerId, d));
    const fuelAdjustments: FuelRateAdjustment[] = fuelSnap.docs.map((d) => mapFuelDoc(customerId, d));

    const tripParts: TripBillingTimestamps = {
        deliveredTimestamp: data.deliveredTimestamp,
        createdAt: data.createdAt,
    };

    const computed = computeTripBillingFromParts(tripParts, taskInput, rateEntries, fuelAdjustments);
    if (!computed) {
        const hubId = extractHubId(taskInput.sourceHub);
        const destination = normalizeDestinationCode(taskInput.destination);
        const vehicleClass = normalizeStoredCode(taskInput.truckType || "4WJ");
        const billDateMs = getTripBillingDateMs(tripParts);
        logger.warn("[billingSnapshot] could not compute billing (no matching rate row)", {
            tripId,
            taskId,
            customerId,
            hubId,
            destination,
            vehicleClass,
            billDateMs,
            rateRowsForCustomer: rateEntries.length,
            rawTaskSourceHub: taskInput.sourceHub,
            rawTaskDestination: taskInput.destination,
        });
        return { ok: false, error: "No matching rate entry found" };
    }

    await tripRef.update({
        billingEstimateThb: computed.finalRateThb,
        billingBaseRateThb: computed.baseRateThb,
        billingRateImportId: computed.rateImportId,
        billingLookupHubId: computed.lookupHubId,
        billingLookupDestination: computed.lookupDestination,
        billingFuelAdjustmentId: computed.fuelAdjustmentId ?? null,
        billingRateMultiplier: computed.rateMultiplier,
        billingAddThbPerTrip: computed.addThbPerTrip,
        billingEffectiveFromDateStr: computed.effectiveFromDateStr ?? null,
        billingCustomerId: computed.customerId,
        updatedAt: admin.firestore.FieldValue.serverTimestamp(),
    });

    return { ok: true, billingEstimateThb: computed.finalRateThb };
}

interface ComputeBillingRequest {
    tripId: string;
}

/**
 * HTTPS Callable: compute and persist billing snapshot for a delivered trip.
 *
 * Call from mobile after marking trip as `delivered`, or from web admin backfill.
 * Idempotent: if `billingEstimateThb` already exists, returns `skipped: true`.
 */
export const computeTripBillingSnapshot = onCall<ComputeBillingRequest, Promise<ComputeBillingResponse>>(
    {
        region: "asia-southeast1",
        enforceAppCheck: true,
        consumeAppCheckToken: true,
    },
    async (request): Promise<ComputeBillingResponse> => {
        const tripId = request.data?.tripId?.trim();
        if (!tripId) {
            throw new HttpsError("invalid-argument", "tripId is required");
        }

        const db = admin.firestore();
        const tripRef = db.collection(COL_TRIP_RECORDS).doc(tripId);
        const tripSnap = await tripRef.get();
        if (!tripSnap.exists) {
            throw new HttpsError("not-found", "Trip not found");
        }

        const data = tripSnap.data() as Record<string, unknown>;
        return tryWriteBillingSnapshotFromTripData(db, tripId, data, tripRef);
    }
);

interface BackfillBillingRequest {
    /** Inclusive start date, calendar day in Asia/Bangkok (yyyy-MM-dd). */
    fromDateStr: string;
    /** Inclusive end date, calendar day in Asia/Bangkok (yyyy-MM-dd). */
    toDateStr: string;
    /** Max trip_records documents to read (ordered by createdAt desc). Default 500, max 2000. */
    maxScan?: number;
    /** Max successful writes per invocation (stops attempting more writes after this). Default 200, max 500. */
    maxWrite?: number;
}

interface BackfillBillingFailure {
    tripId: string;
    error?: string;
}

interface BackfillBillingResponse {
    scanned: number;
    /** delivered without billingEstimateThb before writes attempted */
    eligible: number;
    written: number;
    skipped: number;
    failed: number;
    failures: BackfillBillingFailure[];
    /** true if eligible > written (more work may remain — widen range or run again) */
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
 * Admin-only: scan trip_records by createdAt (Bangkok calendar range), write billing snapshots
 * for delivered trips missing billingEstimateThb (same rules as computeTripBillingSnapshot).
 */
export const backfillTripBillingSnapshots = onCall<BackfillBillingRequest, Promise<BackfillBillingResponse>>(
    {
        region: "asia-southeast1",
        enforceAppCheck: false, // Admin-only function protected by auth check; App Check enforcement disabled to avoid web client token issues
    },
    async (request): Promise<BackfillBillingResponse> => {
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

        const snap = await db
            .collection(COL_TRIP_RECORDS)
            .where("createdAt", ">=", start)
            .where("createdAt", "<=", end)
            .orderBy("createdAt", "desc")
            .limit(maxScan)
            .get();

        let eligible = 0;
        let written = 0;
        let skipped = 0;
        let failed = 0;
        let attempted = 0;
        const failures: BackfillBillingFailure[] = [];

        for (const doc of snap.docs) {
            const data = doc.data() as Record<string, unknown>;
            if (data.status !== "delivered") continue;
            if (typeof data.billingEstimateThb === "number") continue;
            eligible++;

            if (written >= maxWrite) continue;

            attempted++;
            const result = await tryWriteBillingSnapshotFromTripData(db, doc.id, data, doc.ref);
            if (result.ok === true && result.skipped !== true && result.billingEstimateThb != null) {
                written++;
            } else if (result.skipped === true) {
                skipped++;
            } else {
                failed++;
                if (failures.length < 25) {
                    failures.push({ tripId: doc.id, error: result.error });
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
