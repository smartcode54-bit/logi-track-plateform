// UI3 (#143): the `/app` shell after the critique's four P1s. The header holds no decoys and keeps one height; the
// sidebar and the user menu name the slice of the data from one label source; the language and theme live in the
// user menu (reachable at every width), `<html lang>` follows the language, the breadcrumb reads the menu's labels
// in Thai, there is a skip link, and sign-out has one destination. The browser talks to a fake BFF.
import fs from "node:fs";
import path from "node:path";
import React from "react";
import { act, screen, waitFor, within } from "@testing-library/react";
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
import { BOOT_LOADING_TEXT } from "@/components/boot-loading";
import { useBreadcrumb } from "@/context/breadcrumb";
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
    // Every test starts as a first visit: no stored sidebar choice.
    document.cookie = "sidebar_state=; path=/; max-age=0";
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
        // (The boot loader is a status too while the dictionary loads; the page placeholder follows it.)
        const words = await screen.findByText("Loading...");
        expect(words.closest("[role=status]")).not.toBeNull();
    });
});

describe("the scope line (one label source)", () => {
    it("names a tenant admin's tenant and role in the sidebar and the user menu", async () => {
        await renderShell(tenantAdmin);
        expect(screen.getByTestId("sidebar-scope")).toHaveTextContent("WRT · Tenant admin");
        const trigger = screen.getByTestId("user-menu-trigger");
        expect(trigger).toHaveTextContent("WRT · Tenant admin");
        // The accessible name holds the visible text, scope line included (WCAG 2.5.3).
        expect(trigger).toHaveAccessibleName("Account menu for Admin, WRT · Tenant admin");
        await userEvent.setup().click(trigger);
        expect(await screen.findByTestId("user-menu-scope")).toHaveTextContent("WRT · Tenant admin");
    });

    it("names a customer-scope user's customer, and the logo leads to that user's home", async () => {
        await renderShell(customer);
        expect(screen.getByTestId("sidebar-scope")).toHaveTextContent("CJSF · Customer view");
        expect(screen.getByRole("link", { name: /LogiTrack Pro/ })).toHaveAttribute("href", "/app/driver-monitor");
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
        await waitFor(() => expect(within(crumbs()).getByText("Trips to bill")).toBeInTheDocument());
        expect(within(crumbs()).getByText("Billing")).toBeInTheDocument();
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
        await waitFor(() => expect(within(crumbs()).getByText("เที่ยวที่ต้องวางบิล")).toBeInTheDocument());
        expect(within(crumbs()).getByText("การวางบิล")).toBeInTheDocument();
        expect(within(crumbs()).getByText("หน้าแรก")).toBeInTheDocument();
        expect(within(crumbs()).queryByText(/income|accounting/i)).toBeNull();
    });

    it("keeps a link back to the list on an untitled detail page, and ends with a generic name", async () => {
        mockPathname = "/app/customers/abc/edit";
        await renderShell(makeMe({ ...tenantAdmin, capabilities: ["fleet:manage_customers"] }));
        const crumbs = screen.getByRole("navigation", { name: "Breadcrumb" });
        expect(within(crumbs).getByRole("link", { name: "Customers" })).toHaveAttribute("href", "/app/customers");
        expect(within(crumbs).getByText("Customers")).not.toHaveAttribute("aria-current");
        expect(within(crumbs).getByText("Edit")).toHaveAttribute("aria-current", "page");
        expect(within(crumbs).queryByText(/abc/)).toBeNull();
    });

    it("shows a crumb the principal cannot open as text, not a link (the gate's table, as the sidebar)", async () => {
        mockPathname = "/app/trucks/view";
        // The trucks page itself is out of reach: its crumb is plain text.
        await renderShell(makeMe({ ...tenantAdmin, capabilities: ["accounting:view_income"] }));
        const crumbs = screen.getByRole("navigation", { name: "Breadcrumb" });
        expect(within(crumbs).getByText("Trucks").closest("a")).toBeNull();
        expect(within(crumbs).getByText("Details")).toHaveAttribute("aria-current", "page");
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
        // The sidebar no longer carries its own sign-out (it went to "/", the header to "/login").
        expect(screen.queryAllByRole("button", { name: /Logout|Sign out/ })).toHaveLength(0);
        const user = userEvent.setup();
        await user.click(screen.getByTestId("user-menu-trigger"));
        await user.click(await screen.findByRole("menuitem", { name: "Sign out" }));
        await waitFor(() => expect(navigated).toEqual(["/login"]));
        expect(web.count("POST", "/api/auth/logout")).toBe(1);
    });
});

