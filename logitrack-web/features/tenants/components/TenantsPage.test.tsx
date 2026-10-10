// T18 owner addition (2026-10-10): a platform admin creates a carrier tenant and its admin in the UI
// (POST /v1/tenants, POST /v1/users {role: tenant_admin, tenantId}), assigns and removes tenant admins
// (PUT/DELETE /v1/tenants/{id}/members/{userId}) and edits names and status (PATCH /v1/tenants/{id}).
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import React from "react";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

vi.mock("@/lib/loginGeo", () => ({ resolveLoginGeoForClient: async () => null }));
const refresh = vi.fn();
vi.mock("next/navigation", () => ({ useRouter: () => ({ refresh, replace: vi.fn(), push: vi.fn() }), usePathname: () => "/app/security-center/tenants" }));

import { TenantsPage } from "./TenantsPage";
import { TenantSwitcher } from "@/components/tenant-switcher";
import { configureFirebaseBridge } from "@/lib/firebaseBridge";
import { fakeWeb, goErr, json, makeMe, type FakeCall } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import type { TenantDTO } from "../api/tenants";

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
    window.history.replaceState(null, "", "/app/security-center/tenants");
    configureFirebaseBridge(async () => {
        throw new Error("no Firebase SDK here");
    });
    refresh.mockReset();
});
afterEach(() => {
    vi.unstubAllGlobals();
    configureFirebaseBridge(undefined);
});

const platformAdmin = makeMe({
    id: "u-pa",
    tenant: null,
    tenants: [],
    platformRoles: ["platform_admin"],
    capabilities: ["platform:manage_tenants", "platform:manage_platform_roles", "users:view", "users:manage", "users:assign_role", "users:revoke_sessions"],
    legacyAuthUid: undefined,
});

function carrier(id: string, over: Partial<TenantDTO> = {}): TenantDTO {
    return { id, kind: "carrier", code: id.toUpperCase(), nameTh: `ขนส่ง ${id}`, nameEn: null, status: "active", legalType: "company", contractorTenantId: null, createdAt: "2026-10-10T00:00:00Z", ...over };
}

