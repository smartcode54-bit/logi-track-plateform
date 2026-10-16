/**
 * Users administration over Go. Member reads retain their tenant contract; platform reads use
 * projected list/detail DTOs. Writes retain their dedicated routes and server authorization.
 */
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";

import { goFetch, goFetchEnvelope, goPath } from "@/lib/goFetch";
import { goInfiniteQueryFn, goNextPageParam } from "@/lib/goQuery";
import { isPlatformPrincipal, type MeDTO, type PlatformRole, type TenantRole } from "@/features/auth/api/me";
import { queryKeys } from "@/lib/queryKeys";
import { POLL_INTERVAL_MS, pollWhileVisible } from "@/lib/realtime/polling";

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
    /**
     * Whether the caller may use the account-wide actions on this user (temporary password, email, disable,
     * invite, sessions; T62, Appendix C §C.8 "Reach"). A user who also belongs to an organisation outside the
     * caller's reach is `false` with `accountReason: "outside_reach"`; its memberships in the caller's tenants
     * stay manageable. Absent from an API older than T62: treated as manageable (Go still refuses).
     */
    manageAccount?: boolean;
    /** Why `manageAccount` is false: `self`, `deleted`, `outside_reach`, `platform_target`, `steward_only`, `tenant_admin_only`. */
    accountReason?: string;
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
export const USERS_POLL_MS = POLL_INTERVAL_MS;

/** The endpoint plane is part of every users query identity. */
export type UsersReach = "all" | "tenant";

export function usersReach(me: Pick<MeDTO, "platformRoles"> | null | undefined): UsersReach {
    return isPlatformPrincipal(me) ? "all" : "tenant";
}

export function usersReadPath(reach: UsersReach): string {
    return reach === "all" ? "/v1/platform/users" : USERS_PATH;
}

/** `['users', {q, role, status, reach}]`: the reach is in the key, so a changed reach never shows another's rows. */
export function usersQueryKey(filter: UsersFilter, reach: UsersReach = "tenant") {
    return queryKeys.users.list({ q: reach === "all" ? "" : filter.q, role: filter.role, status: filter.status, reach });
}

type UsersKey = ReturnType<typeof usersQueryKey>;

/** The query of `GET /v1/users` for a filter (only the documented parameters are sent). */
export function usersQuery(filter: Pick<UsersFilter, "q" | "role" | "status">, reach: UsersReach = "tenant") {
    return {
        q: reach === "all" ? undefined : filter.q.trim() || undefined,
        role: filter.role || undefined,
        status: filter.status || undefined,
        sort: "last_login_at",
        limit: USERS_PAGE_SIZE,
    };
}

/**
 * The keyset list, newest sign-in first, polled while visible. Platform reads use their own DTO.
 */
export function useUsers(filter: UsersFilter, enabled = true, reach: UsersReach = "tenant", pollMs: number = USERS_POLL_MS) {
    return useInfiniteQuery({
        queryKey: usersQueryKey(filter, reach),
        queryFn: goInfiniteQueryFn<UserDTO, UsersKey>(([, f]) => usersReadPath(f.reach), {
            query: ([, f]) => usersQuery(f, f.reach),
        }),
        initialPageParam: undefined as string | undefined,
        getNextPageParam: goNextPageParam,
        staleTime: pollMs,
        gcTime: 5 * 60_000,
        ...pollWhileVisible(pollMs),
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

/**
 * `PUT /v1/tenants/{tenantId}/members/{userId} {role, status?}`: adds the membership or changes its role. Go reads an
 * absent status as `active` (a role PUT reactivates), so a role change of an existing membership passes its current
 * status to keep a suspended membership suspended.
 */
export function setMemberRole(tenantId: string, userId: string, role: TenantRole | string, status?: "active" | "suspended"): Promise<unknown> {
    return goFetch(goPath`/v1/tenants/${tenantId}/members/${userId}`, { method: "PUT", body: status ? { role, status } : { role } });
}

/** `PUT /v1/tenants/{tenantId}/members/{userId} {status}`: suspends or reactivates one membership (T62). */
export function setMemberStatus(tenantId: string, userId: string, status: "active" | "suspended"): Promise<unknown> {
    return goFetch(goPath`/v1/tenants/${tenantId}/members/${userId}`, { method: "PUT", body: { status } });
}

/** `POST /v1/tenants/{tenantId}/members {email, role}`: a person by email (T62). */
export interface AddMemberInput {
    email: string;
    /** A tenant role other than `driver` (a driver works through its drivers profile). */
    role: TenantRole;
    /** Used only when no account holds the address yet. */
    displayName?: string;
}

/**
 * The answer of `POST /v1/tenants/{id}/members`: the same whether the account existed (it gets the membership)
 * or not (it is created and invited by a reset link, R29); it never says where else the person belongs.
 */
export interface AddMemberResult {
    tenantId: string;
    userId: string;
    role: TenantRole;
    status: "active";
}

export function addMemberByEmail(tenantId: string, input: AddMemberInput): Promise<AddMemberResult> {
    const body: Record<string, unknown> = { email: input.email.trim(), role: input.role };
    if (input.displayName?.trim()) body.displayName = input.displayName.trim();
    return goFetch<AddMemberResult>(goPath`/v1/tenants/${tenantId}/members`, { method: "POST", body });
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
    if (!email || !legacy.uid || reach === "all") return null;
    const page = await goFetchEnvelope<UserDTO[]>(USERS_PATH, {
        query: { q: email, limit: USERS_PAGE_SIZE },
    });
    return (page.data ?? []).find((u) => u.legacyAuthUid === legacy.uid) ?? null;
}

