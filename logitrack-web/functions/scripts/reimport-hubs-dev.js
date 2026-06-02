/**
 * reimport-hubs-dev.js
 *
 * Reads `hubs` collection from Prod, deduplicates by source_id,
 * wipes the Dev `hubs` collection, then re-imports using source_id as doc ID.
 *
 * Usage:
 *   node functions/scripts/reimport-hubs-dev.js [--dry-run]
 *
 * --dry-run  Print what would be written; do NOT touch Dev Firestore.
 *
 * Requires both service account key files at D:\Secret\LOGI-TRACK\
 */

const admin = require("firebase-admin");
const path = require("path");

const PROD_KEY = path.resolve(
  "D:/Secret/LOGI-TRACK/logitrack-prod-firebase-adminsdk-fbsvc-13423c6233.json"
);
const DEV_KEY = path.resolve(
  "D:/Secret/LOGI-TRACK/logi-track-wrt-dev-firebase-adminsdk-fbsvc-9bc95442d2.json"
);

const BATCH_SIZE = 400;
const DRY_RUN = process.argv.includes("--dry-run");

// ─── Init ──────────────────────────────────────────────────────────────────
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

// Score a doc by how many non-empty fields it has — prefer the richer record
function richness(data) {
  return Object.values(data).filter((v) => v !== null && v !== undefined && v !== "").length;
}

async function main() {
  if (DRY_RUN) log("🔍 DRY RUN — Dev Firestore will NOT be modified");

  // ── 1. Read all hubs from Prod ──────────────────────────────────────────
  log("Reading hubs from Prod…");
  const prodSnap = await prodDb.collection("hubs").get();
  log(`  ↳ ${prodSnap.size} documents found`);

  // ── 2. Deduplicate by source_id ─────────────────────────────────────────
  const bySourceId = new Map(); // source_id → { docId, data }

  let missing = 0;
  for (const docSnap of prodSnap.docs) {
    const data = docSnap.data();
    const sourceId = (data.source_id ?? data.hubId ?? data.hubCode ?? "").toString().trim();

    if (!sourceId) {
      missing++;
      log(`  ⚠  Doc ${docSnap.id} has no source_id — skipping`);
      continue;
    }

    const existing = bySourceId.get(sourceId);
    if (!existing || richness(data) > richness(existing.data)) {
      bySourceId.set(sourceId, { docId: docSnap.id, data });
    }
  }

  const dupes = prodSnap.size - missing - bySourceId.size;
  log(`  ↳ After dedup: ${bySourceId.size} unique hubs (removed ${dupes} duplicate${dupes !== 1 ? "s" : ""}, skipped ${missing} without source_id)`);

  if (DRY_RUN) {
    log("\n── Unique hubs that would be written ──");
    for (const [sourceId, { data }] of bySourceId) {
      const name = data.source_name_th ?? data.source_name_en ?? data.station_name_th ?? "";
      const type = data.station_type ?? "?";
      console.log(`  [${type.padEnd(14)}] ${sourceId.padEnd(20)} ${name}`);
    }
    log("\n✅ Dry run complete — no changes made");
    return;
  }

  // ── 3. Wipe Dev hubs collection ─────────────────────────────────────────
  log("Deleting existing Dev hubs…");
  const devSnap = await devDb.collection("hubs").get();
  log(`  ↳ ${devSnap.size} documents to delete`);

  let deleted = 0;
  for (let i = 0; i < devSnap.docs.length; i += BATCH_SIZE) {
    const batch = devDb.batch();
    devSnap.docs.slice(i, i + BATCH_SIZE).forEach((d) => batch.delete(d.ref));
    await batch.commit();
    deleted += Math.min(BATCH_SIZE, devSnap.docs.length - i);
    log(`  ↳ Deleted ${deleted}/${devSnap.size}`);
  }

  // ── 4. Write deduplicated hubs (doc ID = source_id) ─────────────────────
  log("Writing deduplicated hubs to Dev…");
  const entries = [...bySourceId.entries()];
  let written = 0;

  for (let i = 0; i < entries.length; i += BATCH_SIZE) {
    const batch = devDb.batch();
    entries.slice(i, i + BATCH_SIZE).forEach(([sourceId, { data }]) => {
      const ref = devDb.collection("hubs").doc(sourceId);
      batch.set(ref, { ...data, source_id: sourceId }); // ensure source_id field is set
    });
    await batch.commit();
    written += Math.min(BATCH_SIZE, entries.length - i);
    log(`  ↳ Written ${written}/${entries.length}`);
  }

  log(`\n✅ Done — ${written} hubs in Dev (was ${devSnap.size}, deduped from ${prodSnap.size} prod docs)`);
}

main().catch((err) => {
  console.error("❌ Error:", err);
  process.exit(1);
});
