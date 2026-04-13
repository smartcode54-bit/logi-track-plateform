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
exports.logSecurityEvent = void 0;
exports.appendSecurityEvent = appendSecurityEvent;
const admin = __importStar(require("firebase-admin"));
const https_1 = require("firebase-functions/v2/https");
const COLLECTION = "security_events";
const CALLABLE_ALLOWED_TYPES = new Set(["role_matrix_saved"]);
/** Append one event (Admin SDK). Used by callables and other server handlers. */
async function appendSecurityEvent(input) {
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
exports.logSecurityEvent = (0, https_1.onCall)(async (request) => {
    if (!request.auth) {
        throw new https_1.HttpsError("unauthenticated", "User must be authenticated");
    }
    if (request.auth.token.admin !== true) {
        throw new https_1.HttpsError("permission-denied", "Only admins can log security events");
    }
    const data = request.data;
    const type = typeof data.type === "string" ? data.type.trim() : "";
    if (!type || !CALLABLE_ALLOWED_TYPES.has(type)) {
        throw new https_1.HttpsError("invalid-argument", "Invalid or unsupported event type");
    }
    const summary = typeof data.summary === "string" && data.summary.trim() !== ""
        ? data.summary.trim()
        : type;
    let details = {};
    if (data.details != null && typeof data.details === "object" && !Array.isArray(data.details)) {
        details = data.details;
    }
    const severity = data.severity === "warning" || data.severity === "critical" || data.severity === "info"
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
//# sourceMappingURL=securityEvents.js.map