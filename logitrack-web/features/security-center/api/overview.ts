/**
 * The Security Center counters over Go (T54S; T51 routes, Appendix B §B.2.5, §B.2.6; developer-spec.md
 * §10.7, §10.13; Appendix E §E.2.4):
 *
 * - `['security','overview',{reach}]`: `GET /v1/security/overview` (`security:view_overview`), the
 *   overview cards computed in PostgreSQL. Replaces the seven `getCountFromServer` reads and the
 *   collection-group count of `lib/fetchSecurityOverviewStats.ts`, and the histogram the page built
 *   from 150 listened documents (`events.hourly24h`, `events.daily7d`).
 * - `['security','usersByRole',{reach}]`: `GET /v1/stats/users-by-role`, the RBAC distribution and the
 *   role cards of the matrix page.
 * - `['security','status']`: `GET /v1/security/status` (`security:view_status`, no tenant context).
 *
 * Freshness: 60 s / 5 min with a focus refetch (no `users.changed` / `installations.updated` event,
 * Appendix B §B.4.2), stale only after 5 min for a platform principal (each of its reads is an audit
 * row, `reachPollMs`); the status (30 s / 5 min, no tenant context, so never audited) polls every 60 s
 * while its page is visible (Appendix E §E.6).
 */
import { useQuery } from "@tanstack/react-query";

import { goQueryFn } from "@/lib/goQuery";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";
import { POLL_INTERVAL_MS, pollWhileVisible } from "@/lib/realtime/polling";
import { readEndpoint, reachPollMs, type SecurityReach } from "./reach";

export const SECURITY_OVERVIEW_PATH = "/v1/security/overview";
export const USERS_BY_ROLE_PATH = "/v1/stats/users-by-role";
export const SECURITY_STATUS_PATH = "/v1/security/status";
/** The status page's poll while the tab is visible (Appendix E §E.6). */
export const SECURITY_STATUS_POLL_MS = POLL_INTERVAL_MS;


export interface OverviewScopeDTO {
    all: boolean;
    tenantId: string | null;
}

/** `GET /v1/security/status` (Go `iam.SystemStatus`). */
export interface SystemStatusDTO {
    /** `ok`: the body was read from PostgreSQL. */
    database: string;
    /** `ok`, `down` or `unknown`. */
    redis: string;
    scheduler: { leaderPresent: boolean };
    outbox: { pending: number; oldestSeconds: number };
    jobs: { queued: number; running: number; failed24h: number; succeeded24h: number };
}

/** The activity counters of `security_events` the caller sees (`security.Activity`). */
export interface EventActivityDTO {
    last24h: number;
    last7d: number;
    bySeverity24h: Record<string, number>;
    /** `[23]` is the hour before now, `[0]` the hour that ended 23 hours earlier. */
    hourly24h: number[];
    /** `[6]` is the 24 hours before now, `[0]` the day that ended 6 days earlier. */
    daily7d: number[];
}

/** `GET /v1/security/overview` (Go `iam.Overview`). A section the caller may not see is null. */
export interface SecurityOverviewDTO {
    generatedAt: string;
    scope: OverviewScopeDTO;
    users: { total: number; byStatus: Record<string, number> };
    sessions: { active: number; revoked24h: number; revokedByReason24h: Record<string, number> };
    logins: { failed24h: number; lockouts24h: number };
    events: EventActivityDTO;
    /** Null without `security:view_mobile_clients`. */
    installations: {
        total: number;
        seen7d: number;
        belowFloor: number;
        belowLatest: number;
        unknownVersion: number;
        minAllowedVersion: string | null;
        latestVersion: string | null;
    } | null;
    /** Platform quarantine counts, absent when the caller cannot inspect them. */
    quarantine: { rows: number; byTable: Record<string, number> } | null;
    /** Null without `security:view_status`. */
    system: SystemStatusDTO | null;
}

/** `GET /v1/stats/users-by-role` (Go `iam.UsersByRole`): every value listed, zero included. */
export interface UsersByRoleDTO {
    scope: OverviewScopeDTO;
    /** Users in reach that are not deleted, each once. */
    total: number;
    /** Users with no role on any axis. */
    none: number;
    tenantRoles: Record<string, number>;
    scopes: Record<string, number>;
    platformRoles: Record<string, number>;
}

export function securityOverviewKey(reach: SecurityReach) {
    return queryKeys.security.overview({ reach });
}

export function usersByRoleKey(reach: SecurityReach) {
    return queryKeys.security.usersByRole({ reach });
}

type OverviewKey = ReturnType<typeof securityOverviewKey>;
type UsersByRoleKey = ReturnType<typeof usersByRoleKey>;

/** `['security','overview',{reach}]`; disabled until the reach is known and the caller may read it. */
export function useSecurityOverview(reach: SecurityReach | undefined, enabled = true) {
    return useQuery({
        queryKey: securityOverviewKey(reach ?? "tenant"),
        queryFn: goQueryFn<SecurityOverviewDTO, OverviewKey>(([, , p]) => readEndpoint(SECURITY_OVERVIEW_PATH, p.reach)),
        gcTime: QUERY_POLICY.list.gcTime,
        staleTime: reachPollMs(reach, QUERY_POLICY.list.staleTime),
        refetchOnWindowFocus: true,
        enabled: enabled && reach !== undefined,
    });
}

/** `['security','usersByRole',{reach}]`. */
export function useUsersByRole(reach: SecurityReach | undefined, enabled = true) {
    return useQuery({
        queryKey: usersByRoleKey(reach ?? "tenant"),
        queryFn: goQueryFn<UsersByRoleDTO, UsersByRoleKey>(([, , p]) => readEndpoint(USERS_BY_ROLE_PATH, p.reach)),
        gcTime: QUERY_POLICY.list.gcTime,
        staleTime: reachPollMs(reach, QUERY_POLICY.list.staleTime),
        refetchOnWindowFocus: true,
        enabled: enabled && reach !== undefined,
    });
}

/** `['security','status']`, polled every 60 s while the tab is visible. */
export function useSecurityStatus(enabled = true) {
    return useQuery({
        queryKey: queryKeys.security.status(),
        queryFn: goQueryFn<SystemStatusDTO>(SECURITY_STATUS_PATH),
        ...QUERY_POLICY.live,
        ...pollWhileVisible(SECURITY_STATUS_POLL_MS),
        enabled,
    });
}

/** The backlog age above which the outbox relay counts as stuck (no relay leader, broker down). */
export const OUTBOX_STALE_SECONDS = 300;

/** Whether the platform looks healthy: Redis up, a scheduler leader, and an outbox backlog under 5 min. */
export function systemHealthy(s: SystemStatusDTO): boolean {
    return s.redis === "ok" && s.scheduler.leaderPresent && s.outbox.oldestSeconds < OUTBOX_STALE_SECONDS;
}

/** The RBAC distribution of the overview donut: drivers, operations roles, everyone else. */
export function roleDistribution(stats: UsersByRoleDTO): { total: number; drivers: number; operations: number; others: number } {
    const n = (r: string) => stats.tenantRoles[r] ?? 0;
    const drivers = n("driver");
    const operations = n("manager") + n("operation_staff") + n("operator");
    const total = stats.total;
    // A user with several roles counts once in each, so the remainder is clamped, never negative.
    const others = Math.max(0, total - drivers - operations);
    return { total, drivers, operations, others };
}
