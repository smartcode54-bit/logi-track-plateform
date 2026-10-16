import { useCallback } from "react";

import { useMe } from "@/features/auth/api/useMe";
import type { MeDTO } from "@/features/auth/api/me";
import { usersReach, type UsersReach } from "@/features/users/api/users";

export type SecurityReach = UsersReach;

/** The longest a platform principal's Security Center read waits before refreshing by itself. */
export const PLATFORM_POLL_MS = 5 * 60_000;

/** The reach of `me`: `all` for a platform principal, else `tenant`. */
export function securityReach(me: Pick<MeDTO, "platformRoles"> | null | undefined): SecurityReach {
    return usersReach(me);
}

/** Select an explicit platform read without changing ordinary tenant endpoints. */
export function readEndpoint(path: "/v1/security-events" | "/v1/security/overview" | "/v1/stats/users-by-role" | "/v1/api-keys" | "/v1/app-installations" | "/v1/app-installations/stats", reach: SecurityReach): string {
    return reach === "all" ? path.replace("/v1/", "/v1/platform/") : path;
}

/**
 * The interval (or `staleTime`) of a Security Center read for `reach`: `tenantMs` for a tenant
 * principal, at least `PLATFORM_POLL_MS` for a platform principal, using projected reads.
 */
export function reachPollMs(reach: SecurityReach | undefined, tenantMs: number): number {
    return reach === "all" ? Math.max(tenantMs, PLATFORM_POLL_MS) : tenantMs;
}

/** The signed-in principal's reach, `undefined` until `['me']` resolves. */
export function useSecurityReach(): SecurityReach | undefined {
    const select = useCallback((me: MeDTO | null) => (me ? securityReach(me) : undefined), []);
    return useMe(select).data;
}
