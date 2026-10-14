/**
 * The scope line of the app shell (T18; UI3, issue #143): which slice of the data the principal is working in and
 * as what. It is the one label source for the role: the sidebar header, the user menu (and its trigger) and the
 * 403 page all print `principalRoleLabel`. From `['me']`, never from the legacy claims (`getRole`).
 *
 * - Slice: the active tenant's name, else the customer scope's name ("CJSF", or "CJSF +1" for several).
 * - Role: the platform role (`platform_admin` first), else the role in the active tenant (a carrier's
 *   `tenant_admin` reads "Partner admin"), else `support`, else dispatcher, else "Customer view".
 *
 * Examples: "WRT · Tenant admin", "CJSF · Customer view", "Platform admin".
 */
import { tenantName, type MeDTO } from "../api/me";

type Translate = (key: string, fallbackOrParams?: string | Record<string, string | number>) => string;

export interface PrincipalScope {
    /** The tenant or customer scope the data is cut to; null when there is none (a platform admin outside a tenant). */
    slice: string | null;
    /** What the principal is in that slice. */
    role: string;
}

/** Joins slice and role; the same separator in both languages. */
const SEPARATOR = " · ";

/**
 * A customer view: customer scopes and nothing that outranks them (no tenant, no platform role, not a dispatcher;
 * Go's `authz.IsCustomerScope` also requires `!Dispatcher`). Only then do the scopes name the slice.
 */
function isCustomerView(me: MeDTO): boolean {
    return !me.tenant && me.platformRoles.length === 0 && !me.dispatcher && me.customerScopes.length > 0;
}

function roleOf(me: MeDTO, t: Translate): string {
    if (me.platformRoles.includes("platform_admin")) return t("users.platformRole.platform_admin");
    if (me.tenant) {
        if (me.tenant.kind === "carrier" && me.tenant.role === "tenant_admin") return t("shell.scope.partnerAdmin");
        return t(`users.tenantRole.${me.tenant.role}`, me.tenant.role);
    }
    if (me.platformRoles.includes("support")) return t("users.platformRole.support");
    if (me.dispatcher) return t("users.scopeKind.dispatcher");
    if (isCustomerView(me)) return t("shell.scope.customerView");
    return "";
}

function sliceOf(me: MeDTO, t: Translate, language: string): string | null {
    if (me.tenant) return tenantName(me.tenant, language) || null;
    // A support, dispatcher or platform principal that also holds customer scopes is not a customer view.
    if (!isCustomerView(me)) return null;
    const names = me.customerScopes.map((s) => s.name.trim()).filter(Boolean);
    if (names.length === 0) return null;
    if (names.length === 1) return names[0];
    return t("shell.scope.andMore", { name: names[0], count: names.length - 1 });
}

/** The slice and the role of `me`, or null when signed out. */
export function principalScope(me: MeDTO | null | undefined, t: Translate, language: string): PrincipalScope | null {
    if (!me) return null;
    return { slice: sliceOf(me, t, language), role: roleOf(me, t) };
}

/** The scope line as one string: "WRT · Tenant admin", "CJSF · Customer view", "Platform admin"; "" when signed out. */
export function principalRoleLabel(me: MeDTO | null | undefined, t: Translate, language: string): string {
    const scope = principalScope(me, t, language);
    if (!scope) return "";
    return [scope.slice, scope.role].filter(Boolean).join(SEPARATOR);
}
