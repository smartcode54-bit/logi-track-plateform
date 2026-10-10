"use client";

/**
 * The web session (T18 over TW4's `['me']`; developer-spec.md §10.4, §10.6; Appendix C §C.4.5, §C.6.3;
 * Appendix E §E.3.3-§E.3.4; R36-R38, R40, R50, R79, R80). Go owns identity: who is signed in comes from
 * `['me']` (`GET /v1/me` through the BFF, features/auth/api/me.ts), and the HttpOnly cookies `lt_at` /
 * `lt_rt` are set and read only by the BFF routes under `/api/auth/*`. Firebase remains a bridge until
 * TW7: a principal entitled to a Firestore session signs in to Firebase with a custom token from
 * `POST /api/auth/firebase-token` (lib/firebaseBridge.ts), so the pages still on Firestore keep working
 * (R9).
 *
 * `useAuth()` keeps its shape for the unmigrated consumers:
 * - `currentUser`: the Firebase uid while bridged (Firestore writes carry it), else the Go user id;
 *   email, name and photo from `['me']`.
 * - `me`: `['me']` itself (prefer `useMe(select)` in new code).
 * - `customClaims`: synthesised from `['me']` by `claimsFromMe` for `getRole()`, `isAdmin()` and
 *   `can()` until TW7: `admin`, `role`, `capabilities` and `dispatcher` from Go, and only the legacy
 *   Firestore ids (`customerScopeId`, `partnerScopeId`, `driverId`) from the bridge token, so no page
 *   decides what a user may do from Firebase claims (R5, R27).
 * - `loading`: until `['me']` has answered and, for a signed-in principal, the bridge has first
 *   settled. A later forced re-mint (`claims_changed`, tenant switch) does not bring it back, so the
 *   `/app` layout keeps the page mounted (lib/firebaseBridge.ts).
 * - The context value is memoised and every function in it is stable (ends `context/auth.tsx:171`).
 *
 * The cache follows the session through TW4's runtime (lib/queryClient.ts `bindQueryClientToSession`,
 * mounted by app/providers.tsx): a session end empties it, a `claims_changed` refresh invalidates every
 * query, and any change of principal in `['me']` (another user, another tenant, signed out) drops or
 * resets the previous principal's data. This provider never repeats those; it owns the Firebase bridge
 * (sign-out on a session end or for a signed-out visitor, the forced re-mint after `claims_changed`,
 * which waits for the `['me']` refetch) and the sign-in, logout and tenant-switch flows.
 *
 * A signed-out visitor (`['me']` resolved `null`, no error) holds no Firebase session either: one
 * left in this browser by a previous user (IndexedDB outlives the Go session) is signed out on every
 * page, the landing page's sign-in dialogs included, before anyone signs in there.
 *
 * Removed by T18: the Firebase auth listener, `setAdminClaims` on every load (`context/auth.tsx:51-59`
 * before), and the `users/{uid}.forceLogoutAt` listener (`:91-129`), replaced by SSE `session.revoked`
 * (context/realtime.tsx).
 */
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useSyncExternalStore } from "react";
import { useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";

import {
    claimsFromMe,
    meQueryOptions,
    readMe,
    refetchMe,
    type MeDTO,
    type SynthesizedClaims,
} from "@/features/auth/api/me";
import { ApiError, isApiError } from "@/lib/apiError";
import { recordLoginGeo, signInWithGoogle, signInWithPassword, signOutSession, switchTenant as switchSessionTenant } from "@/lib/authClient";
import {
    bridgeSettledFor,
    getBridgeState,
    signOutFirebaseBridge,
    subscribeBridge,
    syncFirebaseBridge,
    type BridgeState,
} from "@/lib/firebaseBridge";
import { resolveLoginGeoForClient } from "@/lib/loginGeo";
import { clearCacheExceptMe, resetSessionCache } from "@/lib/queryClient";
import { queryKeys } from "@/lib/queryKeys";
import { endSession, onSessionEnd } from "@/lib/sessionEnd";
import { onClaimsRefreshed, sharedRefresh } from "@/lib/sharedRefresh";

/** The signed-in user as the unmigrated pages read it (the fields of the Firebase `User` they use). */
export interface AuthUser {
    /** The Firebase uid while bridged (the uid of Firestore documents), else the Go user id. */
    uid: string;
    /** The Go user id (`users.id`). */
    id: string;
    email: string | null;
    displayName: string | null;
    photoURL: string | null;
    emailVerified: boolean;
    metadata: { creationTime?: string; lastSignInTime?: string };
}

export type AuthContextType = {
    currentUser: AuthUser | null;
    /** `['me']`: the Go principal, `null` when signed out or still loading. Prefer `useMe(select)` in new code. */
    me: MeDTO | null;
    /** Legacy claims synthesised from `['me']` (plus the bridge token's legacy ids) until TW7. */
    customClaims: SynthesizedClaims | null;
    loading: boolean;
    /** `['me']` failed for another reason than "signed out" (the api unreachable); the shell offers a retry. */
    error: ApiError | null;
    retry: () => void;
    /** `POST /api/auth/login`; throws the ApiError (`invalid_credentials`, `password_change_required`, ...). */
    login: (email: string, password: string) => Promise<void>;
    /** `POST /api/auth/google` with a GIS ID token and the nonce it was issued for. */
    loginWithGoogle: (idToken: string, nonce: string) => Promise<void>;
    /** Revokes the session, expires both cookies, signs out of Firebase and drops every cached query. */
    logout: () => Promise<void>;
    /** A forced refresh: new claims in the access token, `['me']` and the bridge token (stays signed in). */
    refreshClaims: () => Promise<void>;
    /** `POST /api/auth/tenant`, then `['me']`, the bridge and every tenant-scoped query follow. */
    switchTenant: (tenantId: string) => Promise<void>;
};

const AuthContext = createContext<AuthContextType | null>(null);

/**
 * After a sign-in: the principal (`GET /v1/me` through `parseMe`), then the bridge (forced: once per
 * login, the legacy claims follow the current role, R80), and only then the cache, so the bridge
 * effect below finds it settled and does not mint a second token. A tab with no principal cached
 * drops what it held while signed out first; a tab that held another principal is reset by
 * `watchPrincipal` (lib/queryClient.ts) when `['me']` is set, and the same principal keeps its cache.
 */
export async function completeSignIn(client: QueryClient): Promise<MeDTO> {
    const me = await readMe();
    await syncFirebaseBridge({ id: me.id, legacyAuthUid: me.legacyAuthUid }, { force: true });
    if (!client.getQueryData<MeDTO | null>(queryKeys.me())) clearCacheExceptMe(client);
    client.setQueryData<MeDTO | null>(queryKeys.me(), me);
    return me;
}

/**
 * Where the user signed in: Go `users.last_login_*` (`PATCH /v1/me`), and while bridged the legacy
 * `users/{uid}` document the Security Center active-users list reads until P6. Best effort, in the
 * background: it may wait for the browser's location prompt and must not delay the navigation.
 */
function recordSignInLocation(client: QueryClient): void {
    void (async () => {
        const geo = await resolveLoginGeoForClient().catch(() => null);
        await recordLoginGeo(geo);
        const bridge = getBridgeState();
        if (bridge.status !== "ready" || !bridge.firebaseUid) return;
        // Still the principal who signed in (the location prompt may have taken a while).
        const me = client.getQueryData<MeDTO | null>(queryKeys.me());
        if (!me || me.id !== bridge.userId) return;
        const { updateUserLastLogin } = await import("@/lib/updateUserLastLogin");
        await updateUserLastLogin({ uid: bridge.firebaseUid, email: me.email, displayName: me.displayName }, geo);
    })().catch(() => undefined);
}

function bridgedFor(me: MeDTO, bridge: BridgeState): boolean {
    return bridge.status === "ready" && bridge.userId === me.id && Boolean(bridge.firebaseUid);
}

function authUserFrom(me: MeDTO, bridge: BridgeState): AuthUser {
    const bridged = bridgedFor(me, bridge);
    return {
        uid: bridged ? (bridge.firebaseUid as string) : me.id,
        id: me.id,
        email: me.email,
        displayName: me.displayName,
        photoURL: me.photoUrl,
        emailVerified: bridged ? Boolean(bridge.profile?.emailVerified) : false,
        metadata: bridged
            ? { creationTime: bridge.profile?.creationTime, lastSignInTime: bridge.profile?.lastSignInTime }
            : {},
    };
}

export const AuthProvider = ({ children }: { children: React.ReactNode }) => {
    const client = useQueryClient();
    const meQuery = useQuery(meQueryOptions);
    const bridge = useSyncExternalStore(subscribeBridge, getBridgeState, getBridgeState);
    const me = meQuery.data ?? null;
    const meId = me?.id;
    const legacyAuthUid = me?.legacyAuthUid;

    // The bridge for whoever `['me']` names. Not forced: a restored Firebase session of that very uid
    // is kept, and a user Go refused stays without one for this tab (lib/firebaseBridge.ts).
    useEffect(() => {
        if (meId) void syncFirebaseBridge({ id: meId, legacyAuthUid });
    }, [meId, legacyAuthUid]);

    // A signed-out visitor: whatever Firebase session this browser still holds (a previous user's,
    // restored from IndexedDB) is signed out, so no Firestore session outlives the Go one and a
    // sign-in from any page starts clean.
    const signedOut = !meQuery.isPending && me === null && !meQuery.error;
    useEffect(() => {
        if (signedOut) void signOutFirebaseBridge();
    }, [signedOut]);

    // A session that ended in this tab (lib/sessionEnd.ts): the Firebase session goes too (R50, §10.4
    // step 4). The cache and `['me']` are emptied by the runtime's own listener (bindQueryClientToSession).
    useEffect(() => onSessionEnd(() => void signOutFirebaseBridge()), []);

    // New claims (a `claims_changed` 401 or `session.revoked`, served by a forced refresh in any tab):
    // the runtime invalidates every query, `['me']` included; this waits for that `['me']` refetch
    // (joined, not repeated) and mints the bridge token again, so the legacy claims follow the new role
    // while the user stays signed in (R50, R78, R80).
    const claimsWork = useRef<Promise<void> | null>(null);
    const applyNewClaims = useCallback((): Promise<void> => {
        if (claimsWork.current) return claimsWork.current;
        const work = (async () => {
            const next = await refetchMe(client);
            if (next) await syncFirebaseBridge({ id: next.id, legacyAuthUid: next.legacyAuthUid }, { force: true });
        })()
            .catch((error) => console.warn("[auth] applying new claims failed", error))
            .finally(() => {
                claimsWork.current = null;
            });
        claimsWork.current = work;
        return work;
    }, [client]);

    useEffect(() => onClaimsRefreshed(() => void applyNewClaims()), [applyNewClaims]);

    const login = useCallback(
        async (email: string, password: string) => {
            await signInWithPassword(email, password);
            await completeSignIn(client);
            recordSignInLocation(client);
        },
        [client]
    );

    const loginWithGoogle = useCallback(
        async (idToken: string, nonce: string) => {
            await signInWithGoogle(idToken, nonce);
            await completeSignIn(client);
            recordSignInLocation(client);
        },
        [client]
    );

    const logout = useCallback(async () => {
        // The Go session first: the BFF revokes it and expires both cookies whatever Go answers.
        await signOutSession();
        await signOutFirebaseBridge();
        // No data of this user survives in the tab; `['me']` reads as signed out without a request.
        resetSessionCache(client);
    }, [client]);

    const refreshClaims = useCallback(async () => {
        const ok = await sharedRefresh({ force: true, since: Date.now() });
        if (!ok) {
            endSession(new ApiError({ status: 401, code: "unauthenticated", message: "refresh refused" }));
            return;
        }
        await applyNewClaims();
    }, [applyNewClaims]);

    const switchTenant = useCallback(
        async (tenantId: string) => {
            await switchSessionTenant(tenantId);
            const next = await readMe();
            // The bridge first, so the Firestore reads that refetch below already run as the new tenant.
            await syncFirebaseBridge({ id: next.id, legacyAuthUid: next.legacyAuthUid }, { force: true });
            // Another tenant is another principal: `watchPrincipal` cancels and resets every other query
            // (`['me','tenants']` and `['webFlags']` included), and the mounted ones refetch.
            client.setQueryData<MeDTO | null>(queryKeys.me(), next);
        },
        [client]
    );

    const { refetch } = meQuery;
    const retry = useCallback(() => void refetch(), [refetch]);

    const queryError = meQuery.error;
    const error = useMemo<ApiError | null>(() => {
        if (!queryError) return null;
        return isApiError(queryError) ? queryError : new ApiError({ status: 0, code: "unavailable", message: String(queryError) });
    }, [queryError]);
    const loading = meQuery.isPending || (me !== null && !bridgeSettledFor(bridge, me.id));

    const currentUser = useMemo(() => (me ? authUserFrom(me, bridge) : null), [me, bridge]);
    // Only the bridge token of this very principal contributes its legacy Firestore ids.
    const bridgeClaims = me && bridgedFor(me, bridge) ? bridge.claims : undefined;
    const customClaims = useMemo(() => claimsFromMe(me, bridgeClaims), [me, bridgeClaims]);

    const value = useMemo<AuthContextType>(
        () => ({
            currentUser,
            me,
            customClaims,
            loading,
            error,
            retry,
            login,
            loginWithGoogle,
            logout,
            refreshClaims,
            switchTenant,
        }),
        [currentUser, me, customClaims, loading, error, retry, login, loginWithGoogle, logout, refreshClaims, switchTenant]
    );

    return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
};

export const useAuth = () => useContext(AuthContext);
