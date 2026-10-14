/**
 * The navigation of the `/app` shell, defined once (UI3, issue #143): the main sidebar, its system group, the
 * Security Center sidebar and the header breadcrumbs all read these entries, so a crumb is the menu's own
 * label in the active language (an i18n key of the base dictionary), never a URL slug.
 *
 * Which entries a principal sees is still decided by the edge gate's table (lib/routeCapabilities.ts, TW4).
 */
import type { LucideIcon } from "lucide-react";
import {
    Briefcase,
    Building2,
    Calculator,
    Key,
    Layers,
    LayoutDashboard,
    Mail,
    MapPin,
    MessageCircle,
    Rocket,
    Server,
    Shield,
    Smartphone,
    Truck,
    User,
    Users,
    Wrench,
} from "lucide-react";

export interface NavLink {
    url: string;
    /** Translation key of the label (base dictionary only: the header renders it on every route). */
    labelKey: string;
    icon?: LucideIcon;
    /** The path prefix the entry stands for when it differs from `url` (the utilities open on their first page). */
    match?: string;
}

export interface NavGroup {
    labelKey: string;
    icon: LucideIcon;
    items: readonly NavLink[];
}

export type NavEntry = NavLink | NavGroup;

export function isNavGroup(entry: NavEntry): entry is NavGroup {
    return "items" in entry;
}

export const MAIN_NAV: readonly NavEntry[] = [
    { url: "/app/dashboard", labelKey: "nav.dashboard", icon: LayoutDashboard },
    {
        labelKey: "nav.fleets",
        icon: Truck,
        items: [
            { url: "/app/trucks", labelKey: "nav.truckManagement" },
            { url: "/app/truck-assignment", labelKey: "nav.truckAssignment" },
            { url: "/app/renewals", labelKey: "nav.truckRenewals" },
            { url: "/app/maintenance", labelKey: "nav.maintenanceCosts" },
        ],
    },
    { url: "/app/customers", labelKey: "nav.customers", icon: Building2 },
    { url: "/app/subcontractors", labelKey: "nav.manageSubcontractors", icon: Briefcase },
    { url: "/app/drivers", labelKey: "nav.driverManagement", icon: User },
    { url: "/app/chat", labelKey: "nav.chat", icon: MessageCircle },
    { url: "/app/waitlist", labelKey: "nav.waitlist", icon: Mail },
    {
        labelKey: "nav.accounting",
        icon: Calculator,
        items: [
            { url: "/app/accounting/fuel", labelKey: "nav.fuel" },
            { url: "/app/accounting/fuel-price-history", labelKey: "nav.fuelPriceHistory" },
            { url: "/app/accounting/other", labelKey: "nav.other" },
            { url: "/app/accounting/audit", labelKey: "nav.auditExpense" },
            { url: "/app/accounting/rate-card", labelKey: "nav.rateCard" },
            { url: "/app/accounting/income", labelKey: "nav.income" },
            { url: "/app/accounting/billing-document", labelKey: "nav.billingDocument" },
            { url: "/app/accounting/billing-result", labelKey: "nav.billingResult" },
            { url: "/app/accounting/shopee-express-report", labelKey: "nav.shopeeExpressReport" },
        ],
    },
    {
        labelKey: "nav.operations",
        icon: MapPin,
        items: [
            { url: "/app/job-assign", labelKey: "nav.jobAssign" },
            { url: "/app/sources", labelKey: "nav.sourceManagement" },
            { url: "/app/driver-monitor", labelKey: "nav.driverMonitor" },
            { url: "/app/incident-reports", labelKey: "nav.incidentReports" },
            { url: "/app/standby-records", labelKey: "nav.standbyRecords" },
        ],
    },
    {
        labelKey: "nav.hr",
        icon: Users,
        items: [
            { url: "/app/payroll", labelKey: "nav.payroll" },
            { url: "/app/leave-requests", labelKey: "nav.leaveRequests" },
            { url: "/app/holidays", labelKey: "nav.holidays" },
        ],
    },
];

