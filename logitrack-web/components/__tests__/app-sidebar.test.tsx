// TW4 (#84; developer-spec.md §10.5, §10.7; Appendix E §E.5 row 19): the sidebar shows exactly the
// routes the proxy.ts gate allows over ['me'], and its waitlist badge is ['badges'] (`GET /v1/badges`,
// polled), not a listener on the whole waitlist collection. TW7: the badge reads Go only, with no web
// flag to wait for (T54R had a Firestore count query while the `dashboard` flag was off).
//
// UI3B: only the group of the current page starts open; the current entry is evident (Route Blue, aria-current)
// and scrolled into view; in the icon rail every group opens its pages as a menu to the right; the menu is a
// labelled nav; the open or collapsed state is kept and starts open from lg.
import fs from "node:fs";
import path from "node:path";
import React from "react";
import type { MeDTO } from "@/features/auth/api/me";
import { navigationProfileOf } from "@/features/auth/utils/navigationProfile";
import { homeRouteOf } from "@/features/auth/api/homeRoute";
import { makeMe } from "@/test-utils/fakeWeb";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

let mockPathname = "/app/dashboard";
vi.mock("next/navigation", () => ({ usePathname: () => mockPathname, useRouter: () => ({ push: vi.fn(), replace: vi.fn() }) }));

const { SecurityCenterSidebar } = await import("../security-center-sidebar");
const { AppSidebar } = await import("../app-sidebar");
const { menuFor, menuLinksFor } = await import("../app-nav");
const { initialSidebarOpen, SIDEBAR_OPEN_FROM_PX } = await import("../sidebar-shell");
const { SidebarProvider, sidebarStateFromCookie, useSidebar } = await import("@/components/ui/sidebar");
const { LanguageProvider } = await import("@/context/language");
const { createQueryClient } = await import("@/lib/queryClient");
const { BADGES_POLL_MS, badgesQueryOptions } = await import("@/features/dashboard/api/badges");
const { WEB_ROOT } = await import("@/test-utils/importGraph");

function principal(capabilities: string[]) {
    return {
        id: "u1",
        email: "ann@example.test",
        displayName: "Ann",
        photoUrl: null,
        tenant: { id: "t1", nameTh: "ท", nameEn: null, kind: "own_fleet", role: "manager" },
        tenants: [],
        platformRoles: [],
        dispatcher: false,
        steward: true,
        driver: null,
        customerScopes: [],
        capabilities,
        mustChangePassword: false,
    };
}

let fetched: string[] = [];
const scrolled: Array<{ element: Element; options: unknown }> = [];

beforeEach(() => {
    fetched = [];
    scrolled.length = 0;
    mockPathname = "/app/dashboard";
    vi.stubGlobal(
        "fetch",
        vi.fn(async (input: RequestInfo | URL) => {
            fetched.push(String(input));
            if (String(input) === "/api/go/v1/badges") return new Response(JSON.stringify({ data: { waitlist: 12 } }), { status: 200 });
            throw new Error(`unexpected ${String(input)}`);
        })
    );
    // jsdom has no scrollIntoView: record who was revealed.
    Element.prototype.scrollIntoView = function (this: Element, options?: unknown) {
        scrolled.push({ element: this, options });
    } as Element["scrollIntoView"];
});

afterEach(() => {
    vi.unstubAllGlobals();
    delete (Element.prototype as Partial<Element>).scrollIntoView;
});

function Toggle() {
    const { toggleSidebar } = useSidebar();
    return (
        <button type="button" onClick={toggleSidebar}>
            toggle
        </button>
    );
}

async function renderSidebar(capabilities: string[], { open = true, me, security = false }: { open?: boolean; me?: MeDTO; security?: boolean } = {}) {
    const client = createQueryClient();
    client.setQueryData(["me"], me ?? principal(capabilities));
    render(
        <QueryClientProvider client={client}>
            <LanguageProvider>
                <SidebarProvider defaultOpen={open}>
                    {security ? <SecurityCenterSidebar /> : <AppSidebar />}
                    <Toggle />
                </SidebarProvider>
            </LanguageProvider>
        </QueryClientProvider>
    );
    await screen.findByRole("navigation", { name: security ? "Security center menu" : "Main menu" });
    return client;
}

const hrefs = () =>
    within(document.body)
        .queryAllByRole("link")
        .map((a) => a.getAttribute("href"));

