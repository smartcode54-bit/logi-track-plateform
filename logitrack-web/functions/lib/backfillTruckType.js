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
exports.backfillTruckType = void 0;
const functions = __importStar(require("firebase-functions/v2/https"));
const admin = __importStar(require("firebase-admin"));
const db = admin.firestore();
/** Rewrite truckType == fromType → toType in one collection (batched, 100 per batch). */
async function migrateCollection(collection, fromType, toType, maxUpdate) {
    const snap = await db.collection(collection).where("truckType", "==", fromType).get();
    const stat = { matched: snap.size, updated: 0 };
    let batch = db.batch();
    let count = 0;
    const commits = [];
    for (const doc of snap.docs) {
        if (stat.updated >= maxUpdate)
            break;
        batch.update(doc.ref, { truckType: toType });
        count++;
        stat.updated++;
        if (count >= 100) {
            commits.push(batch.commit());
            batch = db.batch();
            count = 0;
        }
    }
    if (count > 0)
        commits.push(batch.commit());
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
exports.backfillTruckType = functions.onCall({ region: "asia-southeast1", enforceAppCheck: false }, async (request) => {
    if (!request.auth)
        throw new functions.HttpsError("unauthenticated", "Auth required");
    const isAdmin = request.auth.token.admin === true || request.auth.token["role"] === "admin";
    if (!isAdmin)
        throw new functions.HttpsError("permission-denied", "Admin only");
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
});
//# sourceMappingURL=backfillTruckType.js.map