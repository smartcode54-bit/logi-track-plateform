// T54S: the pure parts of the Security Center over Go (role-matrix save body, API-key capability
// rules, the audit CSV and range, the overview distribution).
import { describe, expect, it } from "vitest";

import { CAPABILITY_CATALOG } from "shared-docs/schemas/capabilities";
import { queryKeys } from "@/lib/queryKeys";
import { grantableCapabilities, RELEASE_PUBLISHER_CAPABILITY, expiresAtFromInput } from "./apiKeys";
import { eventActor, eventsCsv, eventTarget, rangeStart, securityEventsQuery, type SecurityEventDTO } from "./events";
import { roleDistribution, systemHealthy, type SystemStatusDTO } from "./overview";
import { cellValue, changedCells, matrixOverrides, matrixQuery, roleMatrixKey, type RoleMatrixDTO } from "./roleMatrix";

const cell = (over: Partial<{ default: boolean; effective: boolean; overridden: boolean; overridable: boolean; platformOverride: boolean }> = {}) => ({
    default: false,
    effective: false,
    overridden: false,
    overridable: true,
    ...over,
});

function matrix(): RoleMatrixDTO {
    return {
        layer: "tenant",
        tenantId: "t-own",
        tenantKind: "own_fleet",
        roles: [
            {
                role: "tenant_admin",
                overridable: false,
                capabilities: { "fleet:view_trucks": cell({ default: true, effective: true, overridable: false }) },
            },
            {
                role: "manager",
                overridable: true,
                capabilities: {
                    "fleet:view_trucks": cell({ default: true, effective: true }),
                    "accounting:view_income": cell({ default: true, effective: false, overridden: true }),
                    "hr:view_payroll": cell({ default: false, effective: false }),
                },
            },
        ],
    };
}

describe("role matrix save body (PUT /v1/roles/matrix, complete override set)", () => {
    it("repeats every row the layer holds and adds only real edits of overridable cells", () => {
        const m = matrix();
        expect(matrixOverrides(m, {})).toEqual([{ role: "manager", capability: "accounting:view_income", allowed: false }]);
        const edits = {
            manager: { "hr:view_payroll": true, "fleet:view_trucks": true },
            tenant_admin: { "fleet:view_trucks": false },
        };
        expect(matrixOverrides(m, edits)).toEqual([
            // A held row keeps its row; an edit to the same value as now is no change; tenant_admin is never sent.
            { role: "manager", capability: "accounting:view_income", allowed: false },
            { role: "manager", capability: "hr:view_payroll", allowed: true },
        ]);
    });

    it("an edit of a held row back to the default is sent (Go then drops the row)", () => {
        const m = matrix();
        expect(matrixOverrides(m, { manager: { "accounting:view_income": true } })).toEqual([
            { role: "manager", capability: "accounting:view_income", allowed: true },
        ]);
        expect(changedCells(m, { manager: { "accounting:view_income": true } })).toHaveLength(1);
        expect(changedCells(m, { manager: { "accounting:view_income": false } })).toHaveLength(0);
    });

    it("shows the edit, else the effective value", () => {
        const m = matrix();
        expect(cellValue(m, {}, "manager", "fleet:view_trucks")).toBe(true);
        expect(cellValue(m, { manager: { "fleet:view_trucks": false } }, "manager", "fleet:view_trucks")).toBe(false);
        expect(cellValue(m, {}, "nobody", "x")).toBe(false);
    });

    it("names the layer in the query and the key", () => {
        expect(matrixQuery({ layer: "default" })).toEqual({});
        expect(matrixQuery({ layer: "platform" })).toEqual({ layer: "platform" });
        expect(matrixQuery({ layer: "tenant", tenantId: "t1" })).toEqual({ tenantId: "t1" });
        expect(roleMatrixKey({ layer: "tenant", tenantId: "t1" })).toEqual(["roles", "matrix", { layer: "tenant", tenantId: "t1" }]);
        // The save invalidates ['roles'], which reaches every layer's matrix.
        expect(queryKeys.roles.matrix()).toEqual(["roles", "matrix"]);
    });
});

describe("API key capabilities (iam.validateCreate rules)", () => {
    const classOf = (key: string) => CAPABILITY_CATALOG.find((c) => c.key === key)?.class;

    it("a tenant key offers tenant-class keys the caller holds; a platform admin any of them", () => {
        const held = ["fleet:view_trucks", "security:view_status", "users:view"];
        const own = grantableCapabilities({ scope: "integration", platformKey: false, platformAdmin: false, held }).map((c) => c.key);
        expect(own.sort()).toEqual(["fleet:view_trucks", "users:view"]);
        const all = grantableCapabilities({ scope: "integration", platformKey: false, platformAdmin: true, held: [] });
        expect(all.length).toBeGreaterThan(40);
        expect(all.every((c) => c.class === "tenant")).toBe(true);
    });

    it("a platform key offers global and mobile keys; cf_shim mobile only; release_publisher one key; never platform or dispatch", () => {
        const platform = grantableCapabilities({ scope: "integration", platformKey: true, platformAdmin: true, held: [] });
        expect(platform.every((c) => c.class === "global" || c.class === "self")).toBe(true);
        const shim = grantableCapabilities({ scope: "cf_shim", platformKey: true, platformAdmin: true, held: [] });
        expect(shim.length).toBeGreaterThan(0);
        expect(shim.every((c) => c.key.startsWith("mobile:"))).toBe(true);
        const pub = grantableCapabilities({ scope: "release_publisher", platformKey: true, platformAdmin: true, held: [] }).map((c) => c.key);
        expect(pub).toEqual([RELEASE_PUBLISHER_CAPABILITY]);
        for (const opts of [
            { scope: "integration" as const, platformKey: true },
            { scope: "script" as const, platformKey: false },
        ]) {
            const keys = grantableCapabilities({ ...opts, platformAdmin: true, held: [] }).map((c) => c.key);
            expect(keys.some((k) => classOf(k) === "platform" || classOf(k) === "scope")).toBe(false);
        }
    });

    it("reads an optional expiry", () => {
        expect(expiresAtFromInput("")).toBeNull();
        expect(expiresAtFromInput("not a date")).toBeNull();
        expect(expiresAtFromInput("2027-01-02T03:04")).toMatch(/^2027-01-0\dT\d\d:04:00\.000Z$/);
    });
});

