"use client";

/**
 * The one sign-out (UI3, issue #143): the app shell's user menu and the public navigation both end the Go session
 * (`useAuth().logout`: the BFF revokes it and expires both cookies, the cache is emptied) and then load the sign-in
 * page. Before UI3 the header went to `/login`, the sidebar to `/` and the public navigation pushed `/`.
 */
import { useCallback } from "react";

import { useAuth } from "@/context/auth";
import { LOGIN_PATH } from "@/lib/sessionEnd";

/** Where every sign-out lands. */
export const SIGNED_OUT_PATH = LOGIN_PATH;

// A full page load, so nothing of the signed-out principal survives in memory.
let navigate: (url: string) => void = (url) => window.location.assign(url);

/** Tests: replaces the full page load. */
export function configureSignOutNavigation(next: (url: string) => void): void {
    navigate = next;
}

export function useSignOut(): () => Promise<void> {
    const auth = useAuth();
    const logout = auth?.logout;
    return useCallback(async () => {
        // `logout` never rejects: the BFF clears the cookies whatever Go answers (lib/authClient.ts).
        await logout?.();
        navigate(SIGNED_OUT_PATH);
    }, [logout]);
}
