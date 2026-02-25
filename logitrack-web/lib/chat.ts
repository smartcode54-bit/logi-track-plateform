/**
 * Chat types and constants for admin–driver chat.
 */

import type { Timestamp } from "firebase/firestore";

export type ChatStatus = "open" | "in_progress" | "closed";
export type ChatPriority = "normal" | "urgent";
export type SenderRole = "admin" | "driver";
export type MessageType = "normal" | "broadcast";

export interface ChatDoc {
  driverId: string;
  driverDisplayName: string;
  status: ChatStatus;
  assignedAdminId: string | null;
  assignedAt: Timestamp | null;
  closedAt: Timestamp | null;
  priority: ChatPriority;
  lastReadByAdmin: Record<string, Timestamp>;
  lastMessage: string;
  lastMessageAt: Timestamp;
  lastMessageBy: string;
  /** When last message was a broadcast; used to show broadcast icon in list. */
  lastMessageType?: MessageType;
  createdAt: Timestamp;
  updatedAt: Timestamp;
}

export interface MessageDoc {
  senderId: string;
  senderRole: SenderRole;
  text: string;
  createdAt: Timestamp;
  imageUrl?: string;
  /** When set to "broadcast", show broadcast icon in UI; set by sendBroadcast Cloud Function. */
  type?: MessageType;
}
