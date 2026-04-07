/**
 * One-time: set top-level partnerCode = "JWT" on trip_records.
 *
 * Default: only updates when BOTH top-level partnerCode AND ocrData.partnerCode are empty
 * (matches effectivePartnerCode on web). Mobile often writes JWT only under ocrData — then
 * the UI still shows JWT, but root partnerCode is missing and this script skips those docs.
 * Use --ignore-ocr-partner to set root partnerCode anyway when root is empty (e.g. OCR already "JWT").
 *
 * Dev dry-run (PowerShell):
 *   $env:GOOGLE_APPLICATION_CREDENTIALS="D:\path\to\dev-sa.json"
 *   $env:FIREBASE_PROJECT_ID="logi-track-wrt-dev"
 *   node scripts/backfill-trip-partner-code-jwt.mjs --dry-run
 *   node scripts/backfill-trip-partner-code-jwt.mjs --dry-run --ignore-ocr-partner
 *
 * Dev apply (after dry-run looks correct):
 *   node scripts/backfill-trip-partner-code-jwt.mjs
 *
 * Optional:
 *   --job-type=line_haul      only docs with jobType == line_haul
 *   --limit=50                stop after N updates (or would-updates in dry-run)
 *   --verbose                 log each doc: skip reason or would-update
 *   --ignore-ocr-partner      only check top-level partnerCode (ignore ocrData.partnerCode
 *                             when deciding to backfill; use if OCR has a code but you still
 *                             want root partnerCode = JWT)
 *   --doc-id=<id>             only process this trip_records document id (debug / one-off)
 */
import { initializeApp, cert, applicationDefault } from "firebase-admin/app";
import { getFirestore, FieldValue, FieldPath } from "firebase-admin/firestore";
import { readFileSync } from "fs";

const dryRun = process.argv.includes("--dry-run");
const verbose = process.argv.includes("--verbose");
const ignoreOcrPartner = process.argv.includes("--ignore-ocr-partner");
const jobTypeArg = process.argv.find((a) => a.startsWith("--job-type="));
const jobTypeFilter = jobTypeArg ? jobTypeArg.split("=")[1] : null;
const limitArg = process.argv.find((a) => a.startsWith("--limit="));
const updateLimit = limitArg ? Number.parseInt(limitArg.split("=")[1], 10) : null;
const docIdArg = process.argv.find((a) => a.startsWith("--doc-id="));
const singleDocId = docIdArg ? docIdArg.slice("--doc-id=".length) : null;

function initAdmin() {
    const keyPath = process.env.GOOGLE_APPLICATION_CREDENTIALS;
    let projectId = process.env.FIREBASE_PROJECT_ID || process.env.GCLOUD_PROJECT;
    if (keyPath) {
        const serviceAccount = JSON.parse(readFileSync(keyPath, "utf8"));
        projectId = projectId || serviceAccount.project_id;
        initializeApp({ credential: cert(serviceAccount), projectId });
    } else {
        if (!projectId) {
            console.error(
                "Missing project: set FIREBASE_PROJECT_ID (e.g. logi-track-wrt-dev) or GOOGLE_APPLICATION_CREDENTIALS with a service account JSON.",
            );
            process.exit(1);
        }
        initializeApp({ credential: applicationDefault(), projectId });
    }
    console.log(`Firestore project: ${projectId}${dryRun ? " (dry-run — no writes)" : " (APPLY — will write to Firestore)"}`);
}

/** @param {Record<string, unknown>} data */
function partnerBackfillSkipReason(data) {
    const top = data.partnerCode;
    const ocrP =
        data.ocrData && typeof data.ocrData === "object" && data.ocrData !== null
            ? /** @type {Record<string, unknown>} */ (data.ocrData).partnerCode
            : undefined;
    const hasTop = top != null && String(top).trim() !== "";
    const hasOcr = ocrP != null && String(ocrP).trim() !== "";
    const blockedByPartner = hasTop || (!ignoreOcrPartner && hasOcr);
    if (blockedByPartner) {
        const parts = [];
        if (hasTop) parts.push(`top partnerCode=${JSON.stringify(top)}`);
        if (hasOcr && !ignoreOcrPartner) parts.push(`ocrData.partnerCode=${JSON.stringify(ocrP)}`);
        return parts.length ? parts.join(", ") : "blockedByPartner";
    }
    if (jobTypeFilter && data.jobType !== jobTypeFilter) {
        return `jobType=${JSON.stringify(data.jobType)} (need --job-type=${jobTypeFilter})`;
    }
    return null;
}

