"use client";

/**
 * `useAuth()`: an adapter over `['me']` that keeps the shape its 41 consumers know (developer-spec.md
 * §10.6, Appendix E §E.3.3-§E.3.4; TW4).
 *
 * - Who is signed in, the role and the capabilities come from `['me']` (`GET /v1/me`, Go session
 *   in the HttpOnly cookies, R36): `customClaims` is synthesised from it for `getRole()`, `isAdmin()`
 *   and `can()` until TW7 (`claimsFromMe`), so no page reads Firebase claims or `permissions_config`
 *   to decide what a user may do.
 * - `currentUser` stays the Firebase user of the bridge session (R80) until TW7: Firestore-backed
 *   pages still write with its uid. Its token contributes only the legacy Firestore ids
 *   (`customerScopeId`, `partnerScopeId`, `driverId`) that those pages filter by.
 * - The context value is memoised and every function in it is stable, so a render of the provider
 *   no longer re-renders and re-runs the effects of every consumer (ends `context/auth.tsx:171`).
 *
 * T18 owns the login and logout UI flows, the bridge sign-in (`POST /api/auth/firebase-token`) and the
 * removal of `setAdminClaims` and the `forceLogoutAt` listener, which stay below until then. Until
 * T18/TW5 replace them with SSE `session.revoked`, the two Firebase signals end the whole session, as
 * a revoked Go session does (lib/sessionEnd.ts): a changed `forceLogoutAt` (an admin disabled the user
 * or changed their role through the legacy callables) and a Firebase sign-out reported from another
 * tab. The Go session is revoked, the cache emptied, `['me']` set to null and an `/app` page leaves
 * for `/login`; signing out of Firebase alone would leave `/app` and `/login` both admitting the user.
 */
import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { getIdToken, getIdTokenResult, onAuthStateChanged, signInWithEmailAndPassword, signOut, type User } from "firebase/auth";
import { httpsCallable, getFunctions } from "firebase/functions";
import { doc, onSnapshot, type Timestamp } from "firebase/firestore";
import { auth, db } from "@/firebase/client";
import { resolveLoginGeoForClient, updateUserLastLogin } from "@/lib/updateUserLastLogin";
import { sharedRefresh } from "@/lib/sharedRefresh";
import { ApiError } from "@/lib/apiError";
import { AUTH_LOGOUT_PATH, endSession } from "@/lib/sessionEnd";
import { queryKeys } from "@/lib/queryKeys";
import { clearCacheExceptMe, resetSessionCache } from "@/lib/queryClient";
import { claimsFromMe, meQueryOptions, type MeDTO, type SynthesizedClaims } from "@/features/auth/api/me";

type AuthContextType = {
  /** The Firebase user of the bridge session (Firestore pages) until TW7; null without one. */
  currentUser: User | null;
  /** `['me']`: the Go principal, null when signed out. Prefer `useMe(select)` in new code. */
  me: MeDTO | null;
  logout: () => Promise<void>;
  login: (email: string, pass: string) => Promise<void>;
  /** Legacy claims synthesised from `['me']` (plus the bridge token's legacy ids) until TW7. */
  customClaims: SynthesizedClaims | null;
  /** True until both `['me']` and the Firebase session state are first known. */
  loading: boolean;
  /** Forced refresh (new access token with fresh claims, R78), then `['me']` and the bridge token again. */
  refreshClaims: () => Promise<void>;
};

const AuthContext = createContext<AuthContextType | null>(null);

// Initialize Firebase Functions
const functions = getFunctions(undefined, "asia-southeast1");

type FirebaseSession = {
  /** The first `onAuthStateChanged` callback has run. */
  ready: boolean;
  user: User | null;
  claims: Record<string, unknown> | null;
};

