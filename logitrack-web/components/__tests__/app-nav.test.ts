// UI3 (#143): the shell's navigation is defined once (components/app-nav.ts) and the header breadcrumb is built
// from its i18n keys, never from URL slugs. Every page under app/app gets a crumb whose text exists in the base
// dictionary of both languages (the header renders it on every route, before any split namespace loads).
//
// UI3B: the menu follows the work loop (Operations, Billing, Costs, Fleet and partners, HR; System at the foot), the
// principal's home is its first entry, the per-role sets of destinations are those of the gate (unchanged: the only
// additions are First mile and Line haul, which became menu entries for whoever could already open them), and every
// page's H1 reads the key of its menu entry.
import fs from "node:fs";
import os from "node:os";
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
            if (fs.statSync(p).isDirectory()) {
                // The shell's catch-all (`[...missing]`, UI3D) only calls notFound(): not a page of its own.
                if (!/^\[\[?\.\.\./.test(name)) walk(p);
            } else if (name === "page.tsx") {
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

    it("names Support (the sidebar's footer entry, UI3C) as a page of its own", () => {
        expect(breadcrumbTrail("/app/support")).toEqual({ crumbs: [{ labelKey: "nav.supportCenter", href: "/app/support" }], detail: false });
        expect(breadcrumbTrail("/app/support", DRIVER_MONITOR_PATH).crumbs).toEqual([{ labelKey: "nav.supportCenter", href: "/app/support" }]);
        expect(activeNavUrl("/app/support")).toBe("/app/support");
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
            // The sidebars' group label and footers (app-sidebar.tsx, security-center-sidebar.tsx).
            "nav.system",
            "nav.supportCenter",
            "securityCenter.title",
            "securityCenter.backToAdmin",
        ];
        for (const key of keys) {
            expect(en[key], `en ${key}`).toBeTruthy();
            expect(th[key], `th ${key}`).toBeTruthy();
            // No shouting labels (DESIGN.md, The No Shouting Rule), and sentence case: after the first word only
            // proper names and initialisms are capitalised (First mile, not First Mile).
            expect(en[key], key).not.toMatch(/^[A-Z\s&]{4,}$/);
            // "and", not "&" ("Fleet and partners" beside "Payroll and HR").
            expect(en[key], key).not.toContain("&");
            const rest = en[key].split(" ").slice(1);
            for (const word of rest) expect(/^[A-Z][a-z]/.test(word) && !["Express", "Shopee"].includes(word), `${key}: "${en[key]}"`).toBe(false);
        }
    });

    it("keep a detail page's entry active", () => {
        expect(activeNavUrl("/app/customers/abc/edit")).toBe("/app/customers");
        expect(activeNavUrl("/app/accounting/income")).toBe("/app/accounting/income");
        expect(activeNavUrl("/app/nowhere")).toBeUndefined();
    });

    it("follow the work loop: Operations, Billing, Costs, Fleet and partners, HR, after the home", () => {
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

/** The page's heading (UI3D): its `<PageHeader`, which renders the one H1 (components/page-header.tsx). */
const HEADING_RX = /<PageHeader\b/;

/**
 * The component that renders the page at `url` and its file: the page's own default export when the page holds a
 * PageHeader, else the one component it imports (statically or through `dynamic(() => import(...).then((m) => m.X))`)
 * whose file holds one.
 */
function pageComponent(url: string): { file: string; name: string } {
    const page = path.join(WEB_ROOT, "app", ...url.split("/").filter(Boolean), "page.tsx");
    const source = fs.readFileSync(page, "utf8");
    const defaultName = (src: string) => src.match(/export default function (\w+)/)?.[1] ?? "";
    if (HEADING_RX.test(source)) return { file: page, name: defaultName(source) };
    const imports = [
        ...[...source.matchAll(/import\(\s*["']([^"']+)["']\s*\)\.then\(\(m\) => m\.(\w+)\)/g)].map((m) => ({ spec: m[1], name: m[2] })),
        ...[...source.matchAll(/import\s*\{\s*(\w+)\s*\}\s*from\s*["']([^"']+)["']/g)].map((m) => ({ spec: m[2], name: m[1] })),
        ...[...source.matchAll(/import\s+(\w+)\s+from\s*["']([^"']+)["']/g)].map((m) => ({ spec: m[2], name: "" })),
    ];
    const candidates = imports
        .map(({ spec, name }) => ({ file: resolveImport(page, spec), name }))
        .filter((c): c is { file: string; name: string } => Boolean(c.file) && HEADING_RX.test(fs.readFileSync(c.file as string, "utf8")));
    expect(candidates, `${url}: the components with a PageHeader`).toHaveLength(1);
    const [found] = candidates;
    return { file: found.file, name: found.name || defaultName(fs.readFileSync(found.file, "utf8")) };
}

/**
 * The page's primary heading: the one `<PageHeader` (or, in the self-test below, `<h1>`) of the file, inside the page
 * component's own body, and not in a branch of it (an `if (...) return` for an error or an empty state), so a heading
 * there cannot stand in for a missing real one. Returns the heading's markup up to its title, or a reason it is not
 * the primary heading.
 */
function primaryHeading(file: string, component: string): { markup?: string; problem?: string } {
    const lines = fs.readFileSync(file, "utf8").split("\n");
    const headingRx = /<PageHeader\b|<h1\b/;
    const h1Lines = lines.flatMap((line, index) => (headingRx.test(line) ? [index] : []));
    if (h1Lines.length !== 1) return { problem: `${h1Lines.length} headings (<PageHeader / <h1>) in the file (expected one)` };
    const declaration = lines.findIndex((line) => new RegExp(`function ${component}\\(`).test(line));
    if (declaration < 0) return { problem: `no function ${component}` };
    const indent = (line: string) => line.length - line.trimStart().length;
    const declIndent = indent(lines[declaration]);
    // The body: after the declaration's last line (the one ending in "{"), up to its closing "}".
    let start = declaration;
    while (!/\{\s*$/.test(lines[start]) || indent(lines[start]) !== declIndent) start += 1;
    let end = start + 1;
    while (!(lines[end].trim() === "}" && indent(lines[end]) === declIndent)) end += 1;
    const [at] = h1Lines;
    if (at <= start || at >= end) {
        // A thin wrapper (the task boards: `<Board kind="all" />`) renders a component of the same file: follow it.
        const body = lines.slice(start + 1, end).join("\n");
        const local = [...body.matchAll(/<([A-Z]\w*)[\s/>]/g)].map((m) => m[1]).find((tag) => lines.some((line) => new RegExp(`^function ${tag}\\(`).test(line)));
        return local ? primaryHeading(file, local) : { problem: `the <h1> is outside ${component}` };
    }
    const bodyIndent = indent(lines.slice(start + 1, end).find((line) => line.trim() !== "") ?? "");
    // The statement of the body that holds the heading: the nearest line above it at the body's indentation.
    let statement = at;
    while (statement > start && !(lines[statement].trim() !== "" && indent(lines[statement]) === bodyIndent)) statement -= 1;
    if (/^(if\b|\} else|else\b|case\b)/.test(lines[statement].trim())) return { problem: `the <h1> is in a branch: "${lines[statement].trim()}"` };
    const markup = lines.slice(at).join("\n");
    if (/<h1\b/.test(lines[at])) return { markup: markup.slice(0, markup.indexOf("</h1>")) };
    // A PageHeader names the page by its `title` prop (the H1 of a menu page).
    const title = /\btitle=\{((?:[^{}\n]|\{[^{}\n]*\})*)\}/.exec(markup);
    return title ? { markup: title[1] } : { problem: "a <PageHeader without a title on its own line" };
}

describe("one name per page (UI3B)", () => {
    // Redirects only (no heading of their own).
    const REDIRECTS = new Set(["/app/users", "/app/operations/roles"]);
    const pages = [...new Set([...urlsOf(MAIN_NAV), ...SYSTEM_NAV.map((l) => l.url), ...SECURITY_NAV.map((l) => l.url), "/app/payroll/config", "/app/payroll/penalties", "/app/utilities/billing-impact"])];

    it("every menu page's primary H1, its PageHeader's title, reads the key of its menu entry (= its crumb and its tab title)", () => {
        expect(pages.length).toBe(44);
        for (const url of pages) {
            if (REDIRECTS.has(url)) continue;
            const { crumbs } = breadcrumbTrail(url);
            const key = crumbs[crumbs.length - 1].labelKey;
            const { file, name } = pageComponent(url);
            const where = `${url}: ${path.relative(WEB_ROOT, file)} ${name}`;
            const { markup, problem } = primaryHeading(file, name);
            expect(problem, where).toBeUndefined();
            const literals = [...(markup ?? "").matchAll(/["']([a-zA-Z]+\.[a-zA-Z.]+)["']/g)].map((m) => m[1]);
            if (literals.length > 0) expect(literals, where).toContain(key);
            // A heading read through a lookup (the task boards' `keys.title`) still names the key in the same file.
            else expect(fs.readFileSync(file, "utf8"), where).toContain(`"${key}"`);
        }
    });

    it("refuses a heading that only an error branch renders", () => {
        const dir = fs.mkdtempSync(path.join(os.tmpdir(), "ui3b-h1-"));
        try {
            const file = path.join(dir, "Page.tsx");
            fs.writeFileSync(
                file,
                [
                    "export function Page() {",
                    "    const failed = true;",
                    "    if (failed) {",
                    '        return <h1>{t("nav.income")}</h1>;',
                    "    }",
                    "    return (",
                    "        <div>body</div>",
                    "    );",
                    "}",
                    "",
                ].join("\n")
            );
            expect(primaryHeading(file, "Page").problem).toMatch(/in a branch/);
            fs.writeFileSync(file, ["export function Page() {", "    return (", '        <h1>{t("nav.income")}</h1>', "    );", "}", ""].join("\n"));
            expect(primaryHeading(file, "Page")).toEqual({ markup: '        <h1>{t("nav.income")}' });
            // UI3D: the PageHeader is the heading; its title is what the menu key is checked against.
            fs.writeFileSync(
                file,
                ["export function Page() {", "    return (", "        <PageHeader", '            title={t("nav.income")}', '            description={t("income.subtitle")}', "        />", "    );", "}", ""].join("\n")
            );
            expect(primaryHeading(file, "Page")).toEqual({ markup: 't("nav.income")' });
            fs.writeFileSync(
                file,
                ["export function Page() {", "    if (failed) {", '        return <PageHeader title={t("nav.income")} />;', "    }", "    return <div />;", "}", ""].join("\n")
            );
            expect(primaryHeading(file, "Page").problem).toMatch(/in a branch/);
        } finally {
            fs.rmSync(dir, { recursive: true, force: true });
        }
    });
});
