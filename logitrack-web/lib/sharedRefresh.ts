/**
 * One access-token refresh per browser at a time (developer-spec.md §10.4 steps 1-3, Appendix E
 * §E.8.3 "Shared refresh", Appendix C §C.4.4; R37, R78). Callers: `goFetch` after a refreshable
 * 401, and from TW5 the realtime provider after a stream error, `event: reconnect` or
 * `session.revoked` `claims_changed` (with `force: true` and `since` = the event's arrival).
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
 * 4. A forced refresh that succeeded (here, or in another tab and found through its marker) runs this
 *    tab's `onClaimsRefreshed` listeners once, after the lock is released and before the caller
 *    retries: TW4 invalidates `['me']` and the active queries, T18 mints the Firebase bridge token
 *    again (the legacy claims follow the new role), the realtime provider reopens the stream. Each
 *    tab has its own cache, bridge and stream, so each tab reacts; several `claims_changed` failures
 *    served by one forced refresh notify once.
 *
 * The race the lock cannot see (a `GET /api/auth/refresh?next=` navigation in another tab) is
 * merged by the BFF, which makes one Go rotation per refresh token in each web process
 * (lib/bff/authRoutes.ts); Go's 30 s refresh-token reuse grace covers what still races (a retry,
 * requests on different web replicas). Tokens never reach this code: the BFF rotates the HttpOnly
 * cookies.
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

export type ClaimsRefreshedListener = () => void;

const claimsListeners = new Set<ClaimsRefreshedListener>();
// The forced-refresh completion time this tab last notified its listeners for.
let notifiedForcedAt = 0;

/**
 * Registers a listener run once in this tab after each forced refresh that served one of its
 * callers (a `claims_changed` 401 in `goFetch`, or the realtime provider's `session.revoked`
 * `claims_changed`); returns the unsubscribe function. Listeners run synchronously; start async work
 * from them and do not await it.
 */
export function onClaimsRefreshed(listener: ClaimsRefreshedListener): () => void {
    claimsListeners.add(listener);
    return () => {
        claimsListeners.delete(listener);
    };
}

function notifyClaimsRefreshed(forcedAt: number): void {
    if (forcedAt <= notifiedForcedAt) return;
    notifiedForcedAt = forcedAt;
    for (const listener of [...claimsListeners]) {
        try {
            listener();
        } catch {
            // One owner's failure must not keep the others on stale claims.
        }
    }
}

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

function forcedRefreshAt(): number {
    return Math.max(lastForcedRefreshAt, readMarker(LAST_FORCED_REFRESH_KEY));
}

/** Whether a refresh that serves this caller completed after `since` (ms since the epoch). */
export function refreshedSince(since: number, force: boolean): boolean {
    const forced = forcedRefreshAt();
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
export async function sharedRefresh({ force = false, since }: SharedRefreshOptions): Promise<boolean> {
    // The forced refresh's completion time when one served this caller, 0 when none did, false when
    // the BFF refused the refresh.
    const result = await withRefreshLock(async (): Promise<number | false> => {
        if (refreshedSince(since, force)) return force ? forcedRefreshAt() : 0;
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
                return at;
            }
            return 0;
        }
        if (res.status === 401) return false;
        const error: ApiError = await apiErrorFromResponse(res);
        throw error;
    });
    if (result === false) return false;
    if (force) notifyClaimsRefreshed(result);
    return true;
}