describe("the Security Center sidebar", () => {
    const securityAdmin = makeMe({ ...platformAdmin, capabilities: ["security:view_overview", "users:view"] });

    it("carries the scope line, and its pages sit under the Security Center in the breadcrumb", async () => {
        mockPathname = "/app/security-center/users";
        await renderShell(securityAdmin);
        expect(screen.getByTestId("sidebar-scope")).toHaveTextContent(/^Platform admin$/);
        const crumbs = screen.getByRole("navigation", { name: "Breadcrumb" });
        expect(within(crumbs).getByRole("link", { name: "Security center" })).toHaveAttribute("href", "/app/security-center");
        expect(within(crumbs).getByText("User management")).toHaveAttribute("aria-current", "page");
        expect(screen.queryByRole("button", { name: /Logout|Sign out/ })).toBeNull();
    });
});

describe("on a phone", () => {
    const desktopWidth = window.innerWidth;
    beforeEach(() => {
        Object.defineProperty(window, "innerWidth", { configurable: true, writable: true, value: 375 });
    });
    afterEach(() => {
        Object.defineProperty(window, "innerWidth", { configurable: true, writable: true, value: desktopWidth });
    });

    it("opens the menu as a titled sheet that closes once a link is tapped (main sidebar)", async () => {
        mockPathname = "/app/accounting/income";
        await renderShell(tenantAdmin);
        const user = userEvent.setup();
        await user.click(screen.getByRole("button", { name: "Show or hide the menu" }));
        const sheet = await screen.findByRole("dialog", { name: "Menu" });
        expect(within(sheet).getByTestId("sidebar-scope")).toHaveTextContent("WRT · Tenant admin");
        await user.click(within(sheet).getByRole("link", { name: "Trips to bill" }));
        await waitFor(() => expect(screen.queryByRole("dialog", { name: "Menu" })).toBeNull());
    });

    it("closes the Security Center sheet once a link is tapped too", async () => {
        mockPathname = "/app/security-center/users";
        await renderShell(makeMe({ ...platformAdmin, capabilities: ["security:view_overview", "users:view"] }));
        const user = userEvent.setup();
        await user.click(screen.getByRole("button", { name: "Show or hide the menu" }));
        const sheet = await screen.findByRole("dialog", { name: "Menu" });
        expect(within(sheet).getByTestId("sidebar-scope")).toHaveTextContent(/^Platform admin$/);
        await user.click(within(sheet).getByRole("link", { name: "Overview" }));
        await waitFor(() => expect(screen.queryByRole("dialog", { name: "Menu" })).toBeNull());
    });

    it("gives every target of the sheet 44 px and a visible, labelled close button (UI3B)", async () => {
        mockPathname = "/app/accounting/income";
        await renderShell(makeMe({ ...tenantAdmin, capabilities: ["accounting:view_income", "accounting:view_rate_card", "fleet:view_trucks"] }));
        const user = userEvent.setup();
        const trigger = screen.getByRole("button", { name: "Show or hide the menu" });
        expect(trigger).toHaveAttribute("aria-expanded", "false");
        expect(trigger).not.toHaveAttribute("aria-controls");
        await user.click(trigger);
        const sheet = await screen.findByRole("dialog", { name: "Menu" });
        expect(trigger).toHaveAttribute("aria-expanded", "true");
        expect(trigger).toHaveAttribute("aria-controls", sheet.id);
        // Items 32 px and sub-items 28 px on a desktop; 44 px below md, where the sidebar is only this sheet.
        const targets = sheet.querySelectorAll("[data-sidebar=menu-button], [data-sidebar=menu-sub-button]");
        expect(targets.length).toBeGreaterThan(4);
        for (const target of Array.from(targets)) {
            if (target.getAttribute("data-size") === "lg") expect(target.className, target.textContent ?? "").toMatch(/\bmin-h-12\b/);
            else expect(target.className, target.textContent ?? "").toMatch(/(^|\s)max-md:h-11(\s|$)/);
        }
        // The sheet's own close button was hidden (0×0); this one is shown, named and 44 px.
        const close = within(sheet).getByRole("button", { name: "Close the menu" });
        expect(close.className).toMatch(/\bsize-11\b/);
        expect(close.className).not.toMatch(/\bhidden\b/);
        await user.click(close);
        await waitFor(() => expect(screen.queryByRole("dialog", { name: "Menu" })).toBeNull());
    });

    it("names the slice in the header instead of repeating the page's heading, keeping the role whole (UI3B)", async () => {
        await renderShell(customer);
        const scope = within(header()).getByTestId("header-scope");
        expect(scope).toHaveTextContent("CJSF · Customer view");
        expect(scope.className).toMatch(/\bsm:hidden\b/);
        const [slice, , role] = Array.from(scope.children);
        expect(slice.className).toMatch(/\btruncate\b/);
        expect(role.className).not.toMatch(/\btruncate\b/);
        expect(screen.getByRole("navigation", { name: "Breadcrumb" }).className).toMatch(/\bhidden\b.*\bsm:block\b/);
    });

    it("makes every item of the user menu 44 px tall, and names languages without flags (UI3B)", async () => {
        await renderShell(tenantAdmin);
        await userEvent.setup().click(screen.getByTestId("user-menu-trigger"));
        const menu = await screen.findByRole("menu");
        const items = [...within(menu).getAllByRole("menuitemradio"), ...within(menu).getAllByRole("menuitem")];
        expect(items).toHaveLength(6);
        for (const item of items) expect(item.className, item.textContent ?? "").toMatch(/\bmax-md:min-h-11\b/);
        expect(menu.querySelector("img")).toBeNull();
    });
});

