import * as admin from "firebase-admin";
import { onCall, HttpsError } from "firebase-functions/v2/https";
import { appendSecurityEvent } from "./securityEvents";

/**
 * Revoke all refresh tokens for a Firebase Auth user (forces re-login on clients using refresh tokens).
 * Admin only. Cannot target self (use another admin or Firebase Console if you must revoke your own tokens).
 */
export const revokeUserRefreshTokens = onCall(async (request) => {
    if (!request.auth) {
        throw new HttpsError("unauthenticated", "User must be authenticated");
    }
    if (request.auth.token.admin !== true) {
        throw new HttpsError("permission-denied", "Only admins can revoke user sessions");
    }

    const { targetUid } = request.data as { targetUid?: string };
    if (!targetUid || typeof targetUid !== "string" || targetUid.trim() === "") {
        throw new HttpsError("invalid-argument", "targetUid is required");
    }

    const uid = targetUid.trim();
    if (uid === request.auth.uid) {
        throw new HttpsError(
            "failed-precondition",
            "Cannot revoke your own refresh tokens from this action; use another admin account or Firebase Console.",
        );
    }

    try {
        await admin.auth().revokeRefreshTokens(uid);
    } catch (e: unknown) {
        const msg = e instanceof Error ? e.message : String(e);
        console.error("[revokeUserRefreshTokens]", e);
        throw new HttpsError("internal", `Failed to revoke tokens: ${msg}`);
    }

    let targetEmail: string | null = null;
    try {
        const u = await admin.auth().getUser(uid);
        targetEmail = u.email ?? null;
    } catch {
        // user may have been deleted; still log revoke attempt
    }

    const actorEmail = request.auth.token.email;
    try {
        await appendSecurityEvent({
            type: "user_sessions_revoked",
            severity: "warning",
            summary: "User refresh tokens revoked (all devices)",
            details: { targetUid: uid, targetEmail },
            actorUid: request.auth.uid,
            actorEmail: typeof actorEmail === "string" ? actorEmail : null,
        });
    } catch (logErr) {
        console.error("[revokeUserRefreshTokens] security_events:", logErr);
    }

    return { ok: true };
});
