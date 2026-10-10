"use client";

/**
 * The web session (T18; developer-spec.md §10.4, §10.6; Appendix C §C.4.5, §C.6.3; R36-R38, R40, R50,
 * R79, R80). Go owns identity: who is signed in comes from `['me']` (`GET /v1/me` through the BFF),
 * and the HttpOnly cookies `lt_at` / `lt_rt` are set and read only by the BFF routes under
 * `/api/auth/*`. Firebase remains a bridge until TW7: a principal entitled to a Firestore session
 * signs in to Firebase with a custom token from `POST /api/auth/firebase-token`
 * (lib/firebaseBridge.ts), so the pages still on Firestore keep working (R9).
 *
 * `useAuth()` keeps its shape for the unmigrated consumers:
 * - `currentUser`: the Firebase uid while bridged (Firestore writes carry it), else the Go user id;
 *   email, name and photo from `['me']`.
 * - `customClaims`: the legacy claims (`admin`, `role`, ...) of the bridged Firebase ID token, else
 *   synthesised from `['me']` (`legacyClaimsFromMe`) for `getRole()` / `can()`.
 * - `loading`: until `['me']` has answered and, for a signed-in principal, the bridge has settled.
 *
 * Removed by T18: the Firebase auth listener, `setAdminClaims` on every load (`context/auth.tsx:51-59`
 * before), and the `users/{uid}.forceLogoutAt` listener (`:91-129`), replaced by SSE `session.revoked`
 * (context/realtime.tsx).
 */
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useSyncExternalStore } from "react";
import { useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";

import {
    legacyClaimsFromMe,
    ME_KEY,
    ME_PATH,
    meQueryOptions,
    MY_TENANTS_KEY,
    refetchMe,
    type Me,
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
import { goFetch } from "@/lib/goFetch";
import { resolveLoginGeoForClient } from "@/lib/loginGeo";
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
    /** `['me']` of the signed-in principal, `null` when signed out or still loading. */
    me: Me | null;
    customClaims: Record<string, unknown> | null;
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

/** Keys that do not depend on the principal's tenant: kept across a tenant switch. */
const TENANT_NEUTRAL_KEYS = new Set(["me", "webFlags"]);

/** The signed-out state: `['me']` is `null` (no refetch for its stale time) and nothing else is cached. */
export function applySignedOut(client: QueryClient): void {
    void client.cancelQueries();
    client.removeQueries({ predicate: (q) => !(q.queryKey.length === 1 && q.queryKey[0] === ME_KEY[0]) });
    client.setQueryData<Me | null>(ME_KEY, null);
}

/** A fresh `GET /v1/me` that does not touch the cache (the caller decides when the cache moves). */
async function fetchFreshMe(): Promise<Me> {
    return goFetch<Me>(ME_PATH);
}

/**
 * After a sign-in: the principal, then the bridge (forced: once per login, the legacy claims follow
 * the current role, R80), and only then the cache, so the bridge effect below finds it settled and
 * does not mint a second token.
 */
export async function completeSignIn(client: QueryClient): Promise<Me> {
    const me = await fetchFreshMe();
    await syncFirebaseBridge({ id: me.id, legacyAuthUid: me.legacyAuthUid }, { force: true });
    client.removeQueries({ predicate: (q) => q.queryKey[0] !== ME_KEY[0] });
    client.setQueryData<Me | null>(ME_KEY, me);
    void client.invalidateQueries({ queryKey: MY_TENANTS_KEY });
    return me;
}

/**
 * Where the user signed in: Go `users.last_login_*` (`PATCH /v1/me`), and while bridged the legacy
 * `users/{uid}` document the Security Center active-users list reads until P6. Best effort, in the
 * background: it may wait for the browser's location prompt and must not delay the navigation.
 */
function recordSignInLocation(): void {
    void (async () => {
        const geo = await resolveLoginGeoForClient().catch(() => null);
        await recordLoginGeo(geo);
        const bridge = getBridgeState();
        if (bridge.status !== "ready" || !bridge.firebaseUid) return;
        const me = await fetchFreshMe().catch(() => null);
        if (!me || me.id !== bridge.userId) return;
        const { updateUserLastLogin } = await import("@/lib/updateUserLastLogin");
        await updateUserLastLogin({ uid: bridge.firebaseUid, email: me.email, displayName: me.displayName }, geo);
    })().catch(() => undefined);
}

function authUserFrom(me: Me, bridge: BridgeState): AuthUser {
    const bridged = bridge.status === "ready" && bridge.userId === me.id && bridge.firebaseUid;
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

function claimsFrom(me: Me, bridge: BridgeState): Record<string, unknown> {
    if (bridge.status === "ready" && bridge.userId === me.id && bridge.claims) return bridge.claims;
    return legacyClaimsFromMe(me);
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

    // A session that ended in this tab (lib/sessionEnd.ts) or another reason for "signed out": drop
    // the principal, every cached query and the Firebase session (R50, §10.4 step 4).
    useEffect(
        () =>
            onSessionEnd(() => {
                applySignedOut(client);
                void signOutFirebaseBridge();
            }),
        [client]
    );

    // New claims (a `claims_changed` 401 or `session.revoked`, served by a forced refresh in any tab):
    // `['me']`, the bridge token and the active queries follow; the user stays signed in (R50, R78).
    const claimsWork = useRef<Promise<void> | null>(null);
    const applyNewClaims = useCallback((): Promise<void> => {
        if (claimsWork.current) return claimsWork.current;
        const work = (async () => {
            const next = await refetchMe(client);
            if (next) await syncFirebaseBridge({ id: next.id, legacyAuthUid: next.legacyAuthUid }, { force: true });
            await client.invalidateQueries({ predicate: (q) => !(q.queryKey.length === 1 && q.queryKey[0] === ME_KEY[0]) });
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
        await signOutSession();
        await signOutFirebaseBridge();
        applySignedOut(client);
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
            const next = await fetchFreshMe();
            await syncFirebaseBridge({ id: next.id, legacyAuthUid: next.legacyAuthUid }, { force: true });
            client.setQueryData<Me | null>(ME_KEY, next);
            await Promise.all([
                client.invalidateQueries({ queryKey: MY_TENANTS_KEY }),
                client.resetQueries({ predicate: (q) => !TENANT_NEUTRAL_KEYS.has(String(q.queryKey[0])) }),
            ]);
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

    const value = useMemo<AuthContextType>(
        () => ({
            currentUser: me ? authUserFrom(me, bridge) : null,
            me,
            customClaims: me ? claimsFrom(me, bridge) : null,
            loading,
            error,
            retry,
            login,
            loginWithGoogle,
            logout,
            refreshClaims,
            switchTenant,
        }),
        [me, bridge, loading, error, retry, login, loginWithGoogle, logout, refreshClaims, switchTenant]
    );

    return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
};

export const useAuth = () => useContext(AuthContext);
