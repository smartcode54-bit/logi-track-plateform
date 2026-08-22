import * as admin from "firebase-admin";
import { onCall, HttpsError } from "firebase-functions/v2/https";
import * as logger from "firebase-functions/logger";
import { appendSecurityEvent } from "./securityEvents";
import { validateTripDocId } from "./core/tripDocId";

const COL_TRIP_RECORDS = "trip_records";
/** Note the collection is camelCase singular — see lib/collections.ts INCIDENT_REPORTS. */
const COL_INCIDENT_REPORTS = "incidentReport";

/** Firestore caps a batch at 500 writes; the trip copy + delete already take two. */
const MAX_INCIDENT_REPORTS = 400;

export interface RenameTripRecordRequest {
    oldId: string;
    newId: string;
}

export interface RenameTripRecordResponse {
    ok: boolean;
    oldId: string;
    newId: string;
    /** How many incidentReport docs were re-pointed at the new trip id. */
    incidentReportsRepointed: number;
}

/**
 * HTTPS Callable (admin only): correct a mistyped trip ID.
 *
 * `trip_records` uses the trip ID the driver typed as the **document ID** (mobile writes
 * `trip_records/{tripId}` with `spxTripId: tripId`), and a Firestore document ID is immutable — so a
 * "rename" is really copy → re-point → delete, done here in one atomic batch.
 *
 * What it fixes up, and why each one matters:
 *  - **`spxTripId`** is rewritten to the new id. Every display surface prefers this field over the
 *    document id (e.g. Income renders `spxTripId || id`), so copying the doc verbatim would move the
 *    trip but keep showing the wrong number.
 *  - **`incidentReport.tripId`** rows are re-pointed. They are queried by trip id
 *    (app/app/incident-reports), so a stale reference orphans the report.
 *  - **`renamedFromTripId` / `renamedAt` / `renamedBy`** are stamped on the moved doc, and a
 *    `security_events` entry is appended, so the correction is traceable afterwards.
 *
 * Deliberately NOT touched: Storage objects at `trip_records/{oldId}/{photoType}.jpg`. The trip's
 * `photos[]` holds download URLs pointing at those objects, so they keep resolving after the move —
 * copying the blobs would risk breaking working photo links to fix nothing but a folder name.
 *
 * **Delivered trips only.** A trip that is still running exists in the driver's on-device state too;
 * the app would keep writing to the old document id and resurrect the row we just deleted, leaving
 * two trips for one job. The server cannot prevent that, so the precondition does.
 */
export const renameTripRecord = onCall<RenameTripRecordRequest, Promise<RenameTripRecordResponse>>(
    {
        region: "asia-southeast1",
        enforceAppCheck: false, // Web admin calls without an App Check token; auth + admin checked below
    },
    async (request): Promise<RenameTripRecordResponse> => {
        if (!request.auth) {
            throw new HttpsError("unauthenticated", "Must be authenticated");
        }
        const isAdmin = request.auth.token.admin === true || request.auth.token["role"] === "admin";
        if (!isAdmin) {
            throw new HttpsError("permission-denied", "Admin only");
        }

        const oldId = (request.data?.oldId ?? "").trim();
        const newId = (request.data?.newId ?? "").trim();

        for (const [label, id] of [["oldId", oldId], ["newId", newId]] as const) {
            const problem = validateTripDocId(id);
            if (problem) throw new HttpsError("invalid-argument", `${label}: ${problem}`);
        }
        if (oldId === newId) {
            throw new HttpsError("invalid-argument", "The new Trip ID is the same as the current one");
        }

        const db = admin.firestore();
        const oldRef = db.collection(COL_TRIP_RECORDS).doc(oldId);
        const newRef = db.collection(COL_TRIP_RECORDS).doc(newId);

        const [oldSnap, newSnap] = await Promise.all([oldRef.get(), newRef.get()]);
        if (!oldSnap.exists) {
            throw new HttpsError("not-found", `Trip ${oldId} not found`);
        }
        if (newSnap.exists) {
            // Never merge two trips: the target may be a real trip belonging to someone else.
            throw new HttpsError("already-exists", `Trip ${newId} already exists`);
        }

        const data = oldSnap.data() as Record<string, unknown>;
        if (data.status !== "delivered") {
            throw new HttpsError(
                "failed-precondition",
                `Only delivered trips can be renamed (this one is '${String(data.status ?? "unknown")}'). ` +
                    "A running trip also lives in the driver's app and would be recreated under the old ID."
            );
        }

        const incidentSnap = await db
            .collection(COL_INCIDENT_REPORTS)
            .where("tripId", "==", oldId)
            .get();
        if (incidentSnap.size > MAX_INCIDENT_REPORTS) {
            throw new HttpsError(
                "failed-precondition",
                `Trip has ${incidentSnap.size} incident reports — too many to re-point in one batch`
            );
        }

        const batch = db.batch();
        batch.set(newRef, {
            ...data,
            // The display reads this in preference to the document id — move it too, or the trip
            // lands on the new id still showing the old number.
            spxTripId: newId,
            renamedFromTripId: oldId,
            renamedAt: admin.firestore.FieldValue.serverTimestamp(),
            renamedBy: request.auth.uid,
            updatedAt: admin.firestore.FieldValue.serverTimestamp(),
        });
        incidentSnap.docs.forEach((doc) => {
            batch.update(doc.ref, { tripId: newId });
        });
        batch.delete(oldRef);
        await batch.commit();

        logger.info("[renameTripRecord] renamed", {
            oldId,
            newId,
            incidentReportsRepointed: incidentSnap.size,
            actorUid: request.auth.uid,
        });

        try {
            const email = request.auth.token.email;
            await appendSecurityEvent({
                type: "trip_record_renamed",
                severity: "warning",
                summary: `Trip ${oldId} renamed to ${newId}`,
                details: {
                    oldId,
                    newId,
                    incidentReportsRepointed: incidentSnap.size,
                    driverId: typeof data.driverId === "string" ? data.driverId : null,
                    taskId: typeof data.taskId === "string" ? data.taskId : null,
                },
                actorUid: request.auth.uid,
                actorEmail: typeof email === "string" ? email : null,
            });
        } catch (logErr) {
            // The rename itself already committed; losing the audit line must not fail the call.
            logger.error("[renameTripRecord] security_events append:", logErr);
        }

        return {
            ok: true,
            oldId,
            newId,
            incidentReportsRepointed: incidentSnap.size,
        };
    }
);
