import type { Timestamp } from "firebase/firestore";

/**
 * Shape of documents in Firestore `broadcasts/{broadcastId}`.
 *
 * Firestore has **no enforced schema** — the console “schema” view only reflects fields
 * that already exist on stored documents. Older sends (before subject/body split) may
 * omit `title`. New sends from `sendBroadcast` always set `title`, `readCount`, etc.
 *
 * @see COLLECTIONS.BROADCASTS in `@/lib/collections`
 */
export interface BroadcastFirestoreDoc {
  createdBy: string;
  createdByName: string;
  /** Subject line; missing on legacy documents. */
  title?: string;
  messageText: string;
  recipientCount: number;
  recipientGroup: string;
  sentAt: Timestamp | null;
  /** Unique readers (mobile “read”); optional on very old docs. */
  readCount?: number;
  readDriverAuthIds?: string[];
}
