// UI3 (#143): the shell's navigation is defined once (components/app-nav.ts) and the header breadcrumb is built
// from its i18n keys, never from URL slugs. Every page under app/app gets a crumb whose text exists in the base
// dictionary of both languages (the header renders it on every route, before any split namespace loads).
//
// UI3B: the menu follows the work loop (Operations, Billing, Costs, Fleet & partners, HR; System at the foot), the
// principal's home is its first entry, the per-role sets of destinations are those of the gate (unchanged: the only
// additions are First mile and Line haul, which became menu entries for whoever could already open them), and every
// page's H1 reads the key of its menu entry.
import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

import {
    activeNavUrl,
    breadcrumbTrail,
    groupOf,
    isNavGroup,
    MAIN_NAV,
    mainNavFor,
    menuFor,
    SECURITY_NAV,
    SYSTEM_NAV,
    type NavEntry,
} from "../app-nav";
import en from "@/context/locales/en";
import th from "@/context/locales/th";
import { WEB_ROOT, resolveImport } from "@/test-utils/importGraph";
import { homeRouteFor, routeAllowed, DASHBOARD_PATH, DRIVER_MONITOR_PATH } from "@/lib/routeCapabilities";
import { ROLE_DEFAULT_CAPABILITIES, STEWARD_CAPABILITIES } from "shared-docs/schemas/capabilities";

/** Every route of the `/app` group, from its page files (`[id]` segments filled with a sample id). */
function appRoutes(): string[] {
    const out: string[] = [];
    const walk = (dir: string) => {
        for (const name of fs.readdirSync(dir)) {
            const p = path.join(dir, name);
            if (fs.statSync(p).isDirectory()) walk(p);
            else if (name === "page.tsx") {
                const rel = path.relative(path.join(WEB_ROOT, "app"), path.dirname(p)).split(path.sep).join("/");
                out.push(`/${rel}`.replace(/\[[^\]]+\]/g, "x1"));
            }
        }
    };
    walk(path.join(WEB_ROOT, "app", "app"));
    return out.sort();
}

const urlsOf = (entries: readonly NavEntry[]) => entries.flatMap((e) => (isNavGroup(e) ? e.items.map((i) => i.url) : [e.url]));

