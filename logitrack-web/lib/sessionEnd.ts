/**
 * Ends the web session in this tab when the refresh cannot save it (developer-spec.md §10.4 step 4,
 * Appendix E §E.8.3; R37, R50): a 401 `session_revoked` or `invalid_token`, a failed refresh, or a
 * 401 after the single refresh-and-retry. Every owner of session state registers a listener (TW4:
 * clear the TanStack cache; TW5: close the event stream; T18: sign out of the Firebase bridge);
 * then the BFF is asked to expire both cookies and the tab goes to `/login?next=<current path>`.
 *
 * No token is handled here: both cookies are HttpOnly and only the BFF reads or sets them (R36).
 */
import type { ApiError } from "./apiError";

export const LOGIN_PATH = "/login";
export const AUTH_LOGOUT_PATH = "/api/auth/logout";

export type SessionEndListener = (cause: ApiError) => void;

const listeners = new Set<SessionEndListener>();
let ended = false;
let navigate: (url: string) => void = (url) => window.location.assign(url);

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

/**
 * Ends the session once per page load, however many requests fail together: runs the listeners,
 * asks the BFF to revoke the session and expire both cookies (`keepalive`, so it completes after the
 * navigation; a failure is ignored because the session is already unusable), then navigates.
 */
export function endSession(cause: ApiError): void {
    if (ended || typeof window === "undefined") return;
    ended = true;
    for (const listener of [...listeners]) {
        try {
            listener(cause);
        } catch {
            // A failing listener must not keep the user on a page whose session is gone.
        }
    }
    try {
        void fetch(AUTH_LOGOUT_PATH, { method: "POST", credentials: "same-origin", keepalive: true }).catch(() => undefined);
    } catch {
        // fetch itself unavailable: navigate anyway.
    }
    navigate(loginUrl(cause));
}
