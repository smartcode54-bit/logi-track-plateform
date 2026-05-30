/**
 * sync-customers-prod-to-dev.js
 *
 * 1. ลบ customers ทั้งหมดใน Dev
 * 2. Copy customers ทั้งหมดจาก Prod → Dev
 *
 * Usage:
 *   cd functions
 *   node scripts/sync-customers-prod-to-dev.js
 */

const admin = require("firebase-admin");
const path = require("path");

const PROD_KEY = path.resolve(
  "D:/Secret/LOGI-TRACK/logitrack-prod-firebase-adminsdk-fbsvc-13423c6233.json"
);
const DEV_KEY = path.resolve(
  "D:/Secret/LOGI-TRACK/logi-track-wrt-dev-firebase-adminsdk-fbsvc-9bc95442d2.json"
);

const COLLECTION = "customers";
const BATCH_SIZE = 400;

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

async function deleteAll() {
  log(`🗑  ลบ ${COLLECTION} ใน Dev...`);
  const snap = await devDb.collection(COLLECTION).get();
  if (snap.empty) { log("  ↳ ไม่มี documents ใน Dev — skip"); return 0; }

  let deleted = 0;
  for (let i = 0; i < snap.docs.length; i += BATCH_SIZE) {
    const batch = devDb.batch();
    snap.docs.slice(i, i + BATCH_SIZE).forEach((d) => batch.delete(d.ref));
    await batch.commit();
    deleted += Math.min(BATCH_SIZE, snap.docs.length - i);
    log(`  ↳ ลบแล้ว ${deleted}/${snap.docs.length}`);
  }
  return deleted;
}

async function copyFromProd() {
  log(`📥 อ่าน ${COLLECTION} จาก Prod...`);
  const snap = await prodDb.collection(COLLECTION).get();
  if (snap.empty) { log("  ↳ Prod ไม่มี documents"); return 0; }

  log(`  ↳ พบ ${snap.docs.length} documents — เขียนลง Dev...`);
  let written = 0;
  for (let i = 0; i < snap.docs.length; i += BATCH_SIZE) {
    const batch = devDb.batch();
    snap.docs.slice(i, i + BATCH_SIZE).forEach((d) => {
      batch.set(devDb.collection(COLLECTION).doc(d.id), d.data());
    });
    await batch.commit();
    written += Math.min(BATCH_SIZE, snap.docs.length - i);
    log(`  ↳ เขียนแล้ว ${written}/${snap.docs.length}`);
  }
  return written;
}

async function main() {
  console.log("=".repeat(50));
  console.log("  sync-customers-prod-to-dev");
  console.log("=".repeat(50));

  const deleted = await deleteAll();
  const written = await copyFromProd();

  console.log("\n" + "=".repeat(50));
  console.log(`  ลบ Dev เก่า : ${deleted} docs`);
  console.log(`  Copy จาก Prod : ${written} docs`);
  console.log("=".repeat(50));
  console.log("Done ✓");
}

main().catch((err) => {
  console.error("Error:", err.message ?? err);
  process.exit(1);
});
