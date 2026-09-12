"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
exports.TENANCY_SETTINGS_DOC = exports.SETTINGS_COLLECTION = void 0;
exports.readOwnFleetTenantId = readOwnFleetTenantId;
exports.tenantForDriverRef = tenantForDriverRef;
exports.buildDriverTenantMap = buildDriverTenantMap;
const firebase_functions_1 = require("firebase-functions");
/**
 * Where the own-fleet tenant is configured. Deliberately Firestore config and not a constant:
 * `backfillCustomerLinks.ts:6` hardcodes a doc id that only exists in one project, which is exactly
 * the trap this design must not repeat now that a staging project is in play.
 *
 * Shape: `settings/tenancy = { ownFleetTenantId: "<subcontractors doc id>" }`
 */
exports.SETTINGS_COLLECTION = "settings";
exports.TENANCY_SETTINGS_DOC = "tenancy";
const COL_DRIVERS = "drivers";
/**
 * Read the own-fleet tenant id. Returns `undefined` when unconfigured — callers must then treat the
 * affected rows as orphans rather than inventing a value.
 */
async function readOwnFleetTenantId(db) {
    try {
        const snap = await db.collection(exports.SETTINGS_COLLECTION).doc(exports.TENANCY_SETTINGS_DOC).get();
        const value = snap.exists ? snap.data()?.ownFleetTenantId : undefined;
        const id = typeof value === "string" ? value.trim() : "";
        if (!id) {
            firebase_functions_1.logger.warn(`[tenant] ${exports.SETTINGS_COLLECTION}/${exports.TENANCY_SETTINGS_DOC}.ownFleetTenantId is not set — ` +
                "own-fleet drivers and trucks will be left as tenant orphans until it is configured");
            return undefined;
        }
        return id;
    }
    catch (err) {
        firebase_functions_1.logger.error("[tenant] failed to read own-fleet tenant setting", { err });
        return undefined;
    }
}
/** The carrier a driver belongs to: their subcontractor, or our own fleet when they have none. */
function tenantFromDriverData(data, ownFleetTenantId) {
    const sub = typeof data?.subcontractorId === "string" ? data.subcontractorId.trim() : "";
    if (sub)
        return sub;
    return ownFleetTenantId;
}
/**
 * Resolve one driver reference to a tenant. `driverRef` may be a **driver doc id or an Auth UID** —
 * both appear in `driverId` fields across the data set, which is why `firestore.rules` checks both.
 */
async function tenantForDriverRef(db, driverRef, ownFleetTenantId) {
    const ref = driverRef.trim();
    if (!ref)
        return undefined;
    const byDocId = await db.collection(COL_DRIVERS).doc(ref).get();
    if (byDocId.exists)
        return tenantFromDriverData(byDocId.data(), ownFleetTenantId);
    const byAuthId = await db
        .collection(COL_DRIVERS)
        .where("authId", "==", ref)
        .limit(1)
        .get();
    if (!byAuthId.empty)
        return tenantFromDriverData(byAuthId.docs[0].data(), ownFleetTenantId);
    return undefined;
}
/**
 * Load every driver once, keyed by **both** doc id and Auth UID, for batch work (sweeper/backfill).
 * Drivers number in the hundreds, so one read pass beats a lookup per row by a wide margin.
 */
async function buildDriverTenantMap(db, ownFleetTenantId) {
    const map = new Map();
    const snap = await db.collection(COL_DRIVERS).get();
    for (const doc of snap.docs) {
        const data = doc.data();
        const tenant = tenantFromDriverData(data, ownFleetTenantId);
        if (!tenant)
            continue;
        map.set(doc.id, tenant);
        const authId = typeof data.authId === "string" ? data.authId.trim() : "";
        if (authId)
            map.set(authId, tenant);
    }
    return map;
}
//# sourceMappingURL=tenantLookups.js.map