/**
 * Driver-app installations and grouped version counts over Go. Member reads retain their tenant
 * reach; platform reads use explicit projected endpoints. Lists poll only their first page.
 */
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";

import { goFetch } from "@/lib/goFetch";
import { goInfiniteQueryFn, goNextPageParam, type GoPage } from "@/lib/goQuery";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";
import { POLL_INTERVAL_MS, pollHeadWhileVisible } from "@/lib/realtime/polling";
import { readEndpoint, reachPollMs, type SecurityReach } from "@/features/security-center/api/reach";

export const INSTALLATIONS_PATH = "/v1/app-installations";
export const INSTALLATION_STATS_PATH = "/v1/app-installations/stats";
export const INSTALLATIONS_PAGE_SIZE = 100;
/** The window of "active" installations (ADR 0007 §4, the release page's 7 days). */
export const ACTIVE_WINDOW = "7d";

export type VersionStatus = "current" | "outdated" | "blocked" | "ahead" | "unknown";

/** One row of `GET /v1/app-installations` (Go `mobileapp.Installation`). */
export interface InstallationDTO {
    driverId: string;
    driverName: string | null;
    installId: string | null;
    tenantId: string;
    tenantName: string | null;
    platform: string;
    appVersion: string | null;
    buildNumber: string | null;
    flavor: string;
    firstSeenAt: string;
    lastSeenAt: string;
    versionStatus: VersionStatus | string;
}

/** `GET /v1/app-installations/stats` (Go `mobileapp.InstallationStats`). */
export interface InstallationStatsDTO {
    since: string;
    total: number;
    byStatus: Record<VersionStatus, number>;
    byFlavor: Record<string, number>;
    byPlatform: Record<string, number>;
    /** With `?belowVersion`: how many a floor of that version would lock out (unparseable never). */
    belowVersion: { version: string; count: number } | null;
    flavor: string | null;
    minAllowedVersion: string | null;
    latestVersion: string | null;
}

export interface InstallationsFilter {
    /** `dev`, `prod` or "" (both). */
    flavor: string;
}

export function installationsKey(filter: InstallationsFilter, reach: SecurityReach) {
    return queryKeys.installations.list({ flavor: filter.flavor, reach });
}

type InstallationsKey = ReturnType<typeof installationsKey>;

/**
 * `['installations',{flavor,reach}]`: the keyset list, polled while visible and while it holds one
 * page: every 60 s, every 5 min for a platform principal.
 */
export function useInstallations(filter: InstallationsFilter, reach: SecurityReach | undefined, enabled = true) {
    return useInfiniteQuery({
        queryKey: installationsKey(filter, reach ?? "tenant"),
        queryFn: goInfiniteQueryFn<InstallationDTO, InstallationsKey>(([, p]) => readEndpoint(INSTALLATIONS_PATH, p.reach), {
            query: ([, p]) => ({ flavor: p.flavor || undefined, limit: INSTALLATIONS_PAGE_SIZE }),
        }),
        initialPageParam: undefined as string | undefined,
        getNextPageParam: goNextPageParam,
        gcTime: QUERY_POLICY.list.gcTime,
        staleTime: reachPollMs(reach, QUERY_POLICY.list.staleTime),
        ...pollHeadWhileVisible(reachPollMs(reach, POLL_INTERVAL_MS)),
        enabled: enabled && reach !== undefined,
    });
}

export function installationRows(pages: readonly GoPage<InstallationDTO>[] | undefined): InstallationDTO[] {
    return (pages ?? []).flatMap((p) => (Array.isArray(p.data) ? p.data : []));
}

export interface InstallationStatsParams {
    /** `7d`, `24h` or an RFC 3339 instant. */
    since: string;
    /** A candidate floor: the answer counts the installations it would lock out. */
    belowVersion: string;
}

export function installationStatsKey(params: InstallationStatsParams, reach: SecurityReach) {
    return queryKeys.installations.stats({ since: params.since, belowVersion: params.belowVersion, reach });
}

/**
 * `['installations','stats',{since,belowVersion,reach}]` (60 s / 5 min, focus). The summary goes stale
 * only after 5 min for a platform principal (an audited read); the impact count of a candidate floor
 * (`belowVersion`) keeps 60 s, because a typed confirmation shows it.
 */
export function useInstallationStats(params: InstallationStatsParams, reach: SecurityReach | undefined, enabled = true) {
    return useQuery({
        queryKey: installationStatsKey(params, reach ?? "tenant"),
        queryFn: ({ signal }) =>
            goFetch<InstallationStatsDTO>(readEndpoint(INSTALLATION_STATS_PATH, reach ?? "tenant"), {
                signal,
                query: { since: params.since || undefined, belowVersion: params.belowVersion || undefined },
            }),
        gcTime: QUERY_POLICY.list.gcTime,
        staleTime: params.belowVersion ? QUERY_POLICY.list.staleTime : reachPollMs(reach, QUERY_POLICY.list.staleTime),
        refetchOnWindowFocus: true,
        enabled: enabled && reach !== undefined,
    });
}
