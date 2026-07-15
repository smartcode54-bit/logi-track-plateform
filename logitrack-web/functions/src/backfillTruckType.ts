import * as functions from "firebase-functions/v2/https";
import * as admin from "firebase-admin";

const db = admin.firestore();

interface BackfillTruckTypeRequest {
  /** Old truckType value to replace (e.g. "PICKUP"). */
  fromType?: string;
  /** New truckType value to write (e.g. "4W"). */
  toType?: string;
  /** Max docs per collection to update per invocation. Default 1000. */
  maxUpdate?: number;
}

interface CollectionStat {
  matched: number;
  updated: number;
}

interface BackfillTruckTypeResponse {
  fromType: string;
  toType: string;
  tasks: CollectionStat;
  trip_records: CollectionStat;
  capped: boolean;
}

/** Rewrite truckType == fromType → toType in one collection (batched, 100 per batch). */
async function migrateCollection(
  collection: string,
  fromType: string,
  toType: string,
  maxUpdate: number
): Promise<CollectionStat> {
  const snap = await db.collection(collection).where("truckType", "==", fromType).get();
  const stat: CollectionStat = { matched: snap.size, updated: 0 };

  let batch = db.batch();
  let count = 0;
  const commits: Promise<unknown>[] = [];

  for (const doc of snap.docs) {
    if (stat.updated >= maxUpdate) break;
    batch.update(doc.ref, { truckType: toType });
    count++;
    stat.updated++;
    if (count >= 100) {
      commits.push(batch.commit());
      batch = db.batch();
      count = 0;
    }
  }
  if (count > 0) commits.push(batch.commit());
  await Promise.all(commits);
  return stat;
}

/**
 * Admin-only callable: migrate a legacy truckType value to a new one across
 * `tasks` and `trip_records`. Idempotent — reruns only touch docs still on the
 * old value. Defaults to PICKUP → 4W; pass fromType/toType to reuse (e.g. 4WH).
 *
 * Note: `trucks` master stores full-name types ("Pickup", "4 Wheels") and is
 * NOT touched — only the derived abbreviation on tasks/trips changes.
 */
export const backfillTruckType = functions.onCall<
  BackfillTruckTypeRequest,
  Promise<BackfillTruckTypeResponse>
>(
  { region: "asia-southeast1", enforceAppCheck: false },
  async (request): Promise<BackfillTruckTypeResponse> => {
    if (!request.auth) throw new functions.HttpsError("unauthenticated", "Auth required");
    const isAdmin =
      request.auth.token.admin === true || request.auth.token["role"] === "admin";
    if (!isAdmin) throw new functions.HttpsError("permission-denied", "Admin only");

    const fromType = (request.data?.fromType ?? "PICKUP").trim();
    const toType = (request.data?.toType ?? "4W").trim();
    if (!fromType || !toType) {
      throw new functions.HttpsError("invalid-argument", "fromType and toType are required");
    }
    if (fromType === toType) {
      throw new functions.HttpsError("invalid-argument", "fromType and toType must differ");
    }
    const maxUpdate = Math.min(Math.max(1, request.data?.maxUpdate ?? 1000), 5000);

    const [tasks, trip_records] = await Promise.all([
      migrateCollection("tasks", fromType, toType, maxUpdate),
      migrateCollection("trip_records", fromType, toType, maxUpdate),
    ]);

    return {
      fromType,
      toType,
      tasks,
      trip_records,
      capped: tasks.matched > tasks.updated || trip_records.matched > trip_records.updated,
    };
  }
);
