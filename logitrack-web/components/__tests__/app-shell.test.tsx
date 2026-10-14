// UI3 (#143): the `/app` shell after the critique's four P1s. The header holds no decoys and keeps one height; the
// sidebar and the user menu name the slice of the data from one label source; the language and theme live in the
// user menu (reachable at every width), `<html lang>` follows the language, the breadcrumb reads the menu's labels
// in Thai, there is a skip link, and sign-out has one destination. The browser talks to a fake BFF.
import fs from "node:fs";
import path from "node:path";
import React from "react";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

let mockPathname = "/app/dashboard";
vi.mock("next/navigation", () => ({
    usePathname: () => mockPathname,
    useRouter: () => ({ push: vi.fn(), replace: vi.fn(), refresh: vi.fn(), back: vi.fn() }),
    useSearchParams: () => new URLSearchParams(),
}));
vi.mock("@/lib/loginGeo", () => ({ resolveLoginGeoForClient: async () => null }));

import AdminLayout from "@/app/app/layout";
import UnauthorizedPage from "@/app/app/unauthorized/page";
import type { MeDTO } from "@/features/auth/api/me";
import { configureSignOutNavigation } from "@/features/auth/hooks/useSignOut";
import { PageLoading } from "@/features/platform/components/PageLoading";
import { fakeWeb, json, makeMe } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import { WEB_ROOT } from "@/test-utils/importGraph";

const navigated: string[] = [];

function serve(me: MeDTO) {
    const web = fakeWeb();
    web.on("GET", "/api/go/v1/me", () => json(200, { data: me }));
    web.on("GET", "/api/go/v1/me/tenants", () => json(200, { data: me.tenants }));
    web.on("GET", "/api/go/v1/badges", () => json(200, { data: {} }));
    web.on("POST", "/api/auth/logout", () => json(200, { data: {} }));
    return web;
}

async function renderShell(me: MeDTO, page: React.ReactNode = <p>page body</p>) {
    const web = serve(me);
    const view = renderWithProviders(<AdminLayout>{page}</AdminLayout>);
    await screen.findByTestId("user-menu-trigger");
    return { web, ...view };
}

const header = () => document.querySelector("header") as HTMLElement;

beforeEach(() => {
    mockPathname = "/app/dashboard";
    navigated.length = 0;
    configureSignOutNavigation((url) => navigated.push(url));
    document.documentElement.lang = "en";
});
afterEach(() => {
    configureSignOutNavigation((url) => window.location.assign(url));
    vi.unstubAllGlobals();
});

const tenantAdmin = makeMe({
    tenant: { id: "t-wrt", nameTh: "วันเพ็ญ รัชดา", nameEn: "WRT", kind: "own_fleet", role: "tenant_admin" },
    tenants: [{ id: "t-wrt", nameTh: "วันเพ็ญ รัชดา", nameEn: "WRT", kind: "own_fleet", role: "tenant_admin", status: "active" }],
    capabilities: ["accounting:view_income", "fleet:view_trucks"],
});
const customer = makeMe({
    displayName: "Chai",
    tenant: null,
    tenants: [],
    steward: false,
    customerScopes: [{ billingPartyId: "bp-cjsf", name: "CJSF" }],
    capabilities: ["operations:view_driver_monitor"],
});
const platformAdmin = makeMe({ displayName: "Gora", tenant: null, tenants: [], platformRoles: ["platform_admin"], capabilities: ["security:view_overview"] });

describe("the app header", () => {
    it("holds no decoy controls and keeps one height whatever the sidebar does", async () => {
        await renderShell(tenantAdmin);
        const bar = header();
        expect(within(bar).queryByRole("textbox")).toBeNull();
        expect(within(bar).queryByRole("button", { name: /generate report/i })).toBeNull();
        expect(within(bar).queryByText(/real-time/i)).toBeNull();
        expect(screen.queryByPlaceholderText(/search (data|security logs)/i)).toBeNull();
        // Every control in the header has a name (the old bell had none) and none is a toggle of language or theme.
        for (const button of within(bar).getAllByRole("button")) expect(button).toHaveAccessibleName();
        expect(within(bar).getByRole("button", { name: "Show or hide the menu" })).toBeInTheDocument();
        expect(within(bar).queryByRole("button", { name: /^(EN|TH)$/ })).toBeNull();
        // One height and the hairline token: no h-12 switch on the collapsed sidebar, no hard-coded zinc border.
        expect(bar.className).toContain("h-16");
        expect(bar.className).not.toMatch(/h-12|zinc|shadow/);
        expect(bar.className).toContain("border-border");
    });

    it("starts with a skip link to the page", async () => {
        await renderShell(tenantAdmin);
        const user = userEvent.setup();
        await user.tab();
        const skip = screen.getByRole("link", { name: "Skip to main content" });
        expect(document.activeElement).toBe(skip);
        expect(skip).toHaveAttribute("href", "#main-content");
        const target = document.getElementById("main-content");
        expect(target).toHaveAttribute("tabindex", "-1");
        expect(target).toHaveTextContent("page body");
    });
});

describe("the page placeholder", () => {
    it("says in words that the page is loading, not only with a spinner", async () => {
        serve(tenantAdmin);
        renderWithProviders(<PageLoading />);
        expect(await screen.findByRole("status")).toHaveTextContent("Loading...");
    });
});

