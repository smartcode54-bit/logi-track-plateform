/**
 * The chat console over Go (T50, issue #70; T47 routes, Appendix B §B.2.16; developer-spec.md §10.7, §10.8;
 * Appendix E §E.2.1 `/app/chat*`, §E.5 rows 1-4, §E.6). The only source since TW7; replaces
 * the two listeners of the conversations panel (`ConversationsPanel.tsx:127-189`, closed chats downloaded and
 * dropped in the browser), the room's chat and full-history listeners (`room/page.tsx:74,90`), the read
 * state written inside a snapshot callback (`:108-113`), the three sequential reads of with-driver and the
 * unauthenticated `notifyChatMessageCreated` callable:
 *
 * - `['chats','queued' | 'mine']`: `GET /v1/chats?filter=queue|mine` keyset pages (closed chats left out by
 *   Go, `unreadForMe` counted by Go), SSE `tenant:{tid}:chats` (15 s / 5 min; the 60 s poll only while the
 *   stream is down);
 * - `['chat', id]`: `GET /v1/chats/{id}`, the chat with its read state (`read.updated` on `chat:{id}`);
 * - `['chat', id, 'messages']`: `GET /v1/chats/{id}/messages?before&limit=50`, newest first, never stale:
 *   the explicit `chat:{id}` topic (`useRealtimeTopic`) writes each new message into page 0. A write made
 *   while the key is fetching is applied again when that fetch lands (lib/queryWrite.ts), and a refetch of
 *   page 0 keeps the sends only this tab knows (`withLocalMessages`), so neither a pushed message nor a
 *   failed or syncing send is dropped by a refetch;
 * - writes: `POST /v1/chats` (open the driver's chat), `POST /v1/chats/{id}/messages` (optimistic, with a
 *   `clientMessageId`: a retry stores nothing twice, R63), `/read`, `/assign`, `/close`, `/reopen`.
 *
 * While the installed 3.x apps still write chats in Firestore (comms not in `PG_OWNED_DOMAINS`) Go writes
 * them back there first and may answer `202 {pending: true, id, docId, ...}` before the mirror caught up
 * (main spec §13.8 as built T47): the room keeps the message as "syncing" and refetches.
 */
import { infiniteQueryOptions, queryOptions, useMutation, useQueryClient, type QueryClient } from "@tanstack/react-query";

import { goFetch, goFetchEnvelope, goPath } from "@/lib/goFetch";
import { goInfiniteQueryFn, goNextPageParam, type GoPage } from "@/lib/goQuery";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";
import { writeThroughFetch } from "@/lib/queryWrite";
import { removeLocalMessage, upsertMessage, withLocalMessages, type ChatDTO, type ChatDetailDTO, type ChatMessageDTO } from "./chatCache";

export * from "./chatCache";

export const CHATS_PATH = "/v1/chats";
/** One page of a chat list or of a room's history (Go's default; Appendix E §E.10: 50 per page). */
export const CHAT_PAGE_SIZE = 50;
/** Go's bound on a message's text (internal/chats `MaxTextLength`). */
export const MAX_MESSAGE_LENGTH = 4000;
/** How long after a written-back send (202) the room refetches its messages (the mirror's acknowledgement). */
export const WRITE_BACK_REFETCH_MS = 3_000;

const UUID_RX = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const NIL_UUID = "00000000-0000-0000-0000-000000000000";

/** Whether `id` can name a Go chat or driver (a Firestore document id cannot). */
export function isGoId(id: string | null | undefined): id is string {
    return typeof id === "string" && UUID_RX.test(id) && id !== NIL_UUID;
}

export type ChatListFilter = "queue" | "mine";

/**
 * The link that opens a driver's chat (the drivers list's Chat action): the Go console opens it by the
 * driver's PostgreSQL id (`driverId`, a driver with a login), the Firestore console by its legacy Firebase
 * uid (`authId`); the link carries both while both exist, and each source reads its own.
 */
export function chatWithDriverHref(driver: { id: string; userId?: string | null; legacyAuthUid?: string | null }): string {
    const params = new URLSearchParams();
    if (driver.userId && isGoId(driver.id)) params.set("driverId", driver.id);
    if (driver.legacyAuthUid) params.set("authId", driver.legacyAuthUid);
    return `/app/chat/with-driver?${params.toString()}`;
}

/** The 202 data of a write Go wrote back to Firestore and the mirror has not acknowledged yet. */
export interface ChatWriteBackPending {
    pending: true;
    id: string;
    collection: string;
    docId: string;
}

export function isWriteBackPending(v: unknown): v is ChatWriteBackPending {
    return typeof v === "object" && v !== null && (v as { pending?: unknown }).pending === true;
}

// --- reads ---------------------------------------------------------------------------------------

/** `['chats','queued' | 'mine']`: one conversation list, keyset pages of 50, newest message first. */
export function chatListQuery(filter: ChatListFilter) {
    return infiniteQueryOptions({
        queryKey: filter === "queue" ? queryKeys.chats.queued() : queryKeys.chats.mine(),
        queryFn: goInfiniteQueryFn<ChatDTO>(CHATS_PATH, { query: { filter, limit: CHAT_PAGE_SIZE } }),
        initialPageParam: undefined as string | undefined,
        getNextPageParam: goNextPageParam,
        ...QUERY_POLICY.chat,
    });
}

