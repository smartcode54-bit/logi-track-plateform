"use strict";
/**
 * Customer/partner LINE group notifications (Wanpenradchada Official Account).
 *
 * App-invoked callable (no Firestore triggers in this project — DB region asia-southeast3). Mobile
 * fires this best-effort after a driver checks in and after a job completes; the callable resolves
 * the destination LINE group (configured per customer/partner as `lineGroupId`), the driver's Thai
 * name (joined from the drivers collection), builds the message from the shared pattern, and pushes
 * it via the LINE Messaging API.
 *
 * The channel access token is a SECRET (LINE_CHANNEL_ACCESS_TOKEN) — never Firestore, never client.
 * Set it with: firebase functions:secrets:set LINE_CHANNEL_ACCESS_TOKEN
 */
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
exports.sendCustomerLineNotification = void 0;
const admin = __importStar(require("firebase-admin"));
const firebase_functions_1 = require("firebase-functions");
const https_1 = require("firebase-functions/v2/https");
const params_1 = require("firebase-functions/params");
const crypto_1 = require("crypto");
const lineMessage_1 = require("./core/lineMessage");
const COL_TASKS = "tasks";
const COL_TRIP_RECORDS = "trip_records";
const COL_DRIVERS = "drivers";
const COL_CUSTOMERS = "customers";
const COL_SUBCONTRACTORS = "subcontractors";
const COL_TRUCKS = "trucks";
const COL_HUBS = "hubs";
const COL_STANDBY_RECORDS = "standby_records";
const COL_INCIDENT_REPORTS = "incidentReport";
const LINE_PUSH_URL = "https://api.line.me/v2/bot/message/push";
/** HTTP function that serves the read-only evidence gallery (see evidenceUrlForToken). */
const EVIDENCE_FN = "tripEvidence";
const lineChannelAccessToken = (0, params_1.defineSecret)("LINE_CHANNEL_ACCESS_TOKEN");
function str(v) {
    return typeof v === "string" ? v.trim() : "";
}
/** Build code→display-name (source_id → source_name_th) so origin/destination read as place names. */
async function buildHubCodeToName(db) {
    const snap = await db.collection(COL_HUBS).get();
    const codeToName = new Map();
    snap.docs.forEach((d) => {
        const data = d.data();
        const code = str(data.source_id) || str(data.hubId);
        if (!code)
            return;
        const name = str(data.source_name_th) || str(data.source_name_en) || str(data.hubName);
        if (name && !codeToName.has(code))
            codeToName.set(code, name);
    });
    return codeToName;
}
function hubLabel(raw, codeToName) {
    const v = str(raw);
    if (!v)
        return "-";
    return codeToName.get(v) ?? v;
}
/** Which entity (customer or partner) owns the destination LINE group — billing source precedence. */
function resolveLinkedEntity(task) {
    const srcId = str(task.sourceHubLinkedCustomerId);
    if (srcId) {
        return { id: srcId, kind: task.sourceHubCustomerLinkKind === "partner" ? "partner" : "customer" };
    }
    const dstId = str(task.destinationLinkedCustomerId);
    if (dstId) {
        return { id: dstId, kind: task.destinationCustomerLinkKind === "partner" ? "partner" : "customer" };
    }
    return null;
}
async function readEntityDoc(db, col, id) {
    if (!id)
        return null;
    try {
        const snap = await db.collection(col).doc(id).get();
        return snap.exists ? snap.data() : null;
    }
    catch {
        return null;
    }
}
/**
 * Resolve the destination LINE group + the customer's code for a trip/task. Prefer the task's linked
 * entity (probe both collections, preferring the link kind's — robust whether a "partner" link id
 * points at subcontractors or a customers row); when there is no task, fall back to the trip's
 * billingCustomerId. Returns empty strings when nothing is configured.
 */
async function resolveLineTarget(db, task, trip) {
    const fromDoc = (doc) => doc
        ? { lineGroupId: str(doc.lineGroupId), customerCode: str(doc.code) }
        : { lineGroupId: "", customerCode: "" };
    if (task) {
        const entity = resolveLinkedEntity(task);
        if (entity) {
            const primary = entity.kind === "partner" ? COL_SUBCONTRACTORS : COL_CUSTOMERS;
            const secondary = entity.kind === "partner" ? COL_CUSTOMERS : COL_SUBCONTRACTORS;
            const doc = (await readEntityDoc(db, primary, entity.id)) ?? (await readEntityDoc(db, secondary, entity.id));
            if (doc)
                return fromDoc(doc);
        }
    }
    const billingId = str(trip?.billingCustomerId);
    if (billingId) {
        const doc = (await readEntityDoc(db, COL_CUSTOMERS, billingId)) ?? (await readEntityDoc(db, COL_SUBCONTRACTORS, billingId));
        if (doc)
            return fromDoc(doc);
    }
    return { lineGroupId: "", customerCode: "" };
}
async function loadDriverByDocId(db, docId) {
    if (!docId)
        return null;
    try {
        const snap = await db.collection(COL_DRIVERS).doc(docId).get();
        return snap.exists ? snap.data() : null;
    }
    catch {
        return null;
    }
}
async function loadDriverByAuthId(db, authId) {
    if (!authId)
        return null;
    try {
        const snap = await db.collection(COL_DRIVERS).where("authId", "==", authId).limit(1).get();
        return snap.empty ? null : snap.docs[0].data();
    }
    catch {
        return null;
    }
}
/** Truck type display: the trucks/{id}.type, falling back to the denormalized snapshot on the trip/task. */
async function loadTruckType(db, truckId, fallback) {
    const id = str(truckId);
    if (id) {
        try {
            const snap = await db.collection(COL_TRUCKS).doc(id).get();
            const t = str(snap.data()?.type);
            if (t)
                return t;
        }
        catch {
            /* ignore — use fallback */
        }
    }
    return str(fallback) || "-";
}
function plateFromDriver(driver) {
    const active = driver?.activeTruck;
    const current = driver?.currentAssignment;
    return str(active?.truckPlate) || str(current?.truckPlate);
}
/** Count evidence photos on a trip (main photos + per-stop photos). */
function countTripPhotos(trip) {
    let n = Array.isArray(trip.photos) ? trip.photos.length : 0;
    const stops = trip.deliveryStopsProgress;
    if (Array.isArray(stops)) {
        for (const s of stops) {
            const ph = s?.photos;
            if (Array.isArray(ph))
                n += ph.length;
        }
    }
    return n;
}
/** Count incident photos (map + situation1 + situation2) across a trip's incident reports. */
function countIncidentPhotos(incidents) {
    let n = 0;
    for (const r of incidents) {
        for (const k of ["mapPhotoUrl", "situation1PhotoUrl", "situation2PhotoUrl"]) {
            if (str(r[k]))
                n += 1;
        }
    }
    return n;
}
/** Standby group: from standby.customerId only (ADR 0008 — the record is self-contained). Probe both. */
async function resolveStandbyTarget(db, standby) {
    const cid = str(standby.customerId);
    if (!cid)
        return { lineGroupId: "", customerCode: "" };
    const doc = (await readEntityDoc(db, COL_CUSTOMERS, cid)) ?? (await readEntityDoc(db, COL_SUBCONTRACTORS, cid));
    return doc ? { lineGroupId: str(doc.lineGroupId), customerCode: str(doc.code) } : { lineGroupId: "", customerCode: "" };
}
/** Public URL of the read-only evidence gallery for a token (project-aware: dev vs prod). */
function evidenceUrlForToken(evidenceToken) {
    const project = process.env.GCLOUD_PROJECT || process.env.GCP_PROJECT || "";
    return `https://asia-southeast1-${project}.cloudfunctions.net/${EVIDENCE_FN}?k=${evidenceToken}`;
}
async function pushLineMessage(token, to, message) {
    const res = await fetch(LINE_PUSH_URL, {
        method: "POST",
        headers: {
            "Content-Type": "application/json",
            Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify({ to, messages: [message] }),
    });
    if (!res.ok) {
        const body = await res.text().catch(() => "");
        throw new Error(`LINE push HTTP ${res.status}: ${body.slice(0, 300)}`);
    }
}
exports.sendCustomerLineNotification = (0, https_1.onCall)({
    region: "asia-southeast1",
    enforceAppCheck: false, // Auth-checked below; web/admin re-triggers may lack an App Check token.
    secrets: [lineChannelAccessToken],
}, async (request) => {
    if (!request.auth) {
        throw new https_1.HttpsError("unauthenticated", "Must be authenticated");
    }
    const event = request.data?.event;
    if (event !== "checkin" && event !== "delivered" && event !== "standby") {
        throw new https_1.HttpsError("invalid-argument", "event must be 'checkin', 'delivered' or 'standby'");
    }
    const force = request.data?.force === true;
    const token = lineChannelAccessToken.value();
    if (!token) {
        // Feature ships dark until the OA token is configured — don't error the caller.
        firebase_functions_1.logger.warn("[lineNotify] LINE_CHANNEL_ACCESS_TOKEN is not set — skipping send", { event });
        return { ok: true, skipped: true, reason: "no channel access token" };
    }
    const db = admin.firestore();
    if (event === "checkin") {
        const taskId = str(request.data?.taskId);
        if (!taskId)
            throw new https_1.HttpsError("invalid-argument", "taskId is required for checkin");
        const taskSnap = await db.collection(COL_TASKS).doc(taskId).get();
        if (!taskSnap.exists)
            throw new https_1.HttpsError("not-found", "Task not found");
        const task = taskSnap.data();
        if (!force && task.lineCheckinNotifiedAt) {
            return { ok: true, skipped: true, reason: "already notified" };
        }
        const [target, codeToName, driver] = await Promise.all([
            resolveLineTarget(db, task, null),
            buildHubCodeToName(db),
            loadDriverByDocId(db, str(task.driverId)),
        ]);
        if (!target.lineGroupId)
            return { ok: true, skipped: true, reason: "no lineGroupId configured" };
        const truckType = await loadTruckType(db, task.truckId, task.truckType);
        const ctx = {
            dateLine: (0, lineMessage_1.formatBuddhistShortDate)(task.checkInAt ?? task.date),
            originLabel: hubLabel(task.sourceHub, codeToName),
            destinationLabel: hubLabel(task.destination, codeToName),
            tripNo: str(task.taskId) || taskId,
            driverNameTh: (0, lineMessage_1.resolveDriverNameTh)(driver, str(task.driverName)),
            driverCode: (0, lineMessage_1.resolveDriverCustomerCode)(driver, target.customerCode),
            plate: str(task.licensePlate) || plateFromDriver(driver),
            phone: str(driver?.mobile) || str(task.driverPhone),
            partner: str(task.sourceHubLinkedCustomerCode) || str(task.destinationLinkedCustomerCode),
            truckType,
            checkInHm: (0, lineMessage_1.formatBangkokHm)(task.checkInAt),
        };
        try {
            await pushLineMessage(token, target.lineGroupId, (0, lineMessage_1.buildCheckinMessage)(ctx));
        }
        catch (e) {
            firebase_functions_1.logger.error("[lineNotify] checkin push failed", { taskId, error: String(e) });
            throw new https_1.HttpsError("internal", "LINE push failed");
        }
        await taskSnap.ref.update({ lineCheckinNotifiedAt: admin.firestore.FieldValue.serverTimestamp() });
        return { ok: true };
    }
    if (event === "standby") {
        const standbyId = str(request.data?.standbyId);
        if (!standbyId)
            throw new https_1.HttpsError("invalid-argument", "standbyId is required for standby");
        const standbySnap = await db.collection(COL_STANDBY_RECORDS).doc(standbyId).get();
        if (!standbySnap.exists)
            throw new https_1.HttpsError("not-found", "Standby record not found");
        const standby = standbySnap.data();
        if (!force && standby.lineNotifiedAt) {
            return { ok: true, skipped: true, reason: "already notified" };
        }
        const [target, codeToName, driver] = await Promise.all([
            resolveStandbyTarget(db, standby),
            buildHubCodeToName(db),
            loadDriverByAuthId(db, str(standby.driverId)),
        ]);
        if (!target.lineGroupId)
            return { ok: true, skipped: true, reason: "no lineGroupId configured" };
        const photoCount = Array.isArray(standby.photos) ? standby.photos.length : 0;
        let evidenceToken = str(standby.evidenceToken);
        let newEvidenceToken = false;
        if (photoCount > 0 && !evidenceToken) {
            evidenceToken = (0, crypto_1.randomBytes)(12).toString("base64url");
            newEvidenceToken = true;
        }
        const evidenceUrl = photoCount > 0 && evidenceToken ? evidenceUrlForToken(evidenceToken) : undefined;
        const durationMin = typeof standby.durationMinutes === "number" ? standby.durationMinutes : undefined;
        const ctx = {
            dateLine: (0, lineMessage_1.formatBuddhistShortDate)(standby.endedAt ?? standby.startedAt ?? standby.createdAt),
            startLabel: hubLabel(standby.startLocation, codeToName),
            endLabel: hubLabel(standby.endLocation, codeToName),
            driverNameTh: (0, lineMessage_1.resolveDriverNameTh)(driver, str(standby.driverId)),
            driverCode: (0, lineMessage_1.resolveDriverCustomerCode)(driver, target.customerCode),
            plate: str(standby.truckLicensePlate) || plateFromDriver(driver),
            phone: str(driver?.mobile),
            partner: target.customerCode,
            startedHm: (0, lineMessage_1.formatBangkokHm)(standby.startedAt),
            endedHm: (0, lineMessage_1.formatBangkokHm)(standby.endedAt),
            durationText: durationMin != null ? `${durationMin} นาที` : "-",
            notes: str(standby.note) || undefined,
            evidenceUrl,
            photoCount: photoCount > 0 ? photoCount : undefined,
        };
        try {
            await pushLineMessage(token, target.lineGroupId, (0, lineMessage_1.buildStandbyMessage)(ctx));
        }
        catch (e) {
            firebase_functions_1.logger.error("[lineNotify] standby push failed", { standbyId, error: String(e) });
            throw new https_1.HttpsError("internal", "LINE push failed");
        }
        const standbyUpdates = {
            lineNotifiedAt: admin.firestore.FieldValue.serverTimestamp(),
        };
        if (newEvidenceToken)
            standbyUpdates.evidenceToken = evidenceToken;
        await standbySnap.ref.update(standbyUpdates);
        return { ok: true };
    }
    // event === "delivered"
    const tripId = str(request.data?.tripId);
    if (!tripId)
        throw new https_1.HttpsError("invalid-argument", "tripId is required for delivered");
    const tripSnap = await db.collection(COL_TRIP_RECORDS).doc(tripId).get();
    if (!tripSnap.exists)
        throw new https_1.HttpsError("not-found", "Trip not found");
    const trip = tripSnap.data();
    if (!force && trip.lineDeliveredNotifiedAt) {
        return { ok: true, skipped: true, reason: "already notified" };
    }
    const taskId = str(trip.taskId);
    const [task, codeToName, driver, incidentSnap] = await Promise.all([
        taskId
            ? db.collection(COL_TASKS).doc(taskId).get().then((s) => (s.exists ? s.data() : null))
            : Promise.resolve(null),
        buildHubCodeToName(db),
        loadDriverByAuthId(db, str(trip.driverId)),
        db.collection(COL_INCIDENT_REPORTS).where("tripId", "==", tripId).get(),
    ]);
    const incidents = incidentSnap.docs.map((d) => d.data());
    const target = await resolveLineTarget(db, task, trip);
    if (!target.lineGroupId)
        return { ok: true, skipped: true, reason: "no lineGroupId configured" };
    const truckType = await loadTruckType(db, trip.truckId, trip.truckType);
    const ocr = trip.ocrData;
    // Evidence gallery link (one URL = 1 message): mint an unguessable token on first send.
    // Photo count and note fold in any incident/delay reports on this trip (ADR 0025 §5).
    const photoCount = countTripPhotos(trip) + countIncidentPhotos(incidents);
    let evidenceToken = str(trip.evidenceToken);
    let newEvidenceToken = false;
    if (photoCount > 0 && !evidenceToken) {
        evidenceToken = (0, crypto_1.randomBytes)(12).toString("base64url");
        newEvidenceToken = true;
    }
    const evidenceUrl = photoCount > 0 && evidenceToken ? evidenceUrlForToken(evidenceToken) : undefined;
    const ctx = {
        dateLine: (0, lineMessage_1.formatBuddhistShortDate)(trip.std ?? trip.createdAt),
        originLabel: hubLabel(trip.origin ?? task?.sourceHub, codeToName),
        destinationLabel: hubLabel(trip.destination ?? task?.destination, codeToName),
        tripNo: str(ocr?.tripId) || str(trip.spxTripId) || tripId,
        driverNameTh: (0, lineMessage_1.resolveDriverNameTh)(driver, str(trip.driverId)),
        driverCode: (0, lineMessage_1.resolveDriverCustomerCode)(driver, target.customerCode),
        plate: str(trip.truckLicensePlate) || plateFromDriver(driver),
        phone: str(driver?.mobile) || str(task?.driverPhone),
        partner: str(trip.partnerCode) || str(task?.sourceHubLinkedCustomerCode),
        truckType,
        parcels: typeof trip.parcelCount === "number" ? String(trip.parcelCount) : undefined,
        checkInHm: (0, lineMessage_1.formatBangkokHm)(task?.checkInAt),
        departHm: (0, lineMessage_1.formatBangkokHm)(trip.std),
        arriveHm: (0, lineMessage_1.formatBangkokHm)(trip.ata),
        doneHm: (0, lineMessage_1.formatBangkokHm)(trip.deliveredTimestamp),
        notes: incidents.length > 0
            ? (0, lineMessage_1.buildDelayNote)(incidents.map((i) => i.delayCause))
            : undefined,
        evidenceUrl,
        photoCount: photoCount > 0 ? photoCount : undefined,
    };
    try {
        await pushLineMessage(token, target.lineGroupId, (0, lineMessage_1.buildDeliveredMessage)(ctx));
    }
    catch (e) {
        firebase_functions_1.logger.error("[lineNotify] delivered push failed", { tripId, error: String(e) });
        throw new https_1.HttpsError("internal", "LINE push failed");
    }
    const deliveredUpdates = {
        lineDeliveredNotifiedAt: admin.firestore.FieldValue.serverTimestamp(),
    };
    if (newEvidenceToken)
        deliveredUpdates.evidenceToken = evidenceToken;
    await tripSnap.ref.update(deliveredUpdates);
    return { ok: true };
});
//# sourceMappingURL=lineNotify.js.map