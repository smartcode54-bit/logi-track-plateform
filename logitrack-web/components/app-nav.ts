/**
 * The shell's complete route catalog and profile menus (UI3F). Capability filtering preserves access; profile
 * allowlists only shorten presentation. Sidebars, breadcrumbs and quick-jump share these labels and ordering.
 */
import type { LucideIcon } from "lucide-react";
import {
    AlertTriangle,
    ClipboardList,
    HelpCircle,
    IdCard,
    Inbox,
    Key,
    Landmark,
    Layers,
    LayoutDashboard,
    MessageSquare,
    Radar,
    ReceiptText,
    Rocket,
    Route,
    Server,
    Settings,
    Shield,
    Building2,
    Smartphone,
    Truck,
    Users,
    Wallet,
    Wrench,
} from "lucide-react";

import { CUSTOMER_MENU_HREFS, PARTNER_MENU, profileRouteLabelKey, type NavigationProfile } from "@/features/auth/utils/navigationProfile";
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
        { url: "/app/job-assign", labelKey: "nav.jobAssign", icon: ClipboardList },
        { url: "/app/first-mile", labelKey: "nav.firstMileTasks" },
        { url: "/app/line-haul", labelKey: "nav.lineHaulTasks" },
        DRIVER_MONITOR,
        { url: "/app/incident-reports", labelKey: "nav.incidentReports", icon: AlertTriangle },
        { url: "/app/standby-records", labelKey: "nav.standbyRecords" },
        // Dispatch's line to the drivers on the road (and its broadcasts).
        { url: "/app/chat", labelKey: "nav.chat", icon: MessageSquare },
        { url: "/app/sources", labelKey: "nav.sourceManagement" },
    ],
};