describe("breadcrumbTrail", () => {
    it("names a page by its menu group and entry", () => {
        expect(breadcrumbTrail("/app/accounting/income")).toEqual({
            crumbs: [{ labelKey: "nav.billing" }, { labelKey: "nav.income", href: "/app/accounting/income" }],
            detail: false,
        });
        expect(breadcrumbTrail("/app/accounting/fuel").crumbs[0]).toEqual({ labelKey: "nav.costs" });
        expect(breadcrumbTrail("/app/first-mile").crumbs).toEqual([
            { labelKey: "nav.operations" },
            { labelKey: "nav.firstMileTasks", href: "/app/first-mile" },
        ]);
        expect(breadcrumbTrail("/app/dashboard/")).toEqual({ crumbs: [{ labelKey: "nav.dashboard", href: "/app/dashboard" }], detail: false });
    });

    it("follows the groups of the principal's menu: the driver monitor is the home of some principals", () => {
        expect(breadcrumbTrail("/app/driver-monitor").crumbs).toEqual([
            { labelKey: "nav.operations" },
            { labelKey: "nav.driverMonitor", href: "/app/driver-monitor" },
        ]);
        expect(breadcrumbTrail("/app/driver-monitor", DRIVER_MONITOR_PATH).crumbs).toEqual([{ labelKey: "nav.driverMonitor", href: "/app/driver-monitor" }]);
        expect(breadcrumbTrail("/app/dashboard", DRIVER_MONITOR_PATH).crumbs).toEqual([
            { labelKey: "nav.operations" },
            { labelKey: "nav.dashboard", href: "/app/dashboard" },
        ]);
    });

    it("keeps a detail page's entry as a link and names the page generically until it names itself", () => {
        expect(breadcrumbTrail("/app/trucks/view")).toEqual({
            crumbs: [{ labelKey: "nav.fleetPartners" }, { labelKey: "nav.trucks", href: "/app/trucks" }],
            detail: true,
            detailKey: "shell.crumb.detail",
        });
        expect(breadcrumbTrail("/app/customers/abc/edit")).toMatchObject({
            crumbs: [{ labelKey: "nav.fleetPartners" }, { labelKey: "nav.customers", href: "/app/customers" }],
            detailKey: "shell.crumb.edit",
        });
        expect(breadcrumbTrail("/app/drivers/new").detailKey).toBe("shell.crumb.new");
    });

    it("names the pages opened from another page under their parent", () => {
        expect(breadcrumbTrail("/app/utilities/billing-impact")).toEqual({
            crumbs: [{ labelKey: "nav.utilities", href: "/app/utilities/backfill" }, { labelKey: "nav.billingImpact", href: "/app/utilities/billing-impact" }],
            detail: false,
        });
        expect(breadcrumbTrail("/app/payroll/config").crumbs).toEqual([
            { labelKey: "nav.hr" },
            { labelKey: "nav.payroll", href: "/app/payroll" },
            { labelKey: "driverComp.config.title", href: "/app/payroll/config" },
        ]);
        expect(activeNavUrl("/app/payroll/penalties")).toBe("/app/payroll");
        expect(activeNavUrl("/app/utilities/billing-impact")).toBe("/app/utilities/backfill");
    });

    it("puts the Security Center pages under the Security Center", () => {
        expect(breadcrumbTrail("/app/security-center/users").crumbs).toEqual([
            { labelKey: "nav.securityCenter", href: "/app/security-center" },
            { labelKey: "securityCenter.userManagement", href: "/app/security-center/users" },
        ]);
        expect(breadcrumbTrail("/app/security-center").crumbs).toEqual([{ labelKey: "nav.securityCenter", href: "/app/security-center" }]);
        // The waitlist is the platform's: in the system group, not among the tenant's menu groups.
        expect(breadcrumbTrail("/app/waitlist").crumbs).toEqual([{ labelKey: "nav.waitlist", href: "/app/waitlist" }]);
    });

    it("has no crumbs outside the menu", () => {
        expect(breadcrumbTrail("/app/nowhere")).toEqual({ crumbs: [], detail: false });
        expect(breadcrumbTrail("/about")).toEqual({ crumbs: [], detail: false });
    });

    it("labels every app page from keys both base dictionaries hold, never from the URL", () => {
        const routes = appRoutes();
        expect(routes.length).toBeGreaterThan(50);
        for (const route of routes) {
            if (route === "/app") continue; // redirects to the principal's home
            for (const home of [DASHBOARD_PATH, DRIVER_MONITOR_PATH]) {
                const { crumbs, detailKey } = breadcrumbTrail(route, home);
                expect(crumbs.length, route).toBeGreaterThan(0);
                for (const key of [...crumbs.map((c) => c.labelKey), ...(detailKey ? [detailKey] : [])]) {
                    expect(en[key], `${route} en ${key}`).toBeTruthy();
                    expect(th[key], `${route} th ${key}`).toBeTruthy();
                }
            }
        }
    });
});

