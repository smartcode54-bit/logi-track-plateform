import { describe, it, expect } from "vitest";
import { resolveTenant, isTenantCollection, type TenantLookups } from "./tenantResolve";

const TENANT_WANPEN = "sub_wanpen";
const TENANT_A = "sub_alpha";
const TENANT_B = "sub_bravo";

/** Lookups backed by plain maps, so each test states exactly what the database "knows". */
function lookups(overrides: {
    tasks?: Record<string, string>;
    trips?: Record<string, string>;
    drivers?: Record<string, string>;
    ownFleetTenantId?: string;
} = {}): TenantLookups {
    const { tasks = {}, trips = {}, drivers = {}, ownFleetTenantId } = overrides;
    return {
        tenantOfTask: (id) => tasks[id],
        tenantOfTrip: (id) => trips[id],
        tenantOfDriver: (ref) => drivers[ref],
        ownFleetTenantId,
    };
}

describe("resolveTenant (ADR 0026 §2)", () => {
    describe("tasks", () => {
        it("resolves from the assigned driver", () => {
            const r = resolveTenant("tasks", { driverId: "drv1" }, lookups({ drivers: { drv1: TENANT_A } }));
            expect(r).toEqual({ tenantId: TENANT_A, tenantSource: "driver" });
        });

        it("returns undefined for an unassigned task rather than guessing", () => {
            expect(resolveTenant("tasks", {}, lookups())).toBeUndefined();
        });
    });

    describe("driverId may be a driver doc id OR an Auth UID", () => {
        it("resolves when driverId is the driver doc id", () => {
            const r = resolveTenant("tasks", { driverId: "driverDoc123" }, lookups({ drivers: { driverDoc123: TENANT_A } }));
            expect(r?.tenantId).toBe(TENANT_A);
        });

        it("resolves when driverId is the Auth UID", () => {
            const r = resolveTenant("tasks", { driverId: "authUid456" }, lookups({ drivers: { authUid456: TENANT_B } }));
            expect(r?.tenantId).toBe(TENANT_B);
        });
    });

    describe("trip_records", () => {
        it("prefers the task link over the driver fallback", () => {
            // The task link was frozen when the work happened; the driver's subcontractor is only current.
            const r = resolveTenant(
                "trip_records",
                { taskId: "t1", driverId: "drv1" },
                lookups({ tasks: { t1: TENANT_A }, drivers: { drv1: TENANT_B } })
            );
            expect(r).toEqual({ tenantId: TENANT_A, tenantSource: "task" });
        });

        it("falls back to the driver when the task is not yet stamped", () => {
            const r = resolveTenant(
                "trip_records",
                { taskId: "t1", driverId: "drv1" },
                lookups({ tasks: {}, drivers: { drv1: TENANT_B } })
            );
            expect(r).toEqual({ tenantId: TENANT_B, tenantSource: "driver" });
        });

        it("falls back to the driver when the task id points at nothing", () => {
            const r = resolveTenant(
                "trip_records",
                { taskId: "missing", driverId: "drv1" },
                lookups({ drivers: { drv1: TENANT_A } })
            );
            expect(r?.tenantSource).toBe("driver");
        });

        it("returns undefined when neither link resolves", () => {
            expect(
                resolveTenant("trip_records", { taskId: "t1", driverId: "drv1" }, lookups())
            ).toBeUndefined();
        });
    });

    describe("standby_records", () => {
        it("prefers task over trip over driver", () => {
            const r = resolveTenant(
                "standby_records",
                { taskId: "t1", tripId: "r1", driverId: "drv1" },
                lookups({ tasks: { t1: TENANT_A }, trips: { r1: TENANT_B }, drivers: { drv1: TENANT_WANPEN } })
            );
            expect(r).toEqual({ tenantId: TENANT_A, tenantSource: "task" });
        });

        it("uses the trip when the task is unstamped", () => {
            const r = resolveTenant(
                "standby_records",
                { taskId: "t1", tripId: "r1", driverId: "drv1" },
                lookups({ trips: { r1: TENANT_B }, drivers: { drv1: TENANT_WANPEN } })
            );
            expect(r).toEqual({ tenantId: TENANT_B, tenantSource: "trip" });
        });

        it("uses the driver when neither link is stamped", () => {
            const r = resolveTenant(
                "standby_records",
                { taskId: "t1", tripId: "r1", driverId: "drv1" },
                lookups({ drivers: { drv1: TENANT_WANPEN } })
            );
            expect(r).toEqual({ tenantId: TENANT_WANPEN, tenantSource: "driver" });
        });

        it("resolves a standby with no task/trip link at all via the driver", () => {
            const r = resolveTenant("standby_records", { driverId: "drv1" }, lookups({ drivers: { drv1: TENANT_A } }));
            expect(r).toEqual({ tenantId: TENANT_A, tenantSource: "driver" });
        });
    });

    describe("incidentReport", () => {
        it("prefers the trip link over the driver", () => {
            const r = resolveTenant(
                "incidentReport",
                { tripId: "r1", driverId: "drv1" },
                lookups({ trips: { r1: TENANT_A }, drivers: { drv1: TENANT_B } })
            );
            expect(r).toEqual({ tenantId: TENANT_A, tenantSource: "trip" });
        });

        it("ignores taskId — an incident links to a trip, not a task", () => {
            const r = resolveTenant(
                "incidentReport",
                { taskId: "t1", driverId: "drv1" },
                lookups({ tasks: { t1: TENANT_A }, drivers: { drv1: TENANT_B } })
            );
            expect(r).toEqual({ tenantId: TENANT_B, tenantSource: "driver" });
        });
    });

    describe("drivers", () => {
        it("uses its own subcontractorId", () => {
            const r = resolveTenant("drivers", { subcontractorId: TENANT_A }, lookups({ ownFleetTenantId: TENANT_WANPEN }));
            expect(r).toEqual({ tenantId: TENANT_A, tenantSource: "self" });
        });

        it("falls back to the own-fleet tenant when the driver has no subcontractor", () => {
            const r = resolveTenant("drivers", {}, lookups({ ownFleetTenantId: TENANT_WANPEN }));
            expect(r).toEqual({ tenantId: TENANT_WANPEN, tenantSource: "self" });
        });

        it("treats an empty-string subcontractorId as absent", () => {
            const r = resolveTenant("drivers", { subcontractorId: "  " }, lookups({ ownFleetTenantId: TENANT_WANPEN }));
            expect(r?.tenantId).toBe(TENANT_WANPEN);
        });
    });

    describe("trucks", () => {
        it("uses subcontractorId for a partner truck", () => {
            const r = resolveTenant(
                "trucks",
                { ownershipType: "subcontractor", subcontractorId: TENANT_A },
                lookups({ ownFleetTenantId: TENANT_WANPEN })
            );
            expect(r).toEqual({ tenantId: TENANT_A, tenantSource: "self" });
        });

        it("uses the own-fleet tenant for ownershipType 'own'", () => {
            const r = resolveTenant("trucks", { ownershipType: "own" }, lookups({ ownFleetTenantId: TENANT_WANPEN }));
            expect(r).toEqual({ tenantId: TENANT_WANPEN, tenantSource: "self" });
        });

        it("treats a missing ownershipType as own fleet", () => {
            const r = resolveTenant("trucks", {}, lookups({ ownFleetTenantId: TENANT_WANPEN }));
            expect(r?.tenantId).toBe(TENANT_WANPEN);
        });

        it("does NOT fall back to own fleet for a partner truck missing its subcontractorId", () => {
            // Mislabelling a partner truck as ours is worse than leaving it an orphan we can count.
            const r = resolveTenant(
                "trucks",
                { ownershipType: "subcontractor" },
                lookups({ ownFleetTenantId: TENANT_WANPEN })
            );
            expect(r).toBeUndefined();
        });
    });

    describe("never guesses", () => {
        it("returns undefined for drivers/trucks when ownFleetTenantId is unconfigured", () => {
            expect(resolveTenant("drivers", {}, lookups())).toBeUndefined();
            expect(resolveTenant("trucks", { ownershipType: "own" }, lookups())).toBeUndefined();
        });

        it("ignores non-string link fields instead of coercing them", () => {
            const r = resolveTenant(
                "trip_records",
                { taskId: 123, driverId: "drv1" },
                lookups({ drivers: { drv1: TENANT_A } })
            );
            expect(r).toEqual({ tenantId: TENANT_A, tenantSource: "driver" });
        });

        it("ignores a lookup that returns a blank string", () => {
            const r = resolveTenant(
                "trip_records",
                { taskId: "t1", driverId: "drv1" },
                lookups({ tasks: { t1: "   " }, drivers: { drv1: TENANT_A } })
            );
            expect(r).toEqual({ tenantId: TENANT_A, tenantSource: "driver" });
        });
    });
});

describe("isTenantCollection", () => {
    it("accepts the six in-scope collections", () => {
        for (const c of ["tasks", "trip_records", "standby_records", "incidentReport", "drivers", "trucks"]) {
            expect(isTenantCollection(c)).toBe(true);
        }
    });

    it("rejects collections that are deliberately out of scope (phase 2)", () => {
        expect(isTenantCollection("vehicle_expenses")).toBe(false);
        expect(isTenantCollection("incident_reports")).toBe(false); // real name is incidentReport
    });
});
