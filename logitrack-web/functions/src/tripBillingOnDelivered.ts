import * as admin from "firebase-admin";
import { logger } from "firebase-functions";
import { onCall, HttpsError } from "firebase-functions/v2/https";
import { onSchedule } from "firebase-functions/v2/scheduler";
import {
    computeTripBillingFromParts,
    computeMultiDeliveryBilling,
    extractHubId,
    getTripBillingDateMs,
    normalizeDestinationCode,
    normalizeVehicleClass,
    timestampLikeToMillis,
    type BillingRateEntry,
    type FuelRateAdjustment,
    type TaskBillingInput,
    type TripBillingTimestamps,
    type DeliveryStopForBilling,
} from "./core/billingCompute";

const COL_TASKS = "tasks";
const COL_RATE_ENTRIES = "customer_rate_entries";
const COL_FUEL_ADJ = "customer_fuel_rate_adjustments";
const COL_TRIP_RECORDS = "trip_records";
const COL_HUBS = "hubs";
const COL_SERVICE_FEES = "customer_service_fees";

function normalizeStoredCode(v: string | null | undefined): string {
    return (v ?? "").trim().toUpperCase();
}

/**
 * Hub lookup maps. Kept as TWO separate maps on purpose:
 *  - nameToCode: display name ("ประเวศ18") → source_id ("SPK890146")
 *  - codeToName: source_id ("SPK890174") → display name ("ห้วยขวาง10")
 *
 * They MUST NOT be merged into one map: a merged map makes resolveDestination
 * ambiguous — a destination already stored as a code ("SPK890174") would match
 * the reverse (code→name) entry and get wrongly translated to a Thai display
 * name, which then fails to match rate cards keyed by code. (This was the
 * "No rate: SPK-GW → ห้วยขวาง10" bug.)
 */
interface HubMaps {
    nameToCode: Map<string, string>;
    codeToName: Map<string, string>;
}

async function buildHubMaps(db: admin.firestore.Firestore): Promise<HubMaps> {
    const snap = await db.collection(COL_HUBS).get();
    const nameToCode = new Map<string, string>();
    const codeToName = new Map<string, string>();
    snap.docs.forEach((d) => {
        const data = d.data();
        const code = String(data.source_id ?? data.hubId ?? "").trim();
        if (!code) return;
        for (const nameField of [data.source_name_th, data.source_name_en, data.hubName]) {
            const name = typeof nameField === "string" ? nameField.trim() : "";
            // Skip names equal to the code so a code never maps to itself in nameToCode.
            if (name && name !== code && !nameToCode.has(name)) nameToCode.set(name, code);
        }
        const displayName = typeof data.source_name_th === "string" ? data.source_name_th.trim() : "";
        if (displayName && !codeToName.has(code)) codeToName.set(code, displayName);
    });
    return { nameToCode, codeToName };
}