describe("landmarks, the tab title and the sidebar trigger (UI3B)", () => {
    it("has a banner beside <main>, not inside it, and a named navigation for the menu", async () => {
        await renderShell(tenantAdmin);
        const banner = screen.getByRole("banner");
        const main = screen.getByRole("main");
        expect(main).toHaveAttribute("id", "main-content");
        expect(main).toHaveTextContent("page body");
        expect(main.contains(banner)).toBe(false);
        expect(banner.contains(main)).toBe(false);
        expect(screen.getByRole("navigation", { name: "Main menu" })).toBeInTheDocument();
        expect(screen.getByRole("navigation", { name: "Breadcrumb" })).toBeInTheDocument();
    });

    it("names the tab after the page, in the active language", async () => {
        mockPathname = "/app/accounting/income";
        await renderShell(tenantAdmin);
        await waitFor(() => expect(document.title).toBe("Trips to bill · LogiTrack Pro"));
        const user = userEvent.setup();
        await user.click(screen.getByTestId("user-menu-trigger"));
        await user.click(await screen.findByRole("menuitemradio", { name: "ไทย" }));
        await waitFor(() => expect(document.title).toBe("เที่ยวที่ต้องวางบิล · LogiTrack Pro"));
    });

    it("names a detail page by its own name when the page gives one, else generically", async () => {
        mockPathname = "/app/customers/abc/edit";
        await renderShell(makeMe({ ...tenantAdmin, capabilities: ["fleet:manage_customers"] }));
        await waitFor(() => expect(document.title).toBe("Edit · Customers · LogiTrack Pro"));
    });

    it("uses the plate a truck page gives as its tab title", async () => {
        mockPathname = "/app/trucks/view";
        function TruckPage() {
            const { setCustomLastItem } = useBreadcrumb();
            React.useEffect(() => setCustomLastItem("70-1234"), [setCustomLastItem]);
            return <p>page body</p>;
        }
        await renderShell(makeMe({ ...tenantAdmin, capabilities: ["fleet:view_trucks"] }), <TruckPage />);
        await waitFor(() => expect(document.title).toBe("70-1234 · Trucks · LogiTrack Pro"));
    });

    it("says what the trigger controls and whether it is open, in the active language, with its shortcut", async () => {
        await renderShell(tenantAdmin);
        const trigger = within(header()).getByRole("button", { name: "Show or hide the menu" });
        // jsdom is 1024 px wide: lg, so a first visit starts with the sidebar open.
        expect(trigger).toHaveAttribute("aria-expanded", "true");
        const controlled = document.getElementById(trigger.getAttribute("aria-controls") ?? "");
        expect(controlled).not.toBeNull();
        expect(controlled).toHaveAttribute("data-slot", "sidebar-container");
        expect(trigger).toHaveAttribute("aria-keyshortcuts", "Meta+B Control+B");
        // The shortcut is named in the trigger's tooltip.
        act(() => trigger.focus());
        expect((await screen.findAllByText(/Show or hide the menu \((⌘B|Ctrl\+B)\)/)).length).toBeGreaterThan(0);
        const user = userEvent.setup();
        await user.click(trigger);
        expect(trigger).toHaveAttribute("aria-expanded", "false");
        expect(document.cookie).toContain("sidebar_state=false");
        await user.click(trigger);
        expect(document.cookie).toContain("sidebar_state=true");

        await user.click(screen.getByTestId("user-menu-trigger"));
        await user.click(await screen.findByRole("menuitemradio", { name: "ไทย" }));
        await waitFor(() => expect(within(header()).getByRole("button", { name: "แสดงหรือซ่อนเมนู" })).toBeInTheDocument());
        expect(header().textContent).not.toMatch(/Toggle Sidebar/);
    });

    it("starts a customer's menu with their home, the driver monitor, and no second home above it", async () => {
        mockPathname = "/app/driver-monitor";
        await renderShell(customer);
        const menu = screen.getByRole("navigation", { name: "Main menu" });
        const first = within(menu).getAllByRole("link")[0];
        expect(first).toHaveAttribute("href", "/app/driver-monitor");
        expect(first).toHaveAttribute("aria-current", "page");
        // The dashboard is still there, inside Operations (closed: the driver monitor is a top-level entry).
        expect(within(menu).getByRole("button", { name: "Operations" })).toHaveAttribute("aria-expanded", "false");
    });
});

describe("the page area and the loader", () => {
    it("lets a wide page scroll under the header instead of pushing it out, and the skip link land below it", async () => {
        await renderShell(tenantAdmin);
        expect(document.querySelector("[data-slot=sidebar-inset]")?.className).toMatch(/\bmin-w-0\b/);
        const main = document.getElementById("main-content");
        expect(main?.className).toMatch(/\bmin-w-0\b/);
        expect(main?.className).toMatch(/\bscroll-mt-16\b/);
    });

    it("waits for ['me'] with the same words the language load showed", async () => {
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/me", () => new Promise<Response>(() => undefined));
        renderWithProviders(<AdminLayout>{<p>page body</p>}</AdminLayout>);
        expect(await screen.findByRole("status")).toHaveTextContent(BOOT_LOADING_TEXT);
        expect(screen.queryByText("page body")).toBeNull();
    });
});
