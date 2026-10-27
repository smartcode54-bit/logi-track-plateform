import * as admin from "firebase-admin";
import { logger } from "firebase-functions";
import { onCall, HttpsError } from "firebase-functions/v2/https";

const COL_DRIVERS = "drivers";
const COL_TRIPS = "trip_records";
const COL_TASKS = "tasks";

/** Thai-first display name, mirrors lib/driverName.ts `driverDisplayName`. */
function resolveName(d: FirebaseFirestore.DocumentData | undefined, fallback: string): string {
    if (!d) return fallback;
    const th = String(d.fullNameTh ?? "").trim();
    if (th) return th;
    const n = String(d.name ?? "").trim();
    if (n) return n;
    const c = [d.firstName, d.lastName].filter(Boolean).join(" ").trim();
    if (c) return c;
    const e = String(d.email ?? "").trim();
    if (e) return e;
    return fallback;
}

interface Proposal {
    orphanUid: string;
    driverDocId: string;
    driverName: string;
    trips: number;
}
interface Unresolved {
    orphanUid: string;
    trips: number;
    reason: string;
}

/**
 * Heal driver-name resolution after an email / Google re-link changed a
 * driver's Auth UID.
 *
 * trip_records.driverId stores the Auth UID that was current when the trip was
 * written. A re-link moves the driver to a NEW uid, so old trips carry an
 * "orphan" uid that matches no current driver (authId / doc id / authIdHistory)
 * and the report shows a raw id (e.g. `W47P8pGs…`).
 *
 * This scans trip_records for orphan driverIds, joins each orphan trip to its
 * task (`tasks.driverId` is the driver DOC id the admin picked when assigning),
 * and appends the orphan uid to that driver's `authIdHistory` (arrayUnion).
 * Name resolution then finds the driver again — for every past trip of that
 * uid, across every report and invoice.
 *
 * `dryRun: true` scans and returns the proposed mappings without writing.
 * Idempotent (arrayUnion). Admin only.
 */
export const backfillDriverAuthHistory = onCall(
    { region: "asia-southeast1", enforceAppCheck: false, timeoutSeconds: 540 },
    async (request) => {
        if (!request.auth) {
            throw new HttpsError("unauthenticated", "User must be logged in");
        }
        const db = admin.firestore();
        const user = await admin.auth().getUser(request.auth.uid);
        const claims = (user.customClaims as { admin?: boolean } | null) || {};
        if (!claims.admin) {
            throw new HttpsError("permission-denied", "Only admins can repair driver links");
        }

        const dryRun = request.data?.dryRun === true;

        // 1. Current driver identity: every uid already resolvable, every doc id.
        const driversSnap = await db.collection(COL_DRIVERS).get();
        const knownUids = new Set<string>(); // authId + authUid + authIdHistory
        const driverDocIds = new Set<string>();
        const driverDataById = new Map<string, FirebaseFirestore.DocumentData>();
        driversSnap.forEach((d) => {
            driverDocIds.add(d.id);
            const data = d.data();
            driverDataById.set(d.id, data);
            for (const v of [data.authId, data.authUid]) {
                const s = String(v ?? "").trim();
                if (s) knownUids.add(s);
            }
            const hist = data.authIdHistory;
            if (Array.isArray(hist)) {
                hist.forEach((h) => {
                    const s = String(h ?? "").trim();
                    if (s) knownUids.add(s);
                });
            }
        });

        // 2. Scan trips → orphan driverIds (+ one taskId each to infer the owner).
        const tripsSnap = await db.collection(COL_TRIPS).get();
        const orphans = new Map<string, { count: number; taskId?: string }>();
        tripsSnap.forEach((t) => {
            const data = t.data();
            const did = String(data.driverId ?? "").trim();
            if (!did) return;
            if (knownUids.has(did) || driverDocIds.has(did)) return; // already resolves
            const entry = orphans.get(did) ?? { count: 0 };
            entry.count += 1;
            if (!entry.taskId) {
                const tid = String(data.taskId ?? "").trim();
                if (tid) entry.taskId = tid;
            }
            orphans.set(did, entry);
        });

        // 3. Infer the owning driver for each orphan via its task's driverId (doc id).
        const proposals: Proposal[] = [];
        const unresolved: Unresolved[] = [];
        for (const [uid, info] of orphans) {
            if (!info.taskId) {
                unresolved.push({ orphanUid: uid, trips: info.count, reason: "no taskId on its trips" });
                continue;
            }
            let taskData: FirebaseFirestore.DocumentData | undefined;
            try {
                taskData = (await db.collection(COL_TASKS).doc(info.taskId).get()).data();
            } catch {
                // ignore; treated as unresolved below
            }
            const ownerDocId = String(taskData?.driverId ?? "").trim();
            if (!ownerDocId || !driverDocIds.has(ownerDocId)) {
                unresolved.push({
                    orphanUid: uid,
                    trips: info.count,
                    reason: ownerDocId ? "task.driverId is not a known driver doc" : "task has no driverId (doc id)",
                });
                continue;
            }
            proposals.push({
                orphanUid: uid,
                driverDocId: ownerDocId,
                driverName: resolveName(driverDataById.get(ownerDocId), ownerDocId),
                trips: info.count,
            });
        }

        // 4. Apply: arrayUnion each orphan uid into its owner's authIdHistory.
        let applied = 0;
        if (!dryRun && proposals.length > 0) {
            const byDriver = new Map<string, string[]>();
            for (const p of proposals) {
                const arr = byDriver.get(p.driverDocId) ?? [];
                arr.push(p.orphanUid);
                byDriver.set(p.driverDocId, arr);
            }
            let batch = db.batch();
            let n = 0;
            for (const [docId, uids] of byDriver) {
                batch.update(db.collection(COL_DRIVERS).doc(docId), {
                    authIdHistory: admin.firestore.FieldValue.arrayUnion(...uids),
                    updatedAt: admin.firestore.FieldValue.serverTimestamp(),
                });
                applied += uids.length;
                n++;
                if (n >= 400) {
                    await batch.commit();
                    batch = db.batch();
                    n = 0;
                }
            }
            if (n > 0) await batch.commit();
        }

        logger.info("[backfillDriverAuthHistory] done", {
            dryRun,
            orphanUids: orphans.size,
            proposals: proposals.length,
            unresolved: unresolved.length,
            applied,
        });

        return {
            success: true,
            dryRun,
            scannedTrips: tripsSnap.size,
            orphanUids: orphans.size,
            proposals,
            unresolved,
            applied,
        };
    }
);
