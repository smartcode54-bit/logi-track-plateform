/**
 * Route -> capability map of the `/app` route group (R5, R27, R39; developer-spec.md §10.5, Appendix C
 * §C.2.7), shared by the `proxy.ts` edge gate and, from TW4 on, the sidebar over `['me']`.
 *
 * The table itself is `ROUTE_CAPABILITIES` of `shared-docs/schemas/capabilities.ts`, generated from the
 * Go catalog (`logitrack-api/internal/authz/webroutes.go`, `go generate ./internal/authz`) with colon
 * keys; it replaces the legacy table that `lib/capabilities.ts:340-394` held until TW3. Rules:
 * exact path, else the longest mapped prefix at a segment boundary; an unmapped path is denied; an
 * empty list (`/app/dashboard`, `/app/unauthorized`) admits every signed-in principal; otherwise any
 * one of the listed keys. It is a necessary-condition filter: Go authorises every request again.
 *
 * No server code and no environment here: the module is safe in the browser bundle.
 */
import { ROUTE_CAPABILITIES, type CapabilityKey } from "shared-docs/schemas/capabilities";

export { ROUTE_CAPABILITIES };
export type { CapabilityKey };

export const APP_ROOT = "/app";
export const DASHBOARD_PATH = "/app/dashboard";
export const DRIVER_MONITOR_PATH = "/app/driver-monitor";
export const UNAUTHORIZED_PATH = "/app/unauthorized";

/** `/app/x/` -> `/app/x`; the root stays `/`. */
export function normalizeAppPath(pathname: string): string {
    const p = pathname.replace(/\/+$/, "");
    return p === "" ? "/" : p;
}

/** The entry that governs `pathname`: exact, else the longest mapped prefix; undefined when unmapped. */
export function routeFor(pathname: string): { matched: string; capabilities: readonly CapabilityKey[] } | undefined {
    let p = normalizeAppPath(pathname);
    for (;;) {
        const caps = Object.prototype.hasOwnProperty.call(ROUTE_CAPABILITIES, p) ? ROUTE_CAPABILITIES[p] : undefined;
        if (caps) return { matched: p, capabilities: caps };
        const i = p.lastIndexOf("/");
        if (i <= 0) return undefined;
        p = p.slice(0, i);
    }
}

/** Whether a principal holding `capabilities` may open `pathname` (unmapped: never). */
export function routeAllowed(capabilities: Iterable<string>, pathname: string): boolean {
    const route = routeFor(pathname);
    if (!route) return false;
    if (route.capabilities.length === 0) return true;
    const held = capabilities instanceof Set ? (capabilities as Set<string>) : new Set(capabilities);
    return route.capabilities.some((k) => held.has(k));
}

/** What the gate knows about a principal from its token and `GET /v1/me`. */
export interface HomePrincipal {
    /** Membership role in the active tenant (`tenant_admin`, `manager`, `operation_staff`, `operator`, `user`, `driver`). */
    tenantRole?: string;
    /** Kind of the active tenant (`own_fleet`, `carrier`). */
    tenantKind?: string;
    /** Holds a customer scope (with or without a membership). */
    customerScope: boolean;
    dispatcher: boolean;
    capabilities: Iterable<string>;
}

/**
 * The role's home route, where `/app` itself lands (R89; was `getDefaultRouteForRole`,
 * `lib/permissions.ts:92`, over Firebase claims): the legacy customer, operator and partner roles
 * landed on the driver monitor and everybody else on the dashboard. In Go terms: a customer-scope
 * principal without a membership, a dispatcher, an operator and the `tenant_admin` of a carrier
 * tenant (legacy partner, D2) go to `/app/driver-monitor`; the rest to `/app/dashboard`. A home the
 * principal cannot open falls back to the dashboard, which is open to every signed-in principal.
 */
export function homeRouteFor(p: HomePrincipal): string {
    const monitor =
        (p.customerScope && !p.tenantRole) ||
        p.dispatcher ||
        p.tenantRole === "operator" ||
        (p.tenantRole === "tenant_admin" && p.tenantKind === "carrier");
    if (monitor && routeAllowed(p.capabilities, DRIVER_MONITOR_PATH)) return DRIVER_MONITOR_PATH;
    return DASHBOARD_PATH;
}
