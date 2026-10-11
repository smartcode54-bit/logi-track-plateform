// T54S: the pure parts of the Security Center over Go (role-matrix save body and its fresh read,
// API-key capability rules, the audit CSV and range, the viewer's own cross-tenant reads, the platform
// poll, the overview distribution).
import { afterEach, describe, expect, it, vi } from "vitest";

import { CAPABILITY_CATALOG } from "shared-docs/schemas/capabilities";
import { createQueryClient } from "@/lib/queryClient";
import { queryKeys } from "@/lib/queryKeys";
import { fakeWeb, json } from "@/test-utils/fakeWeb";
import { grantableCapabilities, RELEASE_PUBLISHER_CAPABILITY, expiresAtFromInput } from "./apiKeys";
import {
    eventActor,
    eventsCsv,
    eventTarget,
    isOwnCrossTenantRead,
    rangeStart,
    securityEventsQuery,
    withoutOwnReads,
    type SecurityEventDTO,
} from "./events";
import { roleDistribution, systemHealthy, type SystemStatusDTO } from "./overview";
import { PLATFORM_POLL_MS, reachPollMs } from "./reach";
import {
    cellValue,
    changedCells,
    MatrixChangedError,
    matrixOverrides,
    matrixQuery,
    matrixSignature,
    roleMatrixKey,
    saveRoleMatrixFresh,
    type RoleMatrixDTO,
} from "./roleMatrix";

afterEach(() => vi.unstubAllGlobals());

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

describe("role matrix save against the layer as it is now", () => {
    it("compares two reads of a layer by content, not by order", () => {
        const a = matrix();
        const b = { ...matrix(), roles: [...matrix().roles].reverse() };
        expect(matrixSignature(b)).toBe(matrixSignature(a));
        const c = matrix();
        c.roles[1].capabilities["hr:view_payroll"] = cell({ default: false, effective: true, overridden: true });
        expect(matrixSignature(c)).not.toBe(matrixSignature(a));
        expect(matrixSignature({ ...a, tenantId: "t-other" })).not.toBe(matrixSignature(a));
    });

    it("never deletes a row another admin added after the page loaded: it stops, then saves over the fresh read", async () => {
        // 10:00 the page loads; 10:02 another admin denies hr:view_payroll for the manager; 10:05 this
        // admin toggles an unrelated cell and saves from the 10:00 matrix.
        const shown = matrix();
        const fresh = matrix();
        fresh.roles[1].capabilities["hr:view_payroll"] = cell({ default: true, effective: false, overridden: true });
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/roles/matrix", () => json(200, { data: fresh }));
        web.on("PUT", "/api/go/v1/roles/matrix", () => json(200, { data: fresh, meta: { changes: 1 } }));
        const client = createQueryClient();
        const target = { layer: "default" } as const;
        client.setQueryData(roleMatrixKey(target), shown);
        const edits = { manager: { "fleet:view_trucks": false } };

        // The stale body would have dropped the other admin's row:
        expect(matrixOverrides(shown, edits)).not.toContainEqual({ role: "manager", capability: "hr:view_payroll", allowed: false });

        await expect(saveRoleMatrixFresh(client, target, shown, edits)).rejects.toBeInstanceOf(MatrixChangedError);
        expect(web.count("PUT", "/api/go/v1/roles/matrix")).toBe(0);
        // The fresh read is what the page shows now; the second save goes out with both rows.
        const onScreen = client.getQueryData<RoleMatrixDTO>(roleMatrixKey(target))!;
        expect(matrixSignature(onScreen)).toBe(matrixSignature(fresh));
        await expect(saveRoleMatrixFresh(client, target, onScreen, edits)).resolves.toMatchObject({ changes: 1 });
        expect(web.calls.find((c) => c.method === "PUT")?.body).toEqual({
            overrides: [
                { role: "manager", capability: "fleet:view_trucks", allowed: false },
                { role: "manager", capability: "accounting:view_income", allowed: false },
                { role: "manager", capability: "hr:view_payroll", allowed: false },
            ],
        });
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

describe("the viewer's own cross-tenant reads", () => {
    const access = (over: Partial<SecurityEventDTO> = {}, details: Record<string, unknown> = {}): SecurityEventDTO => ({
        id: "a",
        createdAt: "2026-10-11T03:00:00Z",
        type: "platform_cross_tenant_access",
        severity: "warning",
        summary: "platform principal used X-Act-On-Tenant",
        details: { method: "GET", path: "/v1/security-events", act_on_tenant: "*", outcome: "accepted", ...details },
        actorUserId: "u-me",
        actorEmail: "me@platform.test",
        actorLegacyUid: null,
        targetUserId: null,
        tenantId: null,
        requestId: "r",
        ...over,
    });

    it("matches only the viewer's own accepted GET/HEAD rows", () => {
        expect(isOwnCrossTenantRead(access(), "u-me")).toBe(true);
        expect(isOwnCrossTenantRead(access({}, { method: "HEAD" }), "u-me")).toBe(true);
        expect(isOwnCrossTenantRead(access({ actorUserId: "u-other" }), "u-me")).toBe(false); // someone else's access
        expect(isOwnCrossTenantRead(access({}, { outcome: "refused" }), "u-me")).toBe(false); // a refused attempt
        expect(isOwnCrossTenantRead(access({}, { method: "POST", outcome: "refused" }), "u-me")).toBe(false);
        expect(isOwnCrossTenantRead(access({}, { outcome: undefined }), "u-me")).toBe(false);
        expect(isOwnCrossTenantRead(access({ type: "login_failed" }), "u-me")).toBe(false);
        expect(isOwnCrossTenantRead(access(), undefined)).toBe(false);
    });

    it("hides them unless asked, and counts them either way", () => {
        const rows = [access({ id: "1" }), access({ id: "2", type: "login_lockout", severity: "warning" }), access({ id: "3" })];
        expect(withoutOwnReads(rows, "u-me", false)).toEqual({ rows: [rows[1]], own: 2 });
        expect(withoutOwnReads(rows, "u-me", true)).toEqual({ rows, own: 2 });
        expect(withoutOwnReads(rows, "u-other", false)).toEqual({ rows, own: 0 });
    });

    it("a platform principal's reads refresh by themselves at most every 5 min; a tenant's keep their interval", () => {
        expect(PLATFORM_POLL_MS).toBe(300_000);
        expect(reachPollMs("tenant", 30_000)).toBe(30_000);
        expect(reachPollMs(undefined, 30_000)).toBe(30_000);
        expect(reachPollMs("all", 30_000)).toBe(300_000);
        expect(reachPollMs("all", 600_000)).toBe(600_000);
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
