/**
 * Firestore-backed lookups that feed the pure resolver in `core/tenantResolve.ts`
 * (ADR 0026, spec `shared-docs/specs/multi-tenant-carrier-isolation.md`).
 *
 * Kept separate from `core/` so the resolver stays free of Firestore and unit-testable, and
 * separate from `tenantStamp.ts` so the task callable can reuse it without pulling in the
 * scheduler/backfill machinery.
 */
import * as admin from "firebase-admin";
import { logger } from "firebase-functions";

/**
 * Where the own-fleet tenant is configured. Deliberately Firestore config and not a constant:
 * `backfillCustomerLinks.ts:6` hardcodes a doc id that only exists in one project, which is exactly
 * the trap this design must not repeat now that a staging project is in play.
 *
 * Shape: `settings/tenancy = { ownFleetTenantId: "<subcontractors doc id>" }`
 */
export const SETTINGS_COLLECTION = "settings";
export const TENANCY_SETTINGS_DOC = "tenancy";

const COL_DRIVERS = "drivers";

/**
 * Read the own-fleet tenant id. Returns `undefined` when unconfigured — callers must then treat the
 * affected rows as orphans rather than inventing a value.
 */
export async function readOwnFleetTenantId(
    db: admin.firestore.Firestore
): Promise<string | undefined> {
    try {
        const snap = await db.collection(SETTINGS_COLLECTION).doc(TENANCY_SETTINGS_DOC).get();
        const value = snap.exists ? snap.data()?.ownFleetTenantId : undefined;
        const id = typeof value === "string" ? value.trim() : "";
        if (!id) {
            logger.warn(
                `[tenant] ${SETTINGS_COLLECTION}/${TENANCY_SETTINGS_DOC}.ownFleetTenantId is not set — ` +
                    "own-fleet drivers and trucks will be left as tenant orphans until it is configured"
            );
            return undefined;
        }
        return id;
    } catch (err) {
        logger.error("[tenant] failed to read own-fleet tenant setting", { err });
        return undefined;
    }
}

/** The carrier a driver belongs to: their subcontractor, or our own fleet when they have none. */
function tenantFromDriverData(
    data: admin.firestore.DocumentData | undefined,
    ownFleetTenantId: string | undefined
): string | undefined {
    const sub = typeof data?.subcontractorId === "string" ? data.subcontractorId.trim() : "";
    if (sub) return sub;
    return ownFleetTenantId;
}

/**
 * Resolve one driver reference to a tenant. `driverRef` may be a **driver doc id or an Auth UID** —
 * both appear in `driverId` fields across the data set, which is why `firestore.rules` checks both.
 */
export async function tenantForDriverRef(
    db: admin.firestore.Firestore,
    driverRef: string,
    ownFleetTenantId: string | undefined
): Promise<string | undefined> {
    const ref = driverRef.trim();
    if (!ref) return undefined;

    const byDocId = await db.collection(COL_DRIVERS).doc(ref).get();
    if (byDocId.exists) return tenantFromDriverData(byDocId.data(), ownFleetTenantId);

    const byAuthId = await db
        .collection(COL_DRIVERS)
        .where("authId", "==", ref)
        .limit(1)
        .get();
    if (!byAuthId.empty) return tenantFromDriverData(byAuthId.docs[0].data(), ownFleetTenantId);

    return undefined;
}

/**
 * Load every driver once, keyed by **both** doc id and Auth UID, for batch work (sweeper/backfill).
 * Drivers number in the hundreds, so one read pass beats a lookup per row by a wide margin.
 */
export async function buildDriverTenantMap(
    db: admin.firestore.Firestore,
    ownFleetTenantId: string | undefined
): Promise<Map<string, string>> {
    const map = new Map<string, string>();
    const snap = await db.collection(COL_DRIVERS).get();
    for (const doc of snap.docs) {
        const data = doc.data();
        const tenant = tenantFromDriverData(data, ownFleetTenantId);
        if (!tenant) continue;
        map.set(doc.id, tenant);
        const authId = typeof data.authId === "string" ? data.authId.trim() : "";
        if (authId) map.set(authId, tenant);
    }
    return map;
}