describe("security events helpers", () => {
    const ev = (over: Partial<SecurityEventDTO> = {}): SecurityEventDTO => ({
        id: "e1",
        createdAt: "2026-10-11T01:02:03Z",
        type: "user_created",
        severity: "info",
        summary: "user created",
        details: {},
        actorUserId: "u-1",
        actorEmail: null,
        actorLegacyUid: null,
        targetUserId: "u-2",
        tenantId: "t-1",
        requestId: "r-1",
        ...over,
    });

    it("sends only the set filters", () => {
        expect(securityEventsQuery({ type: "", severity: "", from: "", to: "", limit: 100 })).toEqual({
            type: undefined,
            severity: undefined,
            from: undefined,
            to: undefined,
            limit: 100,
        });
        expect(securityEventsQuery({ type: "login_failed", severity: "warning", from: "2026-10-01T00:00:00.000Z", to: "", limit: 8 })).toMatchObject({
            type: "login_failed",
            severity: "warning",
            from: "2026-10-01T00:00:00.000Z",
            limit: 8,
        });
    });

    it("starts a range at local midnight N days back (a key stable all day)", () => {
        const now = new Date(2026, 9, 11, 15, 30);
        const from = new Date(rangeStart("last7", now));
        expect(from.getDate()).toBe(4);
        expect([from.getHours(), from.getMinutes()]).toEqual([0, 0]);
        expect(rangeStart("last30", now)).toBe(rangeStart("last30", new Date(2026, 9, 11, 23, 59)));
    });

    it("names the actor and the target", () => {
        expect(eventActor(ev({ actorEmail: "a@x.test" }))).toBe("a@x.test");
        expect(eventActor(ev({ actorLegacyUid: "fb-1" }))).toBe("fb-1");
        expect(eventActor(ev({ actorUserId: null }))).toBe("");
        expect(eventTarget(ev({ details: { email: "t@x.test" } }))).toBe("t@x.test");
        expect(eventTarget(ev())).toBe("u-2");
    });

    it("exports CSV with formulas neutralised", () => {
        const csv = eventsCsv([ev({ summary: '=HYPERLINK("http://evil")', details: { name: "+cmd" } })]);
        const [header, line] = csv.split("\n");
        expect(header).toBe("timestamp,type,severity,actor,actorUserId,target,summary,details,tenantId,requestId");
        expect(line).toContain(`"'=HYPERLINK(""http://evil"")"`);
        expect(line).toContain(`"'+cmd"`);
        expect(line.startsWith('"2026-10-11T01:02:03Z"')).toBe(true);
    });
});

describe("overview helpers", () => {
    it("splits the RBAC distribution without going negative", () => {
        const d = roleDistribution({
            scope: { all: false, tenantId: "t" },
            total: 10,
            none: 1,
            tenantRoles: { driver: 4, manager: 2, operation_staff: 1, operator: 1, tenant_admin: 1, user: 0 },
            scopes: { customer: 1, dispatcher: 0 },
            platformRoles: { platform_admin: 0, support: 0 },
        });
        expect(d).toEqual({ total: 10, drivers: 4, operations: 4, others: 2 });
        expect(roleDistribution({ scope: { all: true, tenantId: null }, total: 1, none: 0, tenantRoles: { driver: 1, manager: 1 }, scopes: {}, platformRoles: {} }).others).toBe(0);
    });

    it("calls the platform healthy only with Redis, a scheduler leader and a fresh outbox", () => {
        const s: SystemStatusDTO = {
            database: "ok",
            redis: "ok",
            scheduler: { leaderPresent: true },
            outbox: { pending: 3, oldestSeconds: 4 },
            jobs: { queued: 0, running: 0, failed24h: 0, succeeded24h: 0 },
        };
        expect(systemHealthy(s)).toBe(true);
        expect(systemHealthy({ ...s, redis: "down" })).toBe(false);
        expect(systemHealthy({ ...s, scheduler: { leaderPresent: false } })).toBe(false);
        expect(systemHealthy({ ...s, outbox: { pending: 9, oldestSeconds: 900 } })).toBe(false);
    });
});