/** Translate a display name → code. Codes (and unknown values) pass through unchanged. */
function resolveNameToCode(raw: string | undefined, nameToCode?: Map<string, string>): string | undefined {
    if (!raw || !nameToCode) return raw;
    return nameToCode.get(raw.trim()) ?? raw;
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
        jobCategory: d.jobCategory === "SUPPLEMENTARY" ? "SUPPLEMENTARY" : "PRIMARY",
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
    tripRef: admin.firestore.DocumentReference,
    hubMaps?: HubMaps,
    forceRecompute?: boolean,
    rateCache?: Map<string, { rateEntries: BillingRateEntry[]; fuelAdjustments: FuelRateAdjustment[] }>,
    taskCache?: Map<string, Record<string, unknown>>
): Promise<ComputeBillingResponse> {
    if (data.status !== "delivered") {
        return { ok: true, skipped: true, error: "Trip is not delivered yet" };
    }

    if (!forceRecompute && typeof data.billingEstimateThb === "number") {
        return { ok: true, skipped: true, billingEstimateThb: data.billingEstimateThb as number };
    }

    // Frozen pricing (ADR-0005): a supplementary trip — or any manually-overridden snapshot —
    // keeps its agreed price. Even a forced recompute must NOT overwrite it once it has one.
    const tripFrozen = data.billingManualOverride === true || data.jobCategory === "SUPPLEMENTARY";
    if (forceRecompute && tripFrozen && typeof data.billingEstimateThb === "number") {
        return { ok: true, skipped: true, billingEstimateThb: data.billingEstimateThb as number };
    }

    const taskId = typeof data.taskId === "string" ? data.taskId.trim() : "";
    if (!taskId) {
        logger.warn("[billingSnapshot] delivered trip missing taskId", { tripId });
        return { ok: false, error: "Trip missing taskId" };
    }

    let t: Record<string, unknown> | undefined = taskCache?.get(taskId);
    if (!t) {
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
        t = taskSnap.data() as Record<string, unknown>;
    }
    const rawDestination = typeof t.destination === "string" ? t.destination : undefined;
    const rawSourceHub = typeof t.sourceHub === "string" ? t.sourceHub : undefined;
    const nameToCode = hubMaps?.nameToCode;
    const codeToName = hubMaps?.codeToName;
    // Resolve to CODE form only: display name → source_id ("J&T EXPRESS บางปู" → "SPK-GW",
    // "ประเวศ18" → "SPK890146"). Values already stored as a code ("SPK890174", "SPK-GW")
    // pass through unchanged so they match rate cards keyed by code.
    const resolvedSourceHub = resolveNameToCode(rawSourceHub, nameToCode);
    const resolvedDestination = resolveNameToCode(rawDestination, nameToCode);
    const taskInput: TaskBillingInput = {
        sourceHub: resolvedSourceHub,
        destination: resolvedDestination,
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

    // หลัก/เสริม (ADR-0006, supersedes ADR-0005 decision #3): admins may now pick it explicitly
    // on the task at assign time. When present, it's authoritative — the PRIMARY probe is
    // skipped and billing goes straight to the chosen category's rate card. A legacy task with
    // no explicit value (created before this change) keeps the original derivation: try PRIMARY,
    // fall back to SUPPLEMENTARY. Either way the resolved category is frozen when SUPPLEMENTARY.
    const explicitJobCategory: "PRIMARY" | "SUPPLEMENTARY" | undefined =
        t.jobCategory === "SUPPLEMENTARY" ? "SUPPLEMENTARY" : t.jobCategory === "PRIMARY" ? "PRIMARY" : undefined;
    const overrideFor = (cat: "PRIMARY" | "SUPPLEMENTARY") =>
        cat === "SUPPLEMENTARY" ? { billingManualOverride: true } : {};

    let rateEntries: BillingRateEntry[];
    let fuelAdjustments: FuelRateAdjustment[];
    if (rateCache?.has(customerId)) {
        ({ rateEntries, fuelAdjustments } = rateCache.get(customerId)!);
    } else {
        const [rateSnap, fuelSnap] = await Promise.all([
            db.collection(COL_RATE_ENTRIES).where("customerId", "==", customerId).get(),
            db.collection(COL_FUEL_ADJ).where("customerId", "==", customerId).get(),
        ]);
        rateEntries = rateSnap.docs.map((d) => mapRateDoc(customerId, d));
        fuelAdjustments = fuelSnap.docs.map((d) => mapFuelDoc(customerId, d));
        rateCache?.set(customerId, { rateEntries, fuelAdjustments });
    }

    const tripParts: TripBillingTimestamps = {
        deliveredTimestamp: data.deliveredTimestamp,
        createdAt: data.createdAt,
    };

    // Check if this is a multi-delivery trip
    const isMultiDelivery = data.isMultiDelivery === true;
    const deliveryStopsProgress = Array.isArray(data.deliveryStopsProgress) ? data.deliveryStopsProgress : [];

    if (isMultiDelivery && deliveryStopsProgress.length >= 2) {
        // Multi-delivery billing: stop[0] = base rate (planned destination), stop[1+] = extra stops charged
        const stops: DeliveryStopForBilling[] = deliveryStopsProgress
            .filter((stop: any) => stop.destination && stop.status === "delivered")
            .map((stop: any) => ({
                index: typeof stop.index === "number" ? stop.index : 1,
                destination: String(stop.destination ?? ""),
            }));

        if (stops.length < 2) {
            logger.warn("[billingSnapshot] multi-delivery trip has < 2 delivered stops", {
                tripId,
                taskId,
                delivered: stops.length,
            });
            return { ok: false, error: "Multi-delivery trip has < 2 delivered stops" };
        }

        // Flat extra-stop service fee (customer_service_fees, feeType "extra_stop").
        // When set, every extra stop (stop 2+) is charged this fixed amount instead of a
        // per-route rate-card lookup.
        let extraStopFeeThb: number | undefined;
        try {
            const feeSnap = await db
                .collection(COL_SERVICE_FEES)
                .where("customerId", "==", customerId)
                .get();
            const extraStopDoc = feeSnap.docs.find((d) => d.data().feeType === "extra_stop");
            if (extraStopDoc) {
                const amt = Number(extraStopDoc.data().amountThb ?? 0);
                if (Number.isFinite(amt) && amt >= 0) extraStopFeeThb = amt;
            }
        } catch (e) {
            logger.warn("[billingSnapshot] failed to load extra_stop service fee", { customerId, error: String(e) });
        }

        // Category: honor an explicit task-level choice (ADR-0006) — skip the PRIMARY probe
        // entirely when the task is explicitly marked เสริม. Otherwise (explicit หลัก, or a
        // legacy task with no explicit value) derive as before: try PRIMARY, fall back to
        // SUPPLEMENTARY (ADR-0005).
        const mVehicleClass = normalizeVehicleClass(taskInput.truckType || "4WJ");
        let multiComputed: ReturnType<typeof computeMultiDeliveryBilling>;
        let resolvedCategory: "PRIMARY" | "SUPPLEMENTARY" = "PRIMARY";
        if (explicitJobCategory === "SUPPLEMENTARY") {
            multiComputed = computeMultiDeliveryBilling(
                tripParts, taskInput, stops, mVehicleClass, rateEntries, fuelAdjustments, extraStopFeeThb, "SUPPLEMENTARY"
            );
            resolvedCategory = "SUPPLEMENTARY";
        } else if (explicitJobCategory === "PRIMARY") {
            // Explicit หลัก (ADR-0006): PRIMARY rate rule only, no fallback to เสริม.
            multiComputed = computeMultiDeliveryBilling(
                tripParts, taskInput, stops, mVehicleClass, rateEntries, fuelAdjustments, extraStopFeeThb, "PRIMARY"
            );
            resolvedCategory = "PRIMARY";
        } else {
            // Legacy task with no explicit jobCategory: try PRIMARY, fall back to SUPPLEMENTARY.
            multiComputed = computeMultiDeliveryBilling(
                tripParts, taskInput, stops, mVehicleClass, rateEntries, fuelAdjustments, extraStopFeeThb, "PRIMARY"
            );
            if (!multiComputed) {
                multiComputed = computeMultiDeliveryBilling(
                    tripParts, taskInput, stops, mVehicleClass, rateEntries, fuelAdjustments, extraStopFeeThb, "SUPPLEMENTARY"
                );
                if (multiComputed) resolvedCategory = "SUPPLEMENTARY";
            }
        }

        if (!multiComputed) {
            logger.warn("[billingSnapshot] could not compute multi-delivery billing", {
                tripId,
                taskId,
                customerId,
                stops: stops.length,
            });
            return { ok: false, error: "Could not compute multi-delivery billing" };
        }

        await tripRef.update({
            billingEstimateThb: multiComputed.totalBillingThb,
            billingBaseRateThb: multiComputed.baseRateThb,
            billingStopChargeThb: multiComputed.stopChargeThb,
            billingIsMultiDelivery: true,
            billingLookupHubId: extractHubId(taskInput.sourceHub),
            billingLookupDestination: multiComputed.stopBreakdown[0]?.destination ?? null,
            billingMultiDeliveryBreakdown: multiComputed.stopBreakdown.map((stop) => ({
                stopIndex: stop.stopIndex,
                destination: stop.destination,
                baseRateThb: stop.baseRateThb,
                finalRateThb: stop.finalRateThb,
            })),
            billingFuelAdjustmentId: multiComputed.fuelAdjustmentId ?? null,
            billingRateMultiplier: multiComputed.rateMultiplier,
            billingCustomerId: multiComputed.customerId,
            jobCategory: resolvedCategory,
            ...overrideFor(resolvedCategory),
            updatedAt: admin.firestore.FieldValue.serverTimestamp(),
        });

        return { ok: true, billingEstimateThb: multiComputed.totalBillingThb };
    } else {
        // Single-delivery billing. Category: honor an explicit task-level choice (ADR-0006) —
        // skip the PRIMARY probe entirely when the task is explicitly marked เสริม. Otherwise
        // (explicit หลัก, or a legacy task with no explicit value) derive as before: try PRIMARY,
        // fall back to SUPPLEMENTARY (ADR-0005).
        let resolvedCategory: "PRIMARY" | "SUPPLEMENTARY" = "PRIMARY";
        const computeForCategory = (cat: "PRIMARY" | "SUPPLEMENTARY") => {
            let c = computeTripBillingFromParts(tripParts, taskInput, rateEntries, fuelAdjustments, cat);
            // Fallback for customers whose rate cards key destination by DISPLAY NAME instead of
            // code: if no rate matched the code form, retry once with the Thai display name.
            if (!c && codeToName && rawDestination) {
                const altDest = codeToName.get((resolvedDestination ?? rawDestination).trim())
                    ?? codeToName.get(rawDestination.trim());
                if (altDest && altDest !== resolvedDestination) {
                    c = computeTripBillingFromParts(
                        tripParts, { ...taskInput, destination: altDest }, rateEntries, fuelAdjustments, cat
                    );
                }
            }
            return c;
        };
        let computed: ReturnType<typeof computeForCategory>;
        if (explicitJobCategory === "SUPPLEMENTARY") {
            computed = computeForCategory("SUPPLEMENTARY");
            resolvedCategory = "SUPPLEMENTARY";
        } else if (explicitJobCategory === "PRIMARY") {
            // Explicit หลัก (ADR-0006): PRIMARY rate rule only. No fallback to เสริม —
            // a missing PRIMARY rate must fail loud, not silently bill (and freeze) at a
            // supplementary price.
            computed = computeForCategory("PRIMARY");
            resolvedCategory = "PRIMARY";
        } else {
            // Legacy task with no explicit jobCategory: derive as before (ADR-0005) —
            // try PRIMARY, fall back to SUPPLEMENTARY.
            computed = computeForCategory("PRIMARY");
            if (!computed) {
                computed = computeForCategory("SUPPLEMENTARY");
                if (computed) resolvedCategory = "SUPPLEMENTARY";
            }
        }
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
            return { ok: false, error: `No rate: ${hubId} → ${destination} (${vehicleClass})` };
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
            jobCategory: resolvedCategory,
            ...overrideFor(resolvedCategory),
            updatedAt: admin.firestore.FieldValue.serverTimestamp(),
        });

        return { ok: true, billingEstimateThb: computed.finalRateThb };
    }
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
        enforceAppCheck: false, // Web admin calls without App Check token; auth protected by request.auth check below
    },
    async (request): Promise<ComputeBillingResponse> => {
        if (!request.auth) {
            throw new HttpsError("unauthenticated", "Must be authenticated");
        }
        const tripId = request.data?.tripId?.trim();
        if (!tripId) {
            throw new HttpsError("invalid-argument", "tripId is required");
        }

        const db = admin.firestore();
        const [tripSnap, hubMaps] = await Promise.all([
            db.collection(COL_TRIP_RECORDS).doc(tripId).get(),
            buildHubMaps(db),
        ]);
        if (!tripSnap.exists) {
            throw new HttpsError("not-found", "Trip not found");
        }

        const data = tripSnap.data() as Record<string, unknown>;
        return tryWriteBillingSnapshotFromTripData(db, tripId, data, tripSnap.ref, hubMaps);
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
    /** If true, recompute even trips that already have billingEstimateThb (useful after fuel adjustment changes). */
    forceRecompute?: boolean;
    /** If set, only recompute trips for this customerId. */
    customerId?: string;
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
        timeoutSeconds: 540,
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
        const forceRecompute = request.data?.forceRecompute === true;
        const filterCustomerId = request.data?.customerId?.trim() ?? "";

        const db = admin.firestore();
        const { start, end } = bangkokBoundsToTimestamps(fromDateStr, toDateStr);

        const [snap, hubMaps] = await Promise.all([
            db.collection(COL_TRIP_RECORDS)
                .where("createdAt", ">=", start)
                .where("createdAt", "<=", end)
                .orderBy("createdAt", "desc")
                .limit(maxScan)
                .get(),
            buildHubMaps(db),
        ]);

        // Pre-fetch all tasks in parallel to avoid sequential reads in the loop
        const eligibleDocs = snap.docs.filter((doc) => {
            const data = doc.data() as Record<string, unknown>;
            if (data.status !== "delivered") return false;
            if (filterCustomerId && data.billingCustomerId !== filterCustomerId) return false;
            if (!forceRecompute && typeof data.billingEstimateThb === "number") return false;
            return true;
        });

        const taskIds = [...new Set(
            eligibleDocs
                .map((d) => (d.data() as Record<string, unknown>).taskId)
                .filter((id): id is string => typeof id === "string" && id.trim().length > 0)
        )];
        const taskSnapshots = await Promise.all(taskIds.map((id) => db.collection(COL_TASKS).doc(id).get()));
        const taskCache = new Map<string, Record<string, unknown>>();
        taskSnapshots.forEach((snap) => { if (snap.exists) taskCache.set(snap.id, snap.data() as Record<string, unknown>); });

        let eligible = 0;
        let written = 0;
        let skipped = 0;
        let failed = 0;
        let attempted = 0;
        const failures: BackfillBillingFailure[] = [];
        const rateCache = new Map<string, { rateEntries: BillingRateEntry[]; fuelAdjustments: FuelRateAdjustment[] }>();

        for (const doc of eligibleDocs) {
            const data = doc.data() as Record<string, unknown>;
            eligible++;

            if (written >= maxWrite) continue;

            attempted++;
            const result = await tryWriteBillingSnapshotFromTripData(db, doc.id, data, doc.ref, hubMaps, forceRecompute, rateCache, taskCache);
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

interface NormalizeVehicleClassRequest {
    /** Max customer_rate_entries to scan. Default 5000, max 10000. */
    maxScan?: number;
    /** Max updates per invocation. Default 500, max 1000. */
    maxUpdate?: number;
}

interface NormalizeVehicleClassResponse {
    scanned: number;
    /** Entries with non-standard vehicle class requiring normalization */
    needsUpdate: number;
    /** Successfully updated entries */
    updated: number;
    /** List of updates performed (format, old→new) */
    samples: Array<{ docId: string; old: string; new: string }>;
    /** true if needsUpdate > updated (more work may remain) */
    capped: boolean;
}

/**
 * Admin-only: normalize vehicle class values in all customer_rate_entries.
 * Converts full names (e.g., "4 WHEELS JUMBO") to short codes (e.g., "4WJ").
 */
export const normalizeRateEntryVehicleClasses = onCall<
    NormalizeVehicleClassRequest,
    Promise<NormalizeVehicleClassResponse>
>(
    {
        region: "asia-southeast1",
        enforceAppCheck: false,
    },
    async (request): Promise<NormalizeVehicleClassResponse> => {
        if (request.auth?.token?.admin !== true) {
            throw new HttpsError("permission-denied", "Admin only");
        }

        const maxScan = Math.min(Math.max(1, request.data?.maxScan ?? 5000), 10000);
        const maxUpdate = Math.min(Math.max(1, request.data?.maxUpdate ?? 500), 1000);

        const db = admin.firestore();
        const snap = await db.collection(COL_RATE_ENTRIES).limit(maxScan).get();

        let needsUpdate = 0;
        let updated = 0;
        const samples: Array<{ docId: string; old: string; new: string }> = [];

        for (const doc of snap.docs) {
            const data = doc.data() as Record<string, unknown>;
            const old = String(data.vehicleClass ?? "4WJ").trim().toUpperCase();
            const normalized = normalizeVehicleClass(old);

            if (old !== normalized) {
                needsUpdate++;
                if (updated < maxUpdate) {
                    await doc.ref.update({ vehicleClass: normalized });
                    updated++;
                    if (samples.length < 10) {
                        samples.push({ docId: doc.id, old, new: normalized });
                    }
                }
            }
        }

        return {
            scanned: snap.size,
            needsUpdate,
            updated,
            samples,
            capped: needsUpdate > updated,
        };
    }
);

/**
 * Scheduled: auto-compute billing for recently delivered trips that have no billing yet.
 * Runs every 15 minutes. Scans trips delivered in the last 30 minutes (double the interval
 * for overlap safety). Idempotent — skips trips that already have billingEstimateThb set.
 *
 * Note: a Firestore document trigger cannot be used here because the Firestore database is
 * in asia-southeast3 (Jakarta), which is not a supported Cloud Functions region.
 */
export const autoComputeBillingOnDelivery = onSchedule(
    {
        schedule: "every 15 minutes",
        region: "asia-southeast1",
        timeoutSeconds: 540,
    },
    async () => {
        const db = admin.firestore();
        const windowMs = 30 * 60 * 1000; // 30-minute look-back window
        const since = admin.firestore.Timestamp.fromMillis(Date.now() - windowMs);

        const [snap, hubMaps] = await Promise.all([
            db.collection(COL_TRIP_RECORDS)
                .where("status", "==", "delivered")
                .where("deliveredTimestamp", ">=", since)
                .limit(100)
                .get(),
            buildHubMaps(db),
        ]);

        let written = 0;
        let failed = 0;

        for (const doc of snap.docs) {
            const data = doc.data() as Record<string, unknown>;
            if (typeof data.billingEstimateThb === "number") continue; // already billed

            const result = await tryWriteBillingSnapshotFromTripData(db, doc.id, data, doc.ref, hubMaps);
            if (result.ok && !result.skipped) {
                written++;
                logger.info("[autoComputeBilling] billing written", {
                    tripId: doc.id,
                    billingEstimateThb: result.billingEstimateThb,
                });
            } else if (!result.ok) {
                failed++;
                logger.warn("[autoComputeBilling] billing failed", { tripId: doc.id, error: result.error });
            }
        }

        logger.info("[autoComputeBilling] run complete", { scanned: snap.size, written, failed });
    }
);