describe("the scope line (one label source)", () => {
    it("names a tenant admin's tenant and role in the sidebar and the user menu", async () => {
        await renderShell(tenantAdmin);
        expect(screen.getByTestId("sidebar-scope")).toHaveTextContent("WRT · Tenant admin");
        const trigger = screen.getByTestId("user-menu-trigger");
        expect(trigger).toHaveTextContent("WRT · Tenant admin");
        await userEvent.setup().click(trigger);
        expect(await screen.findByTestId("user-menu-scope")).toHaveTextContent("WRT · Tenant admin");
    });

    it("names a customer-scope user's customer", async () => {
        await renderShell(customer);
        expect(screen.getByTestId("sidebar-scope")).toHaveTextContent("CJSF · Customer view");
        await userEvent.setup().click(screen.getByTestId("user-menu-trigger"));
        expect(await screen.findByTestId("user-menu-scope")).toHaveTextContent("CJSF · Customer view");
    });

    it("names a platform admin", async () => {
        await renderShell(platformAdmin);
        expect(screen.getByTestId("sidebar-scope")).toHaveTextContent(/^Platform admin$/);
        await userEvent.setup().click(screen.getByTestId("user-menu-trigger"));
        expect(await screen.findByTestId("user-menu-scope")).toHaveTextContent(/^Platform admin$/);
    });

    it("is the same label on the 403 page, never the legacy claim role", async () => {
        mockPathname = "/app/unauthorized";
        await renderShell(tenantAdmin, <UnauthorizedPage />);
        expect(await screen.findByText("Signed in as WRT · Tenant admin")).toBeInTheDocument();
        expect(screen.queryByText(/signed in as admin$/i)).toBeNull();
        // No shell file reads the role from the legacy claims any more.
        for (const file of [
            "app/app/layout.tsx",
            "app/app/unauthorized/page.tsx",
            "components/app-sidebar.tsx",
            "components/security-center-sidebar.tsx",
            "components/user-menu.tsx",
        ]) {
            expect(fs.readFileSync(path.join(WEB_ROOT, file), "utf8"), file).not.toMatch(/getRole\(/);
        }
    });
});

describe("language, theme and sign-out in the user menu", () => {
    it("switches the language from the user menu, and <html lang> and the breadcrumb follow in Thai", async () => {
        mockPathname = "/app/accounting/income";
        await renderShell(tenantAdmin);
        const crumbs = () => screen.getByRole("navigation", { name: /Breadcrumb|ตำแหน่งของหน้า/ });
        await waitFor(() => expect(within(crumbs()).getByText("Billing")).toBeInTheDocument());
        expect(within(crumbs()).getByText("Accounting")).toBeInTheDocument();
        expect(document.documentElement.lang).toBe("en");

        const user = userEvent.setup();
        // The trigger is never hidden: the menu, and the language in it, is there at every width.
        expect(screen.getByTestId("user-menu-trigger").className).not.toMatch(/\bhidden\b/);
        await user.click(screen.getByTestId("user-menu-trigger"));
        const english = await screen.findByRole("menuitemradio", { name: "English" });
        expect(english).toHaveAttribute("aria-checked", "true");
        const thai = screen.getByRole("menuitemradio", { name: "ไทย" });
        expect(thai).toHaveAttribute("aria-checked", "false");
        expect(thai).toHaveAttribute("lang", "th");
        await user.click(thai);

        await waitFor(() => expect(document.documentElement.lang).toBe("th"));
        await waitFor(() => expect(within(crumbs()).getByText("การวางบิล")).toBeInTheDocument());
        expect(within(crumbs()).getByText("บัญชี/การเงิน")).toBeInTheDocument();
        expect(within(crumbs()).getByText("หน้าแรก")).toBeInTheDocument();
        expect(within(crumbs()).queryByText(/income|accounting/i)).toBeNull();
    });

    it("gives a detail page its menu entry's label", async () => {
        mockPathname = "/app/trucks/view";
        await renderShell(tenantAdmin);
        const crumbs = screen.getByRole("navigation", { name: "Breadcrumb" });
        expect(within(crumbs).getByText("Truck Management")).toHaveAttribute("aria-current", "page");
        expect(within(crumbs).queryByText(/view/i)).toBeNull();
    });

    it("offers the theme in the user menu", async () => {
        await renderShell(tenantAdmin);
        await userEvent.setup().click(screen.getByTestId("user-menu-trigger"));
        expect(await screen.findByRole("menuitemradio", { name: "Light" })).toBeInTheDocument();
        expect(screen.getByRole("menuitemradio", { name: "Dark" })).toBeInTheDocument();
        expect(screen.getByRole("menuitemradio", { name: "Same as device" })).toHaveAttribute("aria-checked", "true");
    });

    it("signs out to the sign-in page, and only from the user menu", async () => {
        const { web } = await renderShell(tenantAdmin);
        // The sidebar no longer carries its own Logout (it went to "/", the header to "/login").
        expect(screen.queryAllByRole("button", { name: "Logout" })).toHaveLength(0);
        const user = userEvent.setup();
        await user.click(screen.getByTestId("user-menu-trigger"));
        await user.click(await screen.findByRole("menuitem", { name: "Logout" }));
        await waitFor(() => expect(navigated).toEqual(["/login"]));
        expect(web.count("POST", "/api/auth/logout")).toBe(1);
    });
});
