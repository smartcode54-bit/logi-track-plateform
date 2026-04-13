"use strict";
var __createBinding = (this && this.__createBinding) || (Object.create ? (function(o, m, k, k2) {
    if (k2 === undefined) k2 = k;
    var desc = Object.getOwnPropertyDescriptor(m, k);
    if (!desc || ("get" in desc ? !m.__esModule : desc.writable || desc.configurable)) {
      desc = { enumerable: true, get: function() { return m[k]; } };
    }
    Object.defineProperty(o, k2, desc);
}) : (function(o, m, k, k2) {
    if (k2 === undefined) k2 = k;
    o[k2] = m[k];
}));
var __setModuleDefault = (this && this.__setModuleDefault) || (Object.create ? (function(o, v) {
    Object.defineProperty(o, "default", { enumerable: true, value: v });
}) : function(o, v) {
    o["default"] = v;
});
var __importStar = (this && this.__importStar) || (function () {
    var ownKeys = function(o) {
        ownKeys = Object.getOwnPropertyNames || function (o) {
            var ar = [];
            for (var k in o) if (Object.prototype.hasOwnProperty.call(o, k)) ar[ar.length] = k;
            return ar;
        };
        return ownKeys(o);
    };
    return function (mod) {
        if (mod && mod.__esModule) return mod;
        var result = {};
        if (mod != null) for (var k = ownKeys(mod), i = 0; i < k.length; i++) if (k[i] !== "default") __createBinding(result, mod, k[i]);
        __setModuleDefault(result, mod);
        return result;
    };
})();
Object.defineProperty(exports, "__esModule", { value: true });
exports.revokeUserRefreshTokens = void 0;
const admin = __importStar(require("firebase-admin"));
const https_1 = require("firebase-functions/v2/https");
const securityEvents_1 = require("./securityEvents");
/**
 * Revoke all refresh tokens for a Firebase Auth user (forces re-login on clients using refresh tokens).
 * Admin only. Cannot target self (use another admin or Firebase Console if you must revoke your own tokens).
 */
exports.revokeUserRefreshTokens = (0, https_1.onCall)(async (request) => {
    if (!request.auth) {
        throw new https_1.HttpsError("unauthenticated", "User must be authenticated");
    }
    if (request.auth.token.admin !== true) {
        throw new https_1.HttpsError("permission-denied", "Only admins can revoke user sessions");
    }
    const { targetUid } = request.data;
    if (!targetUid || typeof targetUid !== "string" || targetUid.trim() === "") {
        throw new https_1.HttpsError("invalid-argument", "targetUid is required");
    }
    const uid = targetUid.trim();
    if (uid === request.auth.uid) {
        throw new https_1.HttpsError("failed-precondition", "Cannot revoke your own refresh tokens from this action; use another admin account or Firebase Console.");
    }
    try {
        await admin.auth().revokeRefreshTokens(uid);
    }
    catch (e) {
        const msg = e instanceof Error ? e.message : String(e);
        console.error("[revokeUserRefreshTokens]", e);
        throw new https_1.HttpsError("internal", `Failed to revoke tokens: ${msg}`);
    }
    let targetEmail = null;
    try {
        const u = await admin.auth().getUser(uid);
        targetEmail = u.email ?? null;
    }
    catch {
        // user may have been deleted; still log revoke attempt
    }
    const actorEmail = request.auth.token.email;
    try {
        await (0, securityEvents_1.appendSecurityEvent)({
            type: "user_sessions_revoked",
            severity: "warning",
            summary: "User refresh tokens revoked (all devices)",
            details: { targetUid: uid, targetEmail },
            actorUid: request.auth.uid,
            actorEmail: typeof actorEmail === "string" ? actorEmail : null,
        });
    }
    catch (logErr) {
        console.error("[revokeUserRefreshTokens] security_events:", logErr);
    }
    return { ok: true };
});
//# sourceMappingURL=authSessions.js.map