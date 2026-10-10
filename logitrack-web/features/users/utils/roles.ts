/**
 * Role rules the users and tenants pages show (T18). Go decides every write (`CanAssignRole`, not
 * self, steward and platform rules; Appendix C §C.2.2, §C.8); these only hide what would be refused.
 */
import { hasCapability, PLATFORM_ROLES, TENANT_ROLES, type MeDTO, type TenantRole } from "@/features/auth/api/me";
import type { UserDTO } from "../api/users";

export { PLATFORM_ROLES, TENANT_ROLES };

/** Roles offered for a new user: the tenant roles, and `customer` for a scope-only account. */
export const CREATE_ROLES = [...TENANT_ROLES, "customer"] as const;
export type CreateRole = (typeof CREATE_ROLES)[number];

export const CAP = {
    usersView: "users:view",
    usersManage: "users:manage",
    usersAssignRole: "users:assign_role",
    usersRevokeSessions: "users:revoke_sessions",
    driversEdit: "drivers:edit",
    manageTenants: "platform:manage_tenants",
    managePlatformRoles: "platform:manage_platform_roles",
} as const;

/** What the signed-in principal may do on the users page. */
export interface UserActions {
    manage: boolean;
    assignRole: boolean;
    revokeSessions: boolean;
    linkDriver: boolean;
    platformRoles: boolean;
    /** A platform principal works across tenants (chooses the tenant of a new user or membership). */
    platform: boolean;
}

export function userActions(me: MeDTO | null | undefined): UserActions {
    return {
        manage: hasCapability(me, CAP.usersManage),
        assignRole: hasCapability(me, CAP.usersAssignRole),
        revokeSessions: hasCapability(me, CAP.usersRevokeSessions),
        linkDriver: hasCapability(me, CAP.driversEdit) && hasCapability(me, CAP.usersManage),
        platformRoles: hasCapability(me, CAP.managePlatformRoles),
        platform: (me?.platformRoles ?? []).includes("platform_admin"),
    };
}

/** Only a tenant_admin of the tenant, or a platform admin, may grant `tenant_admin` (Appendix B §B.2.4). */
export function grantableRoles(me: MeDTO | null | undefined, tenantId: string): TenantRole[] {
    const platform = (me?.platformRoles ?? []).includes("platform_admin");
    const admin = me?.tenant?.id === tenantId && me?.tenant?.role === "tenant_admin";
    return TENANT_ROLES.filter((r) => r !== "tenant_admin" || platform || admin);
}

/** Whether `user` is the signed-in principal (admin routes refuse to act on the caller, C.4.7). */
export function isSelf(me: MeDTO | null | undefined, user: Pick<UserDTO, "id">): boolean {
    return Boolean(me && me.id === user.id);
}

/** The active-tenant membership of a user, else its only one. */
export function primaryMembership(user: UserDTO, activeTenantId: string | undefined) {
    return user.memberships.find((m) => m.tenantId === activeTenantId) ?? (user.memberships.length === 1 ? user.memberships[0] : undefined);
}
