import * as functions from "firebase-functions/v2/https";
import * as admin from "firebase-admin";

const db = admin.firestore();

/** Load all trucks into a map: truckId → { licensePlate, truckType } */
async function loadTruckMap(): Promise<Map<string, { licensePlate: string; truckType?: string }>> {
  const snap = await db.collection("trucks").get();
  const map = new Map<string, { licensePlate: string; truckType?: string }>();
  snap.docs.forEach((d) => {
    const plate = (d.data().licensePlate as string | undefined) ?? "";
    const truckType = d.data().type as string | undefined;
    if (plate) map.set(d.id, { licensePlate: plate, truckType });
  });
  return map;
}

/** Collect docs from a collection where a field is null OR missing (up to limit). */
async function collectMissingField(
  collection: string,
  field: string,
  limit = 500
): Promise<admin.firestore.QueryDocumentSnapshot[]> {
  const [nullSnap, allSnap] = await Promise.all([
    db.collection(collection).where(field, "==", null).limit(limit).get(),
    db.collection(collection).limit(limit).get(),
  ]);
  const seen = new Set<string>();
  const docs: admin.firestore.QueryDocumentSnapshot[] = [];
  for (const d of [...nullSnap.docs, ...allSnap.docs.filter((d) => d.data()[field] === undefined)]) {
    if (!seen.has(d.id)) { seen.add(d.id); docs.push(d); }
  }
  return docs;
}

/** Flush a write batch and start a new one when count hits 100. */
function maybFlush(
  batches: admin.firestore.WriteBatch[],
  batch: admin.firestore.WriteBatch,
  count: number
): [admin.firestore.WriteBatch, number] {
  if (count >= 100) {
    batches.push(batch);
    return [db.batch(), 0];
  }
  return [batch, count];
}

/**
 * Backfill truckLicensePlate (and truckType where available) into:
 *   - trip_records   → resolved from linked task.licensePlate (immutable snapshot at check-in)
 *   - vehicle_expenses → resolved from truckId → trucks collection
 *   - tasks           → resolved from truckId → trucks collection
 *                       fallback: matched trip_record.truckLicensePlate (already backfilled)
 *
 * Admin-only callable. Idempotent (skips docs that already have the field).
 */
export const backfillTripTruckData = functions.onCall(
  { enforceAppCheck: false },
  async (request) => {
    if (!request.auth) throw new functions.HttpsError("unauthenticated", "Auth required");
    const isAdmin =
      request.auth.token.admin === true ||
      request.auth.token["role"] === "admin";
    if (!isAdmin) throw new functions.HttpsError("permission-denied", "Admin only");

    // ── Load trucks master map once ────────────────────────────────────────
    const truckMap = await loadTruckMap();

    // ── 1. trip_records ────────────────────────────────────────────────────
    const tripStats = { scanned: 0, updated: 0, skipped: 0, errors: 0 };
    {
      const docs = await collectMissingField("trip_records", "truckLicensePlate");
      tripStats.scanned = docs.length;
      const batches: admin.firestore.WriteBatch[] = [];
      let batch = db.batch();
      let count = 0;

      for (const doc of docs) {
        const data = doc.data();
        if (data.truckLicensePlate) { tripStats.skipped++; continue; }

        const taskId = data.taskId as string | undefined;
        if (!taskId) { tripStats.skipped++; continue; }

        try {
          const taskDoc = await db.collection("tasks").doc(taskId).get();
          if (!taskDoc.exists) { tripStats.skipped++; continue; }
          const task = taskDoc.data()!;
          const plate = task.licensePlate as string | undefined;
          const truckType = task.truckType as string | undefined;
          if (!plate) { tripStats.skipped++; continue; }

          batch.update(doc.ref, {
            truckLicensePlate: plate,
            ...(truckType ? { truckType } : {}),
            updatedAt: admin.firestore.FieldValue.serverTimestamp(),
          });
          tripStats.updated++;
          count++;
          [batch, count] = maybFlush(batches, batch, count);
        } catch { tripStats.errors++; }
      }
      if (count > 0) batches.push(batch);
      await Promise.all(batches.map((b) => b.commit()));
    }

    // ── 2. vehicle_expenses ────────────────────────────────────────────────
    const expenseStats = { scanned: 0, updated: 0, skipped: 0, errors: 0 };
    {
      const docs = await collectMissingField("vehicle_expenses", "truckLicensePlate");
      expenseStats.scanned = docs.length;
      const batches: admin.firestore.WriteBatch[] = [];
      let batch = db.batch();
      let count = 0;

      for (const doc of docs) {
        const data = doc.data();
        if (data.truckLicensePlate) { expenseStats.skipped++; continue; }

        const truckId = data.truckId as string | undefined;
        if (!truckId) { expenseStats.skipped++; continue; }

        const truck = truckMap.get(truckId);
        if (!truck?.licensePlate) { expenseStats.skipped++; continue; }

        try {
          batch.update(doc.ref, {
            truckLicensePlate: truck.licensePlate,
            updatedAt: admin.firestore.FieldValue.serverTimestamp(),
          });
          expenseStats.updated++;
          count++;
          [batch, count] = maybFlush(batches, batch, count);
        } catch { expenseStats.errors++; }
      }
      if (count > 0) batches.push(batch);
      await Promise.all(batches.map((b) => b.commit()));
    }

    // ── 3. tasks ───────────────────────────────────────────────────────────
    const taskStats = { scanned: 0, updated: 0, skipped: 0, errors: 0 };
    {
      const docs = await collectMissingField("tasks", "licensePlate");
      taskStats.scanned = docs.length;
      const batches: admin.firestore.WriteBatch[] = [];
      let batch = db.batch();
      let count = 0;

      // Build taskId → truckLicensePlate from already-backfilled trip_records as fallback
      const taskToPlateFromTrip = new Map<string, string>();
      try {
        const tripSnap = await db
          .collection("trip_records")
          .where("truckLicensePlate", "!=", null)
          .limit(1000)
          .get();
        tripSnap.docs.forEach((d) => {
          const taskId = d.data().taskId as string | undefined;
          const plate = d.data().truckLicensePlate as string | undefined;
          if (taskId && plate && !taskToPlateFromTrip.has(taskId)) {
            taskToPlateFromTrip.set(taskId, plate);
          }
        });
      } catch { /* non-critical */ }

      for (const doc of docs) {
        const data = doc.data();
        if (data.licensePlate) { taskStats.skipped++; continue; }

        try {
          // Primary: truckId → trucks master
          const truckId = data.truckId as string | undefined;
          let plate: string | undefined;
          let truckType: string | undefined;

          if (truckId) {
            const truck = truckMap.get(truckId);
            plate = truck?.licensePlate;
            truckType = truck?.truckType ?? (data.truckType as string | undefined);
          }

          // Fallback: trip_record already resolved plate for this task
          if (!plate) plate = taskToPlateFromTrip.get(doc.id);

          if (!plate) { taskStats.skipped++; continue; }

          batch.update(doc.ref, {
            licensePlate: plate,
            ...(truckType && !data.truckType ? { truckType } : {}),
            updatedAt: admin.firestore.FieldValue.serverTimestamp(),
          });
          taskStats.updated++;
          count++;
          [batch, count] = maybFlush(batches, batch, count);
        } catch { taskStats.errors++; }
      }
      if (count > 0) batches.push(batch);
      await Promise.all(batches.map((b) => b.commit()));
    }

    return {
      tripRecords: tripStats,
      vehicleExpenses: expenseStats,
      tasks: taskStats,
    };
  }
);
