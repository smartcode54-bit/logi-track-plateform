/**
 * `['me']`: the signed-in principal from `GET /v1/me` (developer-spec.md §10.6, §10.7; Appendix C
 * §C.8; Appendix E §E.4 last row, §E.6). Go resolves the role, scopes and the effective capability
 * set (catalog defaults, then `role_capability_overrides`), so the web never reads
 * `permissions_config` and never derives permissions from Firebase claims again (R5, R27).
 *
 * - Signed out: on a public page Go answers 401 `unauthenticated` and the refresh is refused;
 *   `goFetch` throws that 401 without ending anything and the query resolves to `null`. On a
 *   protected page `goFetch` has already ended the session (lib/sessionEnd.ts) by then.
 * - Freshness: 5 min stale / 30 min gc; invalidated by a forced refresh after `claims_changed`
 *   (lib/queryClient.ts `bindQueryClientToSession`), a tenant switch, the role-matrix save and, from
 *   TW5, the realtime `roles.changed` event.
 *
 * The selectors below are pure, so components select only what they render (`useMe(select)`).
 */
import { queryOptions, type QueryFunctionContext } from "@tanstack/react-query";
import { ApiError, isApiError } from "@/lib/apiError";
import { goFetch } from "@/lib/goFetch";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";
import type { CapabilityId } from "@/lib/capabilities";
import { toCatalogKey } from "@/lib/capabilityAliases";
import type { RoleId } from "@/lib/roles";

export const ME_PATH = "/v1/me";

/** A membership's tenant as `GET /v1/me` reports it (Go `auth.Tenant`). */
export interface MeTenant {
    id: string;
    nameTh: string;
    nameEn: string | null;
    /** `own_fleet` or `carrier` (the quarantine tenant has no memberships). */
    kind: string;
    /** `tenant_admin`, `manager`, `operation_staff`, `operator`, `user` or `driver`. */
    role: string;
}

export interface MeCustomerScope {
    billingPartyId: string;
    name: string;
}

/** The body of `GET /v1/me` (Go `auth.Me`, Appendix C §C.8). */
export interface MeDTO {
    id: string;
    email: string | null;
    displayName: string | null;
    /** Short-lived signed URL of the profile photo, or null. */
    photoUrl: string | null;
    /** The active tenant with the caller's role in it; null for a scope-only principal. */
    tenant: MeTenant | null;
    tenants: MeTenant[];
    /** `platform_admin`, `support`. */
    platformRoles: string[];
    dispatcher: boolean;
    /** Own-fleet staff or platform_admin: may use the global capability class (R60). */
    steward: boolean;
    driver: { id: string } | null;
    customerScopes: MeCustomerScope[];
    /** Effective capability keys, colon form (`shared-docs/schemas/capabilities.ts`). */
    capabilities: string[];
    mustChangePassword: boolean;
    /** The Firebase uid, only while the bridge mints web custom tokens (T08, R80). */
    legacyAuthUid?: string;
}

function isRecord(value: unknown): value is Record<string, unknown> {
    return typeof value === "object" && value !== null && !Array.isArray(value);
}

const str = (v: unknown): string | null => (typeof v === "string" ? v : null);
const strings = (v: unknown): string[] => (Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : []);

function parseTenant(v: unknown): MeTenant | null {
    if (!isRecord(v) || typeof v.id !== "string") return null;
    return {
        id: v.id,
        nameTh: str(v.nameTh) ?? "",
        nameEn: str(v.nameEn),
        kind: str(v.kind) ?? "",
        role: str(v.role) ?? "",
    };
}

/** Reads the `data` of `GET /v1/me`; a body without an id or a capability list is a `bad_response`. */
export function parseMe(data: unknown): MeDTO {
    if (!isRecord(data) || typeof data.id !== "string" || !Array.isArray(data.capabilities)) {
        throw new ApiError({ status: 200, code: "bad_response", message: "GET /v1/me without id or capabilities" });
    }
    const tenants = Array.isArray(data.tenants) ? data.tenants.map(parseTenant).filter((t): t is MeTenant => t !== null) : [];
    const scopes = Array.isArray(data.customerScopes)
        ? data.customerScopes
              .filter(isRecord)
              .filter((s) => typeof s.billingPartyId === "string")
              .map((s) => ({ billingPartyId: s.billingPartyId as string, name: str(s.name) ?? "" }))
        : [];
    const driver = isRecord(data.driver) && typeof data.driver.id === "string" ? { id: data.driver.id } : null;
    const legacyAuthUid = str(data.legacyAuthUid);
    return {
        id: data.id,
        email: str(data.email),
        displayName: str(data.displayName),
        photoUrl: str(data.photoUrl),
        tenant: parseTenant(data.tenant),
        tenants,
        platformRoles: strings(data.platformRoles),
        dispatcher: data.dispatcher === true,
        steward: data.steward === true,
        driver,
        customerScopes: scopes,
        capabilities: strings(data.capabilities),
        mustChangePassword: data.mustChangePassword === true,
        ...(legacyAuthUid ? { legacyAuthUid } : {}),
    };
}

