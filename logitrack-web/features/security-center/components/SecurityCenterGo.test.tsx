// T54S (issue #74, security-center slice): the Security Center pages over Go. Since TW7 the web has no
// Firebase SDK and no bridge (lib/noFirebase.test.ts; firebaseImports.test.ts walks the pages' import graph).
// Every request goes to the BFF (/api/go/v1/...), a tenant principal reads its own reach and a
// platform principal every tenant with X-Act-On-Tenant: *, whose own audit rows the pages hide behind
// a counted toggle.
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import React from "react";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";

import { fakeWeb, json, makeMe, type FakeCall } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import type { MeDTO } from "@/features/auth/api/me";
import { SecurityOverviewPage } from "./SecurityOverviewPage";
import { RoleMatrixPage } from "./RoleMatrixPage";
import { SecurityAuditPage } from "./SecurityAuditPage";
import { ApiKeysPage } from "./ApiKeysPage";
import { SystemStatusPage } from "./SystemStatusPage";
import type { SecurityEventDTO } from "../api/events";
import type { RoleMatrixDTO } from "../api/roleMatrix";
import type { ApiKeyDTO } from "../api/apiKeys";

beforeAll(() => {
    Element.prototype.hasPointerCapture ??= () => false;
    Element.prototype.releasePointerCapture ??= () => undefined;
    Element.prototype.scrollIntoView ??= () => undefined;
    globalThis.ResizeObserver ??= class {
        observe() {}
        unobserve() {}
        disconnect() {}
    } as unknown as typeof ResizeObserver;
});
beforeEach(() => {
    window.history.replaceState(null, "", "/app/security-center");
});
afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
});

const ALL_SECURITY = [
    "users:view",
    "users:manage",
    "users:revoke_sessions",
    "security:view_overview",
    "security:manage_roles",
    "security:view_audit",
    "security:manage_api_keys",
    "security:view_status",
    "security:view_mobile_clients",
    "security:manage_mobile_release",
    "fleet:view_trucks",
];

function setup(meOver: Partial<MeDTO> = {}) {
    const web = fakeWeb();
    const me = makeMe({ id: "u-self", capabilities: ALL_SECURITY, ...meOver });
    web.on("GET", "/api/go/v1/me", () => json(200, { data: me }));
    return { web, me };
}

const PLATFORM: Partial<MeDTO> = { tenant: null, tenants: [], platformRoles: ["platform_admin"] };

function event(n: number, over: Partial<SecurityEventDTO> = {}): SecurityEventDTO {
    return {
        id: `e-${n}`,
        createdAt: new Date(Date.now() - n * 60_000).toISOString(),
        type: "user_created",
        severity: "info",
        summary: `event ${n}`,
        details: {},
        actorUserId: "u-admin",
        actorEmail: "admin@own.test",
        actorLegacyUid: null,
        targetUserId: null,
        tenantId: "t-own",
        requestId: null,
        ...over,
    };
}

const OVERVIEW = {
    generatedAt: "2026-10-11T03:00:00Z",
    scope: { all: false, tenantId: "t-own" },
    users: { total: 12, byStatus: { active: 10, disabled: 2, reset_required: 0, deleted: 1 } },
    sessions: { active: 7, revoked24h: 3, revokedByReason24h: { admin_revoke: 3 } },
    logins: { failed24h: 5, lockouts24h: 1 },
    events: {
        last24h: 4,
        last7d: 9,
        bySeverity24h: { info: 3, warning: 1, critical: 0 },
        hourly24h: Array.from({ length: 24 }, (_, i) => (i === 23 ? 4 : 0)),
        daily7d: [1, 1, 1, 1, 1, 0, 4],
    },
    installations: { total: 40, seen7d: 30, belowFloor: 2, belowLatest: 6, unknownVersion: 1, minAllowedVersion: "3.1.0", latestVersion: "3.2.0" },
    quarantine: null,
    system: { database: "ok", redis: "ok", scheduler: { leaderPresent: true }, outbox: { pending: 0, oldestSeconds: 0 }, jobs: { queued: 0, running: 0, failed24h: 0, succeeded24h: 4 } },
};

