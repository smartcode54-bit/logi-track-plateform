// T18 (R49, R81; Appendix E §E.5 row 14, §E.10): the Security Center users page on Go. Firebase is not
// reachable from this page at all (the module mocks below throw on import), and every request goes to
// the BFF (/api/go/v1/users*, /api/go/v1/tenants/{id}/members/*).
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import React from "react";
import { act, renderHook, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider } from "@tanstack/react-query";

vi.mock("@/firebase/client", () => {
    throw new Error("the users page must not load the Firebase client");
});
vi.mock("firebase/functions", () => {
    throw new Error("the users page must not call Cloud Functions");
});
vi.mock("@/lib/loginGeo", () => ({ resolveLoginGeoForClient: async () => null }));

import { UsersPage } from "./UsersPage";
import { USERS_PAGE_SIZE, useUsers, type UserDTO } from "../api/users";
import { configureFirebaseBridge } from "@/lib/firebaseBridge";
import { createQueryClient } from "@/lib/queryClient";
import { fakeWeb, goErr, json, makeMe, type FakeCall } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";

beforeAll(() => {
    // Radix pointer handling in jsdom.
    Element.prototype.hasPointerCapture ??= () => false;
    Element.prototype.releasePointerCapture ??= () => undefined;
    Element.prototype.scrollIntoView ??= () => undefined;
    globalThis.ResizeObserver ??= class {
        observe() {}
        unobserve() {}
        disconnect() {}
    } as unknown as typeof ResizeObserver;
});

function user(n: number, over: Partial<UserDTO> = {}): UserDTO {
    return {
        id: `u-${n}`,
        email: `user${n}@own.test`,
        displayName: `User ${n}`,
        photoUrl: null,
        status: "active",
        mustChangePassword: false,
        lastLoginAt: "2026-10-01T03:00:00Z",
        createdAt: "2026-01-01T00:00:00Z",
        legacyAuthUid: `fb-${n}`,
        memberships: [{ tenantId: "t-own", tenantNameTh: "กองรถ", tenantNameEn: "Own fleet", tenantKind: "own_fleet", role: "operator", status: "active" }],
        scopes: [],
        platformRoles: [],
        driver: null,
        ...over,
    };
}

/** GET /v1/users over `total` users, `USERS_PAGE_SIZE` per page; the cursor is the next index. */
function usersRoute(total: () => UserDTO[]) {
    return (call: FakeCall) => {
        const url = new URL(call.url, "http://web.test");
        const from = Number(url.searchParams.get("cursor") ?? "0");
        const all = total();
        const page = all.slice(from, from + USERS_PAGE_SIZE);
        const next = from + USERS_PAGE_SIZE < all.length ? String(from + USERS_PAGE_SIZE) : undefined;
        return json(200, { data: page, ...(next ? { nextCursor: next } : {}) });
    };
}

beforeEach(() => {
    window.history.replaceState(null, "", "/app/security-center/users");
    // The bridge is irrelevant here: Go refuses it, so no Firebase SDK is loaded.
    configureFirebaseBridge(async () => {
        throw new Error("no Firebase SDK on the users page");
    });
});
afterEach(() => {
    vi.unstubAllGlobals();
    configureFirebaseBridge(undefined);
});

