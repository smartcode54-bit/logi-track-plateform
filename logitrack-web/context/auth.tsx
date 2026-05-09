"use client";

import { createContext, useContext, useEffect, useState } from "react";
import { User, signOut, signInWithEmailAndPassword } from "firebase/auth";
import { auth, db } from "@/firebase/client";
import { resolveLoginGeoForClient, updateUserLastLogin } from "@/lib/updateUserLastLogin";
import { onAuthStateChanged } from "firebase/auth";
import { getIdTokenResult, getIdToken } from "firebase/auth";
import { getFunctions, httpsCallable, connectFunctionsEmulator } from "firebase/functions";
import { doc, onSnapshot, Timestamp } from "firebase/firestore";

type ParsedTokenResult = {
  [key: string]: any;
};

type AuthContextType = {
  currentUser: User | null;
  logout: () => Promise<void>;
  login: (email: string, pass: string) => Promise<void>;
  customClaims: ParsedTokenResult | null;
  loading: boolean;
  refreshClaims: () => Promise<void>;
};

const AuthContext = createContext<AuthContextType | null>(null);

// Initialize Firebase Functions
const functions = getFunctions(undefined, "asia-southeast1");

// Connect to emulator in development
if (process.env.NODE_ENV === "development" && typeof window !== "undefined") {
  // Uncomment the following line to use Functions emulator in development
  // connectFunctionsEmulator(functions, "localhost", 5001);
}

export const AuthProvider = ({ children }: { children: React.ReactNode }) => {
  const [currentUser, setCurrentUser] = useState<User | null>(null);
  const [customClaims, setCustomClaims] = useState<ParsedTokenResult | null>(null);
  const [loading, setLoading] = useState(true);


  useEffect(() => {
    const unsubscribe = onAuthStateChanged(auth, async (user) => {
      setCurrentUser(user ? user : null);
      if (user) {
        try {
          const tokenResult = await getIdTokenResult(user);
          setCustomClaims(tokenResult.claims ?? null);
          setLoading(false); // ให้ redirect ทำงานได้ทันที (ไม่รอ setAdminClaims)
          // อัปเดต claims ในพื้นหลังถ้า setAdminClaims ส่งคืน admin
          try {
            const setAdminClaimsFunction = httpsCallable(functions, "setAdminClaims");
            const result = await setAdminClaimsFunction();
            const resultData = result.data as { admin?: boolean };
            if (resultData.admin === true) {
              await getIdToken(user, true);
              const updatedTokenResult = await getIdTokenResult(user);
              setCustomClaims(updatedTokenResult.claims ?? null);
            }
          } catch (funcError) {
            console.error("[Auth] Error calling setAdminClaims:", funcError);
          }
        } catch (error) {
          console.error("[Auth] Error getting token:", error);
          setLoading(false);
        }
      } else {
        setCustomClaims(null);
        setLoading(false);
      }
    });
    return () => unsubscribe();
  }, []);

  // Force logout listener: log out only when forceLogoutAt CHANGES to a new value
  // after the listener has already seen an initial value. We don't compare against
  // wall-clock time (client clocks can drift relative to Firestore server time).
  //
  // NOTE: The listener may fail with "permission-denied" if the user's token
  // claims haven't propagated yet or the user doc doesn't exist. We retry
  // with exponential back-off up to a few times before giving up silently.
  useEffect(() => {
    if (!currentUser?.uid) return;
    let cancelled = false;
    let unsubscribe: (() => void) | null = null;
    let retryCount = 0;
    const MAX_RETRIES = 3;

    function subscribe() {
      if (cancelled) return;
      const userDocRef = doc(db, "users", currentUser!.uid);
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
          console.log("[Auth] forceLogoutAt changed after subscription - logging out");
          try {
            await signOut(auth);
          } catch (err) {
            console.error("[Auth] Error during forced logout:", err);
          }
        }
      }, (err: any) => {
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
  }, [currentUser?.uid]);

  const logout = async () => {
    try {
      await signOut(auth);
      // currentUser will be set to null automatically by onAuthStateChanged
    } catch (error) {
      console.error("Error signing out:", error);
      throw error; // Re-throw to let component handle it
    }
  };

  const login = async (email: string, pass: string) => {
    const cred = await signInWithEmailAndPassword(auth, email, pass);
    if (cred.user) {
      const geo = await resolveLoginGeoForClient();
      await updateUserLastLogin(cred.user, geo);
    }
  };

  const refreshClaims = async () => {
    if (!auth.currentUser) return;
    try {
      console.log("[Auth] Force refreshing ID token to get updated claims...");
      await getIdToken(auth.currentUser, true); // force refresh
      const updatedTokenResult = await getIdTokenResult(auth.currentUser);
      setCustomClaims(updatedTokenResult.claims ?? null);
      console.log("[Auth] Claims refreshed:", updatedTokenResult.claims);
    } catch (error) {
      console.error("[Auth] Error refreshing claims:", error);
    }
  };

  return (
    <AuthContext.Provider value={{ currentUser, logout, login, customClaims, loading, refreshClaims }}>
      {children}
    </AuthContext.Provider>
  );
}

export const useAuth = () => useContext(AuthContext);
