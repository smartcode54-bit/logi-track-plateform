import * as functions from "firebase-functions/v2/https";
import * as admin from "firebase-admin";

const db = admin.firestore();

/**
 * Backfill truckId / truckLicensePlate / truckType into trip_records that lack them.
 * Resolves from the linked task's licensePlate/truckType snapshot (written at check-in time)
 * — more reliable than currentAssignment since the task snapshot is immutable.
 *
 * Admin-only callable.
 */
export const backfillTripTruckData = functions.onCall(
  { enforceAppCheck: false },
  async (request) => {
    if (!request.auth) throw new functions.HttpsError("unauthenticated", "Auth required");
    const isAdmin =
      request.auth.token.admin === true ||
      request.auth.token["role"] === "admin";
    if (!isAdmin) throw new functions.HttpsError("permission-denied", "Admin only");

    const tripSnap = await db
      .collection("trip_records")
      .where("truckLicensePlate", "==", null)
      .limit(500)
      .get();

    // Also catch docs where field simply doesn't exist (null query won't catch missing fields)
    const missingSnap = await db.collection("trip_records").limit(500).get();
    const allDocs = [
      ...tripSnap.docs,
      ...missingSnap.docs.filter(
        (d) => d.data().truckLicensePlate === undefined
      ),
    ];
    // dedup
    const seen = new Set<string>();
    const docs = allDocs.filter((d) => {
      if (seen.has(d.id)) return false;
      seen.add(d.id);
      return true;
    });

    let updated = 0;
    let skipped = 0;
    let errors = 0;

    const batches: admin.firestore.WriteBatch[] = [];
    let batch = db.batch();
    let batchCount = 0;

    for (const doc of docs) {
      const data = doc.data();
      if (data.truckLicensePlate) { skipped++; continue; }

      const taskId = data.taskId as string | undefined;
      if (!taskId) { skipped++; continue; }

      try {
        const taskDoc = await db.collection("tasks").doc(taskId).get();
        if (!taskDoc.exists) { skipped++; continue; }
        const task = taskDoc.data()!;
        const licensePlate = task.licensePlate as string | undefined;
        const truckType = task.truckType as string | undefined;

        if (!licensePlate) { skipped++; continue; }

        batch.update(doc.ref, {
          truckLicensePlate: licensePlate,
          ...(truckType ? { truckType } : {}),
          updatedAt: admin.firestore.FieldValue.serverTimestamp(),
        });
        updated++;
        batchCount++;

        if (batchCount >= 100) {
          batches.push(batch);
          batch = db.batch();
          batchCount = 0;
        }
      } catch {
        errors++;
      }
    }
    if (batchCount > 0) batches.push(batch);
    await Promise.all(batches.map((b) => b.commit()));

    return { totalScanned: docs.length, updated, skipped, errors };
  }
);