describe("useUsers", () => {
    it("pages past 1000 users with the keyset cursor (no 50-row cap, no listUsers(1000))", async () => {
        const web = fakeWeb();
        const many = Array.from({ length: 1050 }, (_, i) => user(i));
        web.on("GET", "/api/go/v1/users", usersRoute(() => many));
        const client = createQueryClient();
        const { result } = renderHook(() => useUsers({ q: "", role: "", status: "" }), {
            wrapper: ({ children }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>,
        });
        await waitFor(() => expect(result.current.data?.pages).toHaveLength(1));
        while (result.current.hasNextPage) {
            await act(async () => {
                await result.current.fetchNextPage();
            });
        }
        const rows = result.current.data!.pages.flatMap((p) => p.data);
        expect(rows).toHaveLength(1050);
        expect(new Set(rows.map((r) => r.id)).size).toBe(1050);
        const urls = web.calls.map((c) => c.url);
        expect(urls[0]).toBe("/api/go/v1/users?sort=last_login_at&limit=50");
        expect(urls.at(-1)).toBe("/api/go/v1/users?sort=last_login_at&limit=50&cursor=1000");
        expect(urls).toHaveLength(21);
    });

    it("sends search, role and status to Go (never filters in the browser)", async () => {
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/users", () => json(200, { data: [] }));
        const client = createQueryClient();
        renderHook(() => useUsers({ q: " ann ", role: "manager", status: "disabled" }), {
            wrapper: ({ children }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>,
        });
        await waitFor(() => expect(web.calls).toHaveLength(1));
        expect(web.calls[0].url).toBe("/api/go/v1/users?q=ann&role=manager&status=disabled&sort=last_login_at&limit=50");
    });
});

describe("UsersPage", () => {
    function setup(meOver = {}) {
        const web = fakeWeb();
        const me = makeMe({ id: "u-self", legacyAuthUid: undefined, ...meOver });
        web.on("GET", "/api/go/v1/me", () => json(200, { data: me }));
        web.on("POST", "/api/auth/firebase-token", () => goErr(403, "permission_denied"));
        return { web, me };
    }

    it("lists through Go, loads more, and makes no Firebase or Cloud Functions call", async () => {
        const { web } = setup();
        const people = Array.from({ length: 60 }, (_, i) => user(i));
        web.on("GET", "/api/go/v1/users", usersRoute(() => people));
        renderWithProviders(<UsersPage />);
        await waitFor(() => expect(screen.getAllByTestId("user-row")).toHaveLength(50));
        await userEvent.setup().click(screen.getByRole("button", { name: "Load more" }));
        await waitFor(() => expect(screen.getAllByTestId("user-row")).toHaveLength(60));
        expect(screen.getByTestId("users-count")).toHaveTextContent("60 users shown");
        expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
        expect(web.calls.every((c) => c.url.startsWith("/api/go/") || c.url.startsWith("/api/auth/"))).toBe(true);
    });

    it("creates a user and shows the temporary password once (R29)", async () => {
        const { web } = setup();
        let created: UserDTO | null = null;
        web.on("GET", "/api/go/v1/users", () => json(200, { data: created ? [created] : [] }));
        web.on("POST", "/api/go/v1/users", (c) => {
            const body = c.body as { email: string; displayName: string };
            created = user(500, { email: body.email, displayName: body.displayName, mustChangePassword: true });
            return json(201, { data: { user: created, temporaryPassword: "SHOWN-ONCE-0001" } });
        });
        renderWithProviders(<UsersPage />);
        const u = userEvent.setup();
        await u.click(await screen.findByRole("button", { name: /Add User/ }));
        const dialog = await screen.findByRole("dialog");
        await u.type(within(dialog).getByLabelText("Display Name"), "New Person");
        await u.type(within(dialog).getByLabelText("Email"), "new@own.test");
        await u.click(within(dialog).getByRole("button", { name: "Create User" }));

        expect(await screen.findByTestId("temporary-password")).toHaveTextContent("SHOWN-ONCE-0001");
        const post = web.calls.find((c) => c.method === "POST" && c.url === "/api/go/v1/users");
        expect(post?.body).toEqual({ email: "new@own.test", displayName: "New Person", role: "user" });
        await u.click(screen.getByRole("button", { name: "Done" }));
        await waitFor(() => expect(screen.queryByTestId("temporary-password")).toBeNull());
        expect(await screen.findByText("Must set a new password at next sign-in")).toBeInTheDocument();
    });

    it("disables with a reason, revokes sessions, and never offers either on the caller's own row", async () => {
        const { web } = setup();
        const self = user(0, { id: "u-self", displayName: "Me Myself" });
        const other = user(1);
        web.on("GET", "/api/go/v1/users", () => json(200, { data: [self, other] }));
        web.on("POST", "/api/go/v1/users/u-1/disable", () => new Response(null, { status: 204 }));
        web.on("DELETE", "/api/go/v1/users/u-1/sessions", () => new Response(null, { status: 204 }));
        renderWithProviders(<UsersPage />);
        const u = userEvent.setup();
        const rows = await screen.findAllByTestId("user-row");
        expect(within(rows[0]).getByRole("switch")).toBeDisabled();

        await u.click(within(rows[1]).getByRole("switch"));
        const confirm = await screen.findByRole("dialog");
        await u.type(within(confirm).getByLabelText("Reason (optional)"), "left the company");
        await u.click(within(confirm).getByRole("button", { name: "Disable" }));
        await waitFor(() => expect(web.calls.some((c) => c.url === "/api/go/v1/users/u-1/disable")).toBe(true));
        expect(web.calls.find((c) => c.url === "/api/go/v1/users/u-1/disable")?.body).toEqual({ reason: "left the company" });

        await u.click(within(rows[1]).getByRole("button", { name: "Actions" }));
        await u.click(await screen.findByRole("menuitem", { name: "Force logout" }));
        await u.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Sign out everywhere" }));
        await waitFor(() => expect(web.calls.some((c) => c.method === "DELETE" && c.url === "/api/go/v1/users/u-1/sessions")).toBe(true));

        await u.click(within(rows[0]).getByRole("button", { name: "Actions" }));
        expect(await screen.findByRole("menuitem", { name: "Force logout" })).toHaveAttribute("aria-disabled", "true");
    });

    it("changes the tenant role through PUT /v1/tenants/{id}/members/{userId}", async () => {
        const { web } = setup();
        const other = user(2);
        web.on("GET", "/api/go/v1/users", () => json(200, { data: [other] }));
        web.on("PUT", "/api/go/v1/tenants/t-own/members/u-2", () => json(200, { data: { role: "manager" } }));
        web.on("GET", "/api/go/v1/customers", () => json(200, { data: [] }));
        renderWithProviders(<UsersPage />);
        const u = userEvent.setup();
        const [row] = await screen.findAllByTestId("user-row");
        await u.click(within(row).getByRole("button", { name: "Actions" }));
        await u.click(await screen.findByRole("menuitem", { name: "Edit Role" }));
        const dialog = await screen.findByRole("dialog");
        await u.click(within(dialog).getByRole("combobox", { name: "Role" }));
        await u.click(await screen.findByRole("option", { name: "Manager" }));
        await u.click(within(dialog).getAllByRole("button", { name: "Save Changes" })[0]);
        await waitFor(() => expect(web.calls.some((c) => c.method === "PUT" && c.url === "/api/go/v1/tenants/t-own/members/u-2")).toBe(true));
        expect(web.calls.find((c) => c.method === "PUT")?.body).toEqual({ role: "manager" });
    });

    it("a tenant principal lists its own reach (no X-Act-On-Tenant); a platform principal lists every tenant with X-Act-On-Tenant: *", async () => {
        const tenantStaff = setup();
        tenantStaff.web.on("GET", "/api/go/v1/users", () => json(200, { data: [user(4)] }));
        const first = renderWithProviders(<UsersPage />);
        await screen.findAllByTestId("user-row");
        const own = tenantStaff.web.calls.filter((c) => c.url.startsWith("/api/go/v1/users"));
        expect(own.length).toBeGreaterThan(0);
        expect(own.every((c) => c.headers["x-act-on-tenant"] === undefined)).toBe(true);
        first.unmount();
        vi.unstubAllGlobals();

        // The bootstrap platform admin: platform role only, no membership, so no `tid`.
        const platform = setup({ tenant: null, tenants: [], platformRoles: ["platform_admin"] });
        const customer = user(5, { memberships: [], scopes: [{ kind: "customer", billingPartyId: "bp-1", name: "CJSF" }] });
        platform.web.on("GET", "/api/go/v1/users", (c) => json(200, { data: c.headers["x-act-on-tenant"] === "*" ? [user(4), customer] : [] }));
        const { client } = renderWithProviders(<UsersPage />);
        await waitFor(() => expect(screen.getAllByTestId("user-row")).toHaveLength(2));
        const all = platform.web.calls.filter((c) => c.url.startsWith("/api/go/v1/users"));
        expect(all.every((c) => c.headers["x-act-on-tenant"] === "*")).toBe(true);
        // The reach is part of the key, so a tenant-reach page is never cached under the platform reach.
        const loaded = client.getQueryCache().findAll({ queryKey: ["users"] }).filter((q) => q.state.data !== undefined);
        expect(loaded.map((q) => (q.queryKey[1] as { reach: string }).reach)).toEqual(["all"]);
    });

    it("hides the write controls without users:manage", async () => {
        const { web } = setup({ capabilities: ["users:view"] });
        web.on("GET", "/api/go/v1/users", () => json(200, { data: [user(3)] }));
        renderWithProviders(<UsersPage />);
        await screen.findAllByTestId("user-row");
        expect(screen.queryByRole("button", { name: /Add User/ })).toBeNull();
        expect(screen.queryByRole("switch")).toBeNull();
        expect(screen.queryByRole("button", { name: "Actions" })).toBeNull();
    });
});
