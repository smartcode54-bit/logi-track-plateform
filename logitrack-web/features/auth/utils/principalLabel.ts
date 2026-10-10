/**
 * The role line under the user's name in the app header (T18): the principal's platform role, else
 * its role in the active tenant (and the tenant's name), else its customer scope. From `['me']`, not
 * from the legacy Firebase claims.
 */
import { tenantName, type MeDTO } from "../api/me";

type Translate = (key: string, fallbackOrParams?: string | Record<string, string | number>) => string;

export function principalRoleLabel(me: MeDTO | null | undefined, t: Translate, language: string): string {
    if (!me) return "";
    if (me.platformRoles.includes("platform_admin")) return t("users.platformRole.platform_admin");
    if (me.tenant) {
        const role = t(`users.tenantRole.${me.tenant.role}`, me.tenant.role);
        const name = tenantName(me.tenant, language);
        return name ? `${role} · ${name}` : role;
    }
    if (me.platformRoles.includes("support")) return t("users.platformRole.support");
    if (me.customerScopes.length > 0) return t("users.scopeKind.customer");
    return "";
}
