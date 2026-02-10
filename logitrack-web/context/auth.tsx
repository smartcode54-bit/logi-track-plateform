"use client";

import { createContext, useContext, useEffect, useState } from "react";
import { User, signOut, signInWithEmailAndPassword } from "firebase/auth";
import { auth } from "@/firebase/client";
import { onAuthStateChanged } from "firebase/auth";
import { getIdTokenResult, getIdToken } from "firebase/auth";
import { getFunctions, httpsCallable, connectFunctionsEmulator } from "firebase/functions";

type ParsedTokenResult = {
  [key: string]: any;
};

type AuthContextType = {
  currentUser: User | null;
  logout: () => Promise<void>;
  login: (email: string, pass: string) => Promise<void>;
  customClaims: ParsedTokenResult | null;
  loading: boolean;
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
          // Get initial token
          const tokenResult = await getIdTokenResult(user);
          console.log("[Auth] Initial token claims:", tokenResult.claims);
          setCustomClaims(tokenResult.claims ?? null);

          // Call Cloud Function to set admin claims if needed
          try {
            console.log("[Auth] Calling setAdminClaims Cloud Function...");
            const setAdminClaimsFunction = httpsCallable(functions, "setAdminClaims");
            const result = await setAdminClaimsFunction();
            console.log("[Auth] setAdminClaims result:", result.data);

            // If admin claim was set, force refresh token to get updated claims
            const resultData = result.data as { admin?: boolean };
            if (resultData.admin === true) {
              console.log("[Auth] Admin claim set, refreshing token...");
              await getIdToken(user, true);
              const updatedTokenResult = await getIdTokenResult(user);
              console.log("[Auth] Updated token claims:", updatedTokenResult.claims);
              setCustomClaims(updatedTokenResult.claims ?? null);
            }
          } catch (funcError) {
            console.error("[Auth] Error calling setAdminClaims:", funcError);
            // Continue without admin claims - user can still use the app
          }
        } catch (error) {
          console.error("[Auth] Error getting token:", error);
        }
      } else {
        setCustomClaims(null);
      }
      setLoading(false);
    });
    return () => unsubscribe();
  }, []);

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
    await signInWithEmailAndPassword(auth, email, pass);
  };

  return (
    <AuthContext.Provider value={{ currentUser, logout, login, customClaims, loading }}>
      {children}
    </AuthContext.Provider>
  );
}

export const useAuth = () => useContext(AuthContext);
