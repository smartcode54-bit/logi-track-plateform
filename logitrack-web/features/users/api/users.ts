/**
 * Users administration over Go (T18 web side; developer-spec.md §4 "PostgreSQL is the users writer
 * from P0", R49, R81; Appendix B §B.2.4, §B.2.5; Appendix C §C.8). Every read and write of the Security
 * Center users page goes through the BFF (`/api/go/v1/users*`, `/api/go/v1/tenants/{id}/members/*`):
 * none of `getUsers`, `createUser`, `updateUserRole`, `setUserDisabled`, `revokeUserRefreshTokens`,
 * `linkDriverToUser` or `syncExistingUsers` is called any more, and the list pages by keyset cursor
 * (no 50-row cap, no `listUsers(1000)`).
 *
 * Go authorises every call (`users:view|manage|assign_role|revoke_sessions`, `drivers:edit`,
 * `platform:manage_platform_roles`), refuses actions on the caller itself, bumps `auth_version` on
 * role, scope, driver-link and platform-role changes (the user's tabs refresh and stay signed in, R50)
 * and ends the sessions on disable and temporary password. The UI hides what `['me']` does not allow.
 *
 * Reach (Appendix C §C.3.9, §C.8): a tenant principal reads the users of its tenant reach. A platform
 * principal (`platform_admin`, `support`) reads every tenant's users only through `X-Act-On-Tenant: *`
 * (read-only bypass, one `platform_cross_tenant_access` audit row per request), so the users reads of
 * such a principal (`GET /v1/users`, the admin search of the tenants page, the legacy-account lookup)
 * carry it. Writes never do: `PUT/DELETE /v1/tenants/{id}/members/{userId}` and
 * `POST /v1/users {tenantId}` are authorised for platform admins without it.
 */
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";

import { goFetch, goFetchEnvelope, goPath } from "@/lib/goFetch";
import { fetchAllPages } from "@/lib/goPages";
import { goInfiniteQueryFn, goNextPageParam } from "@/lib/goQuery";
import { isPlatformPrincipal, type MeDTO, type PlatformRole, type TenantRole } from "@/features/auth/api/me";
import { queryKeys } from "@/lib/queryKeys";

export type UserStatus = "active" | "disabled" | "reset_required" | "deleted";
export type ScopeKind = "customer" | "dispatcher";

/** One membership of a user (`memberships` row joined with its tenant). */
export interface UserMembership {
    tenantId: string;
    tenantNameTh: string;
    tenantNameEn: string | null;
    tenantKind: string;
    role: TenantRole | string;
    status: "active" | "suspended" | string;
}

/** One `user_scopes` row: a customer scope or a dispatcher grant on a billing party. */
export interface UserScope {
    kind: ScopeKind;
    billingPartyId: string;
    name: string;
}

/**
 * A user of `GET /v1/users` (list item) and `GET /v1/users/{id}` (Appendix B §B.2.5 as refined by T18):
 * the account, its memberships, scopes, platform roles and driver link.
 */
export interface UserDTO {
    id: string;
    email: string | null;
    displayName: string | null;
    photoUrl: string | null;
    status: UserStatus;
    mustChangePassword: boolean;
    lastLoginAt: string | null;
    createdAt: string;
    /** The Firebase uid of an imported or bridged user (the `users/{uid}` document id until P6). */
    legacyAuthUid: string | null;
    memberships: UserMembership[];
    scopes: UserScope[];
    platformRoles: PlatformRole[];
    driver: { id: string; displayName: string | null } | null;
}

/** `POST /v1/users`: `role` is a tenant role, or `customer` for a scope-only user (`billingPartyIds`). */
export interface CreateUserInput {
    email: string;
    displayName: string;
    role: TenantRole | "customer";
    /** Platform callers only; tenant staff create in their active tenant. */
    tenantId?: string;
    driverId?: string;
    billingPartyIds?: string[];
    /** Mail a reset link (invite) instead of returning a temporary password (R29). */
    sendInvite?: boolean;
}

export interface CreateUserResult {
    user: UserDTO;
    /** Shown once; absent with `sendInvite` (R29). */
    temporaryPassword?: string;
}

export interface UsersFilter {
    q: string;
    role: string;
    status: string;
}

export const USERS_KEY = queryKeys.users.all();
export const USERS_PATH = "/v1/users";
export const USERS_PAGE_SIZE = 50;
/** The list is polled every 60 s while the tab is visible (Appendix E §E.5 row 14). */
export const USERS_POLL_MS = 60_000;

/** The cross-tenant header of a platform principal (Appendix C §C.3.9; the BFF forwards it). */
export const ACT_ON_TENANT_HEADER = "X-Act-On-Tenant";
/** Its read-only "every tenant" form (`GET`/`HEAD` only). */
export const ACT_ON_ALL_TENANTS = "*";

