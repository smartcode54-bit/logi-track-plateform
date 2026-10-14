/**
 * What the 403 page (app/app/unauthorized/page.tsx, UI3D) tells a principal: the attempted page by its menu name, and
 * who can open it for them.
 */
import { STEWARD_CAPABILITIES } from "shared-docs/schemas/capabilities";
import { breadcrumbTrail } from "@/components/app-nav";
import { routeFor } from "@/lib/routeCapabilities";
import { isDriverOnly } from "../api/homeRoute";
import type { MeDTO } from "../api/me";

/** The `unauthorized.guide.<audience>` line a principal reads. */
export type UnauthorizedAudience = "driver" | "external" | "ownFleet" | "tenantAdmin" | "platform" | "staff";

const STEWARD = new Set<string>(STEWARD_CAPABILITIES);

/**
 * Whether `from` needs only global keys (customers, partners, pickup points, system status, mobile release, waitlist):
 * those are effective for the own fleet's staff and platform admins only (Go `ApplySteward`, R60), so no role or
 * override of a partner carrier's tenant can open them.
 */
function ownFleetOnly(from: string): boolean {
    const caps = routeFor(from)?.capabilities ?? [];
    return caps.length > 0 && caps.every((c) => STEWARD.has(c));
}

/**
 * Who can change what this principal opens:
 * - a driver-only account works in the driver app (the gate sends every `/app` path of a driver to the 403 page);
 * - anyone in a partner carrier's tenant, on a page that needs only global keys: nobody can, the page belongs to the
 *   own fleet (`ownFleet`);
 * - a customer view, a dispatcher, and a partner carrier's tenant admin ("Partner admin"): the transport company that
 *   invited them;
 * - an own-fleet tenant admin: nobody inside the tenant, since `tenant_admin` is not overridable (Go
 *   `authz.Overridable`); the platform team at GORATECH;
 * - a platform role outside a tenant: platform roles open the platform's pages, not a company's work;
 * - other staff (a partner carrier's included, whose admin can grant tenant pages): their organisation's administrator,
 *   who sets role pages in the Security center's role matrix.
 */
export function unauthorizedAudience(me: MeDTO | null | undefined, from = ""): UnauthorizedAudience {
    if (!me) return "staff";
    if (isDriverOnly(me)) return "driver";
    if (!me.tenant) return me.platformRoles.length > 0 ? "platform" : "external";
    if (me.tenant.kind === "carrier" && ownFleetOnly(from)) return "ownFleet";
    if (me.dispatcher) return "external";
    if (me.tenant.role === "tenant_admin") return me.tenant.kind === "carrier" ? "external" : "tenantAdmin";
    return "staff";
}

/** The attempted page as the menu names it ("Trips to bill", "New · Trucks"); "" for a path outside the menu. */
export function attemptedPageName(from: string, t: (key: string) => string, home?: string | null): string {
    if (!from.startsWith("/app/")) return "";
    const { crumbs, detailKey } = breadcrumbTrail(from, home);
    if (crumbs.length === 0) return "";
    return [detailKey ? t(detailKey) : "", t(crumbs[crumbs.length - 1].labelKey)].filter(Boolean).join(" · ");
}
