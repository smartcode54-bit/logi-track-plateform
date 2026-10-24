"use strict";
var __createBinding = (this && this.__createBinding) || (Object.create ? (function(o, m, k, k2) {
    if (k2 === undefined) k2 = k;
    var desc = Object.getOwnPropertyDescriptor(m, k);
    if (!desc || ("get" in desc ? !m.__esModule : desc.writable || desc.configurable)) {
      desc = { enumerable: true, get: function() { return m[k]; } };
    }
    Object.defineProperty(o, k2, desc);
}) : (function(o, m, k, k2) {
    if (k2 === undefined) k2 = k;
    o[k2] = m[k];
}));
var __setModuleDefault = (this && this.__setModuleDefault) || (Object.create ? (function(o, v) {
    Object.defineProperty(o, "default", { enumerable: true, value: v });
}) : function(o, v) {
    o["default"] = v;
});
var __importStar = (this && this.__importStar) || (function () {
    var ownKeys = function(o) {
        ownKeys = Object.getOwnPropertyNames || function (o) {
            var ar = [];
            for (var k in o) if (Object.prototype.hasOwnProperty.call(o, k)) ar[ar.length] = k;
            return ar;
        };
        return ownKeys(o);
    };
    return function (mod) {
        if (mod && mod.__esModule) return mod;
        var result = {};
        if (mod != null) for (var k = ownKeys(mod), i = 0; i < k.length; i++) if (k[i] !== "default") __createBinding(result, mod, k[i]);
        __setModuleDefault(result, mod);
        return result;
    };
})();
Object.defineProperty(exports, "__esModule", { value: true });
exports.backfillDriverAuthHistory = void 0;
const admin = __importStar(require("firebase-admin"));
const firebase_functions_1 = require("firebase-functions");
const https_1 = require("firebase-functions/v2/https");
const COL_DRIVERS = "drivers";
const COL_TRIPS = "trip_records";
const COL_TASKS = "tasks";
/** Thai-first display name, mirrors lib/driverName.ts `driverDisplayName`. */
function resolveName(d, fallback) {
    if (!d)
        return fallback;
    const th = String(d.fullNameTh ?? "").trim();
    if (th)
        return th;
    const n = String(d.name ?? "").trim();
    if (n)
        return n;
    const c = [d.firstName, d.lastName].filter(Boolean).join(" ").trim();
    if (c)
        return c;
    const e = String(d.email ?? "").trim();
    if (e)
        return e;
    return fallback;
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
exports.backfillDriverAuthHistory = (0, https_1.onCall)({ region: "asia-southeast1", enforceAppCheck: false, timeoutSeconds: 540 }, async (request) => {
    if (!request.auth) {
        throw new https_1.HttpsError("unauthenticated", "User must be logged in");
    }
    const db = admin.firestore();
    const user = await admin.auth().getUser(request.auth.uid);
    const claims = user.customClaims || {};
    if (!claims.admin) {
        throw new https_1.HttpsError("permission-denied", "Only admins can repair driver links");
    }
    const dryRun = request.data?.dryRun === true;
    // 1. Current driver identity: every uid already resolvable, every doc id.
    const driversSnap = await db.collection(COL_DRIVERS).get();
    const knownUids = new Set(); // authId + authUid + authIdHistory
    const driverDocIds = new Set();
    const driverDataById = new Map();
    driversSnap.forEach((d) => {
        driverDocIds.add(d.id);
        const data = d.data();
        driverDataById.set(d.id, data);
        for (const v of [data.authId, data.authUid]) {
            const s = String(v ?? "").trim();
            if (s)
                knownUids.add(s);
        }
        const hist = data.authIdHistory;
        if (Array.isArray(hist)) {
            hist.forEach((h) => {
                const s = String(h ?? "").trim();
                if (s)
                    knownUids.add(s);
            });
        }
    });
    // 2. Scan trips → orphan driverIds (+ one taskId each to infer the owner).
    const tripsSnap = await db.collection(COL_TRIPS).get();
    const orphans = new Map();
    tripsSnap.forEach((t) => {
        const data = t.data();
        const did = String(data.driverId ?? "").trim();
        if (!did)
            return;
        if (knownUids.has(did) || driverDocIds.has(did))
            return; // already resolves
        const entry = orphans.get(did) ?? { count: 0 };
        entry.count += 1;
        if (!entry.taskId) {
            const tid = String(data.taskId ?? "").trim();
            if (tid)
                entry.taskId = tid;
        }
        orphans.set(did, entry);
    });
    // 3. Infer the owning driver for each orphan via its task's driverId (doc id).
    const proposals = [];
    const unresolved = [];
    for (const [uid, info] of orphans) {
        if (!info.taskId) {
            unresolved.push({ orphanUid: uid, trips: info.count, reason: "no taskId on its trips" });
            continue;
        }
        let taskData;
        try {
            taskData = (await db.collection(COL_TASKS).doc(info.taskId).get()).data();
        }
        catch {
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
        const byDriver = new Map();
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
        if (n > 0)
            await batch.commit();
    }
    firebase_functions_1.logger.info("[backfillDriverAuthHistory] done", {
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
});
//# sourceMappingURL=backfillDriverAuthHistory.js.map