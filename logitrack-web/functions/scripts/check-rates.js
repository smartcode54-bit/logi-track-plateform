/**
 * READ-ONLY: list customer_rate_entries for a customer, to see which
 * hub→destination(vehicle) routes have a rate card.
 *
 * Usage (from logitrack-web/):
 *   node functions/scripts/check-rates.js --project logitrack-prod --customer z3NKa38VG8p5ccKDqcb2
 */
const admin = require("firebase-admin");

const args = process.argv.slice(2);
function flag(name, def) { const i = args.indexOf(`--${name}`); return i >= 0 ? args[i + 1] : def; }
const PROJECT_ID = flag("project", "logitrack-prod");
const CUSTOMER = flag("customer");

if (!admin.apps.length) admin.initializeApp({ projectId: PROJECT_ID });
const db = admin.firestore();

async function main() {
    console.log(`\n=== RATE ENTRIES for ${CUSTOMER} (project: ${PROJECT_ID}) ===\n`);
    const snap = await db.collection("customer_rate_entries").where("customerId", "==", CUSTOMER).get();
    console.log(`total rate rows: ${snap.size}\n`);
    snap.docs.forEach((d) => {
        const e = d.data();
        console.log(`  ${String(e.hubId ?? "").padEnd(10)} → ${String(e.destinationCode ?? "").padEnd(14)} ${String(e.vehicleClass ?? "").padEnd(6)} ฿${e.rateThb}  eff=${e.effectiveFrom?.toDate?.()?.toISOString?.()?.slice(0,10) ?? e.effectiveFrom}`);
    });
    console.log(`\n=== DONE ===\n`);
    process.exit(0);
}
main().catch((e) => { console.error("Fatal:", e); process.exit(1); });
