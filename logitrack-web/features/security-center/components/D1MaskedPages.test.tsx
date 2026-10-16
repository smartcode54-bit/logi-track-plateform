import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { fakeWeb, json, makeMe } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import { adaptProjection } from "@/lib/projectionMeta";
import { eventsCsv, type SecurityEventDTO } from "../api/events";
import { ActiveUsersPanel } from "./ActiveUsersPanel";
import { SecurityAuditPage } from "./SecurityAuditPage";

const row = {
    id: "u-other", displayName: "****", email: "****", photoUrl: null, status: "active", mustChangePassword: false,
    lastLoginAt: new Date().toISOString(), createdAt: "2026-01-01T00:00:00Z", legacyAuthUid: null,
    memberships: [], scopes: [], platformRoles: [], driver: null, manageAccount: false,
};

afterEach(() => vi.unstubAllGlobals());

describe("D1 platform clients", () => {
    it.each(["platform_admin", "support"] as const)("%s can view masked sessions independently of revoke rights", async (role) => {
        const web = fakeWeb();
        const me = makeMe({ platformRoles: [role], tenant: null, tenants: [], capabilities: ["users:view", "users:view_sessions"] });
        web.on("GET", "/api/go/v1/me", () => json(200, { data: me }));
        web.on("GET", "/api/go/v1/platform/users", () => json(200, { data: [row], meta: { redacted: [
            { path: "/data/0/displayName", kind: "person" }, { path: "/data/0/email", kind: "person" }, { path: "/data/0/photoUrl", kind: "file" },
        ] } }));
        web.on("GET", "/api/go/v1/platform/users/u-other/sessions", () => json(200, { data: [{
            id: "session", platform: "mobile", deviceLabel: "****", userAgent: "****", ip: null,
            appVersion: "3.6.0", installId: null, amr: "pwd", createdAt: row.createdAt, lastSeenAt: row.lastLoginAt,
        }], meta: { redacted: [{ path: "/data/0/deviceLabel", kind: "person" }, { path: "/data/0/userAgent", kind: "person" }, { path: "/data/0/ip", kind: "person" }, { path: "/data/0/installId", kind: "person" }] } }));
        renderWithProviders(<ActiveUsersPanel />);
        await userEvent.setup().click(await screen.findByRole("button", { name: "Sessions" }));
        await screen.findByTestId("session-row");
        expect(screen.getAllByRole("img", { name: "Personal or financial data masked" }).length).toBeGreaterThanOrEqual(5);
        expect(screen.getByRole("button", { name: "End" })).toBeDisabled();
        expect(web.calls.some((c) => c.url === "/api/go/v1/platform/users/u-other/sessions")).toBe(true);
        expect(web.calls.every((c) => c.method === "GET" && c.headers["x-act-on-tenant"] === undefined)).toBe(true);
    });

    it("offers action filters without meta.types and masks only the original evidence fields", async () => {
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe({ platformRoles: ["platform_admin"], capabilities: ["security:view_audit"] }) }));
        web.on("GET", "/api/go/v1/platform/security-events", () => json(200, { data: [{
            id: "event", type: "user_created", severity: "info", createdAt: row.createdAt, summary: "****", actorEmail: "****", actorUserId: null,
            actorLegacyUid: null, targetUserId: null, tenantId: "t", requestId: "r", details: { action: "user_create", basis: "consent", outcome: "allowed", version: "1" },
        }], meta: { redacted: [{ path: "/data/0/actorUserId", kind: "person" }, { path: "/data/0/targetUserId", kind: "person" }] } }));
        renderWithProviders(<SecurityAuditPage />);
        const event = (await screen.findAllByTestId("event-row"))[0];
        expect(JSON.parse(event.querySelector("pre")!.textContent!)).toEqual({
            type: "user_created", details: { action: "user_create", basis: "consent", outcome: "allowed", version: "1" },
            actorUserId: "****", targetUserId: "****", tenantId: "t", requestId: "r",
        });
        const u = userEvent.setup();
        await u.click(screen.getByRole("combobox", { name: "Action Type" }));
        expect(await screen.findByRole("option", { name: "User created" })).toBeInTheDocument();
        await u.click(screen.getByRole("option", { name: "User created" }));
        await screen.findAllByTestId("event-row");
        await waitFor(() => expect(web.calls.some((c) => new URL(c.url, "http://web.test").searchParams.get("type") === "user_created")).toBe(true));
    });

    it("exports redacted identities and null details as masked text", () => {
        const env = adaptProjection({ data: [{ id: "e", type: "user_created", severity: "info", createdAt: row.createdAt, summary: "****", actorEmail: null, actorUserId: null, actorLegacyUid: null, targetUserId: null, details: null, tenantId: "t", requestId: "r" }], meta: { redacted: [
            { path: "/data/0/actorEmail", kind: "person" }, { path: "/data/0/actorUserId", kind: "person" }, { path: "/data/0/targetUserId", kind: "person" }, { path: "/data/0/details", kind: "free_text" },
        ] } });
        const csv = eventsCsv(env.data as SecurityEventDTO[], (key) => key);
        expect(csv).toContain('"****","****","****","****","****"');
        expect(csv).not.toContain("__redactedFields");
    });
});
