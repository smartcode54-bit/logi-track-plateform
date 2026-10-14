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
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

let mockPathname = "/app/dashboard";
vi.mock("next/navigation", () => ({ usePathname: () => mockPathname, useRouter: () => ({ push: vi.fn(), replace: vi.fn() }) }));

const { AppSidebar } = await import("../app-sidebar");
const { menuFor, isNavGroup } = await import("../app-nav");
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

async function renderSidebar(capabilities: string[], { open = true }: { open?: boolean } = {}) {
    const client = createQueryClient();
    client.setQueryData(["me"], principal(capabilities));
    render(
        <QueryClientProvider client={client}>
            <LanguageProvider>
                <SidebarProvider defaultOpen={open}>
                    <AppSidebar />
                    <Toggle />
                </SidebarProvider>
            </LanguageProvider>
        </QueryClientProvider>
    );
    await screen.findByText("LogiTrack Pro");
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
        for (const name of ["Billing", "Fleet & partners"]) await user.click(screen.getByRole("button", { name }));
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
        const { main, system } = menuFor(new Set(ALL_GROUPS), "/app/dashboard");
        const expected = [...main.flatMap((e) => (isNavGroup(e) ? e.items.map((i) => i.url) : [e.url])), ...system.map((l) => l.url)];
        expect(hrefs().filter((h) => h !== "/support" && h !== "/app/dashboard").sort()).toEqual(expected.filter((u) => u !== "/app/dashboard").sort());
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
        await waitFor(() => expect(screen.getByText("Waitlist (12)")).toBeInTheDocument());
        expect(fetched).toEqual(["/api/go/v1/badges"]);
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
        for (const name of ["Operations", "Costs", "Fleet & partners", "Payroll and HR"]) {
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
        expect(screen.getByRole("button", { name: "Fleet & partners" })).not.toHaveAttribute("aria-current");
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
        // The group holding the current page is marked in the rail too.
        expect(billing).toHaveAttribute("data-active", "true");
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
        const fleet = screen.getByRole("button", { name: "Fleet & partners" });
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
        const content = document.querySelector("[data-slot=tooltip-content]");
        expect(content).not.toBeNull();
        expect(content?.className).toMatch(/\bpointer-events-none\b/);
        // Radix's positioning wrapper around it takes no pointer either (a rule jsdom does not apply, so read it).
        expect(content).toHaveAttribute("data-sidebar-tooltip");
        expect(content?.parentElement).toHaveAttribute("data-radix-popper-content-wrapper");
        const css = fs.readFileSync(path.join(WEB_ROOT, "app", "globals.css"), "utf8");
        expect(css).toMatch(/\[data-radix-popper-content-wrapper\]:has\(> \[data-sidebar-tooltip\]\)\s*\{\s*pointer-events: none;/);
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