const ALL_GROUPS = ["fleet:view_trucks", "accounting:view_income", "accounting:view_fuel", "operations:view_driver_monitor", "hr:view_payroll", "waitlist:view"];

describe("AppSidebar", () => {
    it("lists the routes the gate allows, from ['me'] capabilities", async () => {
        await renderSidebar(["fleet:view_trucks", "accounting:view_income", "waitlist:view"]);
        const user = userEvent.setup();
        // Open the closed groups to see every entry.
        for (const name of ["Billing", "Fleet and partners"]) await user.click(screen.getByRole("button", { name }));
        await user.click(screen.getByRole("button", { name: /^Platform/ }));
        const links = hrefs();
        expect(links).toEqual(expect.arrayContaining(["/app/dashboard", "/app/trucks", "/app/accounting/income", "/app/waitlist"]));
        for (const hidden of ["/app/drivers", "/app/accounting/fuel", "/app/security-center", "/app/utilities/backfill", "/app/companies"]) {
            expect(links).not.toContain(hidden);
        }
        expect(await screen.findByText("Waitlist (12)")).toBeInTheDocument();
    });

    it("renders exactly the gate's menu (menuFor) once every group is open", async () => {
        await renderSidebar(ALL_GROUPS);
        const user = userEvent.setup();
        for (const button of screen.getAllByRole("button", { expanded: false })) {
            if (button.closest("[data-sidebar=menu-item]")) await user.click(button);
        }
        const expected = menuLinksFor(menuFor(new Set(ALL_GROUPS), "/app/dashboard")).map((link) => link.href);
        const nav = screen.getByRole("navigation", { name: "Main menu" });
        expect(within(nav).getAllByRole("link").map((link) => link.getAttribute("href"))).toEqual(expected);
        expect(nav.querySelector("nav")).toBeNull();
    });

    it("names the slice in its header from ['me'] and leaves sign-out to the user menu (UI3)", async () => {
        await renderSidebar(["fleet:view_trucks"]);
        // The tenant has no English name, so the Thai one stands in; the role is the label of principalRoleLabel.
        expect(screen.getByTestId("sidebar-scope")).toHaveTextContent("ท · Manager");
        expect(screen.queryByText("Logout")).toBeNull();
        expect(screen.queryByText("Sign out")).toBeNull();
        // UI3B: no "Main menu" label above the list; the list is a navigation landmark of that name.
        expect(screen.queryByText("Main menu")).toBeNull();
        expect(screen.getByRole("navigation", { name: "Main menu" })).toContainElement(screen.getByRole("link", { name: "Dashboard" }));
    });

    it("counts the waitlist from one GET /v1/badges, with no web flags request (TW7)", async () => {
        await renderSidebar(["waitlist:view"]);
        await userEvent.setup().click(screen.getByRole("button", { name: /^Platform/ }));
        await waitFor(() => expect(screen.getByText("Waitlist (12)")).toBeInTheDocument());
        expect(fetched).toEqual(["/api/go/v1/badges"]);
    });

    it("shows the waitlist count on the closed Platform group", async () => {
        await renderSidebar(["waitlist:view"]);
        const platform = await screen.findByRole("button", { name: "Platform, Waitlist (12)" });
        expect(platform).toHaveAttribute("aria-expanded", "false");
        expect(within(platform).getByText("12")).toBeInTheDocument();
    });

    it("keeps the waitlist count on the rail group and its menu link", async () => {
        await renderSidebar(["waitlist:view"], { open: false });
        const railPlatform = await screen.findByRole("button", { name: "Platform, Waitlist (12)" });
        expect(within(railPlatform).getByText("12")).toBeInTheDocument();
        await userEvent.setup().click(railPlatform);
        expect(await screen.findByRole("menuitem", { name: "Waitlist (12)" })).toHaveAttribute("href", "/app/waitlist");
    });

    it("keys the shortened slice and the full role apart, so only the slice is cut", async () => {
        await renderSidebar(["fleet:view_trucks"]);
        const line = screen.getByTestId("sidebar-scope");
        const [slice, , role] = Array.from(line.children);
        expect(slice.className).toMatch(/\btruncate\b/);
        expect(role.className).toMatch(/\bshrink-0\b/);
        expect(role.className).not.toMatch(/\btruncate\b/);
        expect(line.className).toMatch(/\bleading-normal\b/);
    });
});

