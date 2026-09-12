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
exports.autoComputeBillingOnDelivery = exports.normalizeRateEntryVehicleClasses = exports.backfillTripBillingSnapshots = exports.setTripJobCategory = exports.computeTripBillingSnapshot = void 0;
const admin = __importStar(require("firebase-admin"));
const firebase_functions_1 = require("firebase-functions");
const https_1 = require("firebase-functions/v2/https");
const scheduler_1 = require("firebase-functions/v2/scheduler");
const billingCompute_1 = require("./core/billingCompute");
const billingPeriodLock_1 = require("./core/billingPeriodLock");
const COL_TASKS = "tasks";
const COL_RATE_ENTRIES = "customer_rate_entries";
const COL_FUEL_ADJ = "customer_fuel_rate_adjustments";
const COL_TRIP_RECORDS = "trip_records";
const COL_HUBS = "hubs";
const COL_SERVICE_FEES = "customer_service_fees";
const COL_CUSTOMERS = "customers";
const COL_SUBCONTRACTORS = "subcontractors";
function basisFromSnap(snap) {
    return snap.exists && snap.get("billingDateBasis") === "plan" ? "plan" : "delivered";
}
/**
 * Resolve a billing entity's date basis (ADR 0027/0028). The billing entity id may be a customer OR a
 * subcontractor/partner (a hub can link to either — `customerLinkKind`). Read `customers/{id}` first;
 * if that doc doesn't exist the id is a partner, so fall back to `subcontractors/{id}`. Missing = "delivered".
 * Pass `custSnap` when the caller already fetched the customer doc to avoid a second read.
 */
async function resolveBillingDateBasis(db, entityId, custSnap) {
    const cs = custSnap ?? (await db.collection(COL_CUSTOMERS).doc(entityId).get());
    if (cs.exists)
        return basisFromSnap(cs);
    const sub = await db.collection(COL_SUBCONTRACTORS).doc(entityId).get();
    return basisFromSnap(sub);
}
/**
 * The instant a trip bills against (ADR 0027). For a plan-basis customer it is the task's plan date
 * (`tasks.date`); otherwise 0 = "no override", so callers fall back to the delivery instant
 * (ADR 0008 §3). Returned in epoch ms.
 */
function resolvePlanBillingDateMs(basis, task) {
    if (basis !== "plan")
        return 0;
    const planMs = (0, billingCompute_1.timestampLikeToMillis)(task.date);
    return planMs > 0 ? planMs : 0;
}
/**
 * Stamp `billingDate` + `billingCustomerId` on a delivered trip whose price could not be computed
 * (ADR 0027 / 0008 §6). Without this, a plan-basis trip that fails rate lookup would carry no
 * billingDate and vanish from its planned-month Billing Document entirely — the exact "หลุดวางบิล"
 * leak this feature exists to close. Best-effort: a failed stamp must not mask the original error.
 */