/** Whose users a read covers: `all` (a platform principal, with `X-Act-On-Tenant: *`) or the caller's tenant reach. */
export type UsersReach = "all" | "tenant";

export function usersReach(me: Pick<MeDTO, "platformRoles"> | null | undefined): UsersReach {
    return isPlatformPrincipal(me) ? "all" : "tenant";
}

/** The request headers of a users read with this reach. */
export function usersReachHeaders(reach: UsersReach): Record<string, string> | undefined {
    return reach === "all" ? { [ACT_ON_TENANT_HEADER]: ACT_ON_ALL_TENANTS } : undefined;
}

/** `['users', {q, role, status, reach}]`: the reach is in the key, so a changed reach never shows another's rows. */
export function usersQueryKey(filter: UsersFilter, reach: UsersReach = "tenant") {
    return queryKeys.users.list({ q: filter.q, role: filter.role, status: filter.status, reach });
}

type UsersKey = ReturnType<typeof usersQueryKey>;

/** The query of `GET /v1/users` for a filter (only the documented parameters are sent). */
export function usersQuery(filter: Pick<UsersFilter, "q" | "role" | "status">) {
    return {
        q: filter.q.trim() || undefined,
        role: filter.role || undefined,
        status: filter.status || undefined,
        sort: "last_login_at",
        limit: USERS_PAGE_SIZE,
    };
}

/**
 * `['users', filter]`: the keyset list, newest sign-in first, 50 per page, polled while visible. A
 * platform principal passes `reach: "all"` (`usersReach(me)`): every poll then writes one
 * `platform_cross_tenant_access` audit row in Go (Appendix B §B.2.5 web contract).
 */
export function useUsers(filter: UsersFilter, enabled = true, reach: UsersReach = "tenant") {
    return useInfiniteQuery({
        queryKey: usersQueryKey(filter, reach),
        queryFn: goInfiniteQueryFn<UserDTO, UsersKey>(USERS_PATH, {
            query: ([, f]) => usersQuery(f),
            headers: ([, f]) => usersReachHeaders(f.reach),
        }),
        initialPageParam: undefined as string | undefined,
        getNextPageParam: goNextPageParam,
        staleTime: USERS_POLL_MS,
        gcTime: 5 * 60_000,
        refetchInterval: USERS_POLL_MS,
        enabled,
    });
}

/** Invalidates every users list (after any write of this module). */
export function useInvalidateUsers() {
    const client = useQueryClient();
    return () => client.invalidateQueries({ queryKey: USERS_KEY });
}

// --- writes (Appendix B §B.2.4, §B.2.5) ---

export function createUser(input: CreateUserInput): Promise<CreateUserResult> {
    const body: Record<string, unknown> = { email: input.email.trim(), displayName: input.displayName.trim(), role: input.role };
    if (input.tenantId) body.tenantId = input.tenantId;
    if (input.driverId) body.driverId = input.driverId;
    if (input.billingPartyIds && input.billingPartyIds.length > 0) body.billingPartyIds = input.billingPartyIds;
    if (input.sendInvite) body.sendInvite = true;
    return goFetch<CreateUserResult>(USERS_PATH, { method: "POST", body });
}

/** `PUT /v1/tenants/{tenantId}/members/{userId} {role}`: adds the membership or changes its role. */
export function setMemberRole(tenantId: string, userId: string, role: TenantRole | string): Promise<unknown> {
    return goFetch(goPath`/v1/tenants/${tenantId}/members/${userId}`, { method: "PUT", body: { role } });
}

/** `DELETE /v1/tenants/{tenantId}/members/{userId}`. */
export function removeMember(tenantId: string, userId: string): Promise<void> {
    return goFetch<void>(goPath`/v1/tenants/${tenantId}/members/${userId}`, { method: "DELETE" });
}

/** `PUT /v1/users/{id}/scopes/{kind} {billingPartyIds}` (at most 20); an empty list deletes the scope. */
export function setUserScope(userId: string, kind: ScopeKind, billingPartyIds: string[]): Promise<unknown> {
    if (billingPartyIds.length === 0) return goFetch<void>(goPath`/v1/users/${userId}/scopes/${kind}`, { method: "DELETE" });
    return goFetch(goPath`/v1/users/${userId}/scopes/${kind}`, { method: "PUT", body: { billingPartyIds } });
}

