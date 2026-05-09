"use client";

import { createContext, useContext, useEffect, useRef, useState } from "react";
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
  // Track when current session started so we can detect forceLogoutAt set after login
  const sessionStartTimeRef = useRef<number | null>(null);


  useEffect(() => {
    const unsubscribe = onAuthStateChanged(auth, async (user) => {
      setCurrentUser(user ? user : null);
      if (user) {
        // Record session start time (when this user authenticated in current tab)
        sessionStartTimeRef.current = Date.now();
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
        sessionStartTimeRef.current = null;
        setCustomClaims(null);
        setLoading(false);
      }
    });
    return () => unsubscribe();
  }, []);

  // Force logout listener: when admin updates user role, Cloud Function writes
  // forceLogoutAt timestamp on user doc. If it's after our session start, log out.
  useEffect(() => {
    if (!currentUser?.uid) return;
    const userDocRef = doc(db, "users", currentUser.uid);
    const unsubscribe = onSnapshot(userDocRef, async (snapshot) => {
      const data = snapshot.data();
      const forceLogoutAt = data?.forceLogoutAt as Timestamp | undefined;
      if (!forceLogoutAt || !sessionStartTimeRef.current) return;
      const forceLogoutMs = forceLogoutAt.toMillis();
      if (forceLogoutMs > sessionStartTimeRef.current) {
        console.log("[Auth] forceLogoutAt detected after session start - logging out");
        try {
          await signOut(auth);
        } catch (err) {
          console.error("[Auth] Error during forced logout:", err);
        }
      }
    }, (err) => {
      console.error("[Auth] forceLogout listener error:", err);
    });
    return () => unsubscribe();
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
