import type { MeDTO } from "@/features/auth/api/me";

export const SUPPORT_ACCESS_PATH = "/app/settings/support-access";

export type SupportAccessPrincipal = Pick<MeDTO, "tenant" | "tenants">;

/** Require the selected tenant_admin role plus real membership. Support sessions have role support_readonly. */
export function canViewSupportAccess(me: SupportAccessPrincipal | null | undefined): boolean {
    return !!me?.tenant &&
        me.tenant.role === "tenant_admin" && me.tenant.kind !== "quarantine" &&
        me.tenants.some((tenant) => tenant.id === me.tenant?.id && tenant.role === "tenant_admin" && tenant.status !== "suspended");
}
