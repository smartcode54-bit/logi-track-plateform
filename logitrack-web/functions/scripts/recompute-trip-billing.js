/**
 * Recompute & persist the billing snapshot for ONE delivered trip, using the
 * freshly-compiled billing logic (functions/lib/core/billingCompute.js).
 *
 * Use this after reset-multidrop-billing.js to apply the corrected multi-delivery
 * logic WITHOUT waiting for a Cloud Functions deploy. Mirrors the server-side
 * tryWriteBillingSnapshotFromTripData (multi-delivery + single paths).
 *
 * IMPORTANT: run `cd functions && npm run build` first so lib/ has the latest code.
 *
 * Auth: GOOGLE_APPLICATION_CREDENTIALS, or ADC (gcloud auth application-default login).
 *
 * Usage (from logitrack-web/):
 *   node functions/scripts/recompute-trip-billing.js --project logitrack-prod --trip ZXZB26009330851 [--dry-run]
 */
const admin = require("firebase-admin");
const billing = require("../lib/core/billingCompute.js");

const args = process.argv.slice(2);
function flag(name, def) { const i = args.indexOf(`--${name}`); return i >= 0 ? args[i + 1] : def; }
const PROJECT_ID = flag("project", "logitrack-prod");
const TRIP = flag("trip");
const DRY_RUN = args.includes("--dry-run");

if (!TRIP) { console.error("❌  --trip is required"); process.exit(1); }
if (!admin.apps.length) admin.initializeApp({ projectId: PROJECT_ID });
const db = admin.firestore();

function mapRate(customerId, doc) {
    const d = doc.data();
    return {
        id: doc.id,
        customerId,
        importId: String(d.importId ?? ""),
        hubId: String(d.hubId ?? "").trim().toUpperCase(),
        destinationCode: billing.normalizeDestinationCode(String(d.destinationCode ?? "")),
        vehicleClass: String(d.vehicleClass ?? "4WJ").trim().toUpperCase(),
        rateThb: Number(d.rateThb ?? 0),
        effectiveFromMs: billing.timestampLikeToMillis(d.effectiveFrom),
    };
}
function mapFuel(customerId, doc) {
    const d = doc.data();
    return {
        id: doc.id,
        customerId,
        effectiveFromMs: billing.timestampLikeToMillis(d.effectiveFrom),
        rateMultiplier: Number(d.rateMultiplier ?? 1),
        addThbPerTrip: Number(d.addThbPerTrip ?? 0),
    };
}

