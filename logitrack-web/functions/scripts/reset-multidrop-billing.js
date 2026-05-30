/**
 * Reset stale billing snapshots on multi-delivery trips that were billed as a
 * SINGLE delivery before multi-drop support existed.
 *
 * Symptom: trip_record has  isMultiDelivery == true  but  billingIsMultiDelivery
 * is NOT true, while billingEstimateThb is already set (single-trip amount). The
 * idempotent billing functions skip it forever, so it never recomputes as a
 * multi-drop with the extra-stop fee.
 *
 * This script clears the billing* fields so the trip becomes "eligible" again.
 * After running, re-run the billing backfill (or computeTripBillingSnapshot) so
 * the corrected multi-delivery logic recomputes it.
 *
 * Auth: GOOGLE_APPLICATION_CREDENTIALS, or ADC (gcloud auth application-default login).
 *
 * Usage (from logitrack-web/):
 *   node functions/scripts/reset-multidrop-billing.js --project logitrack-prod --trip ZXZB26009330851 [--dry-run]
 *   node functions/scripts/reset-multidrop-billing.js --project logitrack-prod --all [--dry-run]
 *
 * Flags:
 *   --trip <spxTripId|docId>  reset one trip (matched by spxTripId, else doc id)
 *   --all                     scan every trip_record and reset all mis-billed multidrops
 *   --dry-run                 print without writing
 */
const admin = require("firebase-admin");

const args = process.argv.slice(2);
function flag(name, def) { const i = args.indexOf(`--${name}`); return i >= 0 ? args[i + 1] : def; }
const PROJECT_ID = flag("project", "logitrack-prod");
const TRIP = flag("trip");
const ALL = args.includes("--all");
const DRY_RUN = args.includes("--dry-run");

if (!TRIP && !ALL) {
    console.error("❌  Provide --trip <id> or --all");
    process.exit(1);
}

if (!admin.apps.length) admin.initializeApp({ projectId: PROJECT_ID });
const db = admin.firestore();

const FieldValue = admin.firestore.FieldValue;
const BILLING_FIELDS = [
    "billingEstimateThb",
    "billingBaseRateThb",
    "billingStopChargeThb",
    "billingIsMultiDelivery",
    "billingMultiDeliveryBreakdown",
    "billingRateImportId",
    "billingLookupHubId",
    "billingLookupDestination",
    "billingFuelAdjustmentId",
    "billingRateMultiplier",
    "billingAddThbPerTrip",
    "billingEffectiveFromDateStr",
];

function clearPayload() {
    const p = {};
    for (const f of BILLING_FIELDS) p[f] = FieldValue.delete();
    p.updatedAt = FieldValue.serverTimestamp();
    return p;
}

/** A trip that should be reset: multi-delivery, but billed as single (billingIsMultiDelivery not true). */
function needsReset(d) {
    const isMulti = d.isMultiDelivery === true;
    const stops = Array.isArray(d.deliveryStopsProgress)
        ? d.deliveryStopsProgress.filter((s) => s && s.destination && s.status === "delivered")
        : [];
    const billedAsSingle = typeof d.billingEstimateThb === "number" && d.billingIsMultiDelivery !== true;
    return isMulti && stops.length >= 2 && billedAsSingle;
}

async function resetDoc(id, d) {
    const stops = (d.deliveryStopsProgress || []).filter((s) => s && s.destination && s.status === "delivered");
    console.log(`  • ${d.spxTripId ?? id}  (docId=${id})  oldBilling=฿${d.billingEstimateThb}  deliveredStops=${stops.length}`);
    if (DRY_RUN) { console.log(`      🔵 DRY-RUN — would clear billing fields`); return; }
    await db.collection("trip_records").doc(id).update(clearPayload());
    console.log(`      ✅ cleared`);
}

async function main() {
    console.log(`\n=== RESET MULTIDROP BILLING (project: ${PROJECT_ID}, dry-run: ${DRY_RUN}) ===\n`);

    if (TRIP) {
        let doc = null;
        const snap = await db.collection("trip_records").where("spxTripId", "==", TRIP).limit(1).get();
        if (!snap.empty) doc = { id: snap.docs[0].id, data: snap.docs[0].data() };
        if (!doc) { const s = await db.collection("trip_records").doc(TRIP).get(); if (s.exists) doc = { id: s.id, data: s.data() }; }
        if (!doc) { console.error(`❌  trip not found: ${TRIP}`); process.exit(1); }

        if (!needsReset(doc.data)) {
            console.warn(`⚠️   ${TRIP} does not match the "mis-billed multidrop" criteria:`);
            console.warn(`      isMultiDelivery=${doc.data.isMultiDelivery} billingIsMultiDelivery=${doc.data.billingIsMultiDelivery} billingEstimateThb=${doc.data.billingEstimateThb}`);
            console.warn(`      Resetting anyway since you named it explicitly.`);
        }
        await resetDoc(doc.id, doc.data);
        console.log(`\n👉  Next: run the billing backfill for the delivered date, or call computeTripBillingSnapshot.\n`);
        process.exit(0);
    }

    // --all
    const snap = await db.collection("trip_records").where("isMultiDelivery", "==", true).get();
    let matched = 0;
    for (const doc of snap.docs) {
        const d = doc.data();
        if (!needsReset(d)) continue;
        matched++;
        await resetDoc(doc.id, d);
    }
    console.log(`\nDone. Mis-billed multidrops ${DRY_RUN ? "found" : "reset"}: ${matched} / scanned ${snap.size}`);
    console.log(`\n👉  Next: run the billing backfill for the affected date range.\n`);
    process.exit(0);
}
main().catch((e) => { console.error("Fatal:", e); process.exit(1); });
