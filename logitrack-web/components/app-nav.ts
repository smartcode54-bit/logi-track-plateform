/**
 * The navigation of the `/app` shell, defined once (UI3, issue #143): the main sidebar, its system group, the
 * Security Center sidebar and the header breadcrumbs all read these entries, so a crumb is the menu's own
 * label in the active language (an i18n key of the base dictionary), never a URL slug. A page's H1 reads the same
 * key as its entry, so the menu, the crumb, the heading and the tab title name a page with one string (UI3B).
 *
 * The order follows the work loop (UI3B, owner's choice): Operations (assign, run, watch, resolve), Billing (the
 * revenue side: rate cards, trips to bill, documents, statements), Costs, Fleet & partners, then HR; the system
 * group (platform and tenant settings) sits at the foot. The principal's home (`homeRouteOf`) is the first entry, so
 * a customer, partner or dispatcher whose home is the driver monitor does not see a second "home" above it.
 *
 * Which entries a principal sees is still decided by the edge gate's table (lib/routeCapabilities.ts, TW4):
 * `menuFor` only filters, it never adds a route the gate would refuse.
 */
import type { LucideIcon } from "lucide-react";
import {
    HelpCircle,
    IdCard,
    Inbox,
    Key,
    Landmark,
    Layers,
    LayoutDashboard,
    Radar,
    ReceiptText,
    Rocket,
    Route,
    Server,
    Shield,
    Building2,
    Smartphone,
    Truck,
    Users,
    Wallet,
    Wrench,
} from "lucide-react";

import { DASHBOARD_PATH, DRIVER_MONITOR_PATH, routeAllowed } from "@/lib/routeCapabilities";

export interface NavLink {
    url: string;
    /** Translation key of the label (base dictionary only: the header renders it on every route). */
    labelKey: string;
    icon?: LucideIcon;
    /** The path prefix the entry stands for when it differs from `url` (the utilities open on their first page). */
    match?: string;
}

export interface NavGroup {
    /** Also the group's identity (which group is open). */
    labelKey: string;
    icon: LucideIcon;
    items: readonly NavLink[];
}

export type NavEntry = NavLink | NavGroup;

export function isNavGroup(entry: NavEntry): entry is NavGroup {
    return "items" in entry;
}

const DASHBOARD: NavLink = { url: DASHBOARD_PATH, labelKey: "nav.dashboard", icon: LayoutDashboard };
const DRIVER_MONITOR: NavLink = { url: DRIVER_MONITOR_PATH, labelKey: "nav.driverMonitor", icon: Radar };

/** Assign, run the boards, watch the trips, resolve what goes wrong; the pickup points are the boards' master data. */
const OPERATIONS: NavGroup = {
    labelKey: "nav.operations",
    icon: Route,
    items: [
        { url: "/app/job-assign", labelKey: "nav.jobAssign" },
        { url: "/app/first-mile", labelKey: "nav.firstMileTasks" },
        { url: "/app/line-haul", labelKey: "nav.lineHaulTasks" },
        DRIVER_MONITOR,
        { url: "/app/incident-reports", labelKey: "nav.incidentReports" },
        { url: "/app/standby-records", labelKey: "nav.standbyRecords" },
        // Dispatch's line to the drivers on the road (and its broadcasts).
        { url: "/app/chat", labelKey: "nav.chat" },
        { url: "/app/sources", labelKey: "nav.sourceManagement" },
    ],
};

/** The revenue side, in the order a period is billed: prices, the trips to bill, the documents, the statements. */
const BILLING: NavGroup = {
    labelKey: "nav.billing",
    icon: ReceiptText,
    items: [
        { url: "/app/accounting/rate-card", labelKey: "nav.rateCard" },
        { url: "/app/accounting/income", labelKey: "nav.income" },
        { url: "/app/accounting/billing-document", labelKey: "nav.billingDocument" },
        { url: "/app/accounting/billing-result", labelKey: "nav.billingResult" },
        { url: "/app/accounting/shopee-express-report", labelKey: "nav.shopeeExpressReport" },
    ],
};