const BY_ROLE = {
    scope: { all: false, tenantId: "t-own" },
    total: 12,
    none: 0,
    tenantRoles: { tenant_admin: 1, manager: 2, operation_staff: 1, operator: 1, user: 1, driver: 6 },
    scopes: { customer: 0, dispatcher: 0 },
    platformRoles: { platform_admin: 0, support: 0 },
};

function goUser(id: string, lastLoginAt: string | null) {
    return {
        id,
        email: `${id}@own.test`,
        displayName: `Name ${id}`,
        photoUrl: null,
        status: "active",
        mustChangePassword: false,
        lastLoginAt,
        createdAt: "2026-01-01T00:00:00Z",
        legacyAuthUid: null,
        memberships: [],
        scopes: [],
        platformRoles: [],
        driver: null,
    };
}

const onlyBff = (calls: FakeCall[]) => calls.every((c) => c.url.startsWith("/api/go/") || c.url.startsWith("/api/auth/"));

describe("Security overview (Go)", () => {
    function overviewRoutes(web: ReturnType<typeof setup>["web"]) {
        web.on("GET", "/api/go/v1/security/overview", () => json(200, { data: OVERVIEW }));
        web.on("GET", "/api/go/v1/stats/users-by-role", () => json(200, { data: BY_ROLE }));
        web.on("GET", "/api/go/v1/security-events", () => json(200, { data: [event(1), event(2, { severity: "warning", type: "login_failed" })], meta: { types: ["user_created", "login_failed"] } }));
        web.on("GET", "/api/go/v1/users", () =>
            json(200, { data: [goUser("u-recent", new Date(Date.now() - 86_400_000).toISOString()), goUser("u-old", "2020-01-01T00:00:00Z")] })
        );
    }

    it("renders the server counters, eight recent events and the recently active users from Go only", async () => {
        const { web } = setup();
        overviewRoutes(web);
        renderWithProviders(<SecurityOverviewPage />);

        expect(await screen.findByTestId("card-sessions")).toHaveTextContent("7");
        expect(screen.getByTestId("card-logins")).toHaveTextContent("5");
        expect(screen.getByTestId("card-mobile")).toHaveTextContent("40");
        expect(screen.getByTestId("card-system")).toHaveTextContent("Healthy");
        expect(within(await screen.findByTestId("recent-events")).getAllByRole("listitem")).toHaveLength(2);
        expect(screen.getByTestId("event-histogram").children).toHaveLength(24);
        expect(await screen.findByTestId("rbac-distribution")).toHaveTextContent("Drivers (6 · 50%)");
        // Only the user who signed in within 30 days is listed.
        await waitFor(() => expect(screen.getAllByTestId("active-user-row")).toHaveLength(1));
        expect(screen.getByText("u-recent@own.test")).toBeInTheDocument();

        const events = web.calls.filter((c) => c.url.startsWith("/api/go/v1/security-events"));
        expect(events[0].url).toBe("/api/go/v1/security-events?limit=8");
        expect(onlyBff(web.calls)).toBe(true);
        // A tenant principal reads its own reach.
        expect(web.calls.filter((c) => c.url.startsWith("/api/go/v1/")).every((c) => c.headers["x-act-on-tenant"] === undefined)).toBe(true);
    });

    it("a platform principal reads every tenant with X-Act-On-Tenant: *", async () => {
        const { web } = setup(PLATFORM);
        overviewRoutes(web);
        renderWithProviders(<SecurityOverviewPage />);
        await screen.findByTestId("card-sessions");
        await screen.findByTestId("recent-events");
        await screen.findAllByTestId("active-user-row");
        for (const path of ["/api/go/v1/security/overview", "/api/go/v1/stats/users-by-role", "/api/go/v1/security-events", "/api/go/v1/users"]) {
            const calls = web.calls.filter((c) => c.url.split("?")[0] === path);
            expect(calls.length, path).toBeGreaterThan(0);
            expect(calls.every((c) => c.headers["x-act-on-tenant"] === "*"), path).toBe(true);
        }
        // Enough rows that the 8 newest events other than the viewer's own reads are among them.
        expect(web.calls.find((c) => c.url.startsWith("/api/go/v1/security-events"))?.url).toBe("/api/go/v1/security-events?limit=50");
        expect(screen.getByTestId("histogram-own-reads")).toHaveTextContent("include the audit rows of your own all-tenant reads");
    });

    it("a platform principal's recent events leave out its own cross-tenant reads, counted behind a toggle", async () => {
        const { web } = setup(PLATFORM);
        overviewRoutes(web);
        // What Go returns under *: the request's own access row on top (it commits before the read),
        // earlier reads of the same tab, someone else's access, a refused attempt of the viewer, a real event.
        const own = (n: number, details: Record<string, unknown> = {}) =>
            event(n, {
                type: "platform_cross_tenant_access",
                severity: "warning",
                summary: `own read ${n}`,
                actorUserId: "u-self",
                tenantId: null,
                details: { method: "GET", path: "/v1/security-events", act_on_tenant: "*", outcome: "accepted", ...details },
            });
        web.handlers.unshift((c) =>
            c.method === "GET" && c.url.startsWith("/api/go/v1/security-events")
                ? json(200, {
                      data: [
                          own(0),
                          own(1),
                          event(2, { type: "platform_cross_tenant_access", severity: "warning", summary: "support read", actorUserId: "u-support", details: { method: "GET", outcome: "accepted" } }),
                          own(3, { outcome: "refused" }),
                          event(4, { type: "refresh_token_reuse", severity: "critical", summary: "token reuse" }),
                          own(5),
                      ],
                  })
                : undefined
        );
        renderWithProviders(<SecurityOverviewPage />);
        const u = userEvent.setup();
        const list = await screen.findByTestId("recent-events");
        expect(within(list).getAllByRole("listitem").map((li) => li.querySelector("p")?.textContent)).toEqual(["support read", "own read 3", "token reuse"]);
        expect(screen.getByTestId("own-reads")).toHaveTextContent("Show my own cross-tenant reads (3)");
        expect(screen.getByTestId("own-reads")).toHaveTextContent("Hidden, not deleted");

        await u.click(within(screen.getByTestId("own-reads")).getByRole("checkbox"));
        await waitFor(() => expect(within(screen.getByTestId("recent-events")).getAllByRole("listitem")).toHaveLength(6));
        expect(screen.getByTestId("own-reads")).not.toHaveTextContent("Hidden, not deleted");
    });

    it("says so when the newest events are all the viewer's own reads", async () => {
        const { web } = setup(PLATFORM);
        overviewRoutes(web);
        web.handlers.unshift((c) =>
            c.method === "GET" && c.url.startsWith("/api/go/v1/security-events")
                ? json(200, {
                      data: [0, 1].map((n) =>
                          event(n, { type: "platform_cross_tenant_access", actorUserId: "u-self", tenantId: null, details: { method: "GET", outcome: "accepted" } })
                      ),
                  })
                : undefined
        );
        renderWithProviders(<SecurityOverviewPage />);
        expect(await screen.findByText("The 2 newest events are all your own cross-tenant reads.")).toBeInTheDocument();
        expect(screen.getByTestId("own-reads")).toHaveTextContent("(2)");
    });

    it("lists a user's live sessions and ends one, or all of them; never the caller's own", async () => {
        const { web } = setup();
        overviewRoutes(web);
        const recent = new Date(Date.now() - 3_600_000).toISOString();
        web.handlers.unshift((c) =>
            c.method === "GET" && c.url.startsWith("/api/go/v1/users?")
                ? json(200, { data: [goUser("u-self", recent), goUser("u-2", recent)] })
                : undefined
        );
        web.on("GET", "/api/go/v1/users/u-2/sessions", () =>
            json(200, {
                data: [
                    { id: "s-1", platform: "web", amr: "pwd", installId: null, deviceLabel: null, appVersion: null, ip: "203.0.113.9", userAgent: "Firefox", createdAt: recent, lastSeenAt: recent },
                ],
            })
        );
        web.on("DELETE", "/api/go/v1/users/u-2/sessions/s-1", () => new Response(null, { status: 204 }));
        web.on("DELETE", "/api/go/v1/users/u-2/sessions", () => new Response(null, { status: 204 }));
        renderWithProviders(<SecurityOverviewPage />);
        const u = userEvent.setup();
        const rows = await screen.findAllByTestId("active-user-row");
        expect(within(rows[0]).getByRole("button", { name: /Sessions/ })).toBeDisabled();
        await u.click(within(rows[1]).getByRole("button", { name: /Sessions/ }));
        const dialog = await screen.findByRole("dialog");
        expect(await within(dialog).findByText("203.0.113.9")).toBeInTheDocument();
        await u.click(within(dialog).getByRole("button", { name: /End/ }));
        await waitFor(() => expect(web.count("DELETE", "/api/go/v1/users/u-2/sessions/s-1")).toBe(1));
        await u.click(within(dialog).getByRole("button", { name: "Sign out everywhere" }));
        await waitFor(() => expect(web.count("DELETE", "/api/go/v1/users/u-2/sessions")).toBe(1));
    });
});

