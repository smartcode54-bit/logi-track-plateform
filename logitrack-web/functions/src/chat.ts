import * as admin from "firebase-admin";
import { onCall, HttpsError } from "firebase-functions/v2/https";

const db = admin.firestore();

// Firestore is in asia-southeast3; Eventarc (Gen2 Firestore triggers) does not support that region.
// So we use a callable invoked by the client after creating a message, instead of onDocumentCreated.

/**
 * Get FCM tokens for a user from users/{uid}.fcmTokens.
 * Supports field as map (deviceId -> token) or array of tokens.
 */
function getFcmTokensForUser(uid: string): Promise<string[]> {
  return db
    .collection("users")
    .doc(uid)
    .get()
    .then((snap) => {
      const data = snap.data();
      const raw = data?.fcmTokens;
      if (!raw) return [];
      if (Array.isArray(raw)) return raw.filter((t): t is string => typeof t === "string");
      if (typeof raw === "object" && raw !== null)
        return Object.values(raw).filter((t): t is string => typeof t === "string");
      return [];
    });
}

/**
 * Send FCM to all tokens of a user. Removes invalid tokens on failure.
 */
async function sendFcmToUser(uid: string, title: string, body: string, data: Record<string, string>): Promise<void> {
  const tokens = await getFcmTokensForUser(uid);
  if (tokens.length === 0) {
    console.log(`[chatFCM] No fcmTokens for user ${uid}, skip`);
    return;
  }
  const invalidTokens: string[] = [];
  for (const token of tokens) {
    try {
      await admin.messaging().send({
        token,
        notification: { title, body },
        data: { ...data, chatId: data.chatId || "" },
        android: { priority: "high", notification: { channelId: "chat" } },
        apns: { payload: { aps: { sound: "default" } }, fcmOptions: {} },
      });
    } catch (err: unknown) {
      const code = (err as { code?: string })?.code;
      if (code === "messaging/invalid-registration-token" || code === "messaging/registration-token-not-registered")
        invalidTokens.push(token);
      else console.error(`[chatFCM] Send failed for token:`, err);
    }
  }
  if (invalidTokens.length > 0) {
    try {
      const userRef = db.collection("users").doc(uid);
      const snap = await userRef.get();
      const current = snap.data()?.fcmTokens;
      if (typeof current === "object" && current !== null && !Array.isArray(current)) {
        const next: Record<string, string> = {};
        for (const [k, v] of Object.entries(current))
          if (typeof v === "string" && !invalidTokens.includes(v)) next[k] = v;
        await userRef.update({ fcmTokens: next });
      }
    } catch (e) {
      console.error("[chatFCM] Failed to remove invalid tokens:", e);
    }
  }
}

/**
 * Callable: run FCM + auto-assign logic after a chat message is created.
 * Call this from the client after writing to chats/{chatId}/messages/{messageId}.
 * (Replaces onDocumentCreated because Firestore is in asia-southeast3, which is not supported for Gen2 triggers.)
 */