async function main() {
    console.log(`\n=== RECOMPUTE TRIP BILLING (project: ${PROJECT_ID}, dry-run: ${DRY_RUN}) ===\n`);

    let doc = null;
    const snap = await db.collection("trip_records").where("spxTripId", "==", TRIP).limit(1).get();
    if (!snap.empty) doc = { id: snap.docs[0].id, data: snap.docs[0].data() };
    if (!doc) { const s = await db.collection("trip_records").doc(TRIP).get(); if (s.exists) doc = { id: s.id, data: s.data() }; }
    if (!doc) { console.error(`❌  trip not found: ${TRIP}`); process.exit(1); }

    const data = doc.data;
    if (data.status !== "delivered") { console.error(`❌  trip not delivered (status=${data.status})`); process.exit(1); }
    if (typeof data.billingEstimateThb === "number") {
        console.warn(`⚠️   billingEstimateThb already set (฿${data.billingEstimateThb}). Run reset-multidrop-billing.js first. Aborting.`);
        process.exit(1);
    }

    const taskId = String(data.taskId ?? "").trim();
    const taskSnap = await db.collection("tasks").doc(taskId).get();
    if (!taskSnap.exists) { console.error(`❌  task not found: ${taskId}`); process.exit(1); }
    const t = taskSnap.data();

    const taskInput = {
        sourceHub: typeof t.sourceHub === "string" ? t.sourceHub : undefined,
        destination: typeof t.destination === "string" ? t.destination : undefined,
        truckType: typeof t.truckType === "string" ? t.truckType : undefined,
        sourceHubLinkedCustomerId: t.sourceHubLinkedCustomerId,
        destinationLinkedCustomerId: t.destinationLinkedCustomerId,
    };
    const customerId = (taskInput.sourceHubLinkedCustomerId || taskInput.destinationLinkedCustomerId || "").trim();
    if (!customerId) { console.error("❌  task has no linked customer"); process.exit(1); }

    const [rateSnap, fuelSnap, feeSnap] = await Promise.all([
        db.collection("customer_rate_entries").where("customerId", "==", customerId).get(),
        db.collection("customer_fuel_rate_adjustments").where("customerId", "==", customerId).get(),
        db.collection("customer_service_fees").where("customerId", "==", customerId).get(),
    ]);
    const rateEntries = rateSnap.docs.map((d) => mapRate(customerId, d));
    const fuelAdjustments = fuelSnap.docs.map((d) => mapFuel(customerId, d));

    let extraStopFeeThb;
    const extra = feeSnap.docs.find((d) => d.data().feeType === "extra_stop");
    if (extra) { const a = Number(extra.data().amountThb ?? 0); if (Number.isFinite(a) && a >= 0) extraStopFeeThb = a; }

    const tripParts = { deliveredTimestamp: data.deliveredTimestamp, createdAt: data.createdAt };
    const isMulti = data.isMultiDelivery === true;
    const progress = Array.isArray(data.deliveryStopsProgress) ? data.deliveryStopsProgress : [];

    if (isMulti && progress.length >= 2) {
        const stops = progress
            .filter((s) => s.destination && s.status === "delivered")
            .map((s) => ({ index: typeof s.index === "number" ? s.index : 1, destination: String(s.destination ?? "") }));
        if (stops.length < 2) { console.error("❌  < 2 delivered stops"); process.exit(1); }

        console.log(`  customer        : ${customerId}`);
        console.log(`  plannedDest     : ${taskInput.destination}`);
        console.log(`  deliveredStops  : ${stops.map((s) => s.destination).join(", ")}`);
        console.log(`  extraStopFeeThb : ${extraStopFeeThb}`);

        const r = billing.computeMultiDeliveryBilling(
            tripParts, taskInput, stops,
            billing.normalizeVehicleClass(taskInput.truckType || "4WJ"),
            rateEntries, fuelAdjustments, extraStopFeeThb
        );
        if (!r) { console.error("❌  computeMultiDeliveryBilling returned null (no rate card for base stop?)"); process.exit(1); }

        console.log(`\n  → baseRateThb   : ฿${r.baseRateThb}`);
        console.log(`  → stopChargeThb : ฿${r.stopChargeThb}`);
        console.log(`  → totalBilling  : ฿${r.totalBillingThb}`);
        console.log(`  → breakdown     :`);
        r.stopBreakdown.forEach((s) => console.log(`       stop ${s.stopIndex}: ${s.destination}  ฿${s.finalRateThb}`));

        const payload = {
            billingEstimateThb: r.totalBillingThb,
            billingBaseRateThb: r.baseRateThb,
            billingStopChargeThb: r.stopChargeThb,
            billingIsMultiDelivery: true,
            billingLookupHubId: billing.extractHubId(taskInput.sourceHub),
            billingLookupDestination: r.stopBreakdown[0]?.destination ?? null,
            billingMultiDeliveryBreakdown: r.stopBreakdown.map((s) => ({
                stopIndex: s.stopIndex, destination: s.destination, baseRateThb: s.baseRateThb, finalRateThb: s.finalRateThb,
            })),
            billingFuelAdjustmentId: r.fuelAdjustmentId ?? null,
            billingRateMultiplier: r.rateMultiplier,
            billingCustomerId: r.customerId,
            updatedAt: admin.firestore.FieldValue.serverTimestamp(),
        };
        if (DRY_RUN) { console.log(`\n🔵  DRY-RUN — no write.\n`); process.exit(0); }
        await db.collection("trip_records").doc(doc.id).update(payload);
        console.log(`\n✅  Wrote multi-delivery billing snapshot to trip_records/${doc.id}\n`);
    } else {
        const r = billing.computeTripBillingFromParts(tripParts, taskInput, rateEntries, fuelAdjustments);
        if (!r) { console.error("❌  computeTripBillingFromParts returned null"); process.exit(1); }
        const payload = {
            billingEstimateThb: r.finalRateThb,
            billingBaseRateThb: r.baseRateThb,
            billingRateImportId: r.rateImportId,
            billingLookupHubId: r.lookupHubId,
            billingLookupDestination: r.lookupDestination,
            billingFuelAdjustmentId: r.fuelAdjustmentId ?? null,
            billingRateMultiplier: r.rateMultiplier,
            billingAddThbPerTrip: r.addThbPerTrip,
            billingEffectiveFromDateStr: r.effectiveFromDateStr ?? null,
            billingCustomerId: r.customerId,
            updatedAt: admin.firestore.FieldValue.serverTimestamp(),
        };
        console.log(`  single-delivery → ฿${r.finalRateThb}`);
        if (DRY_RUN) { console.log(`\n🔵  DRY-RUN — no write.\n`); process.exit(0); }
        await db.collection("trip_records").doc(doc.id).update(payload);
        console.log(`\n✅  Wrote single-delivery billing snapshot to trip_records/${doc.id}\n`);
    }
    process.exit(0);
}
main().catch((e) => { console.error("Fatal:", e); process.exit(1); });