describe("Role matrix (Go)", () => {
    const KEYS = ["operations:view_first_mile", "operations:manage_tasks"];
    function roleMatrix(): RoleMatrixDTO {
        const roles = ["tenant_admin", "manager", "operation_staff", "operator", "user", "driver"];
        return {
            layer: "tenant",
            tenantId: "t-own",
            tenantKind: "own_fleet",
            roles: roles.map((role) => ({
                role,
                overridable: role !== "tenant_admin",
                capabilities: Object.fromEntries(
                    KEYS.map((k) => [
                        k,
                        {
                            default: role !== "driver",
                            effective: role !== "driver" && !(role === "operator" && k === "operations:manage_tasks"),
                            overridden: role === "operator" && k === "operations:manage_tasks",
                            overridable: role !== "tenant_admin" && role !== "driver",
                        },
                    ])
                ),
            })),
        };
    }

    it("loads the whole matrix in one call and saves the layer's complete override set", async () => {
        const { web } = setup();
        web.on("GET", "/api/go/v1/stats/users-by-role", () => json(200, { data: BY_ROLE }));
        web.on("GET", "/api/go/v1/roles/matrix", () => json(200, { data: roleMatrix() }));
        web.on("PUT", "/api/go/v1/roles/matrix", () => json(200, { data: roleMatrix(), meta: { changes: 1 } }));
        renderWithProviders(<RoleMatrixPage />);
        const u = userEvent.setup();

        await screen.findAllByTestId("matrix-row");
        expect(web.count("GET", "/api/go/v1/roles/matrix")).toBe(1);
        expect(web.calls.find((c) => c.url.startsWith("/api/go/v1/roles/matrix"))?.url).toBe("/api/go/v1/roles/matrix");
        expect(screen.getByRole("checkbox", { name: "Tenant admin: Manage tasks" })).toBeDisabled();
        expect(screen.getByRole("checkbox", { name: "Driver: Manage tasks" })).toBeDisabled();
        expect(await screen.findByTestId("role-counts")).toHaveTextContent("6");

        await u.click(screen.getByRole("checkbox", { name: "Manager: Manage tasks" }));
        expect(screen.getByTestId("pending-changes")).toHaveTextContent("1 unsaved changes");
        await u.click(screen.getByRole("button", { name: "Save Configuration" }));
        await waitFor(() => expect(web.count("PUT", "/api/go/v1/roles/matrix")).toBe(1));
        expect(web.calls.find((c) => c.method === "PUT")?.body).toEqual({
            overrides: [
                { role: "manager", capability: "operations:manage_tasks", allowed: false },
                { role: "operator", capability: "operations:manage_tasks", allowed: false },
            ],
        });
        // The save reads the layer once more before the PUT, and its success invalidates ['roles'] and
        // ['me']: both are read again.
        const put = web.calls.findIndex((c) => c.method === "PUT");
        expect(web.calls.slice(0, put).filter((c) => c.method === "GET" && c.url.startsWith("/api/go/v1/roles/matrix"))).toHaveLength(2);
        await waitFor(() => expect(web.count("GET", "/api/go/v1/roles/matrix")).toBe(3));
        await waitFor(() => expect(web.count("GET", "/api/go/v1/me")).toBeGreaterThan(1));
        expect(onlyBff(web.calls)).toBe(true);
    });

    it("a save over a matrix someone else changed meanwhile writes nothing, shows their change and keeps the edit", async () => {
        const { web } = setup();
        web.on("GET", "/api/go/v1/stats/users-by-role", () => json(200, { data: BY_ROLE }));
        let current = roleMatrix();
        web.on("GET", "/api/go/v1/roles/matrix", () => json(200, { data: current }));
        web.on("PUT", "/api/go/v1/roles/matrix", () => json(200, { data: current, meta: { changes: 1 } }));
        const warn = vi.spyOn(toast, "warning");
        renderWithProviders(<RoleMatrixPage />);
        const u = userEvent.setup();
        await screen.findAllByTestId("matrix-row");

        // Another admin denies "View first mile" for the operator after this page loaded.
        current = roleMatrix();
        const operator = current.roles.find((r) => r.role === "operator")!;
        operator.capabilities["operations:view_first_mile"] = { default: true, effective: false, overridden: true, overridable: true };

        await u.click(screen.getByRole("checkbox", { name: "Manager: Manage tasks" }));
        await u.click(screen.getByRole("button", { name: "Save Configuration" }));
        await waitFor(() => expect(warn).toHaveBeenCalledWith(expect.stringContaining("Someone else changed this matrix after it loaded")));
        expect(web.count("PUT", "/api/go/v1/roles/matrix")).toBe(0);
        // Their row is on screen now, and the edit is still pending.
        await waitFor(() => expect(screen.getByRole("checkbox", { name: "Operator: View First Mile" })).not.toBeChecked());
        expect(screen.getByTestId("pending-changes")).toHaveTextContent("1 unsaved changes");

        await u.click(screen.getByRole("button", { name: "Save Configuration" }));
        await waitFor(() => expect(web.count("PUT", "/api/go/v1/roles/matrix")).toBe(1));
        expect(web.calls.find((c) => c.method === "PUT")?.body).toEqual({
            overrides: [
                { role: "manager", capability: "operations:manage_tasks", allowed: false },
                { role: "operator", capability: "operations:view_first_mile", allowed: false },
                { role: "operator", capability: "operations:manage_tasks", allowed: false },
            ],
        });
    });

    it("a platform admin without a tenant edits the platform-wide layer and may pick a tenant", async () => {
        const { web } = setup(PLATFORM);
        web.on("GET", "/api/go/v1/stats/users-by-role", () => json(200, { data: BY_ROLE }));
        web.on("GET", "/api/go/v1/tenants", () =>
            json(200, { data: [{ id: "t-car", kind: "carrier", code: "C1", nameTh: "ขนส่งหนึ่ง", nameEn: "Carrier One", status: "active", legalType: "company", contractorTenantId: null, createdAt: "2026-01-01T00:00:00Z" }] })
        );
        web.on("GET", "/api/go/v1/roles/matrix", () => json(200, { data: { ...roleMatrix(), layer: "platform", tenantId: null, tenantKind: null } }));
        renderWithProviders(<RoleMatrixPage />);
        const u = userEvent.setup();
        await screen.findAllByTestId("matrix-row");
        expect(web.calls.find((c) => c.url.startsWith("/api/go/v1/roles/matrix"))?.url).toBe("/api/go/v1/roles/matrix?layer=platform");
        await u.click(screen.getByRole("combobox", { name: "Layer" }));
        await u.click(await screen.findByRole("option", { name: "Carrier One" }));
        await waitFor(() => expect(web.calls.some((c) => c.url === "/api/go/v1/roles/matrix?tenantId=t-car")).toBe(true));
    });
});

