"use client";

/**
 * Google Identity Services button (developer-spec.md §4.2, §10.4; Appendix C §C.4.10; R23). The browser
 * gets a single-use nonce from `GET /api/auth/google/nonce`, GIS puts it into the ID token, and the
 * token goes to `POST /api/auth/google {idToken, nonce}`; Go checks the audience
 * (`GOOGLE_OIDC_ALLOWED_CLIENT_IDS`), the nonce and the account. No authorization-code flow, no
 * Firebase popup. The client id is `NEXT_PUBLIC_GOOGLE_OIDC_CLIENT_ID`; without it the button is not
 * shown. A failed attempt fetches a new nonce (each is single use) and renders the button again.
 */
import { useEffect, useRef, useState } from "react";

import { useLanguage } from "@/context/language";
import { googleNonce } from "@/lib/authClient";

export const GIS_SCRIPT_SRC = "https://accounts.google.com/gsi/client";

/** The parts of `google.accounts.id` used here. */
export interface GoogleAccountsId {
    initialize(config: {
        client_id: string;
        callback: (response: { credential?: string }) => void;
        nonce?: string;
        ux_mode?: "popup" | "redirect";
        auto_select?: boolean;
        cancel_on_tap_outside?: boolean;
        itp_support?: boolean;
    }): void;
    renderButton(parent: HTMLElement, options: Record<string, string | number>): void;
    disableAutoSelect?: () => void;
}

declare global {
    interface Window {
        google?: { accounts?: { id?: GoogleAccountsId } };
    }
}

let gisLoading: Promise<GoogleAccountsId> | null = null;

/** Loads the GIS script once per page; a failed load is forgotten so the next mount retries. */
export function loadGoogleIdentity(): Promise<GoogleAccountsId> {
    const ready = window.google?.accounts?.id;
    if (ready) return Promise.resolve(ready);
    if (!gisLoading) {
        gisLoading = new Promise<GoogleAccountsId>((resolve, reject) => {
            const script = document.createElement("script");
            script.src = GIS_SCRIPT_SRC;
            script.async = true;
            script.defer = true;
            script.onload = () => {
                const id = window.google?.accounts?.id;
                if (id) resolve(id);
                else reject(new Error("Google Identity Services did not initialise"));
            };
            script.onerror = () => reject(new Error("Google Identity Services could not be loaded"));
            document.head.appendChild(script);
        });
        gisLoading.catch(() => {
            gisLoading = null;
        });
    }
    return gisLoading;
}

/** The OAuth client id of the GIS button (public, inlined at build time). */
export function googleClientId(): string {
    return process.env.NEXT_PUBLIC_GOOGLE_OIDC_CLIENT_ID ?? "";
}

export interface GoogleSignInButtonProps {
    /** Signs in with the ID token and its nonce; rejects when the sign-in failed (a new nonce is fetched). */
    onCredential: (idToken: string, nonce: string) => Promise<void>;
    disabled?: boolean;
}

export function GoogleSignInButton({ onCredential, disabled = false }: GoogleSignInButtonProps) {
    const { t, language } = useLanguage();
    const clientId = googleClientId();
    const container = useRef<HTMLDivElement>(null);
    const handler = useRef(onCredential);
    const [round, setRound] = useState(0);
    const [unavailable, setUnavailable] = useState(false);

    useEffect(() => {
        handler.current = onCredential;
    }, [onCredential]);

    useEffect(() => {
        if (!clientId) return;
        let cancelled = false;
        void (async () => {
            try {
                const [gis, { nonce }] = await Promise.all([loadGoogleIdentity(), googleNonce()]);
                const el = container.current;
                if (cancelled || !el) return;
                gis.initialize({
                    client_id: clientId,
                    nonce,
                    ux_mode: "popup",
                    auto_select: false,
                    cancel_on_tap_outside: true,
                    itp_support: true,
                    callback: (response) => {
                        if (!response.credential) return;
                        handler.current(response.credential, nonce).catch(() => {
                            // The nonce is spent (or refused): render the button again with a new one.
                            if (!cancelled) setRound((r) => r + 1);
                        });
                    },
                });
                el.replaceChildren();
                gis.renderButton(el, {
                    type: "standard",
                    theme: "outline",
                    size: "large",
                    text: "continue_with",
                    shape: "rectangular",
                    logo_alignment: "left",
                    width: Math.max(200, Math.min(400, el.clientWidth || 320)),
                    locale: language,
                });
                setUnavailable(false);
            } catch (error) {
                console.warn("[auth] Google sign-in unavailable", error);
                if (!cancelled) setUnavailable(true);
            }
        })();
        return () => {
            cancelled = true;
        };
    }, [clientId, language, round]);

    if (!clientId) return null;
    return (
        <div className="relative w-full">
            <div ref={container} className="flex w-full justify-center min-h-[44px]" data-testid="gis-button" />
            {disabled ? <div className="absolute inset-0 cursor-not-allowed bg-background/50" aria-hidden /> : null}
            {unavailable ? <p className="mt-2 text-center text-xs text-muted-foreground">{t("auth.google.unavailable")}</p> : null}
        </div>
    );
}