/** `POST /v1/users/{id}/disable {reason}` (sessions end) or `/enable`. */
export function setUserDisabled(userId: string, disabled: boolean, reason?: string): Promise<void> {
    if (disabled) return goFetch<void>(goPath`/v1/users/${userId}/disable`, { method: "POST", body: { reason: reason?.trim() ?? "" } });
    return goFetch<void>(goPath`/v1/users/${userId}/enable`, { method: "POST", body: {} });
}

/** `PUT /v1/users/{id}/driver-link {driverId}`, or `DELETE` with an empty id. */
export function setDriverLink(userId: string, driverId: string): Promise<void> {
    if (!driverId) return goFetch<void>(goPath`/v1/users/${userId}/driver-link`, { method: "DELETE" });
    return goFetch<void>(goPath`/v1/users/${userId}/driver-link`, { method: "PUT", body: { driverId } });
}

/** `POST /v1/users/{id}/password/temporary`: a generated password, shown once; sessions end (R29, R79). */
export function issueTemporaryPassword(userId: string): Promise<{ temporaryPassword: string }> {
    return goFetch<{ temporaryPassword: string }>(goPath`/v1/users/${userId}/password/temporary`, { method: "POST", body: {} });
}

/** `POST /v1/users/{id}/invite`: (re)sends the reset-link invite (202). */
export function inviteUser(userId: string): Promise<void> {
    return goFetch<void>(goPath`/v1/users/${userId}/invite`, { method: "POST", body: {} });
}

/** `DELETE /v1/users/{id}/sessions`: ends every session of the user (`admin_revoke`). */
export function revokeUserSessions(userId: string): Promise<void> {
    return goFetch<void>(goPath`/v1/users/${userId}/sessions`, { method: "DELETE" });
}

/** `POST /v1/users/{id}/platform-roles {role}` / `DELETE /v1/users/{id}/platform-roles/{role}`. */
export function setPlatformRole(userId: string, role: PlatformRole, granted: boolean): Promise<void> {
    if (granted) return goFetch<void>(goPath`/v1/users/${userId}/platform-roles`, { method: "POST", body: { role } });
    return goFetch<void>(goPath`/v1/users/${userId}/platform-roles/${role}`, { method: "DELETE" });
}

/**
 * The Go user behind a legacy `users/{uid}` document (the active-users list of the Security Center
 * overview reads Firestore until P6, Appendix E §E.5 row 29). The document's `email` only narrows the
 * search (`GET /v1/users?q=<email>`): its owner can rewrite any field of it (`firestore.rules`
 * `match /users/{uid}`), so the match is bound to the document id, which it cannot change, and which
 * is the user's `legacyAuthUid` (every bridged user has one: imported, or set at its first mint,
 * Appendix C §C.6.3). There is no email fallback: a document naming someone else's email resolves to
 * nobody (`null`), never to that someone.
 */
export async function findUserForLegacyAccount(legacy: { uid: string; email: string }, reach: UsersReach = "tenant"): Promise<UserDTO | null> {
    const email = legacy.email.trim();
    if (!email || !legacy.uid) return null;
    const page = await goFetchEnvelope<UserDTO[]>(USERS_PATH, {
        query: { q: email, limit: USERS_PAGE_SIZE },
        headers: usersReachHeaders(reach),
    });
    return (page.data ?? []).find((u) => u.legacyAuthUid === legacy.uid) ?? null;
}

// --- pickers ---

/** One option of the customer-scope picker (`GET /v1/customers?fields=minimal`, Appendix B §B.2.10). */
export interface CustomerOption {
    id: string;
    billingPartyId: string;
    code: string | null;
    name: string;
}

/** One option of the driver-link picker (`GET /v1/drivers?fields=minimal`, Appendix B §B.2.7). */
export interface DriverOption {
    id: string;
    displayName: string | null;
}

/** `['customers', {fields:'minimal'}]` for the customer-scope picker (10 min, §10.7). */
export function useCustomerOptions(enabled: boolean) {
    return useQuery({
        queryKey: queryKeys.customers.list({ fields: "minimal" }),
        queryFn: ({ signal }) => fetchAllPages<CustomerOption>("/v1/customers", { fields: "minimal" }, { signal }),
        staleTime: 10 * 60_000,
        gcTime: 60 * 60_000,
        enabled,
    });
}

/** `['drivers', {fields:'minimal', q}]` for the driver-link picker (searched on the server). */
export function useDriverOptions(q: string, enabled: boolean) {
    const term = q.trim();
    return useQuery({
        queryKey: queryKeys.drivers.list({ fields: "minimal", q: term }),
        queryFn: ({ signal }) =>
            goFetch<DriverOption[]>("/v1/drivers", { signal, query: { fields: "minimal", q: term || undefined, limit: 20 } }),
        staleTime: 5 * 60_000,
        enabled,
    });
}