describe("Security audit (Go)", () => {
    it("pages back with the keyset cursor, sends the filters to Go and searches only the loaded rows", async () => {
        const { web } = setup();
        web.on("GET", "/api/go/v1/security-events", (c) => {
            const url = new URL(c.url, "http://web.test");
            if (url.searchParams.get("cursor") === "next-1") return json(200, { data: [event(3, { summary: "older one" })] });
            return json(200, { data: [event(1, { summary: "first" }), event(2, { summary: "second", severity: "critical" })], nextCursor: "next-1", meta: { types: ["user_created"] } });
        });
        renderWithProviders(<SecurityAuditPage />);
        const u = userEvent.setup();
        await waitFor(() => expect(screen.getAllByTestId("event-row")).toHaveLength(2));
        const first = new URL(web.calls.find((c) => c.url.startsWith("/api/go/v1/security-events"))!.url, "http://web.test");
        expect(first.searchParams.get("limit")).toBe("100");
        expect(first.searchParams.get("from")).toMatch(/^\d{4}-\d\d-\d\dT/);
        expect(first.searchParams.has("cursor")).toBe(false);

        await u.click(screen.getByRole("button", { name: "Load more" }));
        await waitFor(() => expect(screen.getAllByTestId("event-row")).toHaveLength(3));
        expect(screen.getByText("All rows in this range are loaded.")).toBeInTheDocument();

        await u.type(screen.getByRole("textbox", { name: "Search logs..." }), "older");
        await waitFor(() => expect(screen.getAllByTestId("event-row")).toHaveLength(1));
        expect(screen.getByTestId("search-scope")).toHaveTextContent("3 loaded rows");

        await u.click(screen.getByRole("combobox", { name: "Severity" }));
        await u.click(await screen.findByRole("option", { name: "Critical" }));
        await waitFor(() =>
            expect(web.calls.some((c) => c.url.startsWith("/api/go/v1/security-events") && new URL(c.url, "http://web.test").searchParams.get("severity") === "critical")).toBe(true)
        );
        expect(onlyBff(web.calls)).toBe(true);
    });
});

