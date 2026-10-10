/**
 * SSE `session.revoked` on `user:{uid}` (developer-spec.md §4.3, §8.6, §10.4 steps 3-4, §10.8;
 * Appendix C §C.4.7; Appendix E §E.5 row 20; R50). It replaces the `users/{uid}.forceLogoutAt`
 * listener (`context/auth.tsx:91-129` before T18). Payload `{userId, sessionIds, reason}`.
 *
 * - `reason = claims_changed` (role, membership, scope, driver link, platform role): the user stays
 *   signed in. A forced shared refresh picks up the new claims, and its `onClaimsRefreshed`
 *   listeners refetch `['me']` and the active queries, mint the bridge token again and reopen the
 *   stream (Go ends the stream to recompute its topics). A refused refresh ends the session.
 * - any other reason (`disabled`, `password_changed`, `password_reset`, `admin_revoke`,
 *   `refresh_reuse`, `logout`, `logout_all`, `device_relogin`): the event names sessions of this
 *   user, and the stream carries the revocations of every session of the user, not only this tab's
 *   (a sign-out on the phone, or a password change that keeps the session it was made from). The
 *   tab therefore asks Go about its own session with `GET /v1/me`: Go answers `401
 *   session_revoked` once the revoked marker is set, which it is before the event is published
 *   (Appendix C §C.4.7), and `goFetch` then ends the session (listeners, `POST /api/auth/logout`,
 *   `/login?next=...&reason=revoked`). A session still alive just refreshes `['me']`.
 */
import { ApiError } from "../apiError";

export interface SessionRevokedPayload {
    userId?: string;
    sessionIds?: string[];
    reason?: string;
}

export const REASON_CLAIMS_CHANGED = "claims_changed";

export interface SessionRevokedDeps {
    /** `sharedRefresh({force: true, since})`. */
    forcedRefresh: (since: number) => Promise<boolean>;
    /** `GET /v1/me` through goFetch (its 401 handling ends a revoked session). */
    checkSession: () => Promise<unknown>;
    /** Ends the session in this tab (lib/sessionEnd.ts). */
    endSession: (cause: ApiError) => void;
}

function payloadOf(data: unknown): SessionRevokedPayload {
    return typeof data === "object" && data !== null ? (data as SessionRevokedPayload) : {};
}

/** Handles one `session.revoked` frame; resolves when its follow-up has run. Never rejects. */
export async function handleSessionRevoked(data: unknown, receivedAt: number, deps: SessionRevokedDeps): Promise<void> {
    const { reason } = payloadOf(data);
    if (reason === REASON_CLAIMS_CHANGED) {
        try {
            const ok = await deps.forcedRefresh(receivedAt);
            if (!ok) {
                deps.endSession(new ApiError({ status: 401, code: "session_revoked", message: "refresh refused after claims_changed" }));
            }
        } catch {
            // The BFF is unreachable: the next request's 401 claims_changed retries the forced refresh.
        }
        return;
    }
    try {
        await deps.checkSession();
    } catch {
        // A 401 has already ended the session through goFetch; anything else leaves it as it is.
    }
}
