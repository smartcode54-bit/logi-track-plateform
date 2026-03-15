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
