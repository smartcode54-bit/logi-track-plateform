"use client";

/**
 * The login page (T18; developer-spec.md §10.4, §10.5): where to go after signing in (`?next=`, a
 * same-origin `/app` path, else `/app`, whose edge gate picks the role's home, R89), why the user is
 * here (`?reason=revoked` after a revoked session), and the redirect once `['me']` holds a principal
 * (a sign-in on this page, or a visitor who is already signed in). A signed-out visitor's Firebase
 * session left from before the P0 switch is signed out here, so no Firestore session outlives the Go one.
 */
import { useEffect, useRef } from "react";
import { useRouter, useSearchParams } from "next/navigation";

import { useAuth } from "@/context/auth";
import { useLanguage } from "@/context/language";
import { signOutFirebaseBridge } from "@/lib/firebaseBridge";
import { safeNext } from "@/lib/safeNext";

export function useLogin() {
    const { t } = useLanguage();
    const auth = useAuth();
    const router = useRouter();
    const params = useSearchParams();
    const next = safeNext(params.get("next"));
    const notice = params.get("reason") === "revoked" ? t("auth.login.revoked") : "";
    const settled = Boolean(auth && !auth.loading);
    const signedIn = settled && Boolean(auth?.me);
    const signedOut = settled && auth?.me === null && !auth?.error;

    useEffect(() => {
        if (signedIn) router.replace(next);
    }, [signedIn, next, router]);

    const cleaned = useRef(false);
    useEffect(() => {
        if (!signedOut || cleaned.current) return;
        cleaned.current = true;
        void signOutFirebaseBridge();
    }, [signedOut]);

    return { t, next, notice, signedIn };
}
