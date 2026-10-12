"use client";

/**
 * The web session (T18 over TW4's `['me']`, TW7; developer-spec.md §10.4, §10.6; Appendix C §C.4.5;
 * Appendix E §E.3.3-§E.3.4; R36-R38, R50, R79, R80). Go owns identity: who is signed in comes from
 * `['me']` (`GET /v1/me` through the BFF, features/auth/api/me.ts), and the HttpOnly cookies `lt_at` /
 * `lt_rt` are set and read only by the BFF routes under `/api/auth/*`. Since TW7 the web holds no
 * Firebase session at all: the bridge (`POST /api/auth/firebase-token`, `signInWithCustomToken`) went
 * with the last Firestore page (R80).
 *
 * `useAuth()` keeps its shape for its consumers:
 * - `currentUser`: the Go user id as `uid` and `id`; email, name and photo from `['me']`.
 * - `me`: `['me']` itself (prefer `useMe(select)` in new code).
 * - `customClaims`: synthesised from `['me']` alone by `claimsFromMe` for `getRole()`, `isAdmin()` and
 *   `can()`: `admin`, `role`, `capabilities` and `dispatcher` from Go (R5, R27).
 * - `loading`: until `['me']` has answered.
 * - The context value is memoised and every function in it is stable.
 *
 * The cache follows the session through TW4's runtime (lib/queryClient.ts `bindQueryClientToSession`,
 * mounted by app/providers.tsx): a session end empties it, a `claims_changed` refresh invalidates every
 * query, and any change of principal in `['me']` (another user, another tenant, signed out) drops or
 * resets the previous principal's data. This provider never repeats those; it owns the sign-in,
 * logout, forced-refresh and tenant-switch flows.
 *
 * Removed by T18: the Firebase auth listener, `setAdminClaims` on every load, and the
 * `users/{uid}.forceLogoutAt` listener, replaced by SSE `session.revoked`
 * (lib/realtime/RealtimeProvider.tsx). Removed by TW7: the Firebase bridge and its forced re-mints, and
 * the legacy `users/{uid}` last-login write (Go records `users.last_login_*` through `PATCH /v1/me`).
 *
 * The Firebase session a pre-TW7 build persisted in this browser (IndexedDB outlives the Go session) is
 * still removed where the bridge used to sign it out: on logout, on every session end, and once per
 * browser on load, for a signed-in or a signed-out visitor alike (lib/legacyFirebaseCleanup.ts;
 * Appendix C §C.6.3 as built).
 */
import { createContext, useCallback, useContext, useEffect, useMemo, useRef } from "react";
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
import { clearLegacyFirebaseSession } from "@/lib/legacyFirebaseCleanup";
import { resolveLoginGeoForClient } from "@/lib/loginGeo";
import { clearCacheExceptMe, resetSessionCache } from "@/lib/queryClient";
import { queryKeys } from "@/lib/queryKeys";
import { endSession, onSessionEnd } from "@/lib/sessionEnd";
import { onClaimsRefreshed, sharedRefresh } from "@/lib/sharedRefresh";

/** The signed-in user as the pages read it (the fields of the former Firebase `User` they use). */
export interface AuthUser {
    /** The Go user id (`users.id`), as `id`. */
    uid: string;
    /** The Go user id (`users.id`). */
    id: string;
    email: string | null;
    displayName: string | null;
    photoURL: string | null;
}

export type AuthContextType = {
    currentUser: AuthUser | null;
    /** `['me']`: the Go principal, `null` when signed out or still loading. Prefer `useMe(select)` in new code. */
    me: MeDTO | null;
    /** Legacy-shaped claims synthesised from `['me']` alone (`getRole()`, `isAdmin()`, `can()`). */
    customClaims: SynthesizedClaims | null;
    loading: boolean;
    /** `['me']` failed for another reason than "signed out" (the api unreachable); the shell offers a retry. */
    error: ApiError | null;
    retry: () => void;
    /** `POST /api/auth/login`; throws the ApiError (`invalid_credentials`, `password_change_required`, ...). */
    login: (email: string, password: string) => Promise<void>;
    /** `POST /api/auth/google` with a GIS ID token and the nonce it was issued for. */
    loginWithGoogle: (idToken: string, nonce: string) => Promise<void>;
    /** Revokes the session, expires both cookies and drops every cached query. */
    logout: () => Promise<void>;
    /** A forced refresh: new claims in the access token and `['me']` (stays signed in). */
    refreshClaims: () => Promise<void>;
    /** `POST /api/auth/tenant`, then `['me']` and every tenant-scoped query follow. */
    switchTenant: (tenantId: string) => Promise<void>;
};

