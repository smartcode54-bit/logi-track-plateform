/**
 * Upsert a customer_service_fees row for an EXACT customerId.
 *
 * Avoids the duplicate-customer-name problem in the UI dropdown by keying
 * directly on the customer document id.
 *
 * Auth: GOOGLE_APPLICATION_CREDENTIALS, or ADC (gcloud auth application-default login).
 *
 * Usage (from logitrack-web/):
 *   node functions/scripts/set-service-fee.js \
 *     --project logitrack-prod \
 *     --customer z3NKa38VG8p5ccKDqcb2 \
 *     --type standby --unit per_trip --amount 500 \
 *     [--note "เข้าตามรอบเวลาแล้วไม่มีงาน"] [--dry-run]
 *
 * Flags:
 *   --customer <id>   customer document id (required)
 *   --type <t>        feeType: standby | extra_stop | waiting_time | special_handling | service_charge (required)
 *   --unit <u>        per_trip | per_stop  (default: per_trip for standby, per_stop for extra_stop)
 *   --amount <n>      THB amount (required)
 *   --note <s>        optional note
 *   --dry-run         print without writing
 *
 * If a row with the same customerId + feeType already exists, it is UPDATED
 * (amount/unit/note) instead of creating a duplicate.
 */

const admin = require("firebase-admin");

const args = process.argv.slice(2);
function flag(name, def) {
    const i = args.indexOf(`--${name}`);
    return i >= 0 ? args[i + 1] : def;
}
const PROJECT_ID = flag("project", "logitrack-prod");
const CUSTOMER_ID = flag("customer");
const FEE_TYPE = flag("type");
const AMOUNT = Number(flag("amount"));
const NOTE = flag("note");
const DRY_RUN = args.includes("--dry-run");
const DEFAULT_UNIT = FEE_TYPE === "extra_stop" ? "per_stop" : "per_trip";
const UNIT = flag("unit", DEFAULT_UNIT);

const VALID_TYPES = ["standby", "extra_stop", "waiting_time", "special_handling", "service_charge"];

if (!CUSTOMER_ID || !FEE_TYPE || !Number.isFinite(AMOUNT)) {
    console.error("❌  --customer, --type and --amount are required.");
    process.exit(1);
}
if (!VALID_TYPES.includes(FEE_TYPE)) {
    console.error(`❌  --type must be one of: ${VALID_TYPES.join(", ")}`);
    process.exit(1);
}

if (!admin.apps.length) admin.initializeApp({ projectId: PROJECT_ID });
const db = admin.firestore();

async function main() {
    console.log(`\n🚀  Set service fee`);
    console.log(`    project : ${PROJECT_ID}`);
    console.log(`    customer: ${CUSTOMER_ID}`);
    console.log(`    type    : ${FEE_TYPE}  unit: ${UNIT}  amount: ${AMOUNT}`);
    console.log(`    dry-run : ${DRY_RUN}\n`);

    // Sanity check: customer exists
    const custSnap = await db.collection("customers").doc(CUSTOMER_ID).get();
    if (!custSnap.exists) {
        console.warn(`⚠️   customers/${CUSTOMER_ID} does NOT exist — continuing anyway.`);
    } else {
        const c = custSnap.data();
        console.log(`✅  customer found: ${c.name ?? c.customerName ?? c.code ?? "(no name)"}\n`);
    }

    // Look for an existing row with same customerId + feeType
    const existing = await db
        .collection("customer_service_fees")
        .where("customerId", "==", CUSTOMER_ID)
        .get();
    const match = existing.docs.find((d) => d.data().feeType === FEE_TYPE);

    const payload = {
        customerId: CUSTOMER_ID,
        feeType: FEE_TYPE,
        amountThb: AMOUNT,
        unit: UNIT,
        updatedAt: admin.firestore.FieldValue.serverTimestamp(),
    };
    if (NOTE) payload.note = NOTE;

    if (match) {
        console.log(`↩️   Existing row found (${match.id}) → will UPDATE.`);
        if (DRY_RUN) { console.log(`🔵  DRY-RUN — no write.`); }
        else { await match.ref.update(payload); console.log(`✅  Updated customer_service_fees/${match.id}`); }
    } else {
        payload.createdAt = admin.firestore.FieldValue.serverTimestamp();
        console.log(`➕  No existing row → will CREATE.`);
        if (DRY_RUN) { console.log(`🔵  DRY-RUN — no write.`); }
        else { const ref = await db.collection("customer_service_fees").add(payload); console.log(`✅  Created customer_service_fees/${ref.id}`); }
    }

    console.log(`\n👉  Next: run the standby/trip billing backfill, then check the income page.`);
    process.exit(0);
}

main().catch((e) => { console.error("Fatal:", e); process.exit(1); });
