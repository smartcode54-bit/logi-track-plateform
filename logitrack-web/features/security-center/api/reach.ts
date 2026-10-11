/**
 * Whose rows a Security Center read covers (Appendix C §C.3.9; Appendix B §B.2.5 web contract, as the
 * users page reads them since T18). The T51 and T52S reads need a tenant context
 * (`authz.RequireTenant`): a tenant principal reads its tenant reach with no header; a platform
 * principal (`platform_admin`, `support`) reads every tenant through `X-Act-On-Tenant: *`, a
 * read-only bypass that Go records as one `platform_cross_tenant_access` row per request. The reach
 * is part of each query key, so a page read under one reach is never shown under another.
 */
import { useCallback } from "react";

import { useMe } from "@/features/auth/api/useMe";
import type { MeDTO } from "@/features/auth/api/me";
import { usersReach, usersReachHeaders, type UsersReach } from "@/features/users/api/users";

export type SecurityReach = UsersReach;

/** The reach of `me`: `all` for a platform principal, else `tenant`. */
export function securityReach(me: Pick<MeDTO, "platformRoles"> | null | undefined): SecurityReach {
    return usersReach(me);
}

/** The request headers of a read with this reach (`X-Act-On-Tenant: *` for `all`). */
export function reachHeaders(reach: SecurityReach): Record<string, string> | undefined {
    return usersReachHeaders(reach);
}

/** The signed-in principal's reach, `undefined` until `['me']` resolves. */
export function useSecurityReach(): SecurityReach | undefined {
    const select = useCallback((me: MeDTO | null) => (me ? securityReach(me) : undefined), []);
    return useMe(select).data;
}
