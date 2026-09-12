"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
const vitest_1 = require("vitest");
const tenantResolve_1 = require("./tenantResolve");
const TENANT_WANPEN = "sub_wanpen";
const TENANT_A = "sub_alpha";
const TENANT_B = "sub_bravo";
/** Lookups backed by plain maps, so each test states exactly what the database "knows". */
function lookups(overrides = {}) {
    const { tasks = {}, trips = {}, drivers = {}, ownFleetTenantId } = overrides;
    return {
        tenantOfTask: (id) => tasks[id],
        tenantOfTrip: (id) => trips[id],
        tenantOfDriver: (ref) => drivers[ref],
        ownFleetTenantId,
    };
}
(0, vitest_1.describe)("resolveTenant (ADR 0026 §2)", () => {
    (0, vitest_1.describe)("tasks", () => {
        (0, vitest_1.it)("resolves from the assigned driver", () => {
            const r = (0, tenantResolve_1.resolveTenant)("tasks", { driverId: "drv1" }, lookups({ drivers: { drv1: TENANT_A } }));
            (0, vitest_1.expect)(r).toEqual({ tenantId: TENANT_A, tenantSource: "driver" });
        });
        (0, vitest_1.it)("returns undefined for an unassigned task rather than guessing", () => {
            (0, vitest_1.expect)((0, tenantResolve_1.resolveTenant)("tasks", {}, lookups())).toBeUndefined();
        });
    });
    (0, vitest_1.describe)("driverId may be a driver doc id OR an Auth UID", () => {
        (0, vitest_1.it)("resolves when driverId is the driver doc id", () => {
            const r = (0, tenantResolve_1.resolveTenant)("tasks", { driverId: "driverDoc123" }, lookups({ drivers: { driverDoc123: TENANT_A } }));
            (0, vitest_1.expect)(r?.tenantId).toBe(TENANT_A);
        });
        (0, vitest_1.it)("resolves when driverId is the Auth UID", () => {
            const r = (0, tenantResolve_1.resolveTenant)("tasks", { driverId: "authUid456" }, lookups({ drivers: { authUid456: TENANT_B } }));
            (0, vitest_1.expect)(r?.tenantId).toBe(TENANT_B);
        });
    });
    (0, vitest_1.describe)("trip_records", () => {
        (0, vitest_1.it)("prefers the task link over the driver fallback", () => {
            // The task link was frozen when the work happened; the driver's subcontractor is only current.
            const r = (0, tenantResolve_1.resolveTenant)("trip_records", { taskId: "t1", driverId: "drv1" }, lookups({ tasks: { t1: TENANT_A }, drivers: { drv1: TENANT_B } }));
            (0, vitest_1.expect)(r).toEqual({ tenantId: TENANT_A, tenantSource: "task" });
        });
        (0, vitest_1.it)("falls back to the driver when the task is not yet stamped", () => {
            const r = (0, tenantResolve_1.resolveTenant)("trip_records", { taskId: "t1", driverId: "drv1" }, lookups({ tasks: {}, drivers: { drv1: TENANT_B } }));
            (0, vitest_1.expect)(r).toEqual({ tenantId: TENANT_B, tenantSource: "driver" });
        });
        (0, vitest_1.it)("falls back to the driver when the task id points at nothing", () => {
            const r = (0, tenantResolve_1.resolveTenant)("trip_records", { taskId: "missing", driverId: "drv1" }, lookups({ drivers: { drv1: TENANT_A } }));
            (0, vitest_1.expect)(r?.tenantSource).toBe("driver");
        });
        (0, vitest_1.it)("returns undefined when neither link resolves", () => {
            (0, vitest_1.expect)((0, tenantResolve_1.resolveTenant)("trip_records", { taskId: "t1", driverId: "drv1" }, lookups())).toBeUndefined();
        });
    });
    (0, vitest_1.describe)("standby_records", () => {
        (0, vitest_1.it)("prefers task over trip over driver", () => {
            const r = (0, tenantResolve_1.resolveTenant)("standby_records", { taskId: "t1", tripId: "r1", driverId: "drv1" }, lookups({ tasks: { t1: TENANT_A }, trips: { r1: TENANT_B }, drivers: { drv1: TENANT_WANPEN } }));
            (0, vitest_1.expect)(r).toEqual({ tenantId: TENANT_A, tenantSource: "task" });
        });
        (0, vitest_1.it)("uses the trip when the task is unstamped", () => {
            const r = (0, tenantResolve_1.resolveTenant)("standby_records", { taskId: "t1", tripId: "r1", driverId: "drv1" }, lookups({ trips: { r1: TENANT_B }, drivers: { drv1: TENANT_WANPEN } }));
            (0, vitest_1.expect)(r).toEqual({ tenantId: TENANT_B, tenantSource: "trip" });
        });
        (0, vitest_1.it)("uses the driver when neither link is stamped", () => {
            const r = (0, tenantResolve_1.resolveTenant)("standby_records", { taskId: "t1", tripId: "r1", driverId: "drv1" }, lookups({ drivers: { drv1: TENANT_WANPEN } }));
            (0, vitest_1.expect)(r).toEqual({ tenantId: TENANT_WANPEN, tenantSource: "driver" });
        });
        (0, vitest_1.it)("resolves a standby with no task/trip link at all via the driver", () => {
            const r = (0, tenantResolve_1.resolveTenant)("standby_records", { driverId: "drv1" }, lookups({ drivers: { drv1: TENANT_A } }));
            (0, vitest_1.expect)(r).toEqual({ tenantId: TENANT_A, tenantSource: "driver" });
        });
    });
    (0, vitest_1.describe)("incidentReport", () => {
        (0, vitest_1.it)("prefers the trip link over the driver", () => {
            const r = (0, tenantResolve_1.resolveTenant)("incidentReport", { tripId: "r1", driverId: "drv1" }, lookups({ trips: { r1: TENANT_A }, drivers: { drv1: TENANT_B } }));
            (0, vitest_1.expect)(r).toEqual({ tenantId: TENANT_A, tenantSource: "trip" });
        });
        (0, vitest_1.it)("ignores taskId — an incident links to a trip, not a task", () => {
            const r = (0, tenantResolve_1.resolveTenant)("incidentReport", { taskId: "t1", driverId: "drv1" }, lookups({ tasks: { t1: TENANT_A }, drivers: { drv1: TENANT_B } }));
            (0, vitest_1.expect)(r).toEqual({ tenantId: TENANT_B, tenantSource: "driver" });
        });
    });
    (0, vitest_1.describe)("drivers", () => {
        (0, vitest_1.it)("uses its own subcontractorId", () => {
            const r = (0, tenantResolve_1.resolveTenant)("drivers", { subcontractorId: TENANT_A }, lookups({ ownFleetTenantId: TENANT_WANPEN }));
            (0, vitest_1.expect)(r).toEqual({ tenantId: TENANT_A, tenantSource: "self" });
        });
        (0, vitest_1.it)("falls back to the own-fleet tenant when the driver has no subcontractor", () => {
            const r = (0, tenantResolve_1.resolveTenant)("drivers", {}, lookups({ ownFleetTenantId: TENANT_WANPEN }));
            (0, vitest_1.expect)(r).toEqual({ tenantId: TENANT_WANPEN, tenantSource: "self" });
        });
        (0, vitest_1.it)("treats an empty-string subcontractorId as absent", () => {
            const r = (0, tenantResolve_1.resolveTenant)("drivers", { subcontractorId: "  " }, lookups({ ownFleetTenantId: TENANT_WANPEN }));
            (0, vitest_1.expect)(r?.tenantId).toBe(TENANT_WANPEN);
        });
    });
    (0, vitest_1.describe)("trucks", () => {
        (0, vitest_1.it)("uses subcontractorId for a partner truck", () => {
            const r = (0, tenantResolve_1.resolveTenant)("trucks", { ownershipType: "subcontractor", subcontractorId: TENANT_A }, lookups({ ownFleetTenantId: TENANT_WANPEN }));
            (0, vitest_1.expect)(r).toEqual({ tenantId: TENANT_A, tenantSource: "self" });
        });
        (0, vitest_1.it)("uses the own-fleet tenant for ownershipType 'own'", () => {
            const r = (0, tenantResolve_1.resolveTenant)("trucks", { ownershipType: "own" }, lookups({ ownFleetTenantId: TENANT_WANPEN }));
            (0, vitest_1.expect)(r).toEqual({ tenantId: TENANT_WANPEN, tenantSource: "self" });
        });
        (0, vitest_1.it)("treats a missing ownershipType as own fleet", () => {
            const r = (0, tenantResolve_1.resolveTenant)("trucks", {}, lookups({ ownFleetTenantId: TENANT_WANPEN }));
            (0, vitest_1.expect)(r?.tenantId).toBe(TENANT_WANPEN);
        });
        (0, vitest_1.it)("does NOT fall back to own fleet for a partner truck missing its subcontractorId", () => {
            // Mislabelling a partner truck as ours is worse than leaving it an orphan we can count.
            const r = (0, tenantResolve_1.resolveTenant)("trucks", { ownershipType: "subcontractor" }, lookups({ ownFleetTenantId: TENANT_WANPEN }));
            (0, vitest_1.expect)(r).toBeUndefined();
        });
    });
    (0, vitest_1.describe)("never guesses", () => {
        (0, vitest_1.it)("returns undefined for drivers/trucks when ownFleetTenantId is unconfigured", () => {
            (0, vitest_1.expect)((0, tenantResolve_1.resolveTenant)("drivers", {}, lookups())).toBeUndefined();
            (0, vitest_1.expect)((0, tenantResolve_1.resolveTenant)("trucks", { ownershipType: "own" }, lookups())).toBeUndefined();
        });
        (0, vitest_1.it)("ignores non-string link fields instead of coercing them", () => {
            const r = (0, tenantResolve_1.resolveTenant)("trip_records", { taskId: 123, driverId: "drv1" }, lookups({ drivers: { drv1: TENANT_A } }));
            (0, vitest_1.expect)(r).toEqual({ tenantId: TENANT_A, tenantSource: "driver" });
        });
        (0, vitest_1.it)("ignores a lookup that returns a blank string", () => {
            const r = (0, tenantResolve_1.resolveTenant)("trip_records", { taskId: "t1", driverId: "drv1" }, lookups({ tasks: { t1: "   " }, drivers: { drv1: TENANT_A } }));
            (0, vitest_1.expect)(r).toEqual({ tenantId: TENANT_A, tenantSource: "driver" });
        });
    });
});
(0, vitest_1.describe)("isTenantCollection", () => {
    (0, vitest_1.it)("accepts the six in-scope collections", () => {
        for (const c of ["tasks", "trip_records", "standby_records", "incidentReport", "drivers", "trucks"]) {
            (0, vitest_1.expect)((0, tenantResolve_1.isTenantCollection)(c)).toBe(true);
        }
    });
    (0, vitest_1.it)("rejects collections that are deliberately out of scope (phase 2)", () => {
        (0, vitest_1.expect)((0, tenantResolve_1.isTenantCollection)("vehicle_expenses")).toBe(false);
        (0, vitest_1.expect)((0, tenantResolve_1.isTenantCollection)("incident_reports")).toBe(false); // real name is incidentReport
    });
});
//# sourceMappingURL=tenantResolve.test.js.map