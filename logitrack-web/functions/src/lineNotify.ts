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

import * as admin from "firebase-admin";
import { logger } from "firebase-functions";
import { onCall, HttpsError } from "firebase-functions/v2/https";
import { defineSecret } from "firebase-functions/params";
import {
    buildCheckinMessage,
    buildDeliveredMessage,
    formatBangkokHm,
    formatBuddhistShortDate,
    resolveDriverNameTh,
    resolveDriverCustomerCode,
    type LineTripContext,
} from "./core/lineMessage";

const COL_TASKS = "tasks";
const COL_TRIP_RECORDS = "trip_records";
const COL_DRIVERS = "drivers";
const COL_CUSTOMERS = "customers";
const COL_SUBCONTRACTORS = "subcontractors";
const COL_TRUCKS = "trucks";
const COL_HUBS = "hubs";

const LINE_PUSH_URL = "https://api.line.me/v2/bot/message/push";

const lineChannelAccessToken = defineSecret("LINE_CHANNEL_ACCESS_TOKEN");

type LineNotifyEvent = "checkin" | "delivered";

interface SendCustomerLineNotificationRequest {
    event: LineNotifyEvent;
    taskId?: string;
    tripId?: string;
    /** Re-send even if the idempotency flag is already set (admin/manual re-trigger). */
    force?: boolean;
}

interface SendCustomerLineNotificationResponse {
    ok: boolean;
    skipped?: boolean;
    reason?: string;
}

type Doc = Record<string, unknown>;

function str(v: unknown): string {
    return typeof v === "string" ? v.trim() : "";
}

/** Build code→display-name (source_id → source_name_th) so origin/destination read as place names. */
async function buildHubCodeToName(db: admin.firestore.Firestore): Promise<Map<string, string>> {
    const snap = await db.collection(COL_HUBS).get();
    const codeToName = new Map<string, string>();
    snap.docs.forEach((d) => {
        const data = d.data();
        const code = str(data.source_id) || str(data.hubId);
        if (!code) return;
        const name = str(data.source_name_th) || str(data.source_name_en) || str(data.hubName);
        if (name && !codeToName.has(code)) codeToName.set(code, name);
    });
    return codeToName;
}

function hubLabel(raw: unknown, codeToName: Map<string, string>): string {
    const v = str(raw);
    if (!v) return "-";
    return codeToName.get(v) ?? v;
}