async function stampBillingDateOnUnpriced(tripRef, billingDateTs, customerId, tripId) {
    if (!billingDateTs)
        return;
    try {
        await tripRef.update({
            billingDate: billingDateTs,
            billingCustomerId: customerId,
            updatedAt: admin.firestore.FieldValue.serverTimestamp(),
        });
    }
    catch (e) {
        firebase_functions_1.logger.warn("[billingSnapshot] failed to stamp billingDate on unpriced trip", {
            tripId,
            error: String(e),
        });
    }
}
function normalizeStoredCode(v) {
    return (v ?? "").trim().toUpperCase();
}
async function buildHubMaps(db) {
    const snap = await db.collection(COL_HUBS).get();
    const nameToCode = new Map();
    const codeToName = new Map();
    snap.docs.forEach((d) => {
        const data = d.data();
        const code = String(data.source_id ?? data.hubId ?? "").trim();
        if (!code)
            return;
        for (const nameField of [data.source_name_th, data.source_name_en, data.hubName]) {
            const name = typeof nameField === "string" ? nameField.trim() : "";
            // Skip names equal to the code so a code never maps to itself in nameToCode.
            if (name && name !== code && !nameToCode.has(name))
                nameToCode.set(name, code);
        }
        const displayName = typeof data.source_name_th === "string" ? data.source_name_th.trim() : "";
        if (displayName && !codeToName.has(code))
            codeToName.set(code, displayName);
    });
    return { nameToCode, codeToName };
}
/** Translate a display name → code. Codes (and unknown values) pass through unchanged. */
function resolveNameToCode(raw, nameToCode) {
    if (!raw || !nameToCode)
        return raw;
    return nameToCode.get(raw.trim()) ?? raw;
}
function mapRateDoc(customerId, doc) {
    const d = doc.data();
    return {
        id: doc.id,
        customerId,
        importId: String(d.importId ?? ""),
        hubId: normalizeStoredCode(String(d.hubId ?? "")),
        destinationCode: (0, billingCompute_1.normalizeDestinationCode)(String(d.destinationCode ?? "")),
        vehicleClass: normalizeStoredCode(String(d.vehicleClass ?? "4WJ")),
        rateThb: Number(d.rateThb ?? 0),
        effectiveFromMs: (0, billingCompute_1.timestampLikeToMillis)(d.effectiveFrom),
        jobCategory: d.jobCategory === "SUPPLEMENTARY" ? "SUPPLEMENTARY" : "PRIMARY",
        voided: d.voided === true,
    };
}
function mapFuelDoc(customerId, doc) {
    const d = doc.data();
    return {
        id: doc.id,
        customerId,
        effectiveFromMs: (0, billingCompute_1.timestampLikeToMillis)(d.effectiveFrom),
        rateMultiplier: Number(d.rateMultiplier ?? 1),
        addThbPerTrip: Number(d.addThbPerTrip ?? 0),
        // The band printed on an invoice is derived from this price and denormalized onto the
        // trip, so it can never be contradicted by a later edit of the announcement (ADR 0009 §4).
        referenceFuelPriceThb: d.referenceFuelPriceThbPerLitre != null ? Number(d.referenceFuelPriceThbPerLitre) : undefined,
        voided: d.voided === true,
    };
}
/** Shared core: persist billing snapshot from already-read trip fields (idempotent). */
async function tryWriteBillingSnapshotFromTripData(db, tripId, data, tripRef, hubMaps, forceRecompute, rateCache, taskCache) {
    if (data.status !== "delivered") {
        return { ok: true, skipped: true, error: "Trip is not delivered yet" };
    }
    if (!forceRecompute && typeof data.billingEstimateThb === "number") {
        return { ok: true, skipped: true, billingEstimateThb: data.billingEstimateThb };
    }
    // Frozen pricing (ADR-0005): a supplementary trip — or any manually-overridden snapshot —
    // keeps its agreed price. Even a forced recompute must NOT overwrite it once it has one.
    const tripFrozen = data.billingManualOverride === true || data.jobCategory === "SUPPLEMENTARY";
    if (forceRecompute && tripFrozen && typeof data.billingEstimateThb === "number") {
        return { ok: true, skipped: true, billingEstimateThb: data.billingEstimateThb };
    }
    const taskId = typeof data.taskId === "string" ? data.taskId.trim() : "";
    if (!taskId) {
        firebase_functions_1.logger.warn("[billingSnapshot] delivered trip missing taskId", { tripId });
        return { ok: false, error: "Trip missing taskId" };
    }
    let t = taskCache?.get(taskId);
    if (!t) {
        let taskSnap;
        try {
            taskSnap = await db.collection(COL_TASKS).doc(taskId).get();
        }
        catch (e) {
            firebase_functions_1.logger.error("[billingSnapshot] task load failed", e);
            return { ok: false, error: "Failed to load task" };
        }
        if (!taskSnap.exists) {
            firebase_functions_1.logger.warn("[billingSnapshot] task not found", { taskId, tripId });
            return { ok: false, error: "Task not found" };
        }
        t = taskSnap.data();
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
    const taskInput = {
        sourceHub: resolvedSourceHub,
        destination: resolvedDestination,
        truckType: typeof t.truckType === "string" ? t.truckType : undefined,
        // Explicit billing customer chosen at assign time wins over the hub-derived link (ADR 0027).
        billingCustomerId: typeof t.billingCustomerId === "string" ? t.billingCustomerId : undefined,
        sourceHubLinkedCustomerId: typeof t.sourceHubLinkedCustomerId === "string" ? t.sourceHubLinkedCustomerId : undefined,
        destinationLinkedCustomerId: typeof t.destinationLinkedCustomerId === "string" ? t.destinationLinkedCustomerId : undefined,
    };
    const customerId = taskInput.billingCustomerId?.trim() ||
        taskInput.sourceHubLinkedCustomerId?.trim() ||
        taskInput.destinationLinkedCustomerId?.trim() ||
        "";
    if (!customerId) {
        firebase_functions_1.logger.warn("[billingSnapshot] task has no linked customer", { taskId, tripId });
        return { ok: false, error: "Task has no linked customer" };
    }
    // หลัก/เสริม (ADR-0006, supersedes ADR-0005 decision #3): admins may now pick it explicitly
    // on the task at assign time. When present, it's authoritative — the PRIMARY probe is
    // skipped and billing goes straight to the chosen category's rate card. A legacy task with
    // no explicit value (created before this change) keeps the original derivation: try PRIMARY,
    // fall back to SUPPLEMENTARY. Either way the resolved category is frozen when SUPPLEMENTARY.
    const explicitJobCategory = t.jobCategory === "SUPPLEMENTARY" ? "SUPPLEMENTARY" : t.jobCategory === "PRIMARY" ? "PRIMARY" : undefined;
    const overrideFor = (cat) => cat === "SUPPLEMENTARY" ? { billingManualOverride: true } : {};
    let rateEntries;
    let fuelAdjustments;
    let billingDateBasis;
    if (rateCache?.has(customerId)) {
        ({ rateEntries, fuelAdjustments, billingDateBasis } = rateCache.get(customerId));
    }
    else {
        const [rateSnap, fuelSnap, custSnap] = await Promise.all([
            db.collection(COL_RATE_ENTRIES).where("customerId", "==", customerId).get(),
            db.collection(COL_FUEL_ADJ).where("customerId", "==", customerId).get(),
            db.collection(COL_CUSTOMERS).doc(customerId).get(),
        ]);
        rateEntries = rateSnap.docs.map((d) => mapRateDoc(customerId, d));
        fuelAdjustments = fuelSnap.docs.map((d) => mapFuelDoc(customerId, d));
        billingDateBasis = await resolveBillingDateBasis(db, customerId, custSnap);
        rateCache?.set(customerId, { rateEntries, fuelAdjustments, billingDateBasis });
    }
    // Plan-date billing (ADR 0027): a plan-basis customer bills against the task's plan date
    // (tasks.date, Bangkok calendar day) rather than the delivery instant (ADR 0008 §3). Stored on
    // the trip as `billingDate` so the Billing Document / Income group and the recompute scan by it.
    const planBillingMs = resolvePlanBillingDateMs(billingDateBasis, t);
    const billingDateMs = planBillingMs > 0 ? planBillingMs : (0, billingCompute_1.timestampLikeToMillis)(data.deliveredTimestamp);
    const billingDateTs = billingDateMs > 0 ? admin.firestore.Timestamp.fromMillis(billingDateMs) : null;
    const tripParts = {
        deliveredTimestamp: data.deliveredTimestamp,
        createdAt: data.createdAt,
        // Only override for plan basis; delivered basis stays exactly on deliveredTimestamp.
        billingDateMs: planBillingMs > 0 ? planBillingMs : undefined,
    };
    // Check if this is a multi-delivery trip
    const isMultiDelivery = data.isMultiDelivery === true;
    const deliveryStopsProgress = Array.isArray(data.deliveryStopsProgress) ? data.deliveryStopsProgress : [];
    if (isMultiDelivery && deliveryStopsProgress.length >= 2) {
        // Multi-delivery billing: stop[0] = base rate (planned destination), stop[1+] = extra stops charged
        const stops = deliveryStopsProgress
            .filter((stop) => stop.destination && stop.status === "delivered")
            .map((stop) => ({
            index: typeof stop.index === "number" ? stop.index : 1,
            destination: String(stop.destination ?? ""),
        }));
        if (stops.length < 2) {
            firebase_functions_1.logger.warn("[billingSnapshot] multi-delivery trip has < 2 delivered stops", {
                tripId,
                taskId,
                delivered: stops.length,
            });
            return { ok: false, error: "Multi-delivery trip has < 2 delivered stops" };
        }
        // Flat extra-stop service fee (customer_service_fees, feeType "extra_stop").
        // When set, every extra stop (stop 2+) is charged this fixed amount instead of a
        // per-route rate-card lookup.
        let extraStopFeeThb;
        try {
            const feeSnap = await db
                .collection(COL_SERVICE_FEES)
                .where("customerId", "==", customerId)
                .get();
            const extraStopDoc = feeSnap.docs.find((d) => d.data().feeType === "extra_stop");
            if (extraStopDoc) {
                const amt = Number(extraStopDoc.data().amountThb ?? 0);
                if (Number.isFinite(amt) && amt >= 0)
                    extraStopFeeThb = amt;
            }
        }
        catch (e) {
            firebase_functions_1.logger.warn("[billingSnapshot] failed to load extra_stop service fee", { customerId, error: String(e) });
        }
        // Category: honor an explicit task-level choice (ADR-0006) — skip the PRIMARY probe
        // entirely when the task is explicitly marked เสริม. Otherwise (explicit หลัก, or a
        // legacy task with no explicit value) derive as before: try PRIMARY, fall back to
        // SUPPLEMENTARY (ADR-0005).
        const mVehicleClass = (0, billingCompute_1.normalizeVehicleClass)(taskInput.truckType || "4WJ");
        let multiComputed;
        let resolvedCategory = "PRIMARY";
        if (explicitJobCategory === "SUPPLEMENTARY") {
            multiComputed = (0, billingCompute_1.computeMultiDeliveryBilling)(tripParts, taskInput, stops, mVehicleClass, rateEntries, fuelAdjustments, extraStopFeeThb, "SUPPLEMENTARY");
            resolvedCategory = "SUPPLEMENTARY";
        }
        else if (explicitJobCategory === "PRIMARY") {
            // Explicit หลัก (ADR-0006): PRIMARY rate rule only, no fallback to เสริม.
            multiComputed = (0, billingCompute_1.computeMultiDeliveryBilling)(tripParts, taskInput, stops, mVehicleClass, rateEntries, fuelAdjustments, extraStopFeeThb, "PRIMARY");
            resolvedCategory = "PRIMARY";
        }
        else {
            // Legacy task with no explicit jobCategory: try PRIMARY, fall back to SUPPLEMENTARY.
            multiComputed = (0, billingCompute_1.computeMultiDeliveryBilling)(tripParts, taskInput, stops, mVehicleClass, rateEntries, fuelAdjustments, extraStopFeeThb, "PRIMARY");
            if (!multiComputed) {
                multiComputed = (0, billingCompute_1.computeMultiDeliveryBilling)(tripParts, taskInput, stops, mVehicleClass, rateEntries, fuelAdjustments, extraStopFeeThb, "SUPPLEMENTARY");
                if (multiComputed)
                    resolvedCategory = "SUPPLEMENTARY";
            }
        }
        if (!multiComputed) {
            firebase_functions_1.logger.warn("[billingSnapshot] could not compute multi-delivery billing", {
                tripId,
                taskId,
                customerId,
                stops: stops.length,
            });
            await stampBillingDateOnUnpriced(tripRef, billingDateTs, customerId, tripId);
            return { ok: false, error: "Could not compute multi-delivery billing" };
        }
        await tripRef.update({
            billingEstimateThb: multiComputed.totalBillingThb,
            billingBaseRateThb: multiComputed.baseRateThb,
            billingStopChargeThb: multiComputed.stopChargeThb,
            billingIsMultiDelivery: true,
            billingLookupHubId: (0, billingCompute_1.extractHubId)(taskInput.sourceHub),
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
            // Parity with the single-delivery branch (ADR 0009 §4): this path used to write none
            // of the following, so multidrop rows had no round to print on an invoice.
            billingAddThbPerTrip: multiComputed.addThbPerTrip,
            billingRateImportId: multiComputed.rateImportId,
            billingEffectiveFromDateStr: multiComputed.effectiveFromDateStr ?? null,
            billingRoundEffectiveFromDateStr: multiComputed.roundEffectiveFromDateStr,
            billingFuelBandLowerThb: multiComputed.fuelBandLowerThb ?? null,
            billingFuelBandUpperThb: multiComputed.fuelBandUpperThb ?? null,
            billingReferenceFuelPriceThb: multiComputed.referenceFuelPriceThb ?? null,
            // Plan-date axis (ADR 0027): queryable field the Billing Document / Income group by.
            billingDate: billingDateTs,
            jobCategory: resolvedCategory,
            ...overrideFor(resolvedCategory),
            updatedAt: admin.firestore.FieldValue.serverTimestamp(),
        });
        return { ok: true, billingEstimateThb: multiComputed.totalBillingThb };
    }
    else {
        // Single-delivery billing. Category: honor an explicit task-level choice (ADR-0006) —
        // skip the PRIMARY probe entirely when the task is explicitly marked เสริม. Otherwise
        // (explicit หลัก, or a legacy task with no explicit value) derive as before: try PRIMARY,
        // fall back to SUPPLEMENTARY (ADR-0005).
        let resolvedCategory = "PRIMARY";
        const computeForCategory = (cat) => {
            let c = (0, billingCompute_1.computeTripBillingFromParts)(tripParts, taskInput, rateEntries, fuelAdjustments, cat);
            // Fallback for customers whose rate cards key destination by DISPLAY NAME instead of
            // code: if no rate matched the code form, retry once with the Thai display name.
            if (!c && codeToName && rawDestination) {
                const altDest = codeToName.get((resolvedDestination ?? rawDestination).trim())
                    ?? codeToName.get(rawDestination.trim());
                if (altDest && altDest !== resolvedDestination) {
                    c = (0, billingCompute_1.computeTripBillingFromParts)(tripParts, { ...taskInput, destination: altDest }, rateEntries, fuelAdjustments, cat);
                }
            }
            return c;
        };
        let computed;
        if (explicitJobCategory === "SUPPLEMENTARY") {
            computed = computeForCategory("SUPPLEMENTARY");
            resolvedCategory = "SUPPLEMENTARY";
        }
        else if (explicitJobCategory === "PRIMARY") {
            // Explicit หลัก (ADR-0006): PRIMARY rate rule only. No fallback to เสริม —
            // a missing PRIMARY rate must fail loud, not silently bill (and freeze) at a
            // supplementary price.
            computed = computeForCategory("PRIMARY");
            resolvedCategory = "PRIMARY";
        }
        else {
            // Legacy task with no explicit jobCategory: derive as before (ADR-0005) —
            // try PRIMARY, fall back to SUPPLEMENTARY.
            computed = computeForCategory("PRIMARY");
            if (!computed) {
                computed = computeForCategory("SUPPLEMENTARY");
                if (computed)
                    resolvedCategory = "SUPPLEMENTARY";
            }
        }
        if (!computed) {
            const hubId = (0, billingCompute_1.extractHubId)(taskInput.sourceHub);
            const destination = (0, billingCompute_1.normalizeDestinationCode)(taskInput.destination);
            const vehicleClass = normalizeStoredCode(taskInput.truckType || "4WJ");
            const billDateMs = (0, billingCompute_1.getTripBillingDateMs)(tripParts);
            firebase_functions_1.logger.warn("[billingSnapshot] could not compute billing (no matching rate row)", {
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
            await stampBillingDateOnUnpriced(tripRef, billingDateTs, customerId, tripId);
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
            billingRoundEffectiveFromDateStr: computed.roundEffectiveFromDateStr,
            billingFuelBandLowerThb: computed.fuelBandLowerThb ?? null,
            billingFuelBandUpperThb: computed.fuelBandUpperThb ?? null,
            billingReferenceFuelPriceThb: computed.referenceFuelPriceThb ?? null,
            billingCustomerId: computed.customerId,
            // Plan-date axis (ADR 0027): queryable field the Billing Document / Income group by.
            billingDate: billingDateTs,
            jobCategory: resolvedCategory,
            ...overrideFor(resolvedCategory),
            updatedAt: admin.firestore.FieldValue.serverTimestamp(),
        });
        return { ok: true, billingEstimateThb: computed.finalRateThb };
    }
}
/**
 * HTTPS Callable: compute and persist billing snapshot for a delivered trip.
 *
 * Call from mobile after marking trip as `delivered`, or from web admin backfill.
 * Idempotent: if `billingEstimateThb` already exists, returns `skipped: true`.
 */
exports.computeTripBillingSnapshot = (0, https_1.onCall)({
    region: "asia-southeast1",
    enforceAppCheck: false, // Web admin calls without App Check token; auth protected by request.auth check below
}, async (request) => {
    if (!request.auth) {
        throw new https_1.HttpsError("unauthenticated", "Must be authenticated");
    }
    const tripId = request.data?.tripId?.trim();
    if (!tripId) {
        throw new https_1.HttpsError("invalid-argument", "tripId is required");
    }
    const db = admin.firestore();
    const [tripSnap, hubMaps] = await Promise.all([
        db.collection(COL_TRIP_RECORDS).doc(tripId).get(),
        buildHubMaps(db),
    ]);
    if (!tripSnap.exists) {
        throw new https_1.HttpsError("not-found", "Trip not found");
    }
    const data = tripSnap.data();
    return tryWriteBillingSnapshotFromTripData(db, tripId, data, tripSnap.ref, hubMaps, request.data?.forceRecompute === true);
});
/**
 * HTTPS Callable (admin only): change a delivered trip's หลัก/เสริม (jobCategory) and re-derive its
 * price from the target category's rate card — the sanctioned manual escape hatch of ADR-0005/0006
 * (see shared-docs/adr/0002-edit-job-category-on-delivered-trip.md).
 *
 * Atomic: computes the new price FIRST for the explicit target category (no PRIMARY↔SUPPLEMENTARY
 * fallback). If no rate matches, throws and writes NOTHING — the trip keeps its old category + price.
 * On success writes `tasks.jobCategory` (source of truth, ADR-0006) AND the trip billing snapshot in
 * one batch, setting `billingManualOverride` true for SUPPLEMENTARY (re-freeze) and false for PRIMARY
 * (explicitly un-freeze so a future recompute is allowed). This is the ONLY path that moves a frozen
 * price; the guards in computeTripBillingSnapshot / backfill are left untouched.
 */
exports.setTripJobCategory = (0, https_1.onCall)({
    region: "asia-southeast1",
    enforceAppCheck: false, // Web admin calls without App Check token; auth + admin checked below
}, async (request) => {
    if (!request.auth) {
        throw new https_1.HttpsError("unauthenticated", "Must be authenticated");
    }
    const isAdmin = request.auth.token.admin === true || request.auth.token["role"] === "admin";
    if (!isAdmin) {
        throw new https_1.HttpsError("permission-denied", "Admin only");
    }
    const tripId = request.data?.tripId?.trim();
    if (!tripId) {
        throw new https_1.HttpsError("invalid-argument", "tripId is required");
    }
    const targetCategory = request.data?.jobCategory === "SUPPLEMENTARY" ? "SUPPLEMENTARY" : "PRIMARY";
    const db = admin.firestore();
    const tripRef = db.collection(COL_TRIP_RECORDS).doc(tripId);
    const [tripSnap, hubMaps] = await Promise.all([tripRef.get(), buildHubMaps(db)]);
    if (!tripSnap.exists) {
        throw new https_1.HttpsError("not-found", "Trip not found");
    }
    const data = tripSnap.data();
    if (data.status !== "delivered") {
        throw new https_1.HttpsError("failed-precondition", "Trip is not delivered yet");
    }
    const taskId = typeof data.taskId === "string" ? data.taskId.trim() : "";
    if (!taskId) {
        throw new https_1.HttpsError("failed-precondition", "Trip missing taskId");
    }
    const taskRef = db.collection(COL_TASKS).doc(taskId);
    const taskSnap = await taskRef.get();
    if (!taskSnap.exists) {
        throw new https_1.HttpsError("not-found", "Task not found");
    }
    const t = taskSnap.data();
    // No-op when the task and trip already carry the target category (nothing to recompute).
    const currentTaskCategory = t.jobCategory === "SUPPLEMENTARY" ? "SUPPLEMENTARY" : t.jobCategory === "PRIMARY" ? "PRIMARY" : undefined;
    if (currentTaskCategory === targetCategory && data.jobCategory === targetCategory) {
        return {
            ok: true,
            skipped: true,
            billingEstimateThb: typeof data.billingEstimateThb === "number" ? data.billingEstimateThb : undefined,
        };
    }
    // Resolve billing inputs — identical resolution to tryWriteBillingSnapshotFromTripData.
    const rawDestination = typeof t.destination === "string" ? t.destination : undefined;
    const rawSourceHub = typeof t.sourceHub === "string" ? t.sourceHub : undefined;
    const nameToCode = hubMaps.nameToCode;
    const codeToName = hubMaps.codeToName;
    const resolvedSourceHub = resolveNameToCode(rawSourceHub, nameToCode);
    const resolvedDestination = resolveNameToCode(rawDestination, nameToCode);
    const taskInput = {
        sourceHub: resolvedSourceHub,
        destination: resolvedDestination,
        truckType: typeof t.truckType === "string" ? t.truckType : undefined,
        billingCustomerId: typeof t.billingCustomerId === "string" ? t.billingCustomerId : undefined,
        sourceHubLinkedCustomerId: typeof t.sourceHubLinkedCustomerId === "string" ? t.sourceHubLinkedCustomerId : undefined,
        destinationLinkedCustomerId: typeof t.destinationLinkedCustomerId === "string" ? t.destinationLinkedCustomerId : undefined,
    };
    const customerId = taskInput.billingCustomerId?.trim() ||
        taskInput.sourceHubLinkedCustomerId?.trim() ||
        taskInput.destinationLinkedCustomerId?.trim() ||
        "";
    if (!customerId) {
        throw new https_1.HttpsError("failed-precondition", "Task has no linked customer");
    }
    const [rateSnap, fuelSnap, custSnap] = await Promise.all([
        db.collection(COL_RATE_ENTRIES).where("customerId", "==", customerId).get(),
        db.collection(COL_FUEL_ADJ).where("customerId", "==", customerId).get(),
        db.collection(COL_CUSTOMERS).doc(customerId).get(),
    ]);
    const rateEntries = rateSnap.docs.map((d) => mapRateDoc(customerId, d));
    const fuelAdjustments = fuelSnap.docs.map((d) => mapFuelDoc(customerId, d));
    // Re-derive under the same plan-date axis (ADR 0027) the delivery-time snapshot used, so a
    // manual หลัก/เสริม change reprices against the plan month, not the delivery instant.
    const planBillingMs = resolvePlanBillingDateMs(await resolveBillingDateBasis(db, customerId, custSnap), t);
    const billingDateMs = planBillingMs > 0 ? planBillingMs : (0, billingCompute_1.timestampLikeToMillis)(data.deliveredTimestamp);
    const billingDateTs = billingDateMs > 0 ? admin.firestore.Timestamp.fromMillis(billingDateMs) : null;
    const tripParts = {
        deliveredTimestamp: data.deliveredTimestamp,
        createdAt: data.createdAt,
        billingDateMs: planBillingMs > 0 ? planBillingMs : undefined,
    };
    const noRateError = () => {
        const hubId = (0, billingCompute_1.extractHubId)(taskInput.sourceHub);
        const destination = (0, billingCompute_1.normalizeDestinationCode)(taskInput.destination);
        const vehicleClass = normalizeStoredCode(taskInput.truckType || "4WJ");
        firebase_functions_1.logger.warn("[setTripJobCategory] no matching rate row for target category", {
            tripId,
            taskId,
            targetCategory,
            customerId,
            hubId,
            destination,
            vehicleClass,
            rateRowsForCustomer: rateEntries.length,
        });
        return new https_1.HttpsError("failed-precondition", `No ${targetCategory} rate: ${hubId} → ${destination} (${vehicleClass})`);
    };
    // Compute FIRST; only build the write payload if a rate matches the target category.
    let tripUpdate;
    let newBillingEstimateThb;
    const isMultiDelivery = data.isMultiDelivery === true;
    const deliveryStopsProgress = Array.isArray(data.deliveryStopsProgress) ? data.deliveryStopsProgress : [];
    if (isMultiDelivery && deliveryStopsProgress.length >= 2) {
        const stops = deliveryStopsProgress
            .filter((stop) => stop.destination && stop.status === "delivered")
            .map((stop) => ({
            index: typeof stop.index === "number" ? stop.index : 1,
            destination: String(stop.destination ?? ""),
        }));
        if (stops.length < 2) {
            throw new https_1.HttpsError("failed-precondition", "Multi-delivery trip has < 2 delivered stops");
        }
        // Flat extra-stop service fee (customer_service_fees, feeType "extra_stop") — same lookup
        // as the delivery path so re-derivation matches the original computation exactly.
        let extraStopFeeThb;
        try {
            const feeSnap = await db.collection(COL_SERVICE_FEES).where("customerId", "==", customerId).get();
            const extraStopDoc = feeSnap.docs.find((d) => d.data().feeType === "extra_stop");
            if (extraStopDoc) {
                const amt = Number(extraStopDoc.data().amountThb ?? 0);
                if (Number.isFinite(amt) && amt >= 0)
                    extraStopFeeThb = amt;
            }
        }
        catch (e) {
            firebase_functions_1.logger.warn("[setTripJobCategory] failed to load extra_stop service fee", {
                customerId,
                error: String(e),
            });
        }
        const mVehicleClass = (0, billingCompute_1.normalizeVehicleClass)(taskInput.truckType || "4WJ");
        const multiComputed = (0, billingCompute_1.computeMultiDeliveryBilling)(tripParts, taskInput, stops, mVehicleClass, rateEntries, fuelAdjustments, extraStopFeeThb, targetCategory);
        if (!multiComputed)
            throw noRateError();
        tripUpdate = {
            billingEstimateThb: multiComputed.totalBillingThb,
            billingBaseRateThb: multiComputed.baseRateThb,
            billingStopChargeThb: multiComputed.stopChargeThb,
            billingIsMultiDelivery: true,
            billingLookupHubId: (0, billingCompute_1.extractHubId)(taskInput.sourceHub),
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
        };
        newBillingEstimateThb = multiComputed.totalBillingThb;
    }
    else {
        // Single-delivery. Mirror the delivery path's codeToName retry, but for the explicit
        // target category only (no PRIMARY↔SUPPLEMENTARY fallback — the admin chose the category).
        const computeForCategory = (cat) => {
            let c = (0, billingCompute_1.computeTripBillingFromParts)(tripParts, taskInput, rateEntries, fuelAdjustments, cat);
            if (!c && codeToName && rawDestination) {
                const altDest = codeToName.get((resolvedDestination ?? rawDestination).trim()) ??
                    codeToName.get(rawDestination.trim());
                if (altDest && altDest !== resolvedDestination) {
                    c = (0, billingCompute_1.computeTripBillingFromParts)(tripParts, { ...taskInput, destination: altDest }, rateEntries, fuelAdjustments, cat);
                }
            }
            return c;
        };
        const computed = computeForCategory(targetCategory);
        if (!computed)
            throw noRateError();
        tripUpdate = {
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
        };
        newBillingEstimateThb = computed.finalRateThb;
    }
    // Atomic write: task category (source of truth) + trip snapshot together. billingManualOverride
    // is set explicitly — true re-freezes SUPPLEMENTARY, false un-freezes PRIMARY (so a later
    // recompute is allowed). Compute already succeeded, so no partial-write on the common failure.
    const batch = db.batch();
    batch.update(taskRef, {
        jobCategory: targetCategory,
        updatedAt: admin.firestore.FieldValue.serverTimestamp(),
    });
    batch.update(tripRef, {
        ...tripUpdate,
        // Keep the plan-date axis (ADR 0027) in sync when the price is re-derived here.
        billingDate: billingDateTs,
        jobCategory: targetCategory,
        billingManualOverride: targetCategory === "SUPPLEMENTARY",
        updatedAt: admin.firestore.FieldValue.serverTimestamp(),
    });
    await batch.commit();
    return { ok: true, billingEstimateThb: newBillingEstimateThb };
});
function bangkokBoundsToTimestamps(fromDateStr, toDateStr) {
    const start = new Date(`${fromDateStr.trim()}T00:00:00+07:00`);
    const end = new Date(`${toDateStr.trim()}T23:59:59.999+07:00`);
    if (Number.isNaN(start.getTime()) || Number.isNaN(end.getTime()) || start > end) {
        throw new https_1.HttpsError("invalid-argument", "Invalid fromDateStr / toDateStr");
    }
    return {
        start: admin.firestore.Timestamp.fromDate(start),
        end: admin.firestore.Timestamp.fromDate(end),
    };
}
/**
 * Admin-only: scan delivered trip_records by `deliveredTimestamp` (Bangkok calendar range) and write
 * billing snapshots (same rules as computeTripBillingSnapshot).
 *
 * The scan axis is `deliveredTimestamp`, not `createdAt` (ADR 0008 §3-4): the Billing Document groups
 * an invoice by delivery date, so scanning by creation date recomputed a different set than the
 * invoice contains — a trip created 30 June and delivered 1 July is on the July invoice yet was never
 * touched by a "recompute July" run. That mismatch is why prices appeared not to update.
 *
 * Exception (ADR 0027): when a single **plan-date** customer is targeted, the scan axis is
 * `billingDate` instead, because that customer's invoice is grouped by the plan date. See scanByPlanDate.
 */
exports.backfillTripBillingSnapshots = (0, https_1.onCall)({
    region: "asia-southeast1",
    enforceAppCheck: false, // Admin-only function protected by auth check; App Check enforcement disabled to avoid web client token issues
    timeoutSeconds: 540,
}, async (request) => {
    if (request.auth?.token?.admin !== true) {
        throw new https_1.HttpsError("permission-denied", "Admin only");
    }
    const fromDateStr = request.data?.fromDateStr?.trim() ?? "";
    const toDateStr = request.data?.toDateStr?.trim() ?? "";
    if (!fromDateStr || !toDateStr) {
        throw new https_1.HttpsError("invalid-argument", "fromDateStr and toDateStr are required (yyyy-MM-dd)");
    }
    const maxScan = Math.min(Math.max(1, request.data?.maxScan ?? 500), 2000);
    const maxWrite = Math.min(Math.max(1, request.data?.maxWrite ?? 200), 500);
    const forceRecompute = request.data?.forceRecompute === true;
    const filterCustomerId = request.data?.customerId?.trim() ?? "";
    const db = admin.firestore();
    const { start, end } = bangkokBoundsToTimestamps(fromDateStr, toDateStr);
    // A plan-date customer (ADR 0027) groups its invoice by `billingDate` (the plan date), not the
    // delivery instant — so a "recompute September" run for such a customer must SCAN by billingDate
    // too, or it would touch a different set of trips than the invoice contains (the same ADR 0008
    // §3 trap, one axis over). Only possible when a single customer is targeted; an "all" run keeps
    // the delivery-date scan since mixed bases cannot share one range.
    let scanByPlanDate = false;
    if (filterCustomerId) {
        try {
            scanByPlanDate = (await resolveBillingDateBasis(db, filterCustomerId)) === "plan";
        }
        catch (e) {
            firebase_functions_1.logger.warn("[billingSnapshot] backfill could not read customer basis", {
                filterCustomerId,
                error: String(e),
            });
        }
    }
    // Surface the real Firestore error rather than a bare INTERNAL (ADR 0008 §8) — both scans need
    // a composite index, and Firestore's message carries the URL that creates it.
    let snap;
    let hubMaps;
    let locks;
    try {
        // No explicit orderBy — the range already implies ascending order.
        const tripQuery = scanByPlanDate
            ? db.collection(COL_TRIP_RECORDS)
                .where("status", "==", "delivered")
                .where("billingCustomerId", "==", filterCustomerId)
                .where("billingDate", ">=", start)
                .where("billingDate", "<=", end)
                .limit(maxScan)
            : db.collection(COL_TRIP_RECORDS)
                .where("status", "==", "delivered")
                .where("deliveredTimestamp", ">=", start)
                .where("deliveredTimestamp", "<=", end)
                .limit(maxScan);
        [snap, hubMaps, locks] = await Promise.all([
            tripQuery.get(),
            buildHubMaps(db),
            (0, billingPeriodLock_1.loadBillingPeriodLocks)(db),
        ]);
    }
    catch (e) {
        const message = e instanceof Error ? e.message : String(e);
        firebase_functions_1.logger.error("[billingSnapshot] backfill query failed", { message });
        throw new https_1.HttpsError("failed-precondition", `Trip backfill query failed: ${message}`);
    }
    // Pre-fetch all tasks in parallel to avoid sequential reads in the loop
    const eligibleDocs = snap.docs.filter((doc) => {
        const data = doc.data();
        // status is already constrained by the query; keep the guard for defence in depth.
        if (data.status !== "delivered")
            return false;
        if (filterCustomerId && data.billingCustomerId !== filterCustomerId)
            return false;
        if (!forceRecompute && typeof data.billingEstimateThb === "number")
            return false;
        return true;
    });
    const taskIds = [...new Set(eligibleDocs
            .map((d) => d.data().taskId)
            .filter((id) => typeof id === "string" && id.trim().length > 0))];
    const taskSnapshots = await Promise.all(taskIds.map((id) => db.collection(COL_TASKS).doc(id).get()));
    const taskCache = new Map();
    taskSnapshots.forEach((snap) => { if (snap.exists)
        taskCache.set(snap.id, snap.data()); });
    let eligible = 0;
    let written = 0;
    let skipped = 0;
    let failed = 0;
    let blocked = 0;
    let attempted = 0;
    const blockedInvoices = new Set();
    const failures = [];
    const rateCache = new Map();
    for (const doc of eligibleDocs) {
        const data = doc.data();
        eligible++;
        if (written >= maxWrite)
            continue;
        // ADR 0008 §5 — a priced row in a sent/paid period keeps its number. Only bulk recompute
        // is gated here; the single-trip callable stays open because ADR 0002 made an explicit
        // admin edit the one sanctioned way to move a settled price.
        if (typeof data.billingEstimateThb === "number") {
            const lock = locks.lockFor(typeof data.billingCustomerId === "string" ? data.billingCustomerId : "", 
            // Lock on the same axis the invoice period is built from (ADR 0027).
            (0, billingCompute_1.timestampLikeToMillis)(scanByPlanDate ? data.billingDate : data.deliveredTimestamp));
            if (lock) {
                blocked++;
                blockedInvoices.add(lock.invoiceNumber);
                continue;
            }
        }
        attempted++;
        const result = await tryWriteBillingSnapshotFromTripData(db, doc.id, data, doc.ref, hubMaps, forceRecompute, rateCache, taskCache);
        if (result.ok === true && result.skipped !== true && result.billingEstimateThb != null) {
            written++;
        }
        else if (result.skipped === true) {
            skipped++;
        }
        else {
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
        blocked,
        blockedInvoices: [...blockedInvoices],
        failures,
        capped: eligible > attempted,
    };
});
/**
 * Admin-only: normalize vehicle class values in all customer_rate_entries.
 * Converts full names (e.g., "4 WHEELS JUMBO") to short codes (e.g., "4WJ").
 */
exports.normalizeRateEntryVehicleClasses = (0, https_1.onCall)({
    region: "asia-southeast1",
    enforceAppCheck: false,
}, async (request) => {
    if (request.auth?.token?.admin !== true) {
        throw new https_1.HttpsError("permission-denied", "Admin only");
    }
    const maxScan = Math.min(Math.max(1, request.data?.maxScan ?? 5000), 10000);
    const maxUpdate = Math.min(Math.max(1, request.data?.maxUpdate ?? 500), 1000);
    const db = admin.firestore();
    const snap = await db.collection(COL_RATE_ENTRIES).limit(maxScan).get();
    let needsUpdate = 0;
    let updated = 0;
    const samples = [];
    for (const doc of snap.docs) {
        const data = doc.data();
        const old = String(data.vehicleClass ?? "4WJ").trim().toUpperCase();
        const normalized = (0, billingCompute_1.normalizeVehicleClass)(old);
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
});
/**
 * Scheduled: auto-compute billing for recently delivered trips that have no billing yet.
 * Runs every 15 minutes. Scans trips delivered in the last 30 minutes (double the interval
 * for overlap safety). Idempotent — skips trips that already have billingEstimateThb set.
 *
 * Note: a Firestore document trigger cannot be used here because the Firestore database is
 * in asia-southeast3 (Jakarta), which is not a supported Cloud Functions region.
 */
exports.autoComputeBillingOnDelivery = (0, scheduler_1.onSchedule)({
    schedule: "every 15 minutes",
    region: "asia-southeast1",
    timeoutSeconds: 540,
}, async () => {
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
        const data = doc.data();
        if (typeof data.billingEstimateThb === "number")
            continue; // already billed
        const result = await tryWriteBillingSnapshotFromTripData(db, doc.id, data, doc.ref, hubMaps);
        if (result.ok && !result.skipped) {
            written++;
            firebase_functions_1.logger.info("[autoComputeBilling] billing written", {
                tripId: doc.id,
                billingEstimateThb: result.billingEstimateThb,
            });
        }
        else if (!result.ok) {
            failed++;
            firebase_functions_1.logger.warn("[autoComputeBilling] billing failed", { tripId: doc.id, error: result.error });
        }
    }
    firebase_functions_1.logger.info("[autoComputeBilling] run complete", { scanned: snap.size, written, failed });
});
//# sourceMappingURL=tripBillingOnDelivered.js.map