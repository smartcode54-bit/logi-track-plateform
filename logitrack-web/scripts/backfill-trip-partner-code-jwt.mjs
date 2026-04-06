/**
 * One-time: set partnerCode = "JWT" on trip_records missing partnerCode.
 *
 * Usage:
 *   GOOGLE_APPLICATION_CREDENTIALS=/path/to/sa.json node scripts/backfill-trip-partner-code-jwt.mjs
 *   node scripts/backfill-trip-partner-code-jwt.mjs --dry-run
 *
 * Optional: --job-type=line_haul  (only docs with jobType == line_haul)
 */
import { initializeApp, cert, applicationDefault } from "firebase-admin/app";
import { getFirestore, FieldValue, FieldPath } from "firebase-admin/firestore";
import { readFileSync } from "fs";

const dryRun = process.argv.includes("--dry-run");
const jobTypeArg = process.argv.find((a) => a.startsWith("--job-type="));
const jobTypeFilter = jobTypeArg ? jobTypeArg.split("=")[1] : null;

function initAdmin() {
    const keyPath = process.env.GOOGLE_APPLICATION_CREDENTIALS;
    if (keyPath) {
        const serviceAccount = JSON.parse(readFileSync(keyPath, "utf8"));
        initializeApp({ credential: cert(serviceAccount) });
    } else {
        initializeApp({ credential: applicationDefault() });
    }
}

async function main() {
    initAdmin();
    const db = getFirestore();
    const col = db.collection("trip_records");
    let lastDoc = null;
    let scanned = 0;
    let updated = 0;
    const batchSize = 400;

    // eslint-disable-next-line no-constant-condition
    while (true) {
        let q = col.orderBy(FieldPath.documentId()).limit(500);
        if (lastDoc) q = q.startAfter(lastDoc);
        const snap = await q.get();
        if (snap.empty) break;

        let batch = db.batch();
        let batchCount = 0;

        for (const doc of snap.docs) {
            scanned++;
            const data = doc.data();
            const existing = data.partnerCode;
            if (existing != null && String(existing).trim() !== "") {
                lastDoc = doc;
                continue;
            }
            if (jobTypeFilter && data.jobType !== jobTypeFilter) {
                lastDoc = doc;
                continue;
            }

            if (dryRun) {
                console.log(`[dry-run] would update ${doc.id}`);
                updated++;
            } else {
                batch.update(doc.ref, {
                    partnerCode: "JWT",
                    updatedAt: FieldValue.serverTimestamp(),
                });
                batchCount++;
                updated++;
                if (batchCount >= batchSize) {
                    await batch.commit();
                    batch = db.batch();
                    batchCount = 0;
                }
            }
            lastDoc = doc;
        }

        if (!dryRun && batchCount > 0) {
            await batch.commit();
        }

        if (snap.size < 500) break;
    }

    console.log(
        dryRun ? `Dry-run complete. scanned=${scanned} would_update=${updated}` : `Done. scanned=${scanned} updated=${updated}`,
    );
}

main().catch((e) => {
    console.error(e);
    process.exit(1);
});