describe("Security audit (Go): a platform principal's own reads and the poll", () => {
    it("hides the viewer's own cross-tenant reads from the rows, the KPIs and the export unless asked, and stops polling after load more", async () => {
        const { web } = setup(PLATFORM);
        const own = (n: number) =>
            event(n, { type: "platform_cross_tenant_access", severity: "warning", summary: `own ${n}`, actorUserId: "u-self", tenantId: null, details: { method: "GET", outcome: "accepted" } });
        web.on("GET", "/api/go/v1/security-events", (c) => {
            const url = new URL(c.url, "http://web.test");
            if (url.searchParams.get("cursor") === "next-1") return json(200, { data: [own(5), event(6, { summary: "older" })] });
            return json(200, { data: [own(1), own(2), event(3, { summary: "real", severity: "critical" })], nextCursor: "next-1", meta: { types: [] } });
        });
        renderWithProviders(<SecurityAuditPage />);
        const u = userEvent.setup();
        await waitFor(() => expect(screen.getAllByTestId("event-row")).toHaveLength(1));
        expect(screen.getByText("Warning / critical").parentElement).toHaveTextContent("1"); // the critical one, not the 2 own warnings
        expect(screen.getByTestId("own-reads")).toHaveTextContent("(2)");
        expect(screen.getByTestId("loaded-count")).toHaveTextContent("Showing 1 of 3 loaded rows");
        expect(screen.queryByTestId("poll-paused")).toBeNull();

        await u.click(within(screen.getByTestId("own-reads")).getByRole("checkbox"));
        await waitFor(() => expect(screen.getAllByTestId("event-row")).toHaveLength(3));
        expect(screen.getByText("Warning / critical").parentElement).toHaveTextContent("3");
        await u.click(within(screen.getByTestId("own-reads")).getByRole("checkbox"));

        await u.click(screen.getByRole("button", { name: "Load more" }));
        await waitFor(() => expect(screen.getAllByTestId("event-row")).toHaveLength(2));
        expect(screen.getByTestId("own-reads")).toHaveTextContent("(3)");
        expect(screen.getByTestId("poll-paused")).toHaveTextContent("Auto-refresh is paused while older pages are loaded");
        expect(web.calls.filter((c) => c.url.startsWith("/api/go/v1/security-events")).every((c) => c.headers["x-act-on-tenant"] === "*")).toBe(true);
    });
});

