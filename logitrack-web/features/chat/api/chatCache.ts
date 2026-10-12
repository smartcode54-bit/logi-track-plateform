/**
 * The chat cache shapes and their pure updaters (T50; developer-spec.md §10.7, §10.8; Appendix B §B.2.16,
 * §B.4.3; Appendix E §E.5 rows 1-4, §E.5.1). They are shared by the hooks (features/chat/api/chats.go.ts:
 * the optimistic send) and the realtime event map (lib/realtime/eventMap.ts: `message.created` and
 * `read.updated` on `chat:{id}`), so a message the stream delivers and the one the send answers meet in one
 * place and are kept once: by `id`, else by the sender's `clientMessageId` (R63).
 *
 * No React and no Firebase here: lib/realtime imports it.
 */
import type { InfiniteData } from "@tanstack/react-query";
import type { GoPage } from "@/lib/goQuery";

export type ChatStatus = "open" | "in_progress" | "closed";
export type ChatPriority = "normal" | "urgent";

/** `GET /v1/chats` item (internal/chats `Chat`). */
export interface ChatDTO {
    id: string;
    tenantId: string;
    legacyDocId: string | null;
    driverId: string | null;
    driverName: string;
    driverUserId: string | null;
    driverLegacyUid: string | null;
    status: ChatStatus | string;
    priority: ChatPriority | string;
    assignedAdminUserId: string | null;
    assignedAdminName: string | null;
    assignedAt: string | null;
    closedAt: string | null;
    lastMessagePreview: string | null;
    lastMessageAt: string | null;
    lastMessageByUserId: string | null;
    lastMessageType: string | null;
    createdAt: string;
    updatedAt: string;
    /** The other side's messages after the caller's read state (driver messages for staff), counted by Go. */
    unreadForMe: number;
    /** The caller's read state. */
    lastReadAt: string | null;
}

/** One reader's read state. */
export interface ChatReadDTO {
    userId: string;
    lastReadAt: string;
    isDriver: boolean;
}

/** `GET /v1/chats/{id}`: the chat and every reader's read state. */
export interface ChatDetailDTO extends ChatDTO {
    reads: ChatReadDTO[];
    driverLastReadAt: string | null;
}

/** `GET /v1/chats/{id}/messages` item (internal/chats `Message`). */
export interface ChatMessageDTO {
    id: string;
    chatId: string;
    legacyDocId: string | null;
    senderUserId: string | null;
    senderRole: "admin" | "driver" | string;
    /** The driver's name on a driver message (null on a staff message for a driver reader). */
    senderName: string | null;
    text: string | null;
    imageFileId: string | null;
    /** A short-lived signed URL of the image (absent from a stream event: the page refetches). */
    imageUrl: string | null;
    messageType: "normal" | "broadcast" | string;
    clientMessageId: string | null;
    createdAt: string;
    /** Client only: a send not answered yet (`sending`), written back and not mirrored yet (`syncing`), or failed. */
    local?: "sending" | "syncing" | "failed";
}

/** The cache of `['chat', id, 'messages']`: keyset pages, page 0 the newest. */
export type MessagesCache = InfiniteData<GoPage<ChatMessageDTO>, string | undefined>;

function isRecord(v: unknown): v is Record<string, unknown> {
    return typeof v === "object" && v !== null && !Array.isArray(v);
}

const str = (v: unknown): string | null => (typeof v === "string" && v !== "" ? v : null);

function isMessagesCache(v: unknown): v is MessagesCache {
    return isRecord(v) && Array.isArray(v.pages) && v.pages.length > 0 && v.pages.every((p) => isRecord(p) && Array.isArray(p.data));
}

/**
 * The message of a `message.created` frame on `chat:{id}` (Appendix B §B.4.3: the whole message, its id,
 * `chatId`, sender, text, image file, type, `clientMessageId`, `createdAt`), or null when it is unreadable.
 */
export function messageFromEvent(data: unknown): ChatMessageDTO | null {
    if (!isRecord(data)) return null;
    const id = str(data.id);
    const chatId = str(data.chatId);
    const createdAt = str(data.createdAt) ?? str(data.at);
    const senderRole = str(data.senderRole);
    if (!id || !chatId || !createdAt || !senderRole) return null;
    return {
        id,
        chatId,
        legacyDocId: str(data.legacyDocId),
        senderUserId: str(data.senderUserId),
        senderRole,
        senderName: str(data.senderName),
        text: typeof data.text === "string" ? data.text : null,
        imageFileId: str(data.imageFileId),
        imageUrl: null,
        messageType: str(data.messageType) ?? "normal",
        clientMessageId: str(data.clientMessageId),
        createdAt,
    };
}

/**
 * The messages cache with `msg` in it: unchanged when a message with its id is there; replacing the
 * message that carries the same `clientMessageId` (the optimistic copy of a send) in place; else at the
 * head of page 0 (the newest page). No cache (the room is not open): nothing to write.
 */
