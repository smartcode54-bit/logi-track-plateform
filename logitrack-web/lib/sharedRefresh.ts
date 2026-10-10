/**
 * One access-token refresh per browser at a time (developer-spec.md §10.4 steps 1-3, Appendix E
 * §E.8.3 "Shared refresh", Appendix C §C.4.4; R37, R78). Callers: `goFetch` after a refreshable
 * 401, and from TW5 the realtime provider after a stream error, `event: reconnect` or
 * `session.revoked` `claims_changed`.
 *
 * 1. Every caller enters `navigator.locks.request("lt-refresh")`, which serialises all tabs of the
 *    origin (an in-tab queue stands in where the Web Locks API is missing).
 * 2. Inside the lock it reads the completion markers in localStorage (timestamps, no token
 *    material): `lt:lastRefreshAt` for any refresh and `lt:lastForcedRefreshAt` for a forced one. If
 *    a refresh completed after the caller's trigger (the failed request's start), the cookie was
 *    already rotated by another tab or caller, so it returns without calling the BFF. A
 *    `claims_changed` caller needs fresh claims, so only a forced refresh counts for it: an unforced
 *    refresh may have been the BFF's no-op while `lt_at` still had more than 120 s left.
 * 3. Otherwise it calls `POST /api/auth/refresh` (body `{"force":true}` only when forced) and
 *    records the completion time.
 *
 * The race the lock cannot see (a `GET /api/auth/refresh?next=` navigation in another tab) is
 * absorbed by Go's 30 s refresh-token reuse grace. Tokens never reach this code: the BFF rotates
 * the HttpOnly cookies.
 */
import { ApiError, apiErrorFromResponse, networkError } from "./apiError";

export const REFRESH_LOCK = "lt-refresh";
export const AUTH_REFRESH_PATH = "/api/auth/refresh";
export const LAST_REFRESH_KEY = "lt:lastRefreshAt";
export const LAST_FORCED_REFRESH_KEY = "lt:lastForcedRefreshAt";

// In-tab copies, for when localStorage is unavailable (private mode, blocked storage).
let lastRefreshAt = 0;
let lastForcedRefreshAt = 0;
let tabQueue: Promise<unknown> = Promise.resolve();

function readMarker(key: string): number {
    try {
        const value = Number(window.localStorage.getItem(key));
        return Number.isFinite(value) ? value : 0;
    } catch {
        return 0;
    }
}

function writeMarker(key: string, at: number): void {
    try {
        window.localStorage.setItem(key, String(at));
    } catch {
        // The in-tab copy still dedupes this tab's callers.
    }
}

/** Whether a refresh that serves this caller completed after `since` (ms since the epoch). */
export function refreshedSince(since: number, force: boolean): boolean {
    const forced = Math.max(lastForcedRefreshAt, readMarker(LAST_FORCED_REFRESH_KEY));
    if (force) return forced > since;
    return Math.max(lastRefreshAt, readMarker(LAST_REFRESH_KEY), forced) > since;
}

function withRefreshLock<T>(fn: () => Promise<T>): Promise<T> {
    const locks = typeof navigator !== "undefined" ? navigator.locks : undefined;
    if (locks && typeof locks.request === "function") {
        return locks.request(REFRESH_LOCK, { mode: "exclusive" }, fn) as Promise<T>;
    }
    const run = tabQueue.then(fn, fn);
    tabQueue = run.catch(() => undefined);
    return run;
}

export interface SharedRefreshOptions {
    /** Send `{"force":true}`: the 401 was `token_expired` with `details.reason = "claims_changed"`. */
    force?: boolean;
    /** The caller's trigger time (`Date.now()` when the failed request started, or the event arrived). */
    since: number;
}

/**
 * Resolves `true` when the session has a fresh access cookie (refreshed here or by another tab or
 * caller after `since`), `false` when the BFF refused the refresh (401: the refresh token is
 * expired, revoked or reused, and the BFF has cleared both cookies). A network failure or another
 * status rejects with an ApiError and ends nothing: the session may still be valid. The refresh
 * request itself is never aborted, since a rotation whose response is lost would leave the browser
 * with a superseded refresh token.
 */
export function sharedRefresh({ force = false, since }: SharedRefreshOptions): Promise<boolean> {
    return withRefreshLock(async () => {
        if (refreshedSince(since, force)) return true;
        let res: Response;
        try {
            res = await fetch(AUTH_REFRESH_PATH, {
                method: "POST",
                credentials: "same-origin",
                cache: "no-store",
                ...(force ? { headers: { "Content-Type": "application/json" }, body: JSON.stringify({ force: true }) } : {}),
            });
        } catch (cause) {
            throw networkError(cause);
        }
        if (res.ok) {
            const at = Date.now();
            lastRefreshAt = at;
            writeMarker(LAST_REFRESH_KEY, at);
            if (force) {
                lastForcedRefreshAt = at;
                writeMarker(LAST_FORCED_REFRESH_KEY, at);
            }
            return true;
        }
        if (res.status === 401) return false;
        const error: ApiError = await apiErrorFromResponse(res);
        throw error;
    });
}
