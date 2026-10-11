/**
 * The driver-app release admin over Go (T54S; routes of T52S `GET /v1/app-releases`,
 * `PUT /v1/app-releases/floor`, Appendix B §B.2.18, ADR 0007, R43, R82; developer-spec.md §9.7, §11.5,
 * §10.7). `settings('mobile_app')` keeps the two writers of ADR 0007 apart:
 *
 * - the floor half (`minAllowedVersion*`) is written only by `PUT /v1/app-releases/floor` (people,
 *   `security:manage_mobile_release`), from this page;
 * - the release half (`latestVersion`, `apkDownloadUrl`, `apkSha256`, notes, ...) only by
 *   `POST /v1/app-releases`, which `cmd/release` calls with a `release_publisher` key. The page shows it
 *   read-only: there is no web form for the download link any more.
 *
 * Go refuses a floor without the gate's download link (409 `failed_precondition`, `no_apk_url`: a
 * blocked driver would face a dialog with no button) and a floor above the latest release (422
 * `above_latest`); the page disables both before asking.
 *
 * Keys: `['mobileReleases',{flavor}]` (the releases with the gate in `meta.settings`; 5 min / 30 min),
 * invalidated with `['mobileSettings']` and `['installations']` by the floor write and by SSE
 * `mobile_settings.changed`.
 */
import { useQuery } from "@tanstack/react-query";

import { goFetchEnvelope } from "@/lib/goFetch";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";

export const RELEASES_PATH = "/v1/app-releases";
export const FLOOR_PATH = "/v1/app-releases/floor";
export const RELEASES_LIMIT = 20;
/**
 * The version form of a floor and of a release, exactly Go's `mobileapp.releaseVersionRx`: canonical
 * MAJOR.MINOR.PATCH, no leading zeros, at most nine digits a part. A laxer form would pass "3.1.00"
 * (numerically the 3.1.0 floor) through every client guard and fail only at the PUT.
 */
export const RELEASE_VERSION_RX = /^(0|[1-9]\d{0,8})\.(0|[1-9]\d{0,8})\.(0|[1-9]\d{0,8})$/;

/** The staff view of `settings('mobile_app')` (Go `mobileapp.GateView`); null where unset. */
export interface GateViewDTO {
    flavor: string | null;
    minAllowedVersion: string | null;
    latestVersion: string | null;
    latestBuildNumber: string | null;
    apkDownloadUrl: string | null;
    apkSha256: string | null;
    apkSizeBytes: number | null;
    releaseNotes: string | null;
    releasedAt: string | null;
    minAllowedVersionSetAt: string | null;
    /** Null for a caller without `security:manage_mobile_release`. */
    minAllowedVersionSetBy: string | null;
    releasedBy: string | null;
    updatedAt: string | null;
}

/** One published build (Go `mobileapp.Release`). */
export interface ReleaseDTO {
    id: string;
    flavor: string;
    version: string;
    buildNumber: string;
    apkKey: string;
    apkUrl: string | null;
    apkSizeBytes: number;
    apkSha256: string;
    releaseNotes: string | null;
    releasedAt: string;
    releasedBy: string | null;
    storageBackend: string;
    /** This build is the gate's latest. */
    latest: boolean;
}

export interface MobileReleasesDTO {
    releases: ReleaseDTO[];
    gate: GateViewDTO;
}

const EMPTY_GATE: GateViewDTO = {
    flavor: null,
    minAllowedVersion: null,
    latestVersion: null,
    latestBuildNumber: null,
    apkDownloadUrl: null,
    apkSha256: null,
    apkSizeBytes: null,
    releaseNotes: null,
    releasedAt: null,
    minAllowedVersionSetAt: null,
    minAllowedVersionSetBy: null,
    releasedBy: null,
    updatedAt: null,
};

function gateOf(meta: Record<string, unknown> | undefined): GateViewDTO {
    const s = meta?.settings;
    return typeof s === "object" && s !== null ? { ...EMPTY_GATE, ...(s as Partial<GateViewDTO>) } : EMPTY_GATE;
}

export function mobileReleasesKey(flavor: string) {
    return queryKeys.mobileReleases({ flavor });
}

/** `['mobileReleases',{flavor}]`: the newest builds and the gate document. */
export function useMobileReleases(flavor = "", enabled = true) {
    return useQuery({
        queryKey: mobileReleasesKey(flavor),
        queryFn: async ({ signal }): Promise<MobileReleasesDTO> => {
            const env = await goFetchEnvelope<ReleaseDTO[]>(RELEASES_PATH, {
                signal,
                query: { flavor: flavor || undefined, limit: RELEASES_LIMIT },
            });
            return { releases: Array.isArray(env.data) ? env.data : [], gate: gateOf(env.meta) };
        },
        ...QUERY_POLICY.entity,
        refetchOnWindowFocus: true,
        enabled,
    });
}

export interface FloorResult {
    gate: GateViewDTO;
    /** False when the floor already had this value (no write, no event). */
    changed: boolean;
    /** Installations seen in the last 7 days (every tenant) that the new floor locks out. */
    blocked7d: number;
}

/** `PUT /v1/app-releases/floor {minAllowedVersion}`; `null` lifts the floor. */
export async function setVersionFloor(minAllowedVersion: string | null): Promise<FloorResult> {
    const env = await goFetchEnvelope<GateViewDTO>(FLOOR_PATH, { method: "PUT", body: { minAllowedVersion } });
    return {
        gate: { ...EMPTY_GATE, ...env.data },
        changed: env.meta?.changed === true,
        blocked7d: typeof env.meta?.blocked7d === "number" ? env.meta.blocked7d : 0,
    };
}

/** The keys a floor change makes stale (the release list, the gate, every installation status). */
export const FLOOR_INVALIDATES = [queryKeys.mobileReleases(), queryKeys.mobileSettings(), queryKeys.installations.all()] as const;
