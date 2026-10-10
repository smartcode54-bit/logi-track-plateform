"use client";

/**
 * Google Identity Services button (developer-spec.md §4.2, §10.4; Appendix C §C.4.10; R23). The browser
 * gets a single-use nonce from `GET /api/auth/google/nonce`, GIS puts it into the ID token, and the
 * token goes to `POST /api/auth/google {idToken, nonce}`; Go checks the audience
 * (`GOOGLE_OIDC_ALLOWED_CLIENT_IDS`), the nonce and the account. No authorization-code flow, no
 * Firebase popup. The client id is `NEXT_PUBLIC_GOOGLE_OIDC_CLIENT_ID`; without it the button is not
 * shown. A failed attempt fetches a new nonce (each is single use) and renders the button again.
 *
 * GIS keeps one configuration per page: each `google.accounts.id.initialize` replaces the callback and
 * the nonce of the previous one. The landing page mounts several buttons (the Hero's, and the one in
 * each login dialog while it is open), so `initialize` is called only by the coordinator below: the
 * configuration always belongs to the most recently mounted button that has a nonce, its one callback
 * hands the credential to that button's handler with that button's nonce (the nonce the ID token
 * carries), and when that button unmounts the next one takes the configuration back and fetches a
 * fresh nonce. A failure refreshes the nonce of the button whose handler ran, never of an unmounted one.
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

/** One mounted button, as the coordinator knows it. */
interface GisEntry {
    /** The nonce of this button's last configuration (`null` until fetched, or after it was sent). */
    nonce: string | null;
    handler: { current: (idToken: string, nonce: string) => Promise<void> };
    /** Fetches a new nonce and renders this button again. */
    refresh: () => void;
}

/** Mounted buttons in mount order; the last one with a nonce owns the page-wide configuration. */
const mounted: GisEntry[] = [];
/** The button whose nonce and handler the GIS configuration carries now. */
let configured: GisEntry | null = null;
let configuredGis: { gis: GoogleAccountsId; clientId: string } | null = null;

/** The page-wide GIS callback: the credential carries the configured button's nonce. */
function dispatch(response: { credential?: string }): void {
    const entry = configured;
    if (!entry || !response.credential || entry.nonce === null) return;
    const nonce = entry.nonce;
    entry.handler.current(response.credential, nonce).catch(() => {
        // The nonce is spent (or refused): this button renders again with a new one.
        entry.refresh();
    });
}

function configure(entry: GisEntry, gis: GoogleAccountsId, clientId: string): void {
    if (entry.nonce === null) return;
    gis.initialize({
        client_id: clientId,
        nonce: entry.nonce,
        ux_mode: "popup",
        auto_select: false,
        cancel_on_tap_outside: true,
        itp_support: true,
        callback: dispatch,
    });
    configured = entry;
    configuredGis = { gis, clientId };
}

/** The most recently mounted button that has a nonce. */
function owner(): GisEntry | undefined {
    for (let i = mounted.length - 1; i >= 0; i--) if (mounted[i].nonce !== null) return mounted[i];
    return undefined;
}

function mountedButton(entry: GisEntry): void {
    if (!mounted.includes(entry)) mounted.push(entry);
}

/** A button got a nonce: it takes the configuration when it is the newest ready button. */
function nonceArrived(entry: GisEntry, nonce: string, gis: GoogleAccountsId, clientId: string): void {
    if (!mounted.includes(entry)) return;
    entry.nonce = nonce;
    if (owner() === entry || configured === null) configure(entry, gis, clientId);
}

function unmounted(entry: GisEntry): void {
    const i = mounted.indexOf(entry);
    if (i !== -1) mounted.splice(i, 1);
    if (configured !== entry) return;
    configured = null;
    const next = owner();
    if (next && configuredGis) {
        // Back to the button still on screen at once, then with a fresh nonce of its own.
        configure(next, configuredGis.gis, configuredGis.clientId);
        next.refresh();
    }
}

/** Tests only: forgets every button (module state outlives a test). */
export function resetGoogleSignInForTests(): void {
    mounted.length = 0;
    configured = null;
    configuredGis = null;
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
    // This button for the coordinator: one object per mounted instance, kept across rounds.
    const [entry] = useState<GisEntry>(() => ({ nonce: null, handler, refresh: () => setRound((r) => r + 1) }));

    useEffect(() => {
        handler.current = onCredential;
    }, [onCredential]);

    // Mount order decides who owns the configuration; on unmount (not on a new round) the next
    // button takes it back.
    useEffect(() => {
        mountedButton(entry);
        return () => unmounted(entry);
    }, [entry]);

    useEffect(() => {
        if (!clientId) return;
        let cancelled = false;
        void (async () => {
            try {
                const [gis, { nonce }] = await Promise.all([loadGoogleIdentity(), googleNonce()]);
                const el = container.current;
                if (cancelled || !el) return;
                nonceArrived(entry, nonce, gis, clientId);
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
    }, [clientId, language, round, entry]);

    if (!clientId) return null;
    return (
        <div className="relative w-full">
            <div ref={container} className="flex w-full justify-center min-h-[44px]" data-testid="gis-button" />
            {disabled ? <div className="absolute inset-0 cursor-not-allowed bg-background/50" aria-hidden /> : null}
            {unavailable ? <p className="mt-2 text-center text-xs text-muted-foreground">{t("auth.google.unavailable")}</p> : null}
        </div>
    );
}