export const AuthProvider = ({ children }: { children: React.ReactNode }) => {
  const client = useQueryClient();
  const meQuery = useQuery(meQueryOptions);
  const [firebase, setFirebase] = useState<FirebaseSession>({ ready: false, user: null, claims: null });

  // The Firebase (bridge) session: its user and the legacy ids in its token claims.
  useEffect(() => {
    let previous: User | null = null;
    const unsubscribe = onAuthStateChanged(auth, async (user) => {
      const hadUser = previous !== null;
      previous = user;
      if (!user) {
        setFirebase({ ready: true, user: null, claims: null });
        // The Firebase user went away while Go still knows the principal: a sign-out in another tab
        // (Firebase reports it to every tab) or Firebase dropping a disabled user. Until T18 both are
        // one sign-in, so the Go session and the cache end too. `logout()` and a forced logout empty
        // `['me']` before signing out, so they do not get here; the first "no user" of a principal
        // without a bridge session (R80) is no transition and is left alone.
        if (hadUser && client.getQueryData(queryKeys.me())) {
          endSession(new ApiError({ status: 401, code: "unauthenticated", message: "firebase signed out" }));
        }
        return;
      }
      let claims: Record<string, unknown> | null = null;
      try {
        claims = (await getIdTokenResult(user)).claims ?? null;
      } catch (error) {
        console.error("[Auth] Error getting token:", error);
      }
      setFirebase({ ready: true, user, claims });
      // Until T18's bridge mints the legacy claims: keep Firestore rules' admin claim current.
      try {
        const setAdminClaimsFunction = httpsCallable(functions, "setAdminClaims");
        const result = await setAdminClaimsFunction();
        const resultData = result.data as { admin?: boolean };
        if (resultData.admin === true) {
          await getIdToken(user, true);
          const updated = (await getIdTokenResult(user)).claims ?? null;
          setFirebase((prev) => (prev.user === user ? { ...prev, claims: updated } : prev));
        }
      } catch (funcError) {
        console.error("[Auth] Error calling setAdminClaims:", funcError);
      }
    });
    return () => unsubscribe();
  }, [client]);

  // Force logout listener (replaced by SSE `session.revoked` in T18/TW5): ends the whole session (Go
  // session, cache, `/app` page, then Firebase) only when forceLogoutAt CHANGES after the listener has
  // seen an initial value. We don't compare against wall-clock time (client clocks can drift relative
  // to Firestore server time).
  //
  // NOTE: The listener may fail with "permission-denied" if the user's token claims haven't
  // propagated yet or the user doc doesn't exist. We retry with exponential back-off up to a few
  // times before giving up silently.
  const firebaseUid = firebase.user?.uid;
  useEffect(() => {
    if (!firebaseUid) return;
    let cancelled = false;
    let unsubscribe: (() => void) | null = null;
    let retryCount = 0;
    const MAX_RETRIES = 3;

    function subscribe() {
      if (cancelled) return;
      const userDocRef = doc(db, "users", firebaseUid!);
      let initialForceLogoutMs: number | null = null;
      let initialized = false;

      unsubscribe = onSnapshot(userDocRef, async (snapshot) => {
        retryCount = 0; // reset on success
        const data = snapshot.data();
        const forceLogoutAt = data?.forceLogoutAt as Timestamp | undefined;
        const currentMs = forceLogoutAt ? forceLogoutAt.toMillis() : null;
        if (!initialized) {
          // First snapshot after subscribing: record baseline, never log out from it.
          initialForceLogoutMs = currentMs;
          initialized = true;
          return;
        }
        if (currentMs !== null && currentMs !== initialForceLogoutMs) {
          console.log("[Auth] forceLogoutAt changed after subscription - ending the session");
          // The legacy callables that write forceLogoutAt (functions/src/users.ts) reach neither Go nor
          // this tab's cache: end it as a revoked session (`onSessionEnd` listeners empty the cache and
          // `['me']`, the BFF revokes the Go session and expires both cookies, `/app` goes to
          // `/login?reason=revoked`), then sign out of Firebase.
          endSession(new ApiError({ status: 401, code: "session_revoked", message: "forceLogoutAt" }));
          try {
            await signOut(auth);
          } catch (err) {
            console.error("[Auth] Error during forced logout:", err);
          }
        }
      }, (err: { code?: string }) => {
        // Gracefully handle permission-denied (claims not ready, doc missing, etc.)
        const code = err?.code || "";
        if (code === "permission-denied" || code === "PERMISSION_DENIED") {
          if (retryCount < MAX_RETRIES) {
            retryCount++;
            const delayMs = Math.min(2000 * Math.pow(2, retryCount - 1), 10000);
            console.debug(`[Auth] forceLogout listener permission-denied, retrying in ${delayMs}ms (${retryCount}/${MAX_RETRIES})`);
            setTimeout(subscribe, delayMs);
          } else {
            console.debug("[Auth] forceLogout listener: giving up after max retries (permission-denied)");
          }
        } else {
          console.error("[Auth] forceLogout listener error:", err);
        }
      });
    }

    subscribe();
    return () => {
      cancelled = true;
      unsubscribe?.();
    };
  }, [firebaseUid]);

  const logout = useCallback(async () => {
    // The Go session first: the BFF revokes it and expires both cookies whatever Go answers.
    try {
      await fetch(AUTH_LOGOUT_PATH, { method: "POST", credentials: "same-origin" });
    } catch (error) {
      console.error("[Auth] Error ending the session:", error);
    }
    // No data of this user survives in the tab; `['me']` reads as signed out without a request.
    resetSessionCache(client);
    try {
      await signOut(auth);
    } catch (error) {
      console.error("Error signing out:", error);
      throw error; // Re-throw to let component handle it
    }
  }, [client]);

  const login = useCallback(
    async (email: string, pass: string) => {
      const cred = await signInWithEmailAndPassword(auth, email, pass);
      if (cred.user) {
        const geo = await resolveLoginGeoForClient();
        await updateUserLastLogin(cred.user, geo);
      }
      // A sign-in changes who `['me']` is (the BFF login of T18 sets the cookies before this runs). The
      // new principal starts from an empty cache, whatever this tab held before.
      clearCacheExceptMe(client);
      await client.invalidateQueries({ queryKey: queryKeys.me() });
    },
    [client]
  );

  const refreshClaims = useCallback(async () => {
    try {
      // A forced refresh runs the onClaimsRefreshed listeners, which invalidate ['me'] and active queries.
      if (!(await sharedRefresh({ force: true, since: Date.now() }))) {
        await client.invalidateQueries({ queryKey: queryKeys.me() });
      }
    } catch (error) {
      console.error("[Auth] Error refreshing the session:", error);
    }
    const user = auth.currentUser;
    if (!user) return;
    try {
      await getIdToken(user, true); // the bridge token's legacy ids
      const claims = (await getIdTokenResult(user)).claims ?? null;
      setFirebase((prev) => (prev.user === user ? { ...prev, claims } : prev));
    } catch (error) {
      console.error("[Auth] Error refreshing claims:", error);
    }
  }, [client]);

  const me = meQuery.data ?? null;
  const customClaims = useMemo(() => claimsFromMe(me, firebase.claims), [me, firebase.claims]);
  const loading = meQuery.isPending || !firebase.ready;

  const value = useMemo<AuthContextType>(
    () => ({ currentUser: firebase.user, me, logout, login, customClaims, loading, refreshClaims }),
    [firebase.user, me, logout, login, customClaims, loading, refreshClaims]
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
};

export const useAuth = () => useContext(AuthContext);