const AuthContext = createContext<AuthContextType | null>(null);

/**
 * After a sign-in: the principal (`GET /v1/me` through `parseMe`), then the cache. A tab with no
 * principal cached drops what it held while signed out first; a tab that held another principal is
 * reset by `watchPrincipal` (lib/queryClient.ts) when `['me']` is set, and the same principal keeps
 * its cache.
 */
export async function completeSignIn(client: QueryClient): Promise<MeDTO> {
    const me = await readMe();
    if (!client.getQueryData<MeDTO | null>(queryKeys.me())) clearCacheExceptMe(client);
    client.setQueryData<MeDTO | null>(queryKeys.me(), me);
    return me;
}

/**
 * Where the user signed in: Go `users.last_login_*` (`PATCH /v1/me`). Best effort, in the background:
 * it may wait for the browser's location prompt and must not delay the navigation.
 */
function recordSignInLocation(): void {
    void (async () => {
        const geo = await resolveLoginGeoForClient().catch(() => null);
        await recordLoginGeo(geo);
    })().catch(() => undefined);
}

function authUserFrom(me: MeDTO): AuthUser {
    return { uid: me.id, id: me.id, email: me.email, displayName: me.displayName, photoURL: me.photoUrl };
}

export const AuthProvider = ({ children }: { children: React.ReactNode }) => {
    const client = useQueryClient();
    const meQuery = useQuery(meQueryOptions);
    const me = meQuery.data ?? null;

    // New claims (a `claims_changed` 401 or `session.revoked`, served by a forced refresh in any tab):
    // the runtime invalidates every query, `['me']` included; this waits for that `['me']` refetch
    // (joined, not repeated), so the role and capabilities follow while the user stays signed in
    // (R50, R78).
    const claimsWork = useRef<Promise<void> | null>(null);
    const applyNewClaims = useCallback((): Promise<void> => {
        if (claimsWork.current) return claimsWork.current;
        const work = (async () => {
            await refetchMe(client);
        })()
            .catch((error) => console.warn("[auth] applying new claims failed", error))
            .finally(() => {
                claimsWork.current = null;
            });
        claimsWork.current = work;
        return work;
    }, [client]);

    useEffect(() => onClaimsRefreshed(() => void applyNewClaims()), [applyNewClaims]);

    // TW7 transition: no page reads a Firebase session, so one a pre-TW7 build left in this browser is
    // deleted once on load and again whenever the Go session ends (lib/legacyFirebaseCleanup.ts).
    useEffect(() => {
        clearLegacyFirebaseSession({ once: true });
        return onSessionEnd(() => clearLegacyFirebaseSession());
    }, []);

    const login = useCallback(
        async (email: string, password: string) => {
            await signInWithPassword(email, password);
            await completeSignIn(client);
            recordSignInLocation();
        },
        [client]
    );

    const loginWithGoogle = useCallback(
        async (idToken: string, nonce: string) => {
            await signInWithGoogle(idToken, nonce);
            await completeSignIn(client);
            recordSignInLocation();
        },
        [client]
    );

    const logout = useCallback(async () => {
        // The Go session first: the BFF revokes it and expires both cookies whatever Go answers.
        await signOutSession();
        // A Firebase session a pre-TW7 build persisted here must not outlive the user's logout.
        clearLegacyFirebaseSession();
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
            // Another tenant is another principal: `watchPrincipal` cancels and resets every other query
            // (`['me','tenants']` included), and the mounted ones refetch.
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
    const loading = meQuery.isPending;

    const currentUser = useMemo(() => (me ? authUserFrom(me) : null), [me]);
    const customClaims = useMemo(() => claimsFromMe(me), [me]);

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