export function chatRows(pages: readonly GoPage<ChatDTO>[] | undefined): ChatDTO[] {
    const seen = new Set<string>();
    const out: ChatDTO[] = [];
    for (const c of (pages ?? []).flatMap((p) => p.data ?? [])) {
        if (seen.has(c.id)) continue;
        seen.add(c.id);
        out.push(c);
    }
    return out;
}

/** `['chat', id]`: the chat and its read state (disabled for an id Go cannot hold). */
export function chatQuery(id: string) {
    return queryOptions({
        queryKey: queryKeys.chat.detail(id),
        queryFn: ({ signal }) => goFetch<ChatDetailDTO>(goPath`/v1/chats/${id}`, { signal }),
        ...QUERY_POLICY.chat,
        enabled: isGoId(id),
    });
}

/**
 * `['chat', id, 'messages']`: keyset pages of 50, newest first (`?before=` the previous page's cursor). Never
 * stale (Appendix E §E.6): the `chat:{id}` stream writes new messages in; the tenant topic's fallback and a
 * resync refetch it. A refetch of page 0 keeps the cached sends the server does not hold yet (`sending`,
 * `syncing`, `failed`: `withLocalMessages`).
 */
export function chatMessagesQuery(id: string) {
    return infiniteQueryOptions({
        queryKey: queryKeys.chat.messages(id),
        queryFn: async ({ signal, pageParam, client, queryKey }) => {
            const page = await goFetchEnvelope<ChatMessageDTO[]>(goPath`/v1/chats/${id}/messages`, {
                signal,
                query: { limit: CHAT_PAGE_SIZE, before: pageParam },
            });
            return pageParam === undefined ? withLocalMessages(page, client.getQueryData(queryKey)) : page;
        },
        initialPageParam: undefined as string | undefined,
        getNextPageParam: goNextPageParam,
        staleTime: Infinity,
        gcTime: 10 * 60_000,
        refetchOnWindowFocus: false,
        enabled: isGoId(id),
    });
}

/** `GET /v1/drivers/{id}` as the chat's driver panel reads it (PostgreSQL ids only; `drivers:view`). */
export interface ChatDriverDTO {
    id: string;
    userId: string | null;
    legacyDocId: string | null;
    legacyAuthUid?: string | null;
    displayName: string;
    firstName: string;
    lastName: string;
    fullNameTh: string | null;
    mobile: string;
    profileUrl: string | null;
    status: string;
    login: { userId: string } | null;
    currentAssignment: { id: string; truckId: string | null; truckPlate: string | null; truckModel: string | null } | null;
    activeTruck: { truckId: string; truckPlate: string | null } | null;
}

/** `['drivers','detail',id]` read from Go (the chat pages hold PostgreSQL driver ids). */
export function chatDriverQuery(id: string) {
    return queryOptions({
        queryKey: queryKeys.drivers.detail(id),
        queryFn: ({ signal }) => goFetch<ChatDriverDTO>(goPath`/v1/drivers/${id}`, { signal }),
        ...QUERY_POLICY.entity,
        enabled: isGoId(id),
    });
}

/** The driver's login (`recipientUserIds` of a broadcast, the chat's driver). */
export function driverUserId(d: Pick<ChatDriverDTO, "userId" | "login"> | null | undefined): string | null {
    return d?.userId ?? d?.login?.userId ?? null;
}

// --- writes --------------------------------------------------------------------------------------

/** `POST /v1/chats {driverId}`: the driver's chat that is not closed (200), a new one (201), or 202 while written back. */
export async function openChatWithDriver(driverId: string): Promise<ChatDTO | ChatWriteBackPending> {
    return goFetch<ChatDTO | ChatWriteBackPending>(CHATS_PATH, { method: "POST", body: { driverId } });
}

export interface SendMessageInput {
    chatId: string;
    text: string;
    /** One per message, kept across its retries (R63). */
    clientMessageId: string;
    /** The sender (the signed-in staff member), for the optimistic copy. */
    senderUserId: string | null;
}

/** `POST /v1/chats/{id}/messages`: 201 (or 200 for a replay) with the message, 202 while written back. */
export async function sendChatMessage({ chatId, text, clientMessageId }: SendMessageInput): Promise<ChatMessageDTO | ChatWriteBackPending> {
    return goFetch<ChatMessageDTO | ChatWriteBackPending>(goPath`/v1/chats/${chatId}/messages`, {
        method: "POST",
        body: { text, clientMessageId },
    });
}

function localMessage(v: SendMessageInput, local: ChatMessageDTO["local"]): ChatMessageDTO {
    return {
        id: `local:${v.clientMessageId}`,
        chatId: v.chatId,
        legacyDocId: null,
        senderUserId: v.senderUserId,
        senderRole: "admin",
        senderName: null,
        text: v.text,
        imageFileId: null,
        imageUrl: null,
        messageType: "normal",
        clientMessageId: v.clientMessageId,
        createdAt: new Date().toISOString(),
        local,
    };
}