export const notifyChatMessageCreated = onCall(
  { region: "asia-southeast1" },
  async (request): Promise<{ ok: boolean }> => {
    const data = request.data as { chatId?: string; messageId?: string };
    const { chatId, messageId } = data;
    if (!chatId || !messageId) {
      throw new HttpsError("invalid-argument", "chatId and messageId are required");
    }

    const msgSnap = await db.collection("chats").doc(chatId).collection("messages").doc(messageId).get();
    if (!msgSnap.exists) {
      throw new HttpsError("not-found", "Message not found");
    }
    const msg = msgSnap.data() as { senderId?: string; senderRole?: string; text?: string };
    const senderRole = msg.senderRole as string | undefined;
    const senderId = msg.senderId as string | undefined;
    const text = (msg.text as string) || "";

    const chatRef = db.collection("chats").doc(chatId);
    const chatSnap = await chatRef.get();
    if (!chatSnap.exists) {
      throw new HttpsError("not-found", "Chat not found");
    }
    const chat = chatSnap.data() as { driverId?: string; assignedAdminId?: string | null; status?: string };
    const driverId = chat.driverId as string | undefined;
    if (!driverId) return { ok: true };

    // Auto-assign: first admin reply sets assignedAdminId and status in_progress
    if (senderRole === "admin" && senderId && (chat.assignedAdminId == null || chat.assignedAdminId === "")) {
      try {
        await chatRef.update({
          assignedAdminId: senderId,
          status: "in_progress",
          assignedAt: admin.firestore.FieldValue.serverTimestamp(),
          updatedAt: admin.firestore.FieldValue.serverTimestamp(),
        });
      } catch (e) {
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
  }
);

const BROADCASTS_COLL = "broadcasts";

/**
 * Callable: send a broadcast message to many drivers.
 * Writes only to the broadcasts collection (one doc per send). Does not write to chats.
 * Sends FCM to each driver so they can open the broadcast list.
 */
export const sendBroadcast = onCall(
  {
    region: "asia-southeast1",
    cors: true,
  },
  async (request): Promise<{ ok: boolean; recipientCount: number }> => {
    if (!request.auth?.token?.admin) {
      throw new HttpsError("permission-denied", "Only admins can send broadcasts");
    }
    const data = request.data as {
      recipientDriverIds?: string[];
      title?: string;
      messageText?: string;
      recipientGroup?: string;
    };
    const recipientDriverIds = data.recipientDriverIds;
    const broadcastTitle = (data.title as string)?.trim();
    const messageText = (data.messageText as string)?.trim();
    const recipientGroup = (data.recipientGroup as string) || "all_driver";
    if (
      !Array.isArray(recipientDriverIds) ||
      recipientDriverIds.length === 0 ||
      !broadcastTitle ||
      !messageText
    ) {
      throw new HttpsError(
        "invalid-argument",
        "recipientDriverIds (non-empty array), title, and messageText are required"
      );
    }
    const adminUid = request.auth.uid as string;

    let createdByName = adminUid;
    try {
      const adminUser = await admin.auth().getUser(adminUid);
      createdByName = adminUser.displayName || adminUser.email || adminUid;
    } catch {
      // keep createdByName as uid
    }

    // 1. Write one document to broadcasts collection only (no chat writes)
    await db.collection(BROADCASTS_COLL).add({
      createdBy: adminUid,
      createdByName,
      title: broadcastTitle,
      messageText,
      recipientCount: recipientDriverIds.length,
      recipientGroup,
      readCount: 0,
      readDriverAuthIds: [],
      sentAt: admin.firestore.FieldValue.serverTimestamp(),
    });

    // 2. Send FCM to each driver (opens broadcast list; no chatId)
    const fcmTitle =
      broadcastTitle.length > 90 ? `${broadcastTitle.slice(0, 87)}…` : broadcastTitle;
    const body = messageText.slice(0, 80) + (messageText.length > 80 ? "…" : "");
    for (const driverId of recipientDriverIds) {
      await sendFcmToUser(driverId, fcmTitle, body, { type: "broadcast" });
    }

    return { ok: true, recipientCount: recipientDriverIds.length };
  }
);

/**
 * Callable: driver opened broadcast detail (tapped read on mobile). Counts each auth user once.
 */
export const markBroadcastRead = onCall(
  {
    region: "asia-southeast1",
    cors: true,
  },
  async (request): Promise<{ ok: boolean; alreadyRead?: boolean }> => {
    if (!request.auth?.uid) {
      throw new HttpsError("unauthenticated", "Sign in required");
    }
    const broadcastId = (request.data as { broadcastId?: string })?.broadcastId?.trim();
    if (!broadcastId) {
      throw new HttpsError("invalid-argument", "broadcastId is required");
    }
    const uid = request.auth.uid;
    const ref = db.collection(BROADCASTS_COLL).doc(broadcastId);
    let alreadyRead = false;
    await db.runTransaction(async (t) => {
      const snap = await t.get(ref);
      if (!snap.exists) {
        throw new HttpsError("not-found", "Broadcast not found");
      }
      const data = snap.data() as { readDriverAuthIds?: string[] };
      const ids = data.readDriverAuthIds ?? [];
      if (ids.includes(uid)) {
        alreadyRead = true;
        return;
      }
      t.update(ref, {
        readDriverAuthIds: admin.firestore.FieldValue.arrayUnion(uid),
        readCount: admin.firestore.FieldValue.increment(1),
      });
    });
    return { ok: true, alreadyRead };
  }
);