describe("API keys (Go)", () => {
    function key(over: Partial<ApiKeyDTO> = {}): ApiKeyDTO {
        return {
            id: "k-1",
            name: "Partner feed",
            scope: "integration",
            keyPrefix: "lt_ab12cd",
            capabilities: ["fleet:view_trucks"],
            tenantId: "t-own",
            tenantNameTh: "กองรถ",
            tenantNameEn: "Own fleet",
            ratePerMin: 600,
            status: "active",
            createdBy: "u-self",
            createdByEmail: "admin@own.test",
            createdAt: "2026-10-01T00:00:00Z",
            expiresAt: null,
            lastUsedAt: null,
            revokedAt: null,
            revokedBy: null,
            ...over,
        };
    }

    it("lists the keys, shows a new secret once and keeps it out of the cache, and revokes", async () => {
        const { web } = setup();
        let keys = [key()];
        web.on("GET", "/api/go/v1/api-keys", () => json(200, { data: keys }));
        web.on("POST", "/api/go/v1/api-keys", (c) => {
            const body = c.body as { name: string };
            const created = key({ id: "k-2", name: body.name, keyPrefix: "lt_new000" });
            keys = [created, ...keys];
            return json(201, { data: { ...created, secret: "lt_new000.SHOWN-ONCE-SECRET" } });
        });
        web.on("DELETE", "/api/go/v1/api-keys/k-1", () => new Response(null, { status: 204 }));
        const { client } = renderWithProviders(<ApiKeysPage />);
        const u = userEvent.setup();

        await waitFor(() => expect(screen.getAllByTestId("api-key-row")).toHaveLength(1));
        expect(web.calls.find((c) => c.url.startsWith("/api/go/v1/api-keys"))?.url).toBe("/api/go/v1/api-keys?limit=50");

        await u.click(screen.getByRole("button", { name: /Create API key/ }));
        const dialog = await screen.findByRole("dialog");
        await u.type(within(dialog).getByLabelText("Name"), "Nightly export");
        // A tenant caller is offered only the tenant keys it holds.
        const offered = within(within(dialog).getByTestId("api-key-capabilities")).getAllByRole("checkbox").map((c) => c.getAttribute("aria-label"));
        expect(offered.sort()).toEqual(["fleet:view_trucks", "security:manage_api_keys", "security:manage_roles", "security:view_audit", "security:view_mobile_clients", "security:view_overview", "users:manage", "users:revoke_sessions", "users:view"].sort());
        await u.click(within(dialog).getByRole("checkbox", { name: "fleet:view_trucks" }));
        await u.click(within(dialog).getByRole("button", { name: "Create API key" }));

        expect(await screen.findByTestId("api-key-secret")).toHaveTextContent("lt_new000.SHOWN-ONCE-SECRET");
        expect(web.calls.find((c) => c.method === "POST")?.body).toEqual({
            name: "Nightly export",
            scope: "integration",
            capabilities: ["fleet:view_trucks"],
            tenantId: "t-own",
            expiresAt: null,
        });
        const cached = JSON.stringify([...client.getQueryCache().getAll().map((q) => q.state.data), ...client.getMutationCache().getAll().map((m) => m.state.data)]);
        expect(cached).not.toContain("SHOWN-ONCE-SECRET");
        await u.click(screen.getByRole("button", { name: "Done" }));
        await waitFor(() => expect(screen.queryByTestId("api-key-secret")).toBeNull());
        await waitFor(() => expect(screen.getAllByTestId("api-key-row")).toHaveLength(2));

        const row = screen.getAllByTestId("api-key-row").find((r) => within(r).queryByText("Partner feed"))!;
        await u.click(within(row).getByRole("button", { name: /Revoke/ }));
        await u.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Revoke key" }));
        await waitFor(() => expect(web.count("DELETE", "/api/go/v1/api-keys/k-1")).toBe(1));
        expect(onlyBff(web.calls)).toBe(true);
    });

    it("a platform admin lists every key with X-Act-On-Tenant: * and may create a release_publisher platform key", async () => {
        const { web } = setup(PLATFORM);
        web.on("GET", "/api/go/v1/api-keys", () => json(200, { data: [key({ tenantId: null, tenantNameTh: null, tenantNameEn: null, scope: "release_publisher" })] }));
        web.on("GET", "/api/go/v1/tenants", () => json(200, { data: [] }));
        web.on("POST", "/api/go/v1/api-keys", () => json(201, { data: { ...key({ id: "k-9" }), secret: "x" } }));
        renderWithProviders(<ApiKeysPage />);
        const u = userEvent.setup();
        const [row] = await screen.findAllByTestId("api-key-row");
        expect(within(row).getByText("Platform")).toBeInTheDocument();
        expect(web.calls.filter((c) => c.url.startsWith("/api/go/v1/api-keys")).every((c) => c.headers["x-act-on-tenant"] === "*")).toBe(true);

        await u.click(screen.getByRole("button", { name: /Create API key/ }));
        const dialog = await screen.findByRole("dialog");
        await u.type(within(dialog).getByLabelText("Name"), "cmd/release");
        await u.click(within(dialog).getByRole("combobox", { name: "Used for" }));
        await u.click(await screen.findByRole("option", { name: "Release publisher" }));
        expect(within(dialog).getByRole("checkbox", { name: "security:manage_mobile_release" })).toBeChecked();
        await u.click(within(dialog).getByRole("button", { name: "Create API key" }));
        await waitFor(() => expect(web.count("POST", "/api/go/v1/api-keys")).toBe(1));
        const post = web.calls.find((c) => c.method === "POST");
        expect(post?.body).toEqual({ name: "cmd/release", scope: "release_publisher", capabilities: ["security:manage_mobile_release"], tenantId: null, expiresAt: null });
        // Writes never carry the cross-tenant header.
        expect(post?.headers["x-act-on-tenant"]).toBeUndefined();
    });
});

describe("System status (Go)", () => {
    it("shows the platform state from GET /v1/security/status", async () => {
        const { web } = setup();
        web.on("GET", "/api/go/v1/security/status", () =>
            json(200, { data: { ...OVERVIEW.system, redis: "down", jobs: { queued: 2, running: 1, failed24h: 3, succeeded24h: 9 } } })
        );
        renderWithProviders(<SystemStatusPage />);
        expect(await screen.findByTestId("status-summary")).toHaveTextContent("Needs attention");
        expect(screen.getAllByTestId("status-row")).toHaveLength(4);
        expect(screen.getByTestId("status-jobs")).toHaveTextContent("3");
        expect(web.calls.filter((c) => c.url.startsWith("/api/go/v1/security/status")).every((c) => c.headers["x-act-on-tenant"] === undefined)).toBe(true);
    });
});