describe("menu entries", () => {
    it("translate in both languages, in sentence case", () => {
        const keys = [
            ...[MAIN_NAV, mainNavFor(DRIVER_MONITOR_PATH)].flatMap((nav) =>
                nav.flatMap((entry) => (isNavGroup(entry) ? [entry.labelKey, ...entry.items.map((i) => i.labelKey)] : [entry.labelKey]))
            ),
            ...SYSTEM_NAV.map((l) => l.labelKey),
            ...SECURITY_NAV.map((l) => l.labelKey),
        ];
        for (const key of keys) {
            expect(en[key], `en ${key}`).toBeTruthy();
            expect(th[key], `th ${key}`).toBeTruthy();
            // No shouting labels (DESIGN.md, The No Shouting Rule), and sentence case: after the first word only
            // proper names and initialisms are capitalised (First mile, not First Mile).
            expect(en[key], key).not.toMatch(/^[A-Z\s&]{4,}$/);
            const rest = en[key].split(" ").slice(1);
            for (const word of rest) expect(/^[A-Z][a-z]/.test(word) && !["Express", "Shopee"].includes(word), `${key}: "${en[key]}"`).toBe(false);
        }
    });

    it("keep a detail page's entry active", () => {
        expect(activeNavUrl("/app/customers/abc/edit")).toBe("/app/customers");
        expect(activeNavUrl("/app/accounting/income")).toBe("/app/accounting/income");
        expect(activeNavUrl("/app/nowhere")).toBeUndefined();
    });

    it("follow the work loop: Operations, Billing, Costs, Fleet & partners, HR, after the home", () => {
        expect(MAIN_NAV.map((e) => e.labelKey)).toEqual(["nav.dashboard", "nav.operations", "nav.billing", "nav.costs", "nav.fleetPartners", "nav.hr"]);
        const billing = MAIN_NAV.find((e) => e.labelKey === "nav.billing");
        expect(billing && isNavGroup(billing) ? billing.items.map((i) => i.url) : []).toEqual([
            "/app/accounting/rate-card",
            "/app/accounting/income",
            "/app/accounting/billing-document",
            "/app/accounting/billing-result",
            "/app/accounting/shopee-express-report",
        ]);
        // The four billing pages read four different names in both languages.
        const names = (dict: Record<string, string>) => ["nav.rateCard", "nav.income", "nav.billingDocument", "nav.billingResult"].map((k) => dict[k]);
        expect(new Set(names(en)).size).toBe(4);
        expect(new Set(names(th)).size).toBe(4);
    });

    it("start with the principal's home, so a monitor-home principal has one home", () => {
        expect(mainNavFor(DASHBOARD_PATH)[0]).toMatchObject({ url: DASHBOARD_PATH });
        const monitorHome = mainNavFor(DRIVER_MONITOR_PATH);
        expect(monitorHome[0]).toMatchObject({ url: DRIVER_MONITOR_PATH });
        expect(urlsOf(monitorHome).filter((u) => u === DRIVER_MONITOR_PATH)).toHaveLength(1);
        // The dashboard is still reachable, with the operations it summarises.
        expect(groupOf(monitorHome, DASHBOARD_PATH)).toBe("nav.operations");
        expect(new Set(urlsOf(monitorHome))).toEqual(new Set(urlsOf(MAIN_NAV)));
    });

    it("give Customers, Companies and Company profile different icons", () => {
        const all = [...urlsOf(MAIN_NAV), ...SYSTEM_NAV.map((l) => l.url)];
        expect(all).toContain("/app/customers");
        const icons = SYSTEM_NAV.filter((l) => ["/app/companies", "/app/settings/company-profile"].includes(l.url)).map((l) => l.icon);
        expect(new Set(icons).size).toBe(2);
        const groupIcons = MAIN_NAV.filter(isNavGroup).map((g) => g.icon);
        expect(new Set(groupIcons).size).toBe(groupIcons.length);
    });
});

