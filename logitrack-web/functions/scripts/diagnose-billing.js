/**
 * DIAGNOSTIC (read-only): why are standby + multi-drop fees not being billed?
 *
 * Reads (never writes) Firestore and reports exactly which prerequisite fails
 * for each standby_record and each multi-delivery trip_record.
 *
 * Auth: set GOOGLE_APPLICATION_CREDENTIALS, or run on a machine with ADC
 *       (gcloud auth application-default login).
 *
 * Usage (from logitrack-web/):
 *   node functions/scripts/diagnose-billing.js [--project logitrack-prod]
 */

const admin = require("firebase-admin");

const args = process.argv.slice(2);
const projectIdx = args.indexOf("--project");
const PROJECT_ID = projectIdx >= 0 ? args[projectIdx + 1] : "logitrack-prod";

if (!admin.apps.length) admin.initializeApp({ projectId: PROJECT_ID });
const db = admin.firestore();

function toMs(v) {
    if (!v) return 0;
    if (typeof v === "number") return v;
    if (v instanceof Date) return v.getTime();
    if (typeof v.toMillis === "function") return v.toMillis();
    if (typeof v.toDate === "function") return v.toDate().getTime();
    return 0;
}
function normDest(d) {
    const u = (d ?? "").trim().toUpperCase();
    if (!u) return "";
    if (u.startsWith("SOCE")) return "SOCE";
    if (u.startsWith("SOCN")) return "SOCN";
    if (u.startsWith("SOCW")) return "SOCW";
    const i = u.indexOf("-");
    return i > 0 ? u.slice(0, i).trim() : u;
}
function extractHub(s) {
    const raw = (s ?? "").trim();
    if (!raw) return "";
    return (raw.split(" - ")[0] ?? raw).trim().toUpperCase();
}
function normVeh(v) {
    const u = (v || "4WJ").trim().toUpperCase();
    const m = { "4 WHEELS JUMBO": "4WJ", "6 WHEELS": "6W", "10 WHEELS": "10W", "2 WHEELS": "2W" };
    return m[u] ?? u;
}