async function main() {
    initAdmin();
    const db = getFirestore();
    const col = db.collection("trip_records");
    let lastDoc = null;
    let scanned = 0;
    let updated = 0;
    const batchSize = 400;
    let stoppedByLimit = false;

    if (singleDocId) {
        const docRef = col.doc(singleDocId);
        const docSnap = await docRef.get();
        if (!docSnap.exists) {
            console.error(`No document: trip_records/${singleDocId}`);
            process.exit(1);
        }
        scanned = 1;
        const data = docSnap.data() ?? {};
        const skip = partnerBackfillSkipReason(data);
        if (skip) {
            console.log(`[skip] ${singleDocId}: ${skip}`);
            console.log(`Dry-run complete. scanned=${scanned} would_update=0`);
            return;
        }
        if (dryRun) {
            console.log(`[dry-run] would update ${singleDocId}`);
            if (verbose) {
                const top = data.partnerCode;
                const ocrP =
                    data.ocrData && typeof data.ocrData === "object" && data.ocrData !== null
                        ? /** @type {Record<string, unknown>} */ (data.ocrData).partnerCode
                        : undefined;
                console.log(
                    `       top partnerCode=${JSON.stringify(top ?? null)} ocrData.partnerCode=${JSON.stringify(ocrP ?? null)}`,
                );
            }
            console.log(`Dry-run complete. scanned=${scanned} would_update=1`);
            return;
        }
        await docRef.update({
            partnerCode: "JWT",
            updatedAt: FieldValue.serverTimestamp(),
        });
        console.log(`Updated trip_records/${singleDocId} (partnerCode=JWT, updatedAt=server)`);
        console.log(`Done. scanned=${scanned} updated=1`);
        return;
    }

    // eslint-disable-next-line no-constant-condition
    outer: while (true) {
        let q = col.orderBy(FieldPath.documentId()).limit(500);
        if (lastDoc) q = q.startAfter(lastDoc);
        const snap = await q.get();
        if (snap.empty) break;

        let batch = db.batch();
        let batchCount = 0;

        for (const doc of snap.docs) {
            scanned++;
            const data = doc.data();
            const skipReason = partnerBackfillSkipReason(data);
            if (skipReason) {
                if (verbose) {
                    console.log(`[skip] ${doc.id}: ${skipReason}`);
                }
                lastDoc = doc;
                continue;
            }

            if (dryRun) {
                console.log(`[dry-run] would update ${doc.id}`);
                if (verbose) {
                    const top = data.partnerCode;
                    const ocrP =
                        data.ocrData && typeof data.ocrData === "object" && data.ocrData !== null
                            ? /** @type {Record<string, unknown>} */ (data.ocrData).partnerCode
                            : undefined;
                    console.log(
                        `       top partnerCode=${JSON.stringify(top ?? null)} ocrData.partnerCode=${JSON.stringify(ocrP ?? null)}`,
                    );
                }
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

            if (updateLimit != null && !Number.isNaN(updateLimit) && updated >= updateLimit) {
                stoppedByLimit = true;
                if (!dryRun && batchCount > 0) {
                    await batch.commit();
                }
                break outer;
            }
        }

        if (!dryRun && batchCount > 0) {
            await batch.commit();
        }

        if (snap.size < 500) break;
    }

    if (stoppedByLimit && updateLimit != null) {
        console.log(`Stopped at --limit=${updateLimit}.`);
    }

    console.log(
        dryRun ? `Dry-run complete. scanned=${scanned} would_update=${updated}` : `Done. scanned=${scanned} updated=${updated}`,
    );
    if (dryRun && scanned > 0 && updated === 0 && !ignoreOcrPartner) {
        console.log(
            "Tip: If docs have ocrData.partnerCode but no top-level partnerCode, re-run with --ignore-ocr-partner.",
        );
    }
    if (!dryRun && scanned > 0 && updated === 0 && !singleDocId) {
        console.log("Tip: no writes matched. Try --verbose, or --doc-id=<Firestore document id> for one trip.");
    }
}

main().catch((e) => {
    console.error(e);
    process.exit(1);
});