// The menus each kind of principal saw before UI3B (captured from the menu definition at fcd41f1e over the role
// defaults of shared-docs/schemas/capabilities.ts and the gate's table). UI3B regroups, reorders and renames; it adds
// First mile and Line haul (pages the same principals could already open, then reachable only through a board) and
// takes nothing away.
const BEFORE: Record<string, { main: string[]; security: string[] }> = {
    admin: {
        main: ["/app/accounting/audit", "/app/accounting/billing-document", "/app/accounting/billing-result", "/app/accounting/fuel", "/app/accounting/fuel-price-history", "/app/accounting/income", "/app/accounting/other", "/app/accounting/rate-card", "/app/accounting/shopee-express-report", "/app/chat", "/app/companies", "/app/customers", "/app/dashboard", "/app/driver-monitor", "/app/drivers", "/app/holidays", "/app/incident-reports", "/app/job-assign", "/app/leave-requests", "/app/maintenance", "/app/payroll", "/app/renewals", "/app/security-center", "/app/settings/company-profile", "/app/sources", "/app/standby-records", "/app/subcontractors", "/app/truck-assignment", "/app/trucks", "/app/utilities/backfill", "/app/waitlist"],
        security: ["/app/security-center", "/app/security-center/api-keys", "/app/security-center/audit", "/app/security-center/mobile-clients", "/app/security-center/mobile-release", "/app/security-center/roles", "/app/security-center/status", "/app/security-center/users"],
    },
    manager: {
        main: ["/app/accounting/audit", "/app/accounting/billing-document", "/app/accounting/billing-result", "/app/accounting/fuel", "/app/accounting/fuel-price-history", "/app/accounting/income", "/app/accounting/other", "/app/accounting/rate-card", "/app/accounting/shopee-express-report", "/app/chat", "/app/companies", "/app/customers", "/app/dashboard", "/app/driver-monitor", "/app/drivers", "/app/incident-reports", "/app/job-assign", "/app/maintenance", "/app/renewals", "/app/settings/company-profile", "/app/sources", "/app/standby-records", "/app/subcontractors", "/app/truck-assignment", "/app/trucks", "/app/waitlist"],
        security: ["/app/security-center/users"],
    },
    operationStaff: {
        main: ["/app/accounting/audit", "/app/accounting/fuel", "/app/accounting/fuel-price-history", "/app/accounting/income", "/app/accounting/other", "/app/accounting/rate-card", "/app/chat", "/app/dashboard", "/app/driver-monitor", "/app/drivers", "/app/incident-reports", "/app/job-assign", "/app/maintenance", "/app/renewals", "/app/sources", "/app/standby-records", "/app/truck-assignment", "/app/trucks"],
        security: [],
    },
    operator: {
        main: ["/app/chat", "/app/dashboard", "/app/driver-monitor", "/app/drivers", "/app/incident-reports", "/app/job-assign", "/app/maintenance", "/app/renewals", "/app/sources", "/app/standby-records", "/app/truck-assignment", "/app/trucks"],
        security: [],
    },
    dispatcher: { main: ["/app/dashboard", "/app/driver-monitor", "/app/incident-reports", "/app/job-assign", "/app/standby-records"], security: [] },
    customer: { main: ["/app/dashboard", "/app/driver-monitor", "/app/incident-reports", "/app/job-assign", "/app/standby-records"], security: [] },
    partner: {
        main: ["/app/accounting/audit", "/app/accounting/billing-document", "/app/accounting/billing-result", "/app/accounting/fuel", "/app/accounting/fuel-price-history", "/app/accounting/income", "/app/accounting/other", "/app/accounting/rate-card", "/app/accounting/shopee-express-report", "/app/chat", "/app/companies", "/app/dashboard", "/app/driver-monitor", "/app/drivers", "/app/holidays", "/app/incident-reports", "/app/job-assign", "/app/leave-requests", "/app/maintenance", "/app/payroll", "/app/renewals", "/app/security-center", "/app/settings/company-profile", "/app/standby-records", "/app/truck-assignment", "/app/trucks", "/app/utilities/backfill"],
        security: ["/app/security-center", "/app/security-center/api-keys", "/app/security-center/audit", "/app/security-center/mobile-clients", "/app/security-center/roles", "/app/security-center/users"],
    },
    platformAdmin: {
        main: ["/app/dashboard", "/app/security-center", "/app/waitlist"],
        security: ["/app/security-center", "/app/security-center/api-keys", "/app/security-center/audit", "/app/security-center/mobile-clients", "/app/security-center/mobile-release", "/app/security-center/roles", "/app/security-center/status", "/app/security-center/tenants", "/app/security-center/users"],
    },
};

const roleCaps = (role: string) => ROLE_DEFAULT_CAPABILITIES.find((r) => r.role === role)?.capabilities ?? [];
const withoutSteward = (caps: readonly string[]) => caps.filter((c) => !(STEWARD_CAPABILITIES as readonly string[]).includes(c));

/** The principals of the role defaults, with the home the gate gives each. */
const PRINCIPALS: Record<string, { capabilities: readonly string[]; home: string }> = {
    admin: { capabilities: roleCaps("tenant_admin"), home: homeRouteFor({ tenantRole: "tenant_admin", tenantKind: "own_fleet", customerScope: false, dispatcher: false, capabilities: roleCaps("tenant_admin") }) },
    manager: { capabilities: roleCaps("manager"), home: homeRouteFor({ tenantRole: "manager", tenantKind: "own_fleet", customerScope: false, dispatcher: false, capabilities: roleCaps("manager") }) },
    operationStaff: { capabilities: roleCaps("operation_staff"), home: homeRouteFor({ tenantRole: "operation_staff", tenantKind: "own_fleet", customerScope: false, dispatcher: false, capabilities: roleCaps("operation_staff") }) },
    operator: { capabilities: roleCaps("operator"), home: homeRouteFor({ tenantRole: "operator", tenantKind: "own_fleet", customerScope: false, dispatcher: false, capabilities: roleCaps("operator") }) },
    dispatcher: { capabilities: roleCaps("dispatcher"), home: homeRouteFor({ customerScope: false, dispatcher: true, capabilities: roleCaps("dispatcher") }) },
    customer: { capabilities: roleCaps("customer"), home: homeRouteFor({ customerScope: true, dispatcher: false, capabilities: roleCaps("customer") }) },
    // A carrier's tenant admin (a partner): the steward-only keys are not effective outside the own fleet.
    partner: {
        capabilities: withoutSteward(roleCaps("tenant_admin")),
        home: homeRouteFor({ tenantRole: "tenant_admin", tenantKind: "carrier", customerScope: false, dispatcher: false, capabilities: withoutSteward(roleCaps("tenant_admin")) }),
    },
    platformAdmin: { capabilities: roleCaps("platform_admin"), home: homeRouteFor({ customerScope: false, dispatcher: false, capabilities: roleCaps("platform_admin") }) },
};

