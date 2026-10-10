/**
 * Ends the web session in this tab when the refresh cannot save it (developer-spec.md §10.4 step 4,
 * Appendix E §E.8.3; R37, R50): a 401 `session_revoked` or `invalid_token`, a failed refresh, or a
 * 401 after the single refresh-and-retry. Every owner of session state registers a listener (TW4:
 * clear the TanStack cache; TW5: close the event stream; T18: sign out of the Firebase bridge);
 * then the BFF is asked to expire both cookies and, on a protected page (`/app`, the `proxy.ts`
 * matcher), the tab goes to `/login?next=<current path>`. A public page (`/`, `/login`, `/about`,
 * ...) is not left: the visitor stays there, signed out, so the login page can never reload itself.
 *
 * No token is handled here: both cookies are HttpOnly and only the BFF reads or sets them (R36).
 */
import type { ApiError } from "./apiError";

export const LOGIN_PATH = "/login";
export const AUTH_LOGOUT_PATH = "/api/auth/logout";
/** The protected route group: `proxy.ts` gates it, and only there does a session end leave the page. */
export const APP_PATH = "/app";

export type SessionEndListener = (cause: ApiError) => void;

const listeners = new Set<SessionEndListener>();
// A navigation to the login page is under way: this page is leaving, nothing more to do.
let leaving = false;
// A session end on a public page whose logout has not settled yet; concurrent failures share it.
let quietEnd: Promise<void> | undefined;
let navigate: (url: string) => void = (url) => window.location.assign(url);

/** Whether `pathname` is in the protected `/app` route group (the `proxy.ts` matcher, Appendix E §E.8.4). */
export function isProtectedPath(pathname: string): boolean {
    return pathname === APP_PATH || pathname.startsWith(`${APP_PATH}/`);
}

/** Whether this tab shows a protected page (false outside a browser). */
export function onProtectedPage(): boolean {
    return typeof window !== "undefined" && isProtectedPath(window.location.pathname);
}

/** Registers a listener run once when the session ends; returns the unsubscribe function. */
export function onSessionEnd(listener: SessionEndListener): () => void {
    listeners.add(listener);
    return () => {
        listeners.delete(listener);
    };
}

/** Replaces the navigation used to reach the login page (tests; the default is a full page load). */
export function configureSessionEnd(options: { navigate?: (url: string) => void }): void {
    if (options.navigate) navigate = options.navigate;
}

/**
 * Tests only: forgets a navigation under way and a pending quiet end. Module state outlives a test,
 * so without this every test after one that ended a session on a protected page would run with
 * `endSession` as a silent no-op, and a "never signs out" assertion could not fail.
 */
export function resetSessionEndForTests(): void {
    leaving = false;
    quietEnd = undefined;
}

/**
 * The login URL that brings the user back here: `/login?next=<path+query>`, plus `reason=revoked`
 * when the session was revoked (admin revoke, disable, password change, refresh reuse). `next` is
 * always a same-origin path; on the login page itself it is left out.
 */
export function loginUrl(cause?: Pick<ApiError, "code">): string {
    const params = new URLSearchParams();
    const { pathname, search } = window.location;
    if (pathname !== LOGIN_PATH && pathname.startsWith("/") && !pathname.startsWith("//")) {
        params.set("next", pathname + search);
    }
    if (cause?.code === "session_revoked") params.set("reason", "revoked");
    const qs = params.toString();
    return qs ? `${LOGIN_PATH}?${qs}` : LOGIN_PATH;
}

/** Best-effort `POST /api/auth/logout`; resolves when it settles and never rejects. */
function logout(): Promise<void> {
    try {
        // `keepalive` lets it complete after a navigation; a failure is ignored because the session
        // is already unusable.
        return fetch(AUTH_LOGOUT_PATH, { method: "POST", credentials: "same-origin", keepalive: true }).then(
            () => undefined,
            () => undefined
        );
    } catch {
        // fetch itself unavailable: carry on.
        return Promise.resolve();
    }
}

/**
 * Ends the session, however many requests fail together: runs the listeners, asks the BFF to revoke
 * the session and expire both cookies, then leaves a protected page for the login page (once per
 * page load). On a public page it does not navigate; failures that arrive before its logout settles
 * are absorbed, and later ones (cookies gone) reach Go as plain `unauthenticated`. Nothing latches
 * past that, so a sign-in on the login page followed by a client-side move to `/app` still ends
 * correctly later.
 */
export function endSession(cause: ApiError): void {
    if (leaving || typeof window === "undefined") return;
    const leave = isProtectedPath(window.location.pathname);
    if (!leave && quietEnd) return;
    if (leave) leaving = true;
    for (const listener of [...listeners]) {
        try {
            listener(cause);
        } catch {
            // A failing listener must not keep the user on a page whose session is gone.
        }
    }
    const done = logout();
    if (leave) {
        navigate(loginUrl(cause));
        return;
    }
    const pending = done.finally(() => {
        if (quietEnd === pending) quietEnd = undefined;
    });
    quietEnd = pending;
}