describe("TenantsPage", () => {
    it("creates a carrier tenant and its first admin, whose temporary password is shown once", async () => {
        const web = fakeWeb();
        const tenants: TenantDTO[] = [carrier("t-a")];
        const members: { user: { id: string; email: string; displayName: string }; role: string; status: string; lastLoginAt: null }[] = [];
        web.on("GET", "/api/go/v1/me", () => json(200, { data: platformAdmin }));
        web.on("GET", "/api/go/v1/tenants", () => json(200, { data: tenants }));
        web.on("POST", "/api/go/v1/tenants", (c) => {
            const body = c.body as { code: string; nameTh: string };
            const t = carrier("t-new", { code: body.code, nameTh: body.nameTh });
            tenants.push(t);
            return json(201, { data: t });
        });
        web.on("GET", /^\/api\/go\/v1\/tenants\/t-new\/members$/, () => json(200, { data: members }));
        web.on("POST", "/api/go/v1/users", (c) => {
            const body = c.body as { email: string; displayName: string };
            members.push({ user: { id: "u-admin", email: body.email, displayName: body.displayName }, role: "tenant_admin", status: "active", lastLoginAt: null });
            return json(201, { data: { user: { id: "u-admin", email: body.email }, temporaryPassword: "TEMP-PASS-0001" } });
        });
        renderWithProviders(<TenantsPage />);
        const u = userEvent.setup();
        expect(await screen.findAllByTestId("tenant-row")).toHaveLength(1);
        expect(web.calls.find((c) => c.url.startsWith("/api/go/v1/tenants"))?.url).toBe("/api/go/v1/tenants?kind=carrier&limit=50");

        await u.click(screen.getByRole("button", { name: "Create tenant" }));
        const form = await screen.findByRole("dialog");
        await u.type(within(form).getByLabelText("Code"), "NEW");
        await u.type(within(form).getByLabelText("Name (Thai)"), "ขนส่งใหม่");
        await u.click(within(form).getByRole("button", { name: "Create tenant" }));
        await waitFor(() => expect(web.calls.some((c) => c.method === "POST" && c.url === "/api/go/v1/tenants")).toBe(true));
        expect(web.calls.find((c) => c.method === "POST" && c.url === "/api/go/v1/tenants")?.body).toEqual({ kind: "carrier", code: "NEW", nameTh: "ขนส่งใหม่", legalType: "company" });

        // The admins dialog of the new tenant opens by itself.
        expect(await screen.findByText("This tenant has no administrator yet.")).toBeInTheDocument();
        await u.click(screen.getByRole("button", { name: "Create a new administrator" }));
        const create = await screen.findByRole("dialog", { name: "Create a tenant administrator" });
        await u.type(within(create).getByLabelText("Display Name"), "Carrier Admin");
        await u.type(within(create).getByLabelText("Email"), "admin@carrier.test");
        await u.click(within(create).getByRole("button", { name: "Create User" }));

        expect(await screen.findByTestId("temporary-password")).toHaveTextContent("TEMP-PASS-0001");
        expect(web.calls.find((c) => c.method === "POST" && c.url === "/api/go/v1/users")?.body).toEqual({
            email: "admin@carrier.test",
            displayName: "Carrier Admin",
            role: "tenant_admin",
            tenantId: "t-new",
        });
    });

    it("assigns an existing user as tenant admin and removes an admin", async () => {
        const web = fakeWeb();
        let admins = [{ user: { id: "u-old", email: "old@c.test", displayName: "Old" }, role: "tenant_admin", status: "active", lastLoginAt: null }];
        web.on("GET", "/api/go/v1/me", () => json(200, { data: platformAdmin }));
        web.on("GET", "/api/go/v1/tenants", () => json(200, { data: [carrier("t-a")] }));
        web.on("GET", "/api/go/v1/tenants/t-a/members", (c: FakeCall) => {
            expect(c.url).toContain("role=tenant_admin");
            return json(200, { data: admins });
        });
        web.on("GET", "/api/go/v1/users", () =>
            json(200, { data: [{ id: "u-ann", email: "ann@c.test", displayName: "Ann", memberships: [], scopes: [], platformRoles: [], status: "active" }] })
        );
        web.on("PUT", "/api/go/v1/tenants/t-a/members/u-ann", () => {
            admins = [...admins, { user: { id: "u-ann", email: "ann@c.test", displayName: "Ann" }, role: "tenant_admin", status: "active", lastLoginAt: null }];
            return json(200, { data: { role: "tenant_admin" } });
        });
        web.on("DELETE", "/api/go/v1/tenants/t-a/members/u-old", () => {
            admins = admins.filter((a) => a.user.id !== "u-old");
            return new Response(null, { status: 204 });
        });
        renderWithProviders(<TenantsPage />);
        const u = userEvent.setup();
        const [row] = await screen.findAllByTestId("tenant-row");
        await u.click(within(row).getByRole("button", { name: "Actions" }));
        await u.click(await screen.findByRole("menuitem", { name: "Administrators" }));
        expect(await screen.findAllByTestId("tenant-admin")).toHaveLength(1);

        await u.type(screen.getByPlaceholderText("Search users by name or email"), "ann");
        await u.click(await screen.findByRole("button", { name: "Make admin" }));
        await waitFor(() => expect(screen.getAllByTestId("tenant-admin")).toHaveLength(2));
        expect(web.calls.find((c) => c.method === "PUT")?.body).toEqual({ role: "tenant_admin" });
        expect(web.calls.find((c) => c.url.startsWith("/api/go/v1/users?"))?.url).toBe("/api/go/v1/users?q=ann&status=active&sort=last_login_at&limit=50");

        const [old] = screen.getAllByTestId("tenant-admin");
        await u.click(within(old).getByRole("button", { name: "Remove administrator" }));
        await waitFor(() => expect(screen.getAllByTestId("tenant-admin")).toHaveLength(1));
        expect(web.calls.some((c) => c.method === "DELETE" && c.url === "/api/go/v1/tenants/t-a/members/u-old")).toBe(true);
    });

    it("edits names and status with PATCH /v1/tenants/{id}", async () => {
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/me", () => json(200, { data: platformAdmin }));
        web.on("GET", "/api/go/v1/tenants", () => json(200, { data: [carrier("t-a", { nameEn: "Old EN" })] }));
        web.on("PATCH", "/api/go/v1/tenants/t-a", () => json(200, { data: carrier("t-a", { status: "suspended" }) }));
        renderWithProviders(<TenantsPage />);
        const u = userEvent.setup();
        const [row] = await screen.findAllByTestId("tenant-row");
        await u.click(within(row).getByRole("button", { name: "Actions" }));
        await u.click(await screen.findByRole("menuitem", { name: "Edit" }));
        const form = await screen.findByRole("dialog");
        expect(within(form).getByLabelText("Code")).toBeDisabled();
        await u.clear(within(form).getByLabelText("Name (English)"));
        await u.type(within(form).getByLabelText("Name (English)"), "New EN");
        await u.click(within(form).getByRole("combobox", { name: "Status" }));
        await u.click(await screen.findByRole("option", { name: "Suspended" }));
        await u.click(within(form).getByRole("button", { name: "Save Changes" }));
        await waitFor(() => expect(web.calls.some((c) => c.method === "PATCH")).toBe(true));
        expect(web.calls.find((c) => c.method === "PATCH")?.body).toEqual({ nameTh: "ขนส่ง t-a", nameEn: "New EN", status: "suspended" });
    });

    it("is closed to everyone without platform:manage_tenants (no tenant request at all)", async () => {
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe({ legacyAuthUid: undefined }) }));
        web.on("POST", "/api/auth/firebase-token", () => goErr(403, "permission_denied"));
        renderWithProviders(<TenantsPage />);
        expect(await screen.findByText("Only platform administrators can manage tenants.")).toBeInTheDocument();
        expect(web.calls.some((c) => c.url.startsWith("/api/go/v1/tenants"))).toBe(false);
    });
});