async function main() {
    console.log(`\n=== BILLING DIAGNOSTIC (project: ${PROJECT_ID}) ===\n`);

    // ── CUSTOMER NAME LOOKUP ───────────────────────────────────────────────
    const custSnap = await db.collection("customers").get();
    const custName = new Map();
    custSnap.docs.forEach((d) => {
        const c = d.data();
        custName.set(d.id, String(c.name ?? c.customerName ?? c.code ?? "").trim() || "(no name)");
    });
    const nm = (id) => `${id} [${custName.get(id) ?? "??? NOT IN customers"}]`;
    console.log(`[customers] total: ${custSnap.size}`);

    // ── STANDBY RATE ENTRIES + SERVICE FEE FALLBACK ────────────────────────
    const ratesSnap = await db.collection("standby_rate_entries").get();
    const standbyRateCustomers = new Set();
    ratesSnap.docs.forEach((d) => {
        const c = String(d.data().customerId ?? "").trim();
        if (c) standbyRateCustomers.add(c);
    });
    // customer_service_fees fallback (feeType "standby" / "extra_stop")
    const feesSnap = await db.collection("customer_service_fees").get();
    const standbyFeeCustomers = new Set();
    const extraStopFeeCustomers = new Set();
    feesSnap.docs.forEach((d) => {
        const x = d.data();
        const c = String(x.customerId ?? "").trim();
        if (!c) return;
        if (x.feeType === "standby") standbyFeeCustomers.add(c);
        if (x.feeType === "extra_stop") extraStopFeeCustomers.add(c);
    });
    // A customer can be standby-billed via a rate entry OR a standby service fee.
    const standbyBillableCustomers = new Set([...standbyRateCustomers, ...standbyFeeCustomers]);
    console.log(`[standby_rate_entries] total docs: ${ratesSnap.size}`);
    console.log(`  customers with a standby rate: ${[...standbyRateCustomers].map(nm).join(", ") || "(none)"}`);
    console.log(`[customer_service_fees] standby fee customers: ${[...standbyFeeCustomers].map(nm).join(", ") || "(none)"}`);
    console.log(`[customer_service_fees] extra_stop fee customers: ${[...extraStopFeeCustomers].map(nm).join(", ") || "(none)"}`);
    if (standbyBillableCustomers.size === 0) {
        console.log(`  ⚠️  NO STANDBY RATE OR SERVICE FEE → every standby record will fail billing.\n`);
    }

    // ── STANDBY RECORDS ────────────────────────────────────────────────────
    const sbSnap = await db.collection("standby_records").get();
    let sbCompleted = 0, sbBilled = 0, sbNoCustomer = 0, sbNoRate = 0, sbReady = 0;
    const sbProblems = [];
    for (const doc of sbSnap.docs) {
        const d = doc.data();
        if (d.status !== "completed") continue;
        sbCompleted++;
        if (typeof d.billingEstimateThb === "number") { sbBilled++; continue; }

        let customerId = (typeof d.customerId === "string" && d.customerId.trim()) || "";
        if (!customerId && typeof d.taskId === "string" && d.taskId.trim()) {
            try {
                const ts = await db.collection("tasks").doc(d.taskId.trim()).get();
                if (ts.exists) {
                    const t = ts.data();
                    customerId = (t.sourceHubLinkedCustomerId || t.destinationLinkedCustomerId || "").trim();
                }
            } catch { /* ignore */ }
        }
        if (!customerId) { sbNoCustomer++; sbProblems.push(`  ✗ ${doc.id}: NO customerId`); continue; }
        if (!standbyBillableCustomers.has(customerId)) { sbNoRate++; sbProblems.push(`  ✗ ${doc.id}: no standby rate/fee for ${nm(customerId)}`); continue; }
        const via = standbyRateCustomers.has(customerId) ? "rate entry" : "service fee";
        sbReady++; sbProblems.push(`  ✓ ${doc.id}: READY via ${via} (${nm(customerId)}) — just run the backfill`);
    }
    console.log(`\n[standby_records]`);
    console.log(`  total: ${sbSnap.size} | completed: ${sbCompleted} | already billed: ${sbBilled}`);
    console.log(`  unbilled → no customer: ${sbNoCustomer} | no rate: ${sbNoRate} | READY (need backfill): ${sbReady}`);
    sbProblems.forEach((l) => console.log(l));

    // ── MULTI-DELIVERY TRIPS ───────────────────────────────────────────────
    const mdSnap = await db.collection("trip_records").where("isMultiDelivery", "==", true).get();
    let mdDelivered = 0, mdBilled = 0, mdFewStops = 0, mdNoCustomer = 0, mdNoRate = 0, mdReady = 0;
    const mdProblems = [];
    // cache rate entries per customer
    const rateCache = new Map();
    async function ratesFor(cid) {
        if (rateCache.has(cid)) return rateCache.get(cid);
        const s = await db.collection("customer_rate_entries").where("customerId", "==", cid).get();
        const rows = s.docs.map((x) => {
            const e = x.data();
            return {
                hubId: String(e.hubId ?? "").trim().toUpperCase(),
                destinationCode: normDest(String(e.destinationCode ?? "")),
                vehicleClass: normVeh(String(e.vehicleClass ?? "4WJ")),
            };
        });
        rateCache.set(cid, rows);
        return rows;
    }

    for (const doc of mdSnap.docs) {
        const d = doc.data();
        if (d.status !== "delivered") continue;
        mdDelivered++;
        if (typeof d.billingEstimateThb === "number") { mdBilled++; continue; }

        const stops = (Array.isArray(d.deliveryStopsProgress) ? d.deliveryStopsProgress : [])
            .filter((s) => s.destination && s.status === "delivered");
        if (stops.length < 2) { mdFewStops++; mdProblems.push(`  ✗ ${doc.id}: only ${stops.length} delivered stop(s)`); continue; }

        const taskId = typeof d.taskId === "string" ? d.taskId.trim() : "";
        let task = null;
        if (taskId) { try { const ts = await db.collection("tasks").doc(taskId).get(); if (ts.exists) task = ts.data(); } catch {} }
        const customerId = ((task?.sourceHubLinkedCustomerId || task?.destinationLinkedCustomerId) || "").trim();
        if (!customerId) { mdNoCustomer++; mdProblems.push(`  ✗ ${doc.id}: task ${taskId} has no linked customer`); continue; }

        const hubId = extractHub(task?.sourceHub);
        const veh = normVeh(task?.truckType || "4WJ");
        const rates = await ratesFor(customerId);
        const stop0Dest = normDest(stops[0].destination);
        const stop0Match = rates.some((r) => r.hubId === hubId && r.destinationCode === stop0Dest && r.vehicleClass === veh);
        if (!stop0Match) {
            mdNoRate++;
            mdProblems.push(`  ✗ ${doc.id}: no rate for base stop ${hubId}→${stop0Dest} (${veh}), customer ${customerId}`);
            continue;
        }
        mdReady++; mdProblems.push(`  ✓ ${doc.id}: READY (${stops.length} stops, customer ${customerId}) — just run the trip backfill`);
    }
    console.log(`\n[trip_records isMultiDelivery=true]`);
    console.log(`  total: ${mdSnap.size} | delivered: ${mdDelivered} | already billed: ${mdBilled}`);
    console.log(`  unbilled → <2 stops: ${mdFewStops} | no customer: ${mdNoCustomer} | no base rate: ${mdNoRate} | READY (need backfill): ${mdReady}`);
    mdProblems.forEach((l) => console.log(l));

    console.log(`\n=== DONE ===\n`);
    process.exit(0);
}

main().catch((e) => { console.error("Fatal:", e); process.exit(1); });