/** Customer records, prices, trips, documents and statements, then the billing tools. */
const BILLING: NavGroup = {
    labelKey: "nav.billing",
    icon: ReceiptText,
    items: [
        { url: "/app/customers", labelKey: "nav.customers" },
        { url: "/app/accounting/rate-card", labelKey: "nav.rateCard" },
        { url: "/app/accounting/income", labelKey: "nav.income" },
        { url: "/app/accounting/billing-document", labelKey: "nav.billingDocument", icon: ReceiptText },
        { url: "/app/accounting/billing-result", labelKey: "nav.billingResult" },
        { url: "/app/accounting/shopee-express-report", labelKey: "nav.shopeeExpressReport" },
        { url: "/app/utilities/backfill", labelKey: "nav.utilities", icon: Wrench, match: "/app/utilities" },
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
 * Fleet master data and carrying partners. Maintenance is here, not under Costs: its work is booking and
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

export const SECURITY_CENTER_PATH = "/app/security-center";

const SETTINGS: NavGroup = {
    labelKey: "nav.settings",
    icon: Settings,
    items: [
        { url: "/app/companies", labelKey: "nav.companies", icon: Landmark },
        { url: "/app/settings/company-profile", labelKey: "nav.companyProfile", icon: IdCard },
    ],
};

const PLATFORM: NavGroup = {
    labelKey: "nav.platform",
    icon: Shield,
    items: [
        { url: SECURITY_CENTER_PATH, labelKey: "nav.securityCenter", icon: Shield },
        { url: "/app/waitlist", labelKey: "nav.waitlist", icon: Inbox },
    ],
};

/** The complete main catalog, before capability filtering or presentation allowlists. */
export const MAIN_NAV: readonly NavEntry[] = [DASHBOARD, OPERATIONS, BILLING, COSTS, FLEET, HR, SETTINGS, PLATFORM];

const MONITOR_HOME_NAV: readonly NavEntry[] = [
    DRIVER_MONITOR,
    { ...OPERATIONS, items: [...OPERATIONS.items.filter((link) => link.url !== DRIVER_MONITOR_PATH), DASHBOARD] },
    BILLING,
    COSTS,
    FLEET,
    HR,
    SETTINGS,
    PLATFORM,
];

/** The main catalog for the computed home; the monitor occurs only once when it is home. */
export function mainNavFor(home: string | null | undefined): readonly NavEntry[] {
    return home === DRIVER_MONITOR_PATH ? MONITOR_HOME_NAV : MAIN_NAV;
}

export const SUPPORT_NAV: NavLink = { url: "/app/support", labelKey: "nav.supportCenter", icon: HelpCircle };

/** Footer links still belong inside the sidebar's one navigation landmark. */
export interface NavMenu {
    main: readonly NavEntry[];
    footer: readonly NavLink[];
}

export interface MenuLink {
    href: string;
    labelKey: string;
    groupLabelKey?: string;
}

/** Actual destinations in display order, including footer links; the first occurrence of a URL wins. */
export function menuLinksFor(menu: NavMenu): MenuLink[] {
    const seen = new Set<string>();
    const links: MenuLink[] = [];
    const add = (link: NavLink, groupLabelKey?: string) => {
        if (seen.has(link.url)) return;
        seen.add(link.url);
        links.push({ href: link.url, labelKey: link.labelKey, ...(groupLabelKey ? { groupLabelKey } : {}) });
    };
    for (const entry of menu.main) {
        if (isNavGroup(entry)) for (const link of entry.items) add(link, entry.labelKey);
        else add(entry);
    }
    for (const link of menu.footer) add(link);
    return links;
}

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

/** The complete capability-filtered menu, independently of a principal's presentation profile. */
export function allowedMenuFor(held: ReadonlySet<string>, home: string | null | undefined): NavMenu {
    const main = mainNavFor(home).flatMap((entry): NavEntry[] => {
        if (isNavGroup(entry)) {
            const items = entry.items.filter((link) => routeAllowed(held, link.url));
            return items.length > 0 ? [{ ...entry, items }] : [];
        }
        return routeAllowed(held, entry.url) ? [entry] : [];
    });
    return { main, footer: routeAllowed(held, SUPPORT_NAV.url) ? [SUPPORT_NAV] : [] };
}

function profileMenu(menu: NavMenu, home: string | null | undefined, profile: NavigationProfile): NavMenu {
    if (profile === "staff") return menu;
    const catalog = new Map<string, NavLink>();
    for (const entry of [...menu.main, ...menu.footer]) {
        for (const link of isNavGroup(entry) ? entry.items : [entry]) {
            catalog.set(link.url, { ...link, labelKey: profileRouteLabelKey(link.url, profile) ?? link.labelKey });
        }
    }
    const linksFor = (hrefs: readonly string[]) => hrefs.flatMap((href) => {
        const link = catalog.get(href);
        return link ? [link] : [];
    });
    const entries: NavEntry[] = profile === "customer"
        ? linksFor(CUSTOMER_MENU_HREFS)
        : PARTNER_MENU.flatMap((entry): NavEntry[] => {
            if (!("items" in entry)) return linksFor([entry.href]);
            const items = linksFor(entry.items.map((item) => item.href));
            return items.length > 0 ? [{ labelKey: entry.labelKey, icon: Truck, items }] : [];
        });
    const homeLink = home ? catalog.get(home) : undefined;
    const main = entries.flatMap((entry): NavEntry[] => {
        if (isNavGroup(entry)) {
            const items = entry.items.filter((link) => link.url !== homeLink?.url);
            return items.length > 0 ? [{ ...entry, items }] : [];
        }
        return entry.url !== homeLink?.url && entry.url !== SUPPORT_NAV.url ? [entry] : [];
    });
    return { main: homeLink ? [homeLink, ...main] : main, footer: linksFor([SUPPORT_NAV.url]) };
}

/** Profile allowlists change presentation only, after intersecting the complete catalog with capabilities. */
export function menuFor(held: ReadonlySet<string>, home: string | null | undefined, profile: NavigationProfile = "staff"): NavMenu {
    return profileMenu(allowedMenuFor(held, home), home, profile);
}

/** Security Center's own allowed pages, followed by the return home and Support links. */
export function securityMenuFor(held: ReadonlySet<string>, home: string | null | undefined): { main: NavLink[]; footer: NavLink[] } {
    const homeHref = home ?? DASHBOARD_PATH;
    return {
        main: SECURITY_NAV.filter((link) => routeAllowed(held, link.url)),
        footer: [
            ...(routeAllowed(held, homeHref) ? [{ url: homeHref, labelKey: "securityCenter.backToAdmin", icon: LayoutDashboard }] : []),
            ...(routeAllowed(held, SUPPORT_NAV.url) ? [SUPPORT_NAV] : []),
        ],
    };
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
        parents: [{ labelKey: "nav.billing" }, { labelKey: "nav.utilities", href: "/app/utilities/backfill" }],
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
        { link: SUPPORT_NAV, parents: [] },
        // The overview is the catalog's Security Center entry; its pages sit under it.
        ...SECURITY_NAV.filter((link) => link.url !== SECURITY_CENTER_PATH).map((link) => ({ link, parents: [{ labelKey: "nav.platform" }, SECURITY_PARENT] })),
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

/** Hidden routes keep their canonical trail; visible profile destinations follow their presented grouping. */
export function breadcrumbTrail(pathname: string, home?: string | null, profile: NavigationProfile = "staff"): BreadcrumbTrail {
    const located = locate(pathname, home);
    if (!located) return { crumbs: [], detail: false };
    const path = stripTrailingSlash(pathname);
    const menu = profileMenu({ main: mainNavFor(home), footer: [SUPPORT_NAV] }, home, profile);
    const visible = menuLinksFor(menu).find((link) => link.href === located.link.url);
    const parents = visible ? (visible.groupLabelKey ? [{ labelKey: visible.groupLabelKey }] : []) : located.parents;
    const labelKey = profileRouteLabelKey(located.link.url, profile) ?? located.link.labelKey;
    const crumbs = [...parents, { labelKey, href: located.link.url }];
    if (path === located.link.url) return { crumbs, detail: false };
    return { crumbs, detail: true, detailKey: detailKeyOf(path) };
}