describe("where am I (UI3B)", () => {
    it("opens only the group of the current page", async () => {
        mockPathname = "/app/accounting/income";
        await renderSidebar(ALL_GROUPS);
        expect(screen.getByRole("button", { name: "Billing" })).toHaveAttribute("aria-expanded", "true");
        for (const name of ["Operations", "Costs", "Fleet and partners", "Payroll and HR"]) {
            expect(screen.getByRole("button", { name })).toHaveAttribute("aria-expanded", "false");
        }
        expect(hrefs()).not.toContain("/app/driver-monitor");
    });

    it("marks the current entry: aria-current, medium weight and Route Blue, a detail page included", async () => {
        mockPathname = "/app/trucks/view";
        await renderSidebar(ALL_GROUPS);
        const current = screen.getByRole("link", { name: "Trucks" });
        expect(current).toHaveAttribute("aria-current", "page");
        expect(current).toHaveAttribute("data-active", "true");
        expect(current.className).toMatch(/data-\[active=true\]:text-sidebar-primary/);
        expect(current.className).toMatch(/data-\[active=true\]:font-medium/);
        // One current entry, and group buttons never claim to be the page.
        expect(document.querySelectorAll("[aria-current=page]")).toHaveLength(1);
        expect(screen.getByRole("button", { name: "Fleet and partners" })).not.toHaveAttribute("aria-current");
    });

    it("says which closed group holds the current page, beyond colour (aria-current=\"true\")", async () => {
        mockPathname = "/app/trucks/view";
        await renderSidebar(ALL_GROUPS);
        const user = userEvent.setup();
        const fleet = screen.getByRole("button", { name: "Fleet and partners" });
        await user.click(fleet);
        expect(fleet).toHaveAttribute("aria-expanded", "false");
        expect(fleet).toHaveAttribute("aria-current", "true");
        expect(fleet).toHaveAttribute("data-active", "true");
        expect(screen.getByRole("button", { name: "Billing" })).not.toHaveAttribute("aria-current");
    });

    it("links Support inside the console and marks it current there (UI3C)", async () => {
        mockPathname = "/app/support";
        await renderSidebar(["fleet:view_trucks"]);
        const support = screen.getByRole("link", { name: "Support" });
        expect(support).toHaveAttribute("href", "/app/support");
        expect(screen.getByRole("navigation", { name: "Main menu" })).toContainElement(support);
        const nav = screen.getByRole("navigation", { name: "Main menu" });
        const scrollArea = nav.querySelector("[data-sidebar-scroll]");
        expect(scrollArea).not.toBeNull();
        expect(nav).toHaveClass("flex", "min-h-0", "flex-1", "flex-col", "overflow-hidden");
        expect(scrollArea).toHaveClass("min-h-0", "flex-1", "overflow-auto");
        expect(nav.contains(support)).toBe(true);
        expect(scrollArea?.contains(support)).toBe(false);
        expect(support).toHaveAttribute("aria-current", "page");
        expect(support).toHaveAttribute("data-active", "true");
        expect(document.querySelectorAll("[aria-current=page]")).toHaveLength(1);
        expect(hrefs()).not.toContain("/support");
    });

    it("scrolls the current entry into view when the sidebar opens", async () => {
        mockPathname = "/app/payroll";
        await renderSidebar(ALL_GROUPS, { open: false });
        expect(scrolled).toHaveLength(0);
        await userEvent.setup().click(screen.getByRole("button", { name: "toggle" }));
        await waitFor(() => expect(scrolled.length).toBeGreaterThan(0));
        const last = scrolled[scrolled.length - 1];
        expect(last.element).toHaveAttribute("href", "/app/payroll");
        expect(last.options).toEqual({ block: "nearest" });
    });
});

describe("the icon rail (UI3B)", () => {
    it("opens a group's pages as a menu to the right, labelled by the group", async () => {
        mockPathname = "/app/accounting/income";
        await renderSidebar(ALL_GROUPS, { open: false });
        const user = userEvent.setup();
        const billing = screen.getByRole("button", { name: "Billing" });
        expect(billing).toHaveAttribute("aria-haspopup", "menu");
        // The group holding the current page is marked in the rail too, by colour and by aria-current.
        expect(billing).toHaveAttribute("data-active", "true");
        expect(billing).toHaveAttribute("aria-current", "true");
        expect(screen.getByRole("button", { name: "Costs" })).not.toHaveAttribute("aria-current");
        await user.click(billing);
        const menu = await screen.findByRole("menu");
        expect(menu).toHaveAttribute("data-side", "right");
        expect(menu.className).toMatch(/\bz-popover\b/);
        expect(within(menu).getByText("Billing")).toBeInTheDocument();
        const items = within(menu).getAllByRole("menuitem");
        expect(items.map((a) => a.getAttribute("href"))).toEqual(["/app/accounting/income"]);
        expect(items[0]).toHaveAttribute("aria-current", "page");
    });

    it("is keyboard operable: Enter opens the menu, arrows move through its pages", async () => {
        await renderSidebar(ALL_GROUPS, { open: false });
        const user = userEvent.setup();
        const fleet = screen.getByRole("button", { name: "Fleet and partners" });
        fleet.focus();
        await user.keyboard("{Enter}");
        const menu = await screen.findByRole("menu");
        const items = within(menu).getAllByRole("menuitem");
        expect(items.map((a) => a.getAttribute("href"))).toEqual(["/app/trucks"]);
        await waitFor(() => expect(document.activeElement).toBe(items[0]));
        await user.keyboard("{Escape}");
        await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());
        expect(document.activeElement).toBe(fleet);
    });

    it("lets the header's sidebar toggle take clicks under the logo's tooltip", async () => {
        await renderSidebar(["fleet:view_trucks"], { open: false });
        act(() => screen.getByRole("link", { name: /LogiTrack Pro/ }).focus());
        await screen.findAllByTestId("sidebar-scope-tooltip");
        const content = document.querySelector<HTMLElement>("[data-slot=tooltip-content]");
        expect(content).not.toBeNull();
        expect(content?.className).toMatch(/\bpointer-events-none\b/);
        // Radix's positioning wrapper around it takes no pointer either: the stylesheet's `pointer-events: none` rule
        // selects this very wrapper (jsdom does not cascade the stylesheet, so match its selector against the DOM).
        const wrapper = content?.parentElement as HTMLElement;
        expect(wrapper).toHaveAttribute("data-radix-popper-content-wrapper");
        const css = fs.readFileSync(path.join(WEB_ROOT, "app", "globals.css"), "utf8");
        const inert = [...css.matchAll(/([^{}]+)\{\s*pointer-events:\s*none;\s*\}/g)].map((m) => m[1].replace(/\/\*[\s\S]*?\*\//g, "").trim());
        expect(inert.some((selector) => wrapper.matches(selector)), inert.join(" | ")).toBe(true);
        // ...and only the sidebar's: an ordinary tooltip's wrapper keeps taking the pointer.
        content?.removeAttribute("data-sidebar-tooltip");
        expect(inert.some((selector) => wrapper.matches(selector))).toBe(false);
    });

    it("keeps a rail tooltip open as the pointer leaves for it: it is the only visible label (WCAG 1.4.13)", async () => {
        await renderSidebar(["fleet:view_trucks"], { open: false });
        const user = userEvent.setup();
        const dashboard = screen.getByRole("link", { name: "Dashboard" });
        await user.hover(dashboard);
        await waitFor(() => expect(document.querySelector("[data-slot=tooltip-content]")).not.toBeNull());
        // Leaving the button towards the tooltip does not close it (a tooltip that is not hoverable closes here).
        fireEvent.pointerLeave(dashboard, { pointerType: "mouse", clientX: 60, clientY: 90 });
        await new Promise((resolve) => setTimeout(resolve, 50));
        expect(document.querySelector("[data-slot=tooltip-content]")).not.toBeNull();
    });
});

describe("the sidebar's state (UI3B)", () => {
    const width = window.innerWidth;
    afterEach(() => {
        document.cookie = "sidebar_state=; path=/; max-age=0";
        Object.defineProperty(window, "innerWidth", { configurable: true, writable: true, value: width });
    });
    const setWidth = (value: number) => Object.defineProperty(window, "innerWidth", { configurable: true, writable: true, value });

    it("reads the stored choice from the cookie the sidebar writes", () => {
        expect(sidebarStateFromCookie("a=1; sidebar_state=false; b=2")).toBe(false);
        expect(sidebarStateFromCookie("sidebar_state=true")).toBe(true);
        expect(sidebarStateFromCookie("other=1")).toBeUndefined();
        expect(sidebarStateFromCookie("")).toBeUndefined();
    });

    it("starts open from lg without a stored choice, as icons below it, and as the user left it otherwise", () => {
        setWidth(SIDEBAR_OPEN_FROM_PX);
        expect(initialSidebarOpen()).toBe(true);
        setWidth(SIDEBAR_OPEN_FROM_PX - 1);
        expect(initialSidebarOpen()).toBe(false);
        document.cookie = "sidebar_state=true; path=/";
        expect(initialSidebarOpen()).toBe(true);
        setWidth(1600);
        document.cookie = "sidebar_state=false; path=/";
        expect(initialSidebarOpen()).toBe(false);
    });

    it("keeps a toggle across a reload (the cookie is written and read back)", async () => {
        setWidth(1280);
        await renderSidebar(["fleet:view_trucks"], { open: initialSidebarOpen() });
        expect(document.querySelector("[data-slot=sidebar][data-state]")).toHaveAttribute("data-state", "expanded");
        await userEvent.setup().click(screen.getByRole("button", { name: "toggle" }));
        expect(document.cookie).toContain("sidebar_state=false");
        // The reload: a new shell reads the choice.
        expect(initialSidebarOpen()).toBe(false);
    });
});

describe("['badges']", () => {
    it("is polled every 60 s while visible, 30 s stale", () => {
        expect(badgesQueryOptions.queryKey).toEqual(["badges"]);
        expect(badgesQueryOptions.staleTime).toBe(30_000);
        expect(badgesQueryOptions.refetchInterval).toBe(BADGES_POLL_MS);
        expect(BADGES_POLL_MS).toBe(60_000);
        expect(badgesQueryOptions.refetchIntervalInBackground).toBe(false);
    });
});

describe("profile menus and navigation landmarks (UI3F)", () => {
    it.each(["customer", "partner"] as const)("renders the %s's resolved destinations and names", async (profile) => {
        const capabilities = ["operations:view_driver_monitor", "operations:view_first_mile", "operations:view_incidents", "chat:view", "fleet:view_trucks", "drivers:view", "fleet:manage_maintenance", "accounting:billing_document", "hr:view_payroll"];
        const me = makeMe({ capabilities, tenant: profile === "customer" ? null : { id: "carrier", nameTh: "รถร่วม", nameEn: "Carrier", kind: "carrier", role: "tenant_admin" }, customerScopes: profile === "customer" ? [{ billingPartyId: "customer", name: "Customer" }] : [] });
        mockPathname = "/app/driver-monitor";
        await renderSidebar(capabilities, { me });
        for (const button of screen.queryAllByRole("button", { expanded: false })) await userEvent.setup().click(button);
        const nav = screen.getByRole("navigation", { name: "Main menu" });
        const expected = menuLinksFor(menuFor(new Set(capabilities), homeRouteOf(me), navigationProfileOf(me)));
        expect(expected).toHaveLength(profile === "customer" ? 4 : 9);
        expect(within(nav).getAllByRole("link").map((link) => link.getAttribute("href"))).toEqual(expected.map((link) => link.href));
        expect(within(nav).getByRole("link", { name: profile === "customer" ? "Your trips" : "Fleet trips" })).toHaveAttribute("aria-current", "page");
        expect(within(nav).getByRole("link", { name: "Transport tasks" })).toHaveAttribute("href", "/app/job-assign");
        expect(within(nav).getByRole("link", { name: "Delivery issues" })).toHaveAttribute("href", "/app/incident-reports");
        expect(within(nav).queryByRole("link", { name: "Payroll" })).toBeNull();
        expect(nav.querySelector("nav")).toBeNull();
        expect(nav).not.toContainElement(screen.getByText(/Version/));
    });

    it("keeps Security Center's return home and Support inside its existing nav", async () => {
        const capabilities = ["users:view"];
        await renderSidebar(capabilities, { security: true });
        const nav = screen.getByRole("navigation", { name: "Security center menu" });
        const back = within(nav).getByRole("link", { name: "Back to the main menu" });
        const support = within(nav).getByRole("link", { name: "Support" });
        expect(back).toHaveAttribute("href", "/app/dashboard");
        expect(support).toHaveAttribute("href", "/app/support");
        const scrollArea = nav.querySelector("[data-sidebar-scroll]");
        expect(scrollArea).not.toBeNull();
        expect(nav).toHaveClass("flex", "min-h-0", "flex-1", "flex-col", "overflow-hidden");
        expect(scrollArea).toHaveClass("min-h-0", "flex-1", "overflow-auto");
        expect(nav.contains(back)).toBe(true);
        expect(nav.contains(support)).toBe(true);
        expect(scrollArea?.contains(back)).toBe(false);
        expect(scrollArea?.contains(support)).toBe(false);
        expect(nav.querySelector("nav")).toBeNull();
    });
});