/** Vehicle expenses and their audit (accounting). */
const COSTS: NavGroup = {
    labelKey: "nav.costs",
    icon: Wallet,
    items: [
        { url: "/app/accounting/fuel", labelKey: "nav.fuel" },
        { url: "/app/accounting/fuel-price-history", labelKey: "nav.fuelPriceHistory" },
        { url: "/app/accounting/other", labelKey: "nav.otherExpenses" },
        { url: "/app/accounting/audit", labelKey: "nav.auditExpense" },
    ],
};

/**
 * Master data of the operation: trucks and who drives them, their papers and upkeep, and the parties on either
 * side (customers billed, partners carrying). Maintenance is here, not under Costs: its work is booking and
 * scheduling services (`fleet:manage_maintenance`, held by operators without any accounting capability), and its
 * cost cards summarise what those services cost.
 */
const FLEET: NavGroup = {
    labelKey: "nav.fleetPartners",
    icon: Truck,
    items: [
        { url: "/app/trucks", labelKey: "nav.trucks" },
        { url: "/app/truck-assignment", labelKey: "nav.truckAssignment" },
        { url: "/app/drivers", labelKey: "nav.drivers" },
        { url: "/app/renewals", labelKey: "nav.truckRenewals" },
        { url: "/app/maintenance", labelKey: "nav.maintenance" },
        { url: "/app/customers", labelKey: "nav.customers" },
        { url: "/app/subcontractors", labelKey: "nav.partners" },
    ],
};

const HR: NavGroup = {
    labelKey: "nav.hr",
    icon: Users,
    items: [
        { url: "/app/payroll", labelKey: "nav.payroll" },
        { url: "/app/leave-requests", labelKey: "nav.leaveRequests" },
        { url: "/app/holidays", labelKey: "nav.holidays" },
    ],
};

/** The main menu of a principal whose home is the dashboard. */
export const MAIN_NAV: readonly NavEntry[] = [DASHBOARD, OPERATIONS, BILLING, COSTS, FLEET, HR];

/**
 * The main menu of a principal whose home is the driver monitor (customers, partners, dispatchers, operators): the
 * monitor is the first entry, and the dashboard joins the operations it summarises.
 */
const MONITOR_HOME_NAV: readonly NavEntry[] = [
    DRIVER_MONITOR,
    { ...OPERATIONS, items: [...OPERATIONS.items.filter((link) => link.url !== DRIVER_MONITOR_PATH), DASHBOARD] },
    BILLING,
    COSTS,
    FLEET,
    HR,
];

/** The main menu for `home` (`homeRouteOf(me)`): the home is its first entry. */
export function mainNavFor(home: string | null | undefined): readonly NavEntry[] {
    return home === DRIVER_MONITOR_PATH ? MONITOR_HOME_NAV : MAIN_NAV;
}

export const SECURITY_CENTER_PATH = "/app/security-center";

/**
 * The system group at the foot of the main sidebar: the platform's own pages (the Security Center, GORATECH's SaaS
 * waitlist; UI3B moved the waitlist here from the tenant's menu) and the tenant's settings.
 */
export const SYSTEM_NAV: readonly NavLink[] = [
    { url: SECURITY_CENTER_PATH, labelKey: "nav.securityCenter", icon: Shield },
    { url: "/app/waitlist", labelKey: "nav.waitlist", icon: Inbox },
    { url: "/app/utilities/backfill", labelKey: "nav.utilities", icon: Wrench, match: "/app/utilities" },
    { url: "/app/companies", labelKey: "nav.companies", icon: Landmark },
    { url: "/app/settings/company-profile", labelKey: "nav.companyProfile", icon: IdCard },
];

/**
 * The footer entry of the main sidebar (UI3C): help inside the console, open to every console principal (the route
 * maps to no capability), so it is not filtered by `menuFor`. Its crumb is the page itself, like the dashboard.
 */
export const SUPPORT_NAV: NavLink = { url: "/app/support", labelKey: "nav.supportCenter", icon: HelpCircle };

/** The Security Center sidebar (it replaces the main one under `/app/security-center`). */
export const SECURITY_NAV: readonly NavLink[] = [
    { url: SECURITY_CENTER_PATH, labelKey: "securityCenter.overviewSoon", icon: LayoutDashboard },
    { url: "/app/security-center/users", labelKey: "securityCenter.userManagement", icon: Users },
    { url: "/app/security-center/tenants", labelKey: "securityCenter.tenants", icon: Building2 },
    { url: "/app/security-center/roles", labelKey: "securityCenter.rolePermissionMatrix", icon: Layers },
    { url: "/app/security-center/audit", labelKey: "securityCenter.securityAudit", icon: Shield },
    { url: "/app/security-center/api-keys", labelKey: "securityCenter.apiKeys", icon: Key },
    { url: "/app/security-center/status", labelKey: "securityCenter.systemStatus", icon: Server },
    { url: "/app/security-center/mobile-clients", labelKey: "securityCenter.mobileClients", icon: Smartphone },
    { url: "/app/security-center/mobile-release", labelKey: "securityCenter.mobileRelease", icon: Rocket },
];

/** The menu a principal holding `held` sees: the entries (and groups with at least one entry) the gate would open. */
export function menuFor(held: ReadonlySet<string>, home: string | null | undefined): { main: NavEntry[]; system: NavLink[] } {
    const canOpen = (url: string) => routeAllowed(held, url);
    const main = mainNavFor(home).flatMap((entry): NavEntry[] => {
        if (isNavGroup(entry)) {
            const visible = entry.items.filter((link) => canOpen(link.url));
            return visible.length > 0 ? [{ ...entry, items: visible }] : [];
        }
        // `/app/dashboard` maps to no capability: every signed-in principal may open it.
        return canOpen(entry.url) ? [entry] : [];
    });
    return { main, system: SYSTEM_NAV.filter((link) => canOpen(link.url)) };
}

/** The group of `entries` that holds the entry `url`, by its label key; undefined for a top-level entry. */
export function groupOf(entries: readonly NavEntry[], url: string | undefined): string | undefined {
    if (!url) return undefined;
    return entries.find((entry) => isNavGroup(entry) && entry.items.some((link) => link.url === url))?.labelKey;
}

/** A crumb: a label key, and a link unless it names a menu group or the current page. */
export interface Crumb {
    labelKey: string;
    href?: string;
}

interface Located {
    link: NavLink;
    /** The crumbs above the entry (its menu group). */
    parents: readonly Crumb[];
    /** The menu entry to highlight when it is not `link` itself (a page opened from that entry's page). */
    menuUrl?: string;
}

const PAYROLL_PARENTS: readonly Crumb[] = [{ labelKey: "nav.hr" }, { labelKey: "nav.payroll", href: "/app/payroll" }];

/** Pages opened from another page, not from the menu: named here for the breadcrumb only. */
const PAGE_ONLY: readonly Located[] = [
    { link: { url: "/app/payroll/config", labelKey: "driverComp.config.title" }, parents: PAYROLL_PARENTS, menuUrl: "/app/payroll" },
    { link: { url: "/app/payroll/penalties", labelKey: "driverComp.penalty.title" }, parents: PAYROLL_PARENTS, menuUrl: "/app/payroll" },
    {
        link: { url: "/app/utilities/billing-impact", labelKey: "nav.billingImpact" },
        parents: [{ labelKey: "nav.utilities", href: "/app/utilities/backfill" }],
        menuUrl: "/app/utilities/backfill",
    },
    { link: { url: "/app/users", labelKey: "nav.users" }, parents: [] },
    // Redirects to the Security Center's matrix; named while the redirect runs.
    { link: { url: "/app/operations/roles", labelKey: "nav.rolePermission" }, parents: [] },
    { link: { url: "/app/unauthorized", labelKey: "unauthorized.title" }, parents: [] },
];

const SECURITY_PARENT: Crumb = { labelKey: "nav.securityCenter", href: SECURITY_CENTER_PATH };

function locatedIn(main: readonly NavEntry[]): readonly Located[] {
    return [
        ...main.flatMap((entry): Located[] =>
            isNavGroup(entry) ? entry.items.map((link) => ({ link, parents: [{ labelKey: entry.labelKey }] })) : [{ link: entry, parents: [] }]
        ),
        ...SYSTEM_NAV.map((link) => ({ link, parents: [] })),
        { link: SUPPORT_NAV, parents: [] },
        // The overview is the Security Center itself (SYSTEM_NAV); its pages sit under it.
        ...SECURITY_NAV.filter((link) => link.url !== SECURITY_CENTER_PATH).map((link) => ({ link, parents: [SECURITY_PARENT] })),
        ...PAGE_ONLY,
    ];
}

const LOCATED = locatedIn(MAIN_NAV);
const LOCATED_MONITOR_HOME = locatedIn(MONITOR_HOME_NAV);

function stripTrailingSlash(pathname: string): string {
    const p = pathname.replace(/\/+$/, "");
    return p === "" ? "/" : p;
}

const covers = (prefix: string, path: string) => path === prefix || path.startsWith(`${prefix}/`);

/** The entry `pathname` belongs to: the exact one, else the longest one it sits under; undefined outside the menu. */
function locate(pathname: string, home?: string | null): Located | undefined {
    const path = stripTrailingSlash(pathname);
    let best: Located | undefined;
    let bestLength = -1;
    for (const located of home === DRIVER_MONITOR_PATH ? LOCATED_MONITOR_HOME : LOCATED) {
        const prefix = located.link.match ?? located.link.url;
        if (covers(prefix, path) && prefix.length > bestLength) {
            best = located;
            bestLength = prefix.length;
        }
    }
    return best;
}

/** The menu entry `pathname` belongs to (its `url`), so a detail page keeps its entry highlighted. */
export function activeNavUrl(pathname: string | null | undefined): string | undefined {
    if (!pathname) return undefined;
    const located = locate(pathname);
    return located ? (located.menuUrl ?? located.link.url) : undefined;
}

export interface BreadcrumbTrail {
    /** Group and page crumbs, after Home. Without `detailKey` the last one is the page. */
    crumbs: Crumb[];
    /** The path is below its entry (a truck's view, a customer's edit page): the entry stays a link above the page. */
    detail: boolean;
    /**
     * The generic name of a detail page ("New", "Edit", "Details"), shown until the page names itself
     * (`useBreadcrumb().setCustomLastItem`, e.g. a truck's plate).
     */
    detailKey?: string;
}

/** "New", "Edit" or "Details" from the last segment of a detail path: words the menu does not hold. */
function detailKeyOf(path: string): string {
    const last = path.slice(path.lastIndexOf("/") + 1);
    if (last === "new") return "shell.crumb.new";
    if (last === "edit") return "shell.crumb.edit";
    return "shell.crumb.detail";
}

/**
 * The breadcrumb of `pathname` from the menu's labels (the menu of a principal whose home is `home`, so the crumbs
 * follow the groups that principal sees); no crumbs for a page outside the menu.
 */
export function breadcrumbTrail(pathname: string, home?: string | null): BreadcrumbTrail {
    const located = locate(pathname, home);
    if (!located) return { crumbs: [], detail: false };
    const path = stripTrailingSlash(pathname);
    const crumbs = [...located.parents, { labelKey: located.link.labelKey, href: located.link.url }];
    if (path === located.link.url) return { crumbs, detail: false };
    return { crumbs, detail: true, detailKey: detailKeyOf(path) };
}
