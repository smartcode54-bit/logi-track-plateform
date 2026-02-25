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
exports.sendBroadcast = exports.notifyChatMessageCreated = void 0;
const admin = __importStar(require("firebase-admin"));
const https_1 = require("firebase-functions/v2/https");
const db = admin.firestore();
// Firestore is in asia-southeast3; Eventarc (Gen2 Firestore triggers) does not support that region.
// So we use a callable invoked by the client after creating a message, instead of onDocumentCreated.
/**
 * Get FCM tokens for a user from users/{uid}.fcmTokens.
 * Supports field as map (deviceId -> token) or array of tokens.
 */
function getFcmTokensForUser(uid) {
    return db
        .collection("users")
        .doc(uid)
        .get()
        .then((snap) => {
        const data = snap.data();
        const raw = data?.fcmTokens;
        if (!raw)
            return [];
        if (Array.isArray(raw))
            return raw.filter((t) => typeof t === "string");
        if (typeof raw === "object" && raw !== null)
            return Object.values(raw).filter((t) => typeof t === "string");
        return [];
    });
}
/**
 * Send FCM to all tokens of a user. Removes invalid tokens on failure.
 */
async function sendFcmToUser(uid, title, body, data) {
    const tokens = await getFcmTokensForUser(uid);
    if (tokens.length === 0) {
        console.log(`[chatFCM] No fcmTokens for user ${uid}, skip`);
        return;
    }
    const invalidTokens = [];
    for (const token of tokens) {
        try {
            await admin.messaging().send({
                token,
                notification: { title, body },
                data: { ...data, chatId: data.chatId || "" },
                android: { priority: "high", notification: { channelId: "chat" } },
                apns: { payload: { aps: { sound: "default" } }, fcmOptions: {} },
            });
        }
        catch (err) {
            const code = err?.code;
            if (code === "messaging/invalid-registration-token" || code === "messaging/registration-token-not-registered")
                invalidTokens.push(token);
            else
                console.error(`[chatFCM] Send failed for token:`, err);
        }
    }
    if (invalidTokens.length > 0) {
        try {
            const userRef = db.collection("users").doc(uid);
            const snap = await userRef.get();
            const current = snap.data()?.fcmTokens;
            if (typeof current === "object" && current !== null && !Array.isArray(current)) {
                const next = {};
                for (const [k, v] of Object.entries(current))
                    if (typeof v === "string" && !invalidTokens.includes(v))
                        next[k] = v;
                await userRef.update({ fcmTokens: next });
            }
        }
        catch (e) {
            console.error("[chatFCM] Failed to remove invalid tokens:", e);
        }
    }
}
/**
 * Callable: run FCM + auto-assign logic after a chat message is created.
 * Call this from the client after writing to chats/{chatId}/messages/{messageId}.
 * (Replaces onDocumentCreated because Firestore is in asia-southeast3, which is not supported for Gen2 triggers.)
 */
exports.notifyChatMessageCreated = (0, https_1.onCall)({ region: "asia-southeast1" }, async (request) => {
    const data = request.data;
    const { chatId, messageId } = data;
    if (!chatId || !messageId) {
        throw new https_1.HttpsError("invalid-argument", "chatId and messageId are required");
    }
    const msgSnap = await db.collection("chats").doc(chatId).collection("messages").doc(messageId).get();
    if (!msgSnap.exists) {
        throw new https_1.HttpsError("not-found", "Message not found");
    }
    const msg = msgSnap.data();
    const senderRole = msg.senderRole;
    const senderId = msg.senderId;
    const text = msg.text || "";
    const chatRef = db.collection("chats").doc(chatId);
    const chatSnap = await chatRef.get();
    if (!chatSnap.exists) {
        throw new https_1.HttpsError("not-found", "Chat not found");
    }
    const chat = chatSnap.data();
    const driverId = chat.driverId;
    if (!driverId)
        return { ok: true };
    // Auto-assign: first admin reply sets assignedAdminId and status in_progress
    if (senderRole === "admin" && senderId && (chat.assignedAdminId == null || chat.assignedAdminId === "")) {
        try {
            await chatRef.update({
                assignedAdminId: senderId,
                status: "in_progress",
                assignedAt: admin.firestore.FieldValue.serverTimestamp(),
                updatedAt: admin.firestore.FieldValue.serverTimestamp(),
            });
        }
        catch (e) {
            console.error("[notifyChatMessageCreated] Auto-assign update failed:", e);
        }
    }
    // Notify recipient: driver when admin sends
    if (senderRole === "admin") {
        const title = "New message from Admin";
        const body = text.slice(0, 80) + (text.length > 80 ? "…" : "");
        await sendFcmToUser(driverId, title, body, { type: "chat", chatId, messageId });
    }
    return { ok: true };
});
const BROADCASTS_COLL = "broadcasts";
/**
 * Callable: send a broadcast message to many drivers.
 * Writes only to the broadcasts collection (one doc per send). Does not write to chats.
 * Sends FCM to each driver so they can open the broadcast list.
 */
exports.sendBroadcast = (0, https_1.onCall)({
    region: "asia-southeast1",
    cors: true,
}, async (request) => {
    if (!request.auth?.token?.admin) {
        throw new https_1.HttpsError("permission-denied", "Only admins can send broadcasts");
    }
    const data = request.data;
    const recipientDriverIds = data.recipientDriverIds;
    const messageText = data.messageText?.trim();
    const recipientGroup = data.recipientGroup || "all_driver";
    if (!Array.isArray(recipientDriverIds) || recipientDriverIds.length === 0 || !messageText) {
        throw new https_1.HttpsError("invalid-argument", "recipientDriverIds (non-empty array) and messageText are required");
    }
    const adminUid = request.auth.uid;
    let createdByName = adminUid;
    try {
        const adminUser = await admin.auth().getUser(adminUid);
        createdByName = adminUser.displayName || adminUser.email || adminUid;
    }
    catch {
        // keep createdByName as uid
    }
    // 1. Write one document to broadcasts collection only (no chat writes)
    await db.collection(BROADCASTS_COLL).add({
        createdBy: adminUid,
        createdByName,
        messageText,
        recipientCount: recipientDriverIds.length,
        recipientGroup,
        sentAt: admin.firestore.FieldValue.serverTimestamp(),
    });
    // 2. Send FCM to each driver (opens broadcast list; no chatId)
    const title = "📢 New Announcement";
    const body = messageText.slice(0, 80) + (messageText.length > 80 ? "…" : "");
    for (const driverId of recipientDriverIds) {
        await sendFcmToUser(driverId, title, body, { type: "broadcast" });
    }
    return { ok: true, recipientCount: recipientDriverIds.length };
});
//# sourceMappingURL=chat.js.map