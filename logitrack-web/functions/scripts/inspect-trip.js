/**
 * READ-ONLY: dump billing-relevant fields for one trip_record.
 *
 * Usage (from logitrack-web/):
 *   node functions/scripts/inspect-trip.js --project logitrack-prod --trip ZXZB26009330851
 *   (or --doc <docId> if you know the document id)
 */
const admin = require("firebase-admin");

const args = process.argv.slice(2);
function flag(name, def) {
    const i = args.indexOf(`--${name}`);
    return i >= 0 ? args[i + 1] : def;
}
const PROJECT_ID = flag("project", "logitrack-prod");
const SPX = flag("trip");
const DOCID = flag("doc");

if (!admin.apps.length) admin.initializeApp({ projectId: PROJECT_ID });
const db = admin.firestore();

function show(label, v) { console.log(`   ${label.padEnd(34)}: ${v === undefined ? "(undefined)" : JSON.stringify(v)}`); }

async function main() {
    console.log(`\n=== INSPECT TRIP (project: ${PROJECT_ID}) ===`);

    let doc = null;
    if (DOCID) {
        const s = await db.collection("trip_records").doc(DOCID).get();
        if (s.exists) doc = { id: s.id, data: s.data() };
    }
    if (!doc && SPX) {
        const snap = await db.collection("trip_records").where("spxTripId", "==", SPX).limit(5).get();
        if (!snap.empty) doc = { id: snap.docs[0].id, data: snap.docs[0].data() };
        if (!doc) {
            const s = await db.collection("trip_records").doc(SPX).get();
            if (s.exists) doc = { id: s.id, data: s.data() };
        }
    }
    if (!doc) { console.error(`❌  trip not found (trip=${SPX} doc=${DOCID})`); process.exit(1); }

    const d = doc.data;
    console.log(`\n📦  trip_records/${doc.id}`);
    show("spxTripId", d.spxTripId);
    show("status", d.status);
    show("taskId", d.taskId);
    show("deliveredTimestamp", d.deliveredTimestamp?.toDate?.()?.toISOString?.() ?? d.deliveredTimestamp);
    show("isMultiDelivery", d.isMultiDelivery);
    show("billingIsMultiDelivery", d.billingIsMultiDelivery);
    show("billingEstimateThb", d.billingEstimateThb);
    show("billingBaseRateThb", d.billingBaseRateThb);
    show("billingCustomerId", d.billingCustomerId);
    show("billingLookupHubId", d.billingLookupHubId);
    show("billingLookupDestination", d.billingLookupDestination);
    show("totalDeliveryStops", d.totalDeliveryStops);
    show("billingMultiDeliveryBreakdown", d.billingMultiDeliveryBreakdown);

    const stops = Array.isArray(d.deliveryStopsProgress) ? d.deliveryStopsProgress : [];
    console.log(`\n   deliveryStopsProgress (${stops.length}):`);
    stops.forEach((s, i) => console.log(`     [${i}] dest=${JSON.stringify(s.destination)} status=${JSON.stringify(s.status)}`));

    if (d.taskId) {
        const ts = await db.collection("tasks").doc(String(d.taskId).trim()).get();
        if (ts.exists) {
            const t = ts.data();
            console.log(`\n📋  tasks/${d.taskId}`);
            show("sourceHub", t.sourceHub);
            show("destination", t.destination);
            show("truckType", t.truckType);
            show("sourceHubLinkedCustomerId", t.sourceHubLinkedCustomerId);
            show("destinationLinkedCustomerId", t.destinationLinkedCustomerId);
        } else {
            console.log(`\n⚠️   tasks/${d.taskId} NOT FOUND`);
        }
    }

    console.log(`\n=== DONE ===\n`);
    process.exit(0);
}
main().catch((e) => { console.error("Fatal:", e); process.exit(1); });