describe("the menu of each kind of principal (UI3B: only grouping, order, names and icons change)", () => {
    for (const [who, principal] of Object.entries(PRINCIPALS)) {
        it(`${who}: the same destinations as before, plus First mile and Line haul where the gate opens them`, () => {
            const held = new Set(principal.capabilities);
            const { main, system } = menuFor(held, principal.home);
            const now = [...urlsOf(main), ...system.map((l) => l.url)].sort();
            const boards = ["/app/first-mile", "/app/line-haul"].filter((u) => routeAllowed(held, u));
            expect(now).toEqual([...BEFORE[who].main, ...boards].sort());
            // Every entry is one the gate opens, and no group is empty.
            for (const url of now) expect(routeAllowed(held, url), url).toBe(true);
            for (const entry of main) if (isNavGroup(entry)) expect(entry.items.length).toBeGreaterThan(0);
            // The Security Center sidebar is untouched.
            expect(SECURITY_NAV.filter((l) => routeAllowed(held, l.url)).map((l) => l.url).sort()).toEqual(BEFORE[who].security);
            // The home is the first entry.
            expect(main[0]).toMatchObject({ url: principal.home });
        });
    }

    it("sends customers, partners, dispatchers and operators home to the driver monitor, the rest to the dashboard", () => {
        expect(PRINCIPALS.customer.home).toBe(DRIVER_MONITOR_PATH);
        expect(PRINCIPALS.partner.home).toBe(DRIVER_MONITOR_PATH);
        expect(PRINCIPALS.dispatcher.home).toBe(DRIVER_MONITOR_PATH);
        expect(PRINCIPALS.operator.home).toBe(DRIVER_MONITOR_PATH);
        expect(PRINCIPALS.admin.home).toBe(DASHBOARD_PATH);
    });
});

/** The source file that renders the H1 of the page at `url`: the page itself, else the one component it imports that does. */
function headingFile(url: string): string {
    const page = path.join(WEB_ROOT, "app", ...url.split("/").filter(Boolean), "page.tsx");
    const source = fs.readFileSync(page, "utf8");
    if (/<h1\b/.test(source)) return page;
    const specs = [...source.matchAll(/(?:from\s+|import\()\s*["']([^"']+)["']/g)].map((m) => m[1]);
    const candidates = specs
        .map((spec) => resolveImport(page, spec))
        .filter((file): file is string => Boolean(file) && /<h1\b/.test(fs.readFileSync(file as string, "utf8")));
    expect(candidates, `${url}: the components with an H1`).toHaveLength(1);
    return candidates[0];
}

describe("one name per page (UI3B)", () => {
    // Redirects only (no heading of their own).
    const REDIRECTS = new Set(["/app/users", "/app/operations/roles"]);
    const pages = [...new Set([...urlsOf(MAIN_NAV), ...SYSTEM_NAV.map((l) => l.url), ...SECURITY_NAV.map((l) => l.url), "/app/payroll/config", "/app/payroll/penalties", "/app/utilities/billing-impact"])];

    it("every menu page's H1 reads the key of its menu entry (= its crumb and its tab title)", () => {
        expect(pages.length).toBeGreaterThan(35);
        for (const url of pages) {
            if (REDIRECTS.has(url)) continue;
            const { crumbs } = breadcrumbTrail(url);
            const key = crumbs[crumbs.length - 1].labelKey;
            const file = headingFile(url);
            const source = fs.readFileSync(file, "utf8");
            const headings = [...source.matchAll(/<h1\b[^>]*>([\s\S]*?)<\/h1>/g)].map((m) => m[1]);
            expect(headings.length, `${url}: ${path.relative(WEB_ROOT, file)}`).toBeGreaterThan(0);
            const literals = headings.flatMap((h) => [...h.matchAll(/["']([a-zA-Z]+\.[a-zA-Z.]+)["']/g)].map((m) => m[1]));
            if (literals.length > 0) expect(literals, `${url}: ${path.relative(WEB_ROOT, file)}`).toContain(key);
            // A heading read through a lookup (the task boards' `keys.title`) still names the key in the same file.
            else expect(source, `${url}: ${path.relative(WEB_ROOT, file)}`).toContain(`"${key}"`);
        }
    });
});