/** Which entity (customer or partner) owns the destination LINE group — billing source precedence. */
function resolveLinkedEntity(task: Doc): { id: string; kind: "customer" | "partner" } | null {
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

interface LineTarget {
    lineGroupId: string;
    /** The customer's `code` (the customerDriverIds key, e.g. "SPX"); "" for a partner without one. */
    customerCode: string;
}

async function readEntityDoc(db: admin.firestore.Firestore, col: string, id: string): Promise<Doc | null> {
    if (!id) return null;
    try {
        const snap = await db.collection(col).doc(id).get();
        return snap.exists ? (snap.data() as Doc) : null;
    } catch {
        return null;
    }
}

/**
 * Resolve the destination LINE group + the customer's code for a trip/task. Prefer the task's linked
 * entity (probe both collections, preferring the link kind's — robust whether a "partner" link id
 * points at subcontractors or a customers row); when there is no task, fall back to the trip's
 * billingCustomerId. Returns empty strings when nothing is configured.
 */
async function resolveLineTarget(
    db: admin.firestore.Firestore,
    task: Doc | null,
    trip: Doc | null
): Promise<LineTarget> {
    const fromDoc = (doc: Doc | null): LineTarget =>
        doc
            ? { lineGroupId: str(doc.lineGroupId), customerCode: str(doc.code) }
            : { lineGroupId: "", customerCode: "" };

    if (task) {
        const entity = resolveLinkedEntity(task);
        if (entity) {
            const primary = entity.kind === "partner" ? COL_SUBCONTRACTORS : COL_CUSTOMERS;
            const secondary = entity.kind === "partner" ? COL_CUSTOMERS : COL_SUBCONTRACTORS;
            const doc = (await readEntityDoc(db, primary, entity.id)) ?? (await readEntityDoc(db, secondary, entity.id));
            if (doc) return fromDoc(doc);
        }
    }
    const billingId = str(trip?.billingCustomerId);
    if (billingId) {
        const doc = (await readEntityDoc(db, COL_CUSTOMERS, billingId)) ?? (await readEntityDoc(db, COL_SUBCONTRACTORS, billingId));
        if (doc) return fromDoc(doc);
    }
    return { lineGroupId: "", customerCode: "" };
}

async function loadDriverByDocId(db: admin.firestore.Firestore, docId: string): Promise<Doc | null> {
    if (!docId) return null;
    try {
        const snap = await db.collection(COL_DRIVERS).doc(docId).get();
        return snap.exists ? (snap.data() as Doc) : null;
    } catch {
        return null;
    }
}

async function loadDriverByAuthId(db: admin.firestore.Firestore, authId: string): Promise<Doc | null> {
    if (!authId) return null;
    try {
        const snap = await db.collection(COL_DRIVERS).where("authId", "==", authId).limit(1).get();
        return snap.empty ? null : (snap.docs[0].data() as Doc);
    } catch {
        return null;
    }
}

/** Truck type display: the trucks/{id}.type, falling back to the denormalized snapshot on the trip/task. */
async function loadTruckType(
    db: admin.firestore.Firestore,
    truckId: unknown,
    fallback: unknown
): Promise<string> {
    const id = str(truckId);
    if (id) {
        try {
            const snap = await db.collection(COL_TRUCKS).doc(id).get();
            const t = str(snap.data()?.type);
            if (t) return t;
        } catch {
            /* ignore — use fallback */
        }
    }
    return str(fallback) || "-";
}

function plateFromDriver(driver: Doc | null): string {
    const active = driver?.activeTruck as { truckPlate?: unknown } | undefined;
    const current = driver?.currentAssignment as { truckPlate?: unknown } | undefined;
    return str(active?.truckPlate) || str(current?.truckPlate);
}

async function pushLineMessage(token: string, to: string, text: string): Promise<void> {
    const res = await fetch(LINE_PUSH_URL, {
        method: "POST",
        headers: {
            "Content-Type": "application/json",
            Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify({ to, messages: [{ type: "text", text }] }),
    });
    if (!res.ok) {
        const body = await res.text().catch(() => "");
        throw new Error(`LINE push HTTP ${res.status}: ${body.slice(0, 300)}`);
    }
}

export const sendCustomerLineNotification = onCall<
    SendCustomerLineNotificationRequest,
    Promise<SendCustomerLineNotificationResponse>
>(
    {
        region: "asia-southeast1",
        enforceAppCheck: false, // Auth-checked below; web/admin re-triggers may lack an App Check token.
        secrets: [lineChannelAccessToken],
    },
    async (request): Promise<SendCustomerLineNotificationResponse> => {
        if (!request.auth) {
            throw new HttpsError("unauthenticated", "Must be authenticated");
        }

        const event = request.data?.event;
        if (event !== "checkin" && event !== "delivered") {
            throw new HttpsError("invalid-argument", "event must be 'checkin' or 'delivered'");
        }
        const force = request.data?.force === true;

        const token = lineChannelAccessToken.value();
        if (!token) {
            // Feature ships dark until the OA token is configured — don't error the caller.
            logger.warn("[lineNotify] LINE_CHANNEL_ACCESS_TOKEN is not set — skipping send", { event });
            return { ok: true, skipped: true, reason: "no channel access token" };
        }

        const db = admin.firestore();

        if (event === "checkin") {
            const taskId = str(request.data?.taskId);
            if (!taskId) throw new HttpsError("invalid-argument", "taskId is required for checkin");

            const taskSnap = await db.collection(COL_TASKS).doc(taskId).get();
            if (!taskSnap.exists) throw new HttpsError("not-found", "Task not found");
            const task = taskSnap.data() as Doc;

            if (!force && task.lineCheckinNotifiedAt) {
                return { ok: true, skipped: true, reason: "already notified" };
            }

            const [target, codeToName, driver] = await Promise.all([
                resolveLineTarget(db, task, null),
                buildHubCodeToName(db),
                loadDriverByDocId(db, str(task.driverId)),
            ]);
            if (!target.lineGroupId) return { ok: true, skipped: true, reason: "no lineGroupId configured" };

            const truckType = await loadTruckType(db, task.truckId, task.truckType);
            const ctx: LineTripContext = {
                dateLine: formatBuddhistShortDate(task.checkInAt ?? task.date),
                originLabel: hubLabel(task.sourceHub, codeToName),
                destinationLabel: hubLabel(task.destination, codeToName),
                tripNo: str(task.taskId) || taskId,
                driverNameTh: resolveDriverNameTh(driver, str(task.driverName)),
                driverCode: resolveDriverCustomerCode(driver, target.customerCode),
                plate: str(task.licensePlate) || plateFromDriver(driver),
                phone: str(driver?.mobile) || str(task.driverPhone),
                partner: str(task.sourceHubLinkedCustomerCode) || str(task.destinationLinkedCustomerCode),
                truckType,
                checkInHm: formatBangkokHm(task.checkInAt),
            };

            try {
                await pushLineMessage(token, target.lineGroupId, buildCheckinMessage(ctx));
            } catch (e) {
                logger.error("[lineNotify] checkin push failed", { taskId, error: String(e) });
                throw new HttpsError("internal", "LINE push failed");
            }
            await taskSnap.ref.update({ lineCheckinNotifiedAt: admin.firestore.FieldValue.serverTimestamp() });
            return { ok: true };
        }

        // event === "delivered"
        const tripId = str(request.data?.tripId);
        if (!tripId) throw new HttpsError("invalid-argument", "tripId is required for delivered");

        const tripSnap = await db.collection(COL_TRIP_RECORDS).doc(tripId).get();
        if (!tripSnap.exists) throw new HttpsError("not-found", "Trip not found");
        const trip = tripSnap.data() as Doc;

        if (!force && trip.lineDeliveredNotifiedAt) {
            return { ok: true, skipped: true, reason: "already notified" };
        }

        const taskId = str(trip.taskId);
        const [task, codeToName, driver] = await Promise.all([
            taskId
                ? db.collection(COL_TASKS).doc(taskId).get().then((s) => (s.exists ? (s.data() as Doc) : null))
                : Promise.resolve<Doc | null>(null),
            buildHubCodeToName(db),
            loadDriverByAuthId(db, str(trip.driverId)),
        ]);

        const target = await resolveLineTarget(db, task, trip);
        if (!target.lineGroupId) return { ok: true, skipped: true, reason: "no lineGroupId configured" };

        const truckType = await loadTruckType(db, trip.truckId, trip.truckType);
        const ocr = trip.ocrData as { tripId?: unknown } | undefined;
        const ctx: LineTripContext = {
            dateLine: formatBuddhistShortDate(trip.std ?? trip.createdAt),
            originLabel: hubLabel(trip.origin ?? task?.sourceHub, codeToName),
            destinationLabel: hubLabel(trip.destination ?? task?.destination, codeToName),
            tripNo: str(ocr?.tripId) || str(trip.spxTripId) || tripId,
            driverNameTh: resolveDriverNameTh(driver, str(trip.driverId)),
            driverCode: resolveDriverCustomerCode(driver, target.customerCode),
            plate: str(trip.truckLicensePlate) || plateFromDriver(driver),
            phone: str(driver?.mobile) || str(task?.driverPhone),
            partner: str(trip.partnerCode) || str(task?.sourceHubLinkedCustomerCode),
            truckType,
            parcels: typeof trip.parcelCount === "number" ? String(trip.parcelCount) : undefined,
            checkInHm: formatBangkokHm(task?.checkInAt),
            departHm: formatBangkokHm(trip.std),
            arriveHm: formatBangkokHm(trip.ata),
            doneHm: formatBangkokHm(trip.deliveredTimestamp),
        };

        try {
            await pushLineMessage(token, target.lineGroupId, buildDeliveredMessage(ctx));
        } catch (e) {
            logger.error("[lineNotify] delivered push failed", { tripId, error: String(e) });
            throw new HttpsError("internal", "LINE push failed");
        }
        await tripSnap.ref.update({ lineDeliveredNotifiedAt: admin.firestore.FieldValue.serverTimestamp() });
        return { ok: true };
    }
);
