/**
 * One-time: backfill `jobCategory = "PRIMARY"` on every `trip_records` document that
 * does not already have a jobCategory (ADR-0005). jobCategory is normally DERIVED at
 * billing time, but already-billed legacy trips are skipped by the idempotency guard
 * and never get a value — all of them predate the supplementary feature, so they are
 * PRIMARY. This fills that gap. Idempotent.
 *
 * Dev dry-run — needs a service account that can read/write Firestore (a normal user
 * login is not enough for the Admin SDK):
 *   (PowerShell)
 *   $env:GOOGLE_APPLICATION_CREDENTIALS="D:\real\path\dev-sa.json"
 *   $env:FIREBASE_PROJECT_ID="logi-track-wrt-dev"
 *   node scripts/backfill-job-category.mjs --dry-run
 *
 * Apply (after dry-run looks right):
 *   node scripts/backfill-job-category.mjs
 *
 * Options:
 *   --dry-run            no writes, just report counts
 *   --verbose            log each doc id touched
 *   --limit=N            stop after N updates
 */

import { initializeApp, cert, applicationDefault } from "firebase-admin/app";
import { getFirestore, FieldValue, FieldPath } from "firebase-admin/firestore";
import { existsSync, readFileSync } from "fs";

const dryRun = process.argv.includes("--dry-run");
const verbose = process.argv.includes("--verbose");
const limitArg = process.argv.find((a) => a.startsWith("--limit="));
const updateLimit = limitArg ? Number.parseInt(limitArg.split("=")[1], 10) : null;

const COLLECTIONS = ["trip_records"];
const PAGE = 400;

function initAdmin() {
    const keyPath = (process.env.GOOGLE_APPLICATION_CREDENTIALS ?? "").trim().replace(/^["']|["']$/g, "");
    let projectId = process.env.FIREBASE_PROJECT_ID || process.env.GCLOUD_PROJECT;
    if (keyPath) {
        if (keyPath === "..." || !existsSync(keyPath)) {
            console.error(`GOOGLE_APPLICATION_CREDENTIALS does not point to a real SA json file: ${keyPath}`);
            process.exit(1);
        }
        const serviceAccount = JSON.parse(readFileSync(keyPath, "utf8"));
        projectId = projectId || serviceAccount.project_id;
        initializeApp({ credential: cert(serviceAccount), projectId });
    } else {
        if (!projectId) {
            console.error("Missing project: set FIREBASE_PROJECT_ID or GOOGLE_APPLICATION_CREDENTIALS.");
            process.exit(1);
        }
        initializeApp({ credential: applicationDefault(), projectId });
    }
    console.log(`Firestore project: ${projectId}${dryRun ? " (dry-run — no writes)" : " (APPLY — will write)"}`);
}

/** @param {import("firebase-admin/firestore").Firestore} db @param {string} collection */
async function backfillCollection(db, collection) {
    let scanned = 0;
    let updated = 0;
    let lastDoc = null;

    while (true) {
        if (updateLimit != null && updated >= updateLimit) break;
        let q = db.collection(collection).orderBy(FieldPath.documentId()).limit(PAGE);
        if (lastDoc) q = q.startAfter(lastDoc);
        const snap = await q.get();
        if (snap.empty) break;

        for (const doc of snap.docs) {
            scanned++;
            lastDoc = doc;
            const data = doc.data() ?? {};
            // Already set (PRIMARY or SUPPLEMENTARY) → leave untouched.
            if (data.jobCategory === "PRIMARY" || data.jobCategory === "SUPPLEMENTARY") continue;

            if (dryRun) {
                if (verbose) console.log(`[dry-run] would set ${collection}/${doc.id}.jobCategory = PRIMARY`);
                updated++;
            } else {
                await doc.ref.update({ jobCategory: "PRIMARY", updatedAt: FieldValue.serverTimestamp() });
                updated++;
                if (verbose) console.log(`[updated] ${collection}/${doc.id}`);
            }
            if (updateLimit != null && updated >= updateLimit) break;
        }
        if (snap.size < PAGE) break;
    }

    console.log(
        dryRun
            ? `[${collection}] dry-run: scanned=${scanned} would_update=${updated}`
            : `[${collection}] done: scanned=${scanned} updated=${updated}`,
    );
}

async function main() {
    initAdmin();
    const db = getFirestore();
    for (const c of COLLECTIONS) {
        await backfillCollection(db, c);
    }
}

main().catch((e) => {
    console.error(e);
    process.exit(1);
});
