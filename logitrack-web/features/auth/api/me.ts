/**
 * `['me']`: the signed-in principal from `GET /v1/me` (developer-spec.md §10.6, §10.7; Appendix B
 * §B.2.3, Appendix C §C.8). It replaces the Firebase auth listener, the per-load `setAdminClaims`
 * call and the `users/{uid}` `forceLogoutAt` listener (T18, R50): who is signed in, in which tenant,
 * with which capabilities, comes from Go.
 *
 * The query resolves `null` for a signed-out visitor: on a public page `goFetch` throws the 401 of a
 * visitor without cookies as is (its refresh is refused, nothing is ended), and the query turns it
 * into "signed out". On a protected page the same 401 has already ended the session through
 * `lib/sessionEnd.ts` (logout, then `/login?next=`). Other failures (the api down) stay errors.
 *
 * Refetched by a `claims_changed` refresh (`onClaimsRefreshed`), a tenant switch and `roles.changed`
 * (TW5); set to `null` on sign-out and on a session end.
 */
import { queryOptions, useQuery, type QueryClient } from "@tanstack/react-query";
import { goFetch } from "@/lib/goFetch";
import { isApiError } from "@/lib/apiError";

/** Tenant roles (`memberships.role`, Appendix A `0002_identity`). */
export const TENANT_ROLES = ["tenant_admin", "manager", "operation_staff", "operator", "user", "driver"] as const;
export type TenantRole = (typeof TENANT_ROLES)[number];

/** Platform roles (`user_platform_roles.role`). */
export const PLATFORM_ROLES = ["platform_admin", "support"] as const;
export type PlatformRole = (typeof PLATFORM_ROLES)[number];

export type TenantKind = "own_fleet" | "carrier" | "quarantine";

/** One tenant of `GET /v1/me` (`tenant`, `tenants[]`) and of `GET /v1/me/tenants` (with `status`). */
export interface MeTenant {
    id: string;
    nameTh: string;
    nameEn: string | null;
    kind: TenantKind | string;
    role: TenantRole | string;
    /** Tenant status (`active`, `pending`, `suspended`); `GET /v1/me/tenants` only. */
    status?: string;
}

export interface MeCustomerScope {
    billingPartyId: string;
    name: string;
}

/** The body of `GET /v1/me` (Appendix C §C.8; `internal/auth/me.go`). */
export interface Me {
    id: string;
    email: string | null;
    displayName: string | null;
    photoUrl: string | null;
    tenant: MeTenant | null;
    tenants: MeTenant[];
    platformRoles: string[];
    dispatcher: boolean;
    steward: boolean;
    driver: { id: string } | null;
    customerScopes: MeCustomerScope[];
    capabilities: string[];
    mustChangePassword: boolean;
    /** The Firebase uid, while the bridge mints web custom tokens and the user has one (T08). */
    legacyAuthUid?: string;
}

export const ME_KEY = ["me"] as const;
export const ME_PATH = "/v1/me";

/** `GET /v1/me`; `null` when the caller holds no session (a 401 that ended nothing or ended the session). */
export async function fetchMe(signal?: AbortSignal): Promise<Me | null> {
    try {
        return await goFetch<Me>(ME_PATH, { signal });
    } catch (error) {
        if (isApiError(error) && error.status === 401) return null;
        throw error;
    }
}

/** A 403 or 404 is an answer, not a failure to retry; network errors and 5xx retry twice. */
export function retryUnavailable(count: number, error: unknown): boolean {
    return count < 2 && (!isApiError(error) || error.status === 0 || error.status >= 500);
}

/** `['me']`, 5 min stale / 30 min gc (developer-spec.md §10.7). */
export const meQueryOptions = queryOptions({
    queryKey: ME_KEY,
    queryFn: ({ signal }) => fetchMe(signal),
    staleTime: 5 * 60_000,
    gcTime: 30 * 60_000,
    retry: retryUnavailable,
});

/** The signed-in principal (`null` when signed out), pending, or an error (the api unreachable). */
export function useMe() {
    return useQuery(meQueryOptions);
}

/** Fetches `['me']` again now, whatever its age (a `claims_changed` refresh, a revocation check). */
export function refetchMe(client: QueryClient): Promise<Me | null> {
    return client.fetchQuery({ ...meQueryOptions, staleTime: 0 });
}

export const MY_TENANTS_KEY = ["me", "tenants"] as const;
export const MY_TENANTS_PATH = "/v1/me/tenants";

/**
 * `['me','tenants']`: every active membership with the tenant status (`GET /v1/me/tenants`), for the
 * tenant switcher. Under the `['me']` prefix, so whatever refetches `['me']` by prefix refetches it.
 */
export function myTenantsQueryOptions(enabled: boolean) {
    return queryOptions({
        queryKey: MY_TENANTS_KEY,
        queryFn: ({ signal }) => goFetch<MeTenant[]>(MY_TENANTS_PATH, { signal }),
        staleTime: 5 * 60_000,
        gcTime: 30 * 60_000,
        retry: retryUnavailable,
        enabled,
    });
}

/** The caller's tenants; disabled until a principal is signed in. */
export function useMyTenants(enabled: boolean) {
    return useQuery(myTenantsQueryOptions(enabled));
}

/** Whether `me` holds capability `key` (colon form, Appendix C §C.2.3). */
export function hasCapability(me: Pick<Me, "capabilities"> | null | undefined, key: string): boolean {
    return Boolean(me?.capabilities?.includes(key));
}

/** Whether `me` holds a platform role (platform_admin or support). */
export function isPlatformPrincipal(me: Pick<Me, "platformRoles"> | null | undefined): boolean {
    return (me?.platformRoles?.length ?? 0) > 0;
}

/** `{allowed, loading}` for one capability over `['me']`; replaces the per-instance `usePermission` read. */
export function useCapability(key: string): { allowed: boolean; loading: boolean } {
    const { data, isPending } = useQuery({ ...meQueryOptions, select: (me) => hasCapability(me, key) });
    return { allowed: data === true, loading: isPending };
}

/** The display name of the principal: name, else email, else "". */
export function meDisplayName(me: Pick<Me, "displayName" | "email"> | null | undefined): string {
    return me?.displayName?.trim() || me?.email || "";
}

/** The display name of a tenant in the active language. */
export function tenantName(t: Pick<MeTenant, "nameTh" | "nameEn">, language: string): string {
    if (language === "en" && t.nameEn) return t.nameEn;
    return t.nameTh || t.nameEn || "";
}

/**
 * The legacy Firebase claim shape (`admin`, `role`, ...) that `getRole()` / `can()` and the
 * unmigrated pages still read, synthesised from `['me']` until TW7 (developer-spec.md §10.6). It
 * follows the minting table of Appendix C §C.6.3; the legacy document ids (`partnerScopeId`,
 * `customerScopeId`, `driverId`) are known only to the bridge, so a bridged session uses the claims
 * of its Firebase ID token instead (lib/firebaseBridge.ts).
 */
export function legacyClaimsFromMe(me: Me): Record<string, unknown> {
    if (me.platformRoles.includes("platform_admin")) return { admin: true, role: "admin" };
    const t = me.tenant;
    if (t) {
        if (t.role === "tenant_admin") {
            return t.kind === "carrier" ? { admin: false, role: "partner" } : { admin: true, role: "admin" };
        }
        return { admin: false, role: t.role };
    }
    if (me.customerScopes.length > 0) return { admin: false, role: "customer" };
    return { admin: false, role: "user" };
}