describe("TenantSwitcher", () => {
    const tenantA = { id: "t-a", nameTh: "เอ", nameEn: "Alpha", kind: "carrier", role: "tenant_admin", status: "active" };
    const tenantB = { id: "t-b", nameTh: "บี", nameEn: "Beta", kind: "carrier", role: "manager", status: "active" };

    it("is hidden with a single tenant", async () => {
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe({ tenant: tenantA, legacyAuthUid: undefined }) }));
        web.on("GET", "/api/go/v1/me/tenants", () => json(200, { data: [tenantA] }));
        renderWithProviders(<TenantSwitcher />);
        await waitFor(() => expect(web.calls.some((c) => c.url === "/api/go/v1/me/tenants")).toBe(true));
        expect(screen.queryByTestId("tenant-switcher")).toBeNull();
    });

    it("switches the session tenant, refetches ['me'] and re-runs the edge gate for the page", async () => {
        const web = fakeWeb();
        let active = tenantA;
        web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe({ tenant: active, legacyAuthUid: undefined }) }));
        web.on("GET", "/api/go/v1/me/tenants", () => json(200, { data: [tenantA, tenantB] }));
        web.on("POST", "/api/auth/firebase-token", () => goErr(403, "permission_denied"));
        web.on("POST", "/api/auth/tenant", (c) => {
            active = (c.body as { tenantId: string }).tenantId === "t-b" ? tenantB : tenantA;
            return new Response(null, { status: 204 });
        });
        renderWithProviders(<TenantSwitcher />);
        const u = userEvent.setup();
        const box = await screen.findByRole("combobox", { name: "Switch organisation" });
        expect(box).toHaveTextContent("Alpha");
        await u.click(box);
        await u.click(await screen.findByRole("option", { name: /Beta/ }));
        await waitFor(() => expect(refresh).toHaveBeenCalledTimes(1));
        expect(web.calls.find((c) => c.url === "/api/auth/tenant")?.body).toEqual({ tenantId: "t-b" });
        await waitFor(() => expect(screen.getByRole("combobox", { name: "Switch organisation" })).toHaveTextContent("Beta"));
    });
});
