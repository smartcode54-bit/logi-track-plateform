/**
 * copy-prod-to-dev-may2026.js
 *
 * Copies May 2026 trip/task/billing data from Prod Firestore → Dev Firestore.
 *
 * Usage:
 *   node functions/scripts/copy-prod-to-dev-may2026.js
 *
 * Requires both service account key files at D:\Secret\LOGI-TRACK\
 */

const admin = require("firebase-admin");
const path = require("path");

// ─── Config ────────────────────────────────────────────────────────────────
const PROD_KEY = path.resolve(
  "D:/Secret/LOGI-TRACK/logitrack-prod-firebase-adminsdk-fbsvc-13423c6233.json"
);
const DEV_KEY = path.resolve(
  "D:/Secret/LOGI-TRACK/logi-track-wrt-dev-firebase-adminsdk-fbsvc-9bc95442d2.json"
);

const MAY_START = new Date("2026-05-01T00:00:00.000+07:00"); // Asia/Bangkok
const MAY_END   = new Date("2026-06-01T00:00:00.000+07:00"); // exclusive

// Collections with date filter (filter by createdAt in May 2026)
const DATE_FILTERED_COLLECTIONS = [
  "trip_records",
  "tasks",
  "standby_records",
];

// Master data — copy ALL documents (small collections, needed for billing)
const MASTER_COLLECTIONS = [
  "customers",
  "drivers",
  "trucks",
  "hubs",
  "customer_rate_entries",
  "customer_fuel_rate_adjustments",
];

const BATCH_SIZE = 400; // Firestore max 500; use 400 to be safe
// ───────────────────────────────────────────────────────────────────────────

// Init two separate Firebase apps
const prodApp = admin.initializeApp(
  { credential: admin.credential.cert(require(PROD_KEY)) },
  "prod"
);
const devApp = admin.initializeApp(
  { credential: admin.credential.cert(require(DEV_KEY)) },
  "dev"
);

const prodDb = prodApp.firestore();
const devDb  = devApp.firestore();

function log(msg) {
  console.log(`[${new Date().toLocaleTimeString("th-TH")}] ${msg}`);
}

async function copyCollection(collectionName, useMonthFilter) {
  log(`▶ ${collectionName}${useMonthFilter ? " (May 2026)" : " (all)"}`);

  let queryRef = prodDb.collection(collectionName);

  if (useMonthFilter) {
    queryRef = queryRef
      .where("createdAt", ">=", admin.firestore.Timestamp.fromDate(MAY_START))
      .where("createdAt", "<",  admin.firestore.Timestamp.fromDate(MAY_END));
  }

  const snapshot = await queryRef.get();

  if (snapshot.empty) {
    log(`  ↳ 0 documents — skip`);
    return 0;
  }

  const docs = snapshot.docs;
  log(`  ↳ ${docs.length} documents found — writing to dev...`);

  let written = 0;
  for (let i = 0; i < docs.length; i += BATCH_SIZE) {
    const chunk = docs.slice(i, i + BATCH_SIZE);
    const batch = devDb.batch();
    chunk.forEach((doc) => {
      batch.set(devDb.collection(collectionName).doc(doc.id), doc.data());
    });
    await batch.commit();
    written += chunk.length;
    log(`  ↳ ${written}/${docs.length} written`);
  }

  return written;
}

async function copySubcollections(collectionName, parentDocIds) {
  // Copy known subcollections for trip_records and drivers
  const SUBCOLLECTIONS = {
    trip_records: [],          // no standard subcollections needed
    drivers: ["mobile_installations"],
  };

  const subs = SUBCOLLECTIONS[collectionName];
  if (!subs || subs.length === 0) return;

  for (const subName of subs) {
    let total = 0;
    for (const docId of parentDocIds) {
      const snap = await prodDb
        .collection(collectionName)
        .doc(docId)
        .collection(subName)
        .get();
      if (snap.empty) continue;

      const batch = devDb.batch();
      snap.docs.forEach((d) => {
        batch.set(
          devDb.collection(collectionName).doc(docId).collection(subName).doc(d.id),
          d.data()
        );
      });
      await batch.commit();
      total += snap.docs.length;
    }
    if (total > 0) log(`  ↳ subcollection ${subName}: ${total} docs written`);
  }
}

async function main() {
  console.log("=".repeat(60));
  console.log("  copy-prod-to-dev-may2026");
  console.log(`  Range : ${MAY_START.toISOString()} → ${MAY_END.toISOString()}`);
  console.log("=".repeat(60));

  const stats = {};

  // Date-filtered collections
  for (const col of DATE_FILTERED_COLLECTIONS) {
    const count = await copyCollection(col, true);
    stats[col] = count;

    // Copy known subcollections for matching docs (if any)
    if (count > 0) {
      const snap = await prodDb.collection(col)
        .where("createdAt", ">=", admin.firestore.Timestamp.fromDate(MAY_START))
        .where("createdAt", "<",  admin.firestore.Timestamp.fromDate(MAY_END))
        .select() // fetch doc IDs only
        .get();
      await copySubcollections(col, snap.docs.map((d) => d.id));
    }
  }

  // Master data collections
  for (const col of MASTER_COLLECTIONS) {
    const count = await copyCollection(col, false);
    stats[col] = count;
  }

  console.log("\n" + "=".repeat(60));
  console.log("  SUMMARY");
  console.log("=".repeat(60));
  let total = 0;
  for (const [col, count] of Object.entries(stats)) {
    console.log(`  ${col.padEnd(38)} ${String(count).padStart(6)} docs`);
    total += count;
  }
  console.log("  " + "-".repeat(46));
  console.log(`  ${"TOTAL".padEnd(38)} ${String(total).padStart(6)} docs`);
  console.log("=".repeat(60));
  console.log("\nDone ✓");
}

main().catch((err) => {
  console.error("Error:", err.message ?? err);
  process.exit(1);
});