/** `GET /v1/me`, or `null` for a signed-out visitor (a 401 that `goFetch` did not turn into a session end). */
export async function fetchMe({ signal }: Pick<QueryFunctionContext, "signal">): Promise<MeDTO | null> {
    try {
        return parseMe(await goFetch<unknown>(ME_PATH, { signal }));
    } catch (error) {
        if (isApiError(error) && error.status === 401) return null;
        throw error;
    }
}

export const meQueryOptions = queryOptions({
    queryKey: queryKeys.me(),
    queryFn: fetchMe,
    ...QUERY_POLICY.me,
    refetchOnWindowFocus: true,
});

// ---------------------------------------------------------------------------------------------
// Selectors
// ---------------------------------------------------------------------------------------------

/** Whether the principal holds `capability` (a catalog key or a legacy id); false when signed out. */
export function hasCapability(me: MeDTO | null | undefined, capability: CapabilityId | string): boolean {
    return !!me && me.capabilities.includes(toCatalogKey(capability));
}

export function isPlatformAdmin(me: MeDTO | null | undefined): boolean {
    return !!me && me.platformRoles.includes("platform_admin");
}

/** A customer-scope principal without a membership: the legacy `customer` role (Appendix C §C.1.6). */
export function isCustomerPrincipal(me: MeDTO | null | undefined): boolean {
    return !!me && !me.tenant && me.customerScopes.length > 0;
}

/**
 * The legacy role label of a principal, as the bridge's custom token would carry it (Appendix C
 * §C.6.3): own-fleet `tenant_admin` and `platform_admin` are `admin`; a carrier `tenant_admin` is
 * `partner`; other tenant roles keep their name; a scope-only customer is `customer`. A principal
 * with none of these (a dispatcher or `support` without a membership) is `user`, the legacy fallback.
 */
export function legacyRoleOf(me: MeDTO): { admin: boolean; role: RoleId } {
    if (isPlatformAdmin(me)) return { admin: true, role: "admin" };
    const t = me.tenant;
    if (t) {
        if (t.role === "tenant_admin") return t.kind === "carrier" ? { admin: false, role: "partner" } : { admin: true, role: "admin" };
        if (["manager", "operation_staff", "operator", "user", "driver"].includes(t.role)) {
            return { admin: false, role: t.role as RoleId };
        }
        return { admin: false, role: "user" };
    }
    if (me.customerScopes.length > 0) return { admin: false, role: "customer" };
    return { admin: false, role: "user" };
}

/** The legacy id claims a Firebase bridge token carries (Appendix C §C.6.3); read from it, never from Go. */
export interface BridgeScopeClaims {
    customerScopeId?: string;
    partnerScopeId?: string;
    driverId?: string;
}

/**
 * The `customClaims` object `useAuth()` keeps for its 41 consumers until TW7 (developer-spec.md
 * §10.6): `admin` and `role` synthesised from `['me']` for `getRole()` / `isAdmin()`, the Go
 * `capabilities` for `can()`, and the legacy Firestore ids (`customerScopeId`, `partnerScopeId`,
 * `driverId`) that only the Firebase bridge token knows, because Firestore-backed pages filter by
 * those ids until their domain moves to Go.
 */
export interface SynthesizedClaims extends BridgeScopeClaims {
    admin: boolean;
    role: RoleId;
    capabilities: string[];
    dispatcher: boolean;
    [key: string]: unknown;
}

function pickString(source: Record<string, unknown> | null | undefined, key: string): string | undefined {
    const v = source?.[key];
    return typeof v === "string" && v.trim() !== "" ? v : undefined;
}

export function claimsFromMe(me: MeDTO | null | undefined, bridgeClaims?: Record<string, unknown> | null): SynthesizedClaims | null {
    if (!me) return null;
    const { admin, role } = legacyRoleOf(me);
    const claims: SynthesizedClaims = { admin, role, capabilities: me.capabilities, dispatcher: me.dispatcher };
    for (const key of ["customerScopeId", "partnerScopeId", "driverId"] as const) {
        const v = pickString(bridgeClaims, key);
        if (v) claims[key] = v;
    }
    return claims;
}