export const SECURITY_CENTER_PATH = "/app/security-center";

/** The system group at the foot of the main sidebar. */
export const SYSTEM_NAV: readonly NavLink[] = [
    { url: SECURITY_CENTER_PATH, labelKey: "nav.securityCenter", icon: Shield },
    { url: "/app/utilities/backfill", labelKey: "nav.utilities", icon: Wrench, match: "/app/utilities" },
    { url: "/app/companies", labelKey: "nav.companies", icon: Building2 },
    { url: "/app/settings/company-profile", labelKey: "nav.companyProfile", icon: Building2 },
];

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

/** A crumb: a label key, and a link unless it names a menu group or the current page. */
export interface Crumb {
    labelKey: string;
    href?: string;
}

interface Located {
    link: NavLink;
    /** The crumbs above the entry (its menu group). */
    parents: readonly Crumb[];
}

/** Pages opened from another page, not from the menu: named here for the breadcrumb only. */
const PAGE_ONLY: readonly Located[] = [
    { link: { url: "/app/first-mile", labelKey: "nav.firstMileTasks" }, parents: [{ labelKey: "nav.operations" }] },
    { link: { url: "/app/line-haul", labelKey: "nav.lineHaulTasks" }, parents: [{ labelKey: "nav.operations" }] },
    { link: { url: "/app/users", labelKey: "nav.users" }, parents: [] },
    // Redirects to the Security Center's matrix; named while the redirect runs.
    { link: { url: "/app/operations/roles", labelKey: "nav.rolePermission" }, parents: [] },
    { link: { url: "/app/unauthorized", labelKey: "unauthorized.title" }, parents: [] },
];

const SECURITY_PARENT: Crumb = { labelKey: "nav.securityCenter", href: SECURITY_CENTER_PATH };

const LOCATED: readonly Located[] = [
    ...MAIN_NAV.flatMap((entry): Located[] =>
        isNavGroup(entry) ? entry.items.map((link) => ({ link, parents: [{ labelKey: entry.labelKey }] })) : [{ link: entry, parents: [] }]
    ),
    ...SYSTEM_NAV.map((link) => ({ link, parents: [] })),
    // The overview is the Security Center itself (SYSTEM_NAV); its pages sit under it.
    ...SECURITY_NAV.filter((link) => link.url !== SECURITY_CENTER_PATH).map((link) => ({ link, parents: [SECURITY_PARENT] })),
    ...PAGE_ONLY,
];

function stripTrailingSlash(pathname: string): string {
    const p = pathname.replace(/\/+$/, "");
    return p === "" ? "/" : p;
}

const covers = (prefix: string, path: string) => path === prefix || path.startsWith(`${prefix}/`);

/** The entry `pathname` belongs to: the exact one, else the longest one it sits under; undefined outside the menu. */
function locate(pathname: string): Located | undefined {
    const path = stripTrailingSlash(pathname);
    let best: Located | undefined;
    let bestLength = -1;
    for (const located of LOCATED) {
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
    return pathname ? locate(pathname)?.link.url : undefined;
}

export interface BreadcrumbTrail {
    /** Group and page crumbs, after Home; the last one is the page. */
    crumbs: Crumb[];
    /**
     * The path is below its entry (a truck's view, a customer's edit page): the entry's label stands for the page
     * until the page names itself (`useBreadcrumb().setCustomLastItem`), then it becomes a link.
     */
    detail: boolean;
}

/** The breadcrumb of `pathname` from the menu's labels; no crumbs for a page outside the menu. */
export function breadcrumbTrail(pathname: string): BreadcrumbTrail {
    const located = locate(pathname);
    if (!located) return { crumbs: [], detail: false };
    const path = stripTrailingSlash(pathname);
    return {
        crumbs: [...located.parents, { labelKey: located.link.labelKey, href: located.link.url }],
        detail: path !== located.link.url,
    };
}
