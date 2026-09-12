"use strict";
/**
 * Pure tenant resolution for the multi-tenant carrier isolation work (ADR 0026, spec
 * `shared-docs/specs/multi-tenant-carrier-isolation.md` §4).
 *
 * `tenantId` says **which carrier organisation ran this row**. It is resolved once, at write time,
 * and frozen — never recomputed on read. A broker's driver moves between carriers, so deriving
 * ownership from `drivers.subcontractorId` at read time would silently rewrite the ownership of
 * every past row, including rows already reported to the dispatcher (ADR 0026 §2).
 *
 * This module is deliberately free of Firestore: the caller fetches whatever it needs and passes
 * lookups in, so every ordering rule below is unit-testable.
 *
 * Nothing here guesses. When the chain runs out, the result is `undefined` and the row is counted
 * as a [[Tenant orphan]] — the same fail-loud stance as `lib/truckType.ts`.
 */
Object.defineProperty(exports, "__esModule", { value: true });
exports.TENANT_COLLECTIONS = void 0;
exports.resolveTenant = resolveTenant;
exports.isTenantCollection = isTenantCollection;
exports.TENANT_COLLECTIONS = [
    "tasks",
    "trip_records",
    "standby_records",
    "incidentReport",
    "drivers",
    "trucks",
];
/** Trimmed string, or "" for anything that is not a usable string. */
function str(value) {
    return typeof value === "string" ? value.trim() : "";
}
/** First non-empty tenant produced by the chain, tagged with how it was found. */
function firstOf(candidates) {
    for (const [tenantSource, tenantId] of candidates) {
        const id = str(tenantId);
        if (id)
            return { tenantId: id, tenantSource };
    }
    return undefined;
}
/**
 * Resolve the tenant for one document.
 *
 * Ordering is per collection and is **not** interchangeable: link-derived values (`task`/`trip`)
 * are preferred over the driver fallback, because the link was frozen when the work happened while
 * the driver's subcontractor is only current. This is also why the backfill must run
 * `tasks` → `trip_records` → `standby_records` → `incidentReport`: running it out of order leaves
 * the links unstamped and silently downgrades every later row to the `driver` approximation.
 */
function resolveTenant(collection, doc, lookups) {
    const taskId = str(doc.taskId);
    const tripId = str(doc.tripId);
    const driverId = str(doc.driverId);
    const viaTask = taskId ? lookups.tenantOfTask(taskId) : undefined;
    const viaTrip = tripId ? lookups.tenantOfTrip(tripId) : undefined;
    const viaDriver = driverId ? lookups.tenantOfDriver(driverId) : undefined;
    switch (collection) {
        case "tasks":
            return firstOf([["driver", viaDriver]]);
        case "trip_records":
            return firstOf([
                ["task", viaTask],
                ["driver", viaDriver],
            ]);
        case "standby_records":
            return firstOf([
                ["task", viaTask],
                ["trip", viaTrip],
                ["driver", viaDriver],
            ]);
        case "incidentReport":
            return firstOf([
                ["trip", viaTrip],
                ["driver", viaDriver],
            ]);
        case "drivers":
            // The driver doc carries its own carrier. No subcontractor = our own employee.
            return firstOf([
                ["self", str(doc.subcontractorId)],
                ["self", lookups.ownFleetTenantId],
            ]);
        case "trucks":
            // `ownershipType` + `subcontractorId` keep their existing meaning ("is this truck ours
            // or a partner's"). tenantId is a separate axis layered on top — see spec §4 for the
            // four call sites that break if those two fields are repurposed.
            return str(doc.ownershipType) === "subcontractor"
                ? firstOf([["self", str(doc.subcontractorId)]])
                : firstOf([["self", lookups.ownFleetTenantId]]);
        default: {
            // Exhaustiveness: a new collection must declare its own ordering, not inherit one.
            const never = collection;
            throw new Error(`resolveTenant: unhandled collection ${String(never)}`);
        }
    }
}
/** True when the value is a collection this module knows how to resolve. */
function isTenantCollection(value) {
    return exports.TENANT_COLLECTIONS.includes(value);
}
//# sourceMappingURL=tenantResolve.js.map