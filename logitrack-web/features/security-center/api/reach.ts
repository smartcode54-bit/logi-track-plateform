/**
 * Whose rows a Security Center read covers (Appendix C §C.3.9; Appendix B §B.2.5 web contract, as the
 * users page reads them since T18). The T51 and T52S reads need a tenant context
 * (`authz.RequireTenant`): a tenant principal reads its tenant reach with no header; a platform
 * principal (`platform_admin`, `support`) reads every tenant through `X-Act-On-Tenant: *`, a
 * read-only bypass that Go records as one `platform_cross_tenant_access` warning row per request. The
 * reach is part of each query key, so a page read under one reach is never shown under another.
 *
 * Because each of those requests is a permanent audit row (and shows in the feed and the overview's
 * 24 h counters), a platform principal's Security Center lists and counters refresh by themselves at
 * most every `PLATFORM_POLL_MS` (poll and focus; developer-spec.md §19.2 question 20 recommends a longer
 * poll over dropping the audit); an explicit Refresh, a filter change, a write or an invalidation still
 * fetch at once. A tenant principal keeps the intervals of Appendix E §E.5; a user's sessions (opened
 * on demand) and the impact count of a candidate floor keep theirs for everyone.
 */
import { useCallback } from "react";

import { useMe } from "@/features/auth/api/useMe";
import type { MeDTO } from "@/features/auth/api/me";
import { usersReach, usersReachHeaders, type UsersReach } from "@/features/users/api/users";

export type SecurityReach = UsersReach;

/** The longest a platform principal's Security Center read waits before refreshing by itself. */
export const PLATFORM_POLL_MS = 5 * 60_000;

/** The reach of `me`: `all` for a platform principal, else `tenant`. */
export function securityReach(me: Pick<MeDTO, "platformRoles"> | null | undefined): SecurityReach {
    return usersReach(me);
}

/** The request headers of a read with this reach (`X-Act-On-Tenant: *` for `all`). */
export function reachHeaders(reach: SecurityReach): Record<string, string> | undefined {
    return usersReachHeaders(reach);
}

/**
 * The interval (or `staleTime`) of a Security Center read for `reach`: `tenantMs` for a tenant
 * principal, at least `PLATFORM_POLL_MS` for a platform principal, whose every read is audited.
 */
export function reachPollMs(reach: SecurityReach | undefined, tenantMs: number): number {
    return reach === "all" ? Math.max(tenantMs, PLATFORM_POLL_MS) : tenantMs;
}

/** The signed-in principal's reach, `undefined` until `['me']` resolves. */
export function useSecurityReach(): SecurityReach | undefined {
    const select = useCallback((me: MeDTO | null) => (me ? securityReach(me) : undefined), []);
    return useMe(select).data;
}
