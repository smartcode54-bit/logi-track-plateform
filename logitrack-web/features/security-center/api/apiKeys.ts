/**
 * API keys over Go (T54S; routes of T32K, Appendix B §B.2.6, Appendix C §C.4.11, §C.8, R2, R49, R82):
 * `GET /v1/api-keys` (keyset, newest first), `POST /v1/api-keys` (the secret is in this response only,
 * never stored, never cached) and `DELETE /v1/api-keys/{id}` (revoke; effective on the key's next
 * request). Every route needs `security:manage_api_keys`; keys are managed by people only.
 *
 * Reach: a tenant caller manages its tenant's keys; a platform_admin lists every key through
 * `X-Act-On-Tenant: *` and creates platform keys (`tenantId` null: the only form of the `cf_shim` and
 * `release_publisher` scopes, and the only keys that may hold global and `mobile:*` capabilities).
 */
import { useInfiniteQuery } from "@tanstack/react-query";

import { CAPABILITY_CATALOG, type CapabilityClass, type CapabilityInfo } from "shared-docs/schemas/capabilities";
import { goFetch, goPath } from "@/lib/goFetch";
import { goInfiniteQueryFn, goNextPageParam, type GoPage } from "@/lib/goQuery";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";
import { reachHeaders, reachPollMs, type SecurityReach } from "./reach";

export const API_KEYS_PATH = "/v1/api-keys";
export const API_KEYS_PAGE_SIZE = 50;

/** The `api_keys.scope` values (R82), in the order of the CHECK. */
export const API_KEY_SCOPES = ["integration", "script", "cf_shim", "release_publisher"] as const;
export type ApiKeyScope = (typeof API_KEY_SCOPES)[number];
/** Scopes that exist only as platform keys (`tenantId` null). */
export const PLATFORM_ONLY_SCOPES: readonly ApiKeyScope[] = ["cf_shim", "release_publisher"];
/** The single capability of a `release_publisher` key. */
export const RELEASE_PUBLISHER_CAPABILITY = "security:manage_mobile_release";

/** One key of `GET /v1/api-keys` (Go `iam.APIKey`); the secret and its hash are never part of it. */
export interface ApiKeyDTO {
    id: string;
    name: string;
    scope: ApiKeyScope | string;
    /** The public prefix of the secret, for recognising a key. */
    keyPrefix: string;
    capabilities: string[];
    /** Null for a platform key. */
    tenantId: string | null;
    tenantNameTh: string | null;
    tenantNameEn: string | null;
    ratePerMin: number;
    status: "active" | "expired" | "revoked" | string;
    createdBy: string;
    createdByEmail: string;
    createdAt: string;
    expiresAt: string | null;
    lastUsedAt: string | null;
    revokedAt: string | null;
    revokedBy: string | null;
}

/** `POST /v1/api-keys` answer: the key and its secret, shown this once (R49). */
export interface CreatedApiKeyDTO extends ApiKeyDTO {
    secret: string;
}

export interface CreateApiKeyInput {
    name: string;
    scope: ApiKeyScope;
    capabilities: string[];
    /** RFC 3339 in the future, or null for no expiry. */
    expiresAt: string | null;
    /** Null = a platform key (platform_admin only). */
    tenantId: string | null;
}

export function apiKeysKey(reach: SecurityReach) {
    return queryKeys.apiKeys({ reach });
}

type ApiKeysKey = ReturnType<typeof apiKeysKey>;

/**
 * `['apiKeys',{reach}]`: the keyset list (60 s / 5 min, focus refetch; own mutations invalidate it); a
 * platform principal's read is audited, so it goes stale only after 5 min (`reachPollMs`).
 */
export function useApiKeys(reach: SecurityReach | undefined, enabled = true) {
    return useInfiniteQuery({
        queryKey: apiKeysKey(reach ?? "tenant"),
        queryFn: goInfiniteQueryFn<ApiKeyDTO, ApiKeysKey>(API_KEYS_PATH, {
            query: { limit: API_KEYS_PAGE_SIZE },
            headers: ([, p]) => reachHeaders(p.reach),
        }),
        initialPageParam: undefined as string | undefined,
        getNextPageParam: goNextPageParam,
        gcTime: QUERY_POLICY.list.gcTime,
        staleTime: reachPollMs(reach, QUERY_POLICY.list.staleTime),
        enabled: enabled && reach !== undefined,
    });
}

export function apiKeyRows(pages: readonly GoPage<ApiKeyDTO>[] | undefined): ApiKeyDTO[] {
    return (pages ?? []).flatMap((p) => (Array.isArray(p.data) ? p.data : []));
}

/** `POST /v1/api-keys`. The caller keeps the secret in component state only and drops it on close. */
export function createApiKey(input: CreateApiKeyInput): Promise<CreatedApiKeyDTO> {
    const body: Record<string, unknown> = {
        name: input.name.trim(),
        scope: input.scope,
        capabilities: input.capabilities,
        tenantId: input.tenantId,
        expiresAt: input.expiresAt,
    };
    return goFetch<CreatedApiKeyDTO>(API_KEYS_PATH, { method: "POST", body });
}

/** `DELETE /v1/api-keys/{id}`: revoked from the key's next request; a revoked key again is a no-op. */
export function revokeApiKey(id: string): Promise<void> {
    return goFetch<void>(goPath`/v1/api-keys/${id}`, { method: "DELETE" });
}

/**
 * The capabilities a new key may hold (Appendix C §C.4.11, as `iam.validateCreate` checks them): a
 * tenant key tenant-class keys only, a platform key global-class and `mobile:*` (self) keys; a
 * `cf_shim` key only `mobile:*`; a `release_publisher` key exactly `security:manage_mobile_release`.
 * Never `platform:*` or `dispatch:*`. A caller that is not a platform_admin grants only keys it holds.
 */
export function grantableCapabilities(opts: {
    scope: ApiKeyScope;
    platformKey: boolean;
    platformAdmin: boolean;
    held: readonly string[];
}): CapabilityInfo[] {
    const allowedClasses: CapabilityClass[] = opts.platformKey ? ["global", "self"] : ["tenant"];
    return CAPABILITY_CATALOG.filter((c) => {
        if (!allowedClasses.includes(c.class)) return false;
        if (opts.scope === "cf_shim" && c.class !== "self") return false;
        if (opts.scope === "release_publisher" && c.key !== RELEASE_PUBLISHER_CAPABILITY) return false;
        if (!opts.platformAdmin && !opts.held.includes(c.key)) return false;
        return true;
    });
}

/** `datetime-local` value -> RFC 3339 (the browser's zone), or null when empty or invalid. */
export function expiresAtFromInput(value: string): string | null {
    if (!value.trim()) return null;
    const d = new Date(value);
    return Number.isNaN(d.getTime()) ? null : d.toISOString();
}