/**
 * Writes `msg` into the room's cache (no cache: nothing), and again on the result of a fetch that was
 * running meanwhile (a first load included), whose server read may predate it.
 */
function writeMessage(client: QueryClient, chatId: string, msg: ChatMessageDTO) {
    writeThroughFetch(client, queryKeys.chat.messages(chatId), (cached) => upsertMessage(cached, msg));
}

/** After a staff write the lists, the badges and the chat (exact, never its message pages) are stale. */
export function invalidateChat(client: QueryClient, chatId: string) {
    return Promise.all([
        client.invalidateQueries({ queryKey: queryKeys.chats.all() }),
        client.invalidateQueries({ queryKey: queryKeys.badges() }),
        client.invalidateQueries({ queryKey: queryKeys.chat.detail(chatId), exact: true }),
    ]);
}

/**
 * Sends a message with its local copy shown at once (`sending`): the answer replaces it (so does the
 * stream's `message.created`, whichever comes first, by `clientMessageId`); a write-back (202) keeps it as
 * `syncing` and refetches the messages once the mirror had time; a failure marks it `failed` (retry with
 * the same `clientMessageId`, or discard). No fetch of the messages is cancelled (a cancelled first load
 * or image refetch was never retried): each write is applied again when a fetch running meanwhile lands,
 * and a refetch keeps the local copy until the server holds the message.
 */
export function useSendChatMessage() {
    const client = useQueryClient();
    return useMutation({
        mutationFn: sendChatMessage,
        onMutate: (v) => {
            writeMessage(client, v.chatId, localMessage(v, "sending"));
        },
        onSuccess: (data, v) => {
            if (isWriteBackPending(data)) {
                writeMessage(client, v.chatId, localMessage(v, "syncing"));
                setTimeout(() => void client.invalidateQueries({ queryKey: queryKeys.chat.messages(v.chatId) }), WRITE_BACK_REFETCH_MS);
            } else {
                writeMessage(client, v.chatId, { ...data, imageUrl: data.imageUrl ?? null });
            }
            void invalidateChat(client, v.chatId);
        },
        onError: (_error, v) => {
            writeMessage(client, v.chatId, localMessage(v, "failed"));
        },
    });
}

/** Drops the local copy of a failed send (again on a fetch that was running meanwhile). */
export function discardLocalMessage(client: QueryClient, chatId: string, clientMessageId: string) {
    writeThroughFetch(client, queryKeys.chat.messages(chatId), (cached) => removeLocalMessage(cached, clientMessageId));
}

/** `POST /v1/chats/{id}/read {lastReadAt}`: the caller's read state, forward only (204). */
export async function markChatRead({ chatId, lastReadAt }: { chatId: string; lastReadAt: string }): Promise<void> {
    await goFetch<void>(goPath`/v1/chats/${chatId}/read`, { method: "POST", body: { lastReadAt } });
}

export function useMarkChatRead() {
    const client = useQueryClient();
    return useMutation({
        mutationFn: markChatRead,
        // The caller's unread counts and the badges moved; the room's read marks come from read.updated.
        onSuccess: () =>
            Promise.all([client.invalidateQueries({ queryKey: queryKeys.chats.all() }), client.invalidateQueries({ queryKey: queryKeys.badges() })]),
    });
}

export type ChatAction = "take" | "return" | "close" | "reopen";

/**
 * The room's actions: take (`assign` to the caller), return to the queue (`assign {adminUserId: null}`),
 * close, reopen. Each answers the chat as `GET /v1/chats/{id}` does (or 202 while written back).
 */
export async function runChatAction({ chatId, action }: { chatId: string; action: ChatAction }): Promise<ChatDetailDTO | ChatWriteBackPending> {
    switch (action) {
        case "take":
            return goFetch<ChatDetailDTO | ChatWriteBackPending>(goPath`/v1/chats/${chatId}/assign`, { method: "POST", body: {} });
        case "return":
            return goFetch<ChatDetailDTO | ChatWriteBackPending>(goPath`/v1/chats/${chatId}/assign`, { method: "POST", body: { adminUserId: null } });
        case "close":
            return goFetch<ChatDetailDTO | ChatWriteBackPending>(goPath`/v1/chats/${chatId}/close`, { method: "POST", body: {} });
        case "reopen":
            return goFetch<ChatDetailDTO | ChatWriteBackPending>(goPath`/v1/chats/${chatId}/reopen`, { method: "POST", body: {} });
    }
}

export function useChatAction() {
    const client = useQueryClient();
    return useMutation({
        mutationFn: runChatAction,
        onSuccess: (data, v) => {
            // The answer is the chat with the caller's view; keep the read state the cache already holds.
            if (!isWriteBackPending(data)) client.setQueryData<ChatDetailDTO>(queryKeys.chat.detail(v.chatId), (prev) => ({ ...prev, ...data }));
            return invalidateChat(client, v.chatId);
        },
    });
}