export function upsertMessage(cached: unknown, msg: ChatMessageDTO): unknown {
    if (!isMessagesCache(cached)) return cached;
    let found = false;
    const pages = cached.pages.map((page) => {
        if (found) return page;
        const at = page.data.findIndex((m) => m.id === msg.id || (msg.clientMessageId !== null && m.clientMessageId === msg.clientMessageId));
        if (at === -1) return page;
        found = true;
        const current = page.data[at];
        if (current.id === msg.id && current.local === undefined && msg.local === undefined) return page;
        const data = page.data.slice();
        // The server's copy wins over a local one; a local copy never replaces the server's.
        if (current.local === undefined && msg.local !== undefined) return page;
        data[at] = msg;
        return { ...page, data };
    });
    if (found) return { ...cached, pages };
    const [head, ...rest] = cached.pages;
    return { ...cached, pages: [{ ...head, data: [msg, ...head.data] }, ...rest] };
}

/** The messages cache without the local copy of `clientMessageId` (a send given up). */
export function removeLocalMessage(cached: unknown, clientMessageId: string): unknown {
    if (!isMessagesCache(cached)) return cached;
    return {
        ...cached,
        pages: cached.pages.map((page) => ({ ...page, data: page.data.filter((m) => !(m.local !== undefined && m.clientMessageId === clientMessageId)) })),
    };
}

/**
 * Page 0 of a messages refetch with the sends only this tab knows (`sending`, `syncing`, `failed`) that the
 * server's page does not hold yet (by `clientMessageId`), taken from the cache the refetch replaces: a
 * refetch rebuilds the pages from the server, and without this a failed send's Retry/Discard, or a send
 * written back and not mirrored yet, would vanish on any refetch (the fallback poll, a resync, an image
 * message). The server's copy wins once it has the message.
 */
export function withLocalMessages(page: GoPage<ChatMessageDTO>, cached: unknown): GoPage<ChatMessageDTO> {
    if (!isMessagesCache(cached)) return page;
    const onServer = new Set((page.data ?? []).map((m) => m.clientMessageId).filter((c): c is string => c !== null));
    const local: ChatMessageDTO[] = [];
    const seen = new Set<string>();
    for (const p of cached.pages) {
        for (const m of p.data) {
            if (m.local === undefined || m.clientMessageId === null || onServer.has(m.clientMessageId) || seen.has(m.clientMessageId)) continue;
            seen.add(m.clientMessageId);
            local.push(m);
        }
    }
    return local.length === 0 ? page : { ...page, data: [...local, ...(page.data ?? [])] };
}

/** Whether a cached message list lacks the image URL of `msg` (a stream event carries none). */
export function needsImageRefetch(cached: unknown, msg: ChatMessageDTO): boolean {
    if (msg.imageFileId === null || !isMessagesCache(cached)) return false;
    return !cached.pages.some((p) => p.data.some((m) => m.id === msg.id && m.imageUrl !== null && m.imageUrl !== undefined));
}

/**
 * Every message of the cache, oldest first, once each (pages overlap after an event was prepended and the
 * list refetched): the order the room shows them in.
 */
export function messagesOldestFirst(cached: Pick<MessagesCache, "pages"> | undefined): ChatMessageDTO[] {
    if (!cached) return [];
    const byKey = new Map<string, ChatMessageDTO>();
    for (const page of cached.pages) {
        for (const m of page.data ?? []) {
            const key = m.clientMessageId ? `c:${m.clientMessageId}` : `i:${m.id}`;
            const prev = byKey.get(key);
            if (!prev || (prev.local !== undefined && m.local === undefined)) byKey.set(key, m);
        }
    }
    // By instant (Go's RFC 3339 strings vary in their fraction digits), then id (uuid v7: time ordered).
    const at = (m: ChatMessageDTO) => Date.parse(m.createdAt) || 0;
    return [...byKey.values()].sort((a, b) => at(a) - at(b) || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
}

/** The `{userId, lastReadAt}` of a `read.updated` frame, or null. */
export function readFromEvent(data: unknown): { chatId: string; userId: string; lastReadAt: string } | null {
    if (!isRecord(data)) return null;
    const chatId = str(data.chatId) ?? str(data.id);
    const userId = str(data.userId);
    const lastReadAt = str(data.lastReadAt);
    return chatId && userId && lastReadAt ? { chatId, userId, lastReadAt } : null;
}

const later = (a: string | null | undefined, b: string): string => (a && Date.parse(a) > Date.parse(b) ? a : b);

/**
 * The chat detail with a reader's read state moved forward (`read.updated`): its `reads[]` row, and
 * `driverLastReadAt` when the reader is the chat's driver (the room's "read" marks). No cache: nothing.
 */
export function applyReadState(cached: unknown, read: { userId: string; lastReadAt: string }): unknown {
    if (!isRecord(cached) || !Array.isArray(cached.reads)) return cached;
    const chat = cached as unknown as ChatDetailDTO;
    const prev = chat.reads.find((r) => r.userId === read.userId);
    const isDriver = prev?.isDriver ?? (chat.driverUserId !== null && chat.driverUserId === read.userId);
    const lastReadAt = later(prev?.lastReadAt, read.lastReadAt);
    const reads = prev ? chat.reads.map((r) => (r.userId === read.userId ? { ...r, lastReadAt } : r)) : [...chat.reads, { userId: read.userId, lastReadAt, isDriver }];
    return { ...chat, reads, ...(isDriver ? { driverLastReadAt: later(chat.driverLastReadAt, lastReadAt) } : {}) };
}
