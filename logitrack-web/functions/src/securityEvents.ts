import * as admin from "firebase-admin";
import { onCall, HttpsError } from "firebase-functions/v2/https";

const COLLECTION = "security_events";

const CALLABLE_ALLOWED_TYPES = new Set(["role_matrix_saved"]);

export type AppendSecurityEventInput = {
    type: string;
    severity?: string;
    summary: string;
    details?: Record<string, unknown>;
    actorUid: string;
    actorEmail?: string | null;
};

/** Append one event (Admin SDK). Used by callables and other server handlers. */
export async function appendSecurityEvent(input: AppendSecurityEventInput): Promise<void> {
    await admin.firestore().collection(COLLECTION).add({
        createdAt: admin.firestore.FieldValue.serverTimestamp(),
        type: input.type,
        severity: input.severity ?? "info",
        summary: input.summary,
        details: input.details ?? {},
        actorUid: input.actorUid,
        actorEmail: input.actorEmail ?? null,
    });
}

/**
 * Client-callable: log a whitelisted security event (admin token only).
 * Writes via Admin SDK so Firestore rules can deny all client writes.
 */
export const logSecurityEvent = onCall(async (request) => {
    if (!request.auth) {
        throw new HttpsError("unauthenticated", "User must be authenticated");
    }
    if (request.auth.token.admin !== true) {
        throw new HttpsError("permission-denied", "Only admins can log security events");
    }

    const data = request.data as {
        type?: string;
        summary?: string;
        details?: Record<string, unknown>;
        severity?: string;
    };

    const type = typeof data.type === "string" ? data.type.trim() : "";
    if (!type || !CALLABLE_ALLOWED_TYPES.has(type)) {
        throw new HttpsError("invalid-argument", "Invalid or unsupported event type");
    }

    const summary =
        typeof data.summary === "string" && data.summary.trim() !== ""
            ? data.summary.trim()
            : type;

    let details: Record<string, unknown> = {};
    if (data.details != null && typeof data.details === "object" && !Array.isArray(data.details)) {
        details = data.details as Record<string, unknown>;
    }

    const severity =
        data.severity === "warning" || data.severity === "critical" || data.severity === "info"
            ? data.severity
            : "info";

    const email = request.auth.token.email;
    await appendSecurityEvent({
        type,
        severity,
        summary,
        details,
        actorUid: request.auth.uid,
        actorEmail: typeof email === "string" ? email : null,
    });

    return { ok: true };
});
