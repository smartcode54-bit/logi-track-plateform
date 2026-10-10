"use client";

/**
 * The login page (T18 over TW4's `['me']`; developer-spec.md §10.4, §10.5): where to go after signing
 * in, why the user is here (`?reason=revoked` after a revoked session), and the redirect once `['me']`
 * holds a principal (a sign-in on this page, or a visitor who is already signed in). Signed in means a
 * Go session: only then can the `proxy.ts` gate let `/app` through, so a Firebase-only session never
 * bounces between this page and the gate.
 *
 * The destination is `?next=` when the URL carries one (a same-origin `/app` path, `safeNext`, else
 * `/app`, whose edge gate picks the role's home), else the principal's home route (`homeRouteOf`, the
 * `proxy.ts` rule, R89), so a plain visit to `/login` lands where `/app` would without a second hop. A
 * signed-out visitor's Firebase session left from before the P0 switch is signed out by `AuthProvider`
 * on every page (context/auth.tsx).
 */
import { useEffect } from "react";
import { useRouter, useSearchParams } from "next/navigation";

import { useAuth } from "@/context/auth";
import { useLanguage } from "@/context/language";
import { homeRouteOf } from "@/features/auth/api/homeRoute";
import type { MeDTO } from "@/features/auth/api/me";
import { safeNext } from "@/lib/safeNext";

/** Where a signed-in visitor of the login page goes: `?next=` when present (made safe), else the role's home. */
export function loginDestination(next: string | null, me: MeDTO): string {
    return next !== null ? safeNext(next) : homeRouteOf(me);
}

export function useLogin() {
    const { t } = useLanguage();
    const auth = useAuth();
    const router = useRouter();
    const params = useSearchParams();
    const next = params.get("next");
    const notice = params.get("reason") === "revoked" ? t("auth.login.revoked") : "";
    const me = auth && !auth.loading ? auth.me : null;
    const destination = me ? loginDestination(next, me) : null;

    useEffect(() => {
        if (destination) router.replace(destination);
    }, [destination, router]);

    return { t, notice, signedIn: destination !== null };
}
