"use client";

import { GoogleAuthProvider, signInWithPopup } from "firebase/auth";
import { auth } from "@/firebase/client";
import { useState } from "react";
import { useRouter } from "next/navigation";
import { Button } from "@/components/ui/button";
import { useLanguage } from "@/context/language";

export default function ContinueWithGoogleButton() {
  const [loading, setLoading] = useState(false);
  const router = useRouter();
  const { t } = useLanguage();

  const handleGoogleSignIn = async () => {
    try {
      setLoading(true);
      const provider = new GoogleAuthProvider();

      // Use popup instead of redirect for better UX
      const result = await signInWithPopup(auth, provider);

      // First get initial token
      let tokenResult = await result.user.getIdTokenResult();

      // If this is a new user or claims aren't set yet, we need to wait for server to set them
      // Force refresh token to get updated claims from server
      await result.user.getIdToken(true);
      tokenResult = await result.user.getIdTokenResult();

      const claims = tokenResult.claims;
      console.log("User claims:", claims); // Debug log

      // Redirect based on role in custom claims
      if (claims?.admin === true) {
        console.log("Admin detected, redirecting to dashboard");
        router.replace("/admin/dashboard");
      } else {
        console.log("Not admin, redirecting to home");
        router.replace("/");
      }
    } catch (error: any) {
      console.error("Error signing in:", error);

      // Handle specific errors
      if (error.code === "auth/popup-closed-by-user") {
        alert("Sign in was cancelled. Please try again.");
      } else if (error.code === "auth/popup-blocked") {
        alert("Popup was blocked. Please allow popups for this site.");
      } else if (error.code === "auth/configuration-not-found") {
        alert(
          "Firebase Authentication is not configured properly.\n\n" +
          "Please check:\n" +
          "1. Firebase Authentication is enabled in Firebase Console\n" +
          "2. Google Sign-In provider is enabled\n" +
          "3. API key and project ID are correct in .env.local\n" +
          "4. Restart Next.js dev server after updating .env.local"
        );
      } else {
        alert(`An error occurred: ${error.message || "Please try again."}`);
      }
    } finally {
      setLoading(false);
    }
  };

  return (
    <Button
      type="button"
      onClick={handleGoogleSignIn}
      disabled={loading}
      className="w-full flex items-center justify-center gap-3"
      variant="outline"
    >
      <svg className="w-5 h-5" viewBox="0 0 24 24">
        <path
          fill="#4285F4"
          d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92c-.26 1.37-1.04 2.53-2.21 3.31v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.09z"
        />
        <path
          fill="#34A853"
          d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z"
        />
        <path
          fill="#FBBC05"
          d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l2.85-2.22.81-.62z"
        />
        <path
          fill="#EA4335"
          d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z"
        />
      </svg>
      {loading ? t("auth.signingIn") : t("auth.continueWithGoogle")}
    </Button>
  );
}


