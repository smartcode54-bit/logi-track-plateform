"use client";

/**
 * The chat room over Go (T50; Appendix E §E.2.1 `/app/chat/room`, §E.5 rows 3-4, §E.10): `['chat', id]` (the
 * chat and its read state, `read.updated`) and `['chat', id, 'messages']` (keyset pages of 50, newest first;
 * "older messages" loads the next page) instead of a listener on the chat document and one on its whole
 * history. The room joins the explicit `chat:{id}` topic while it is open: each `message.created` is written
 * into page 0 at once. Sending is optimistic with a `clientMessageId` (a retry never stores the message
 * twice, R63); the room's read state is one `POST /v1/chats/{id}/read` per newest message seen (debounced,
 * only while the tab is visible), never a write inside a data callback (`room/page.tsx:108-113`).
 */
import { Fragment, useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { CheckCircle, Hand, Loader2, RotateCcw, Send } from "lucide-react";
import { toast } from "sonner";

import { useLanguage } from "@/context/language";
import { PageHeader } from "@/components/page-header";
import { RecordState } from "@/components/record-state";
import { useCan, useMe } from "@/features/auth/api/useMe";
import { apiErrorText, isApiError } from "@/lib/apiError";
import { newIdempotencyKey } from "@/lib/goFetch";
import { useStreamFallbackPoll } from "@/lib/realtime/polling";
import { chatTopic, useRealtimeTopic } from "@/lib/realtime/topics";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { Input } from "@/components/ui/input";

import {
    MAX_MESSAGE_LENGTH,
    chatMessagesQuery,
    chatQuery,
    discardLocalMessage,
    isGoId,
    messagesOldestFirst,
    useChatAction,
    useMarkChatRead,
    useSendChatMessage,
    type ChatAction,
    type ChatDetailDTO,
    type ChatMessageDTO,
} from "../api/chats.go";
import { clockLabel, dayLabel, localDayKey } from "./chatFormat";
import { safeHref } from "./safeHref";

/** The read state is posted this long after the newest message was seen (a burst of messages: one request). */
export const MARK_READ_DEBOUNCE_MS = 800;

const meId = (me: { id: string } | null) => me?.id ?? null;

function usePageVisible(): boolean {
    const [visible, setVisible] = useState(() => typeof document === "undefined" || document.visibilityState !== "hidden");
    useEffect(() => {
        const on = () => setVisible(document.visibilityState !== "hidden");
        document.addEventListener("visibilitychange", on);
        return () => document.removeEventListener("visibilitychange", on);
    }, []);
    return visible;
}

/**
 * Posts the caller's read state for the newest message once it is seen (debounced; forward only). A message
 * of the caller's own needs none: Go moved the sender's read state with the send.
 */
function useMarkReadOnView(chat: ChatDetailDTO | undefined, newest: ChatMessageDTO | undefined, myId: string | null) {
    const markRead = useMarkChatRead();
    const visible = usePageVisible();
    const marked = useRef<{ chatId: string; at: number } | null>(null);
    const { mutate } = markRead;
    useEffect(() => {
        if (!chat || !newest || newest.local !== undefined || !visible) return;
        if (myId !== null && newest.senderUserId === myId) return;
        const at = Date.parse(newest.createdAt);
        if (!Number.isFinite(at)) return;
        const mine = chat.lastReadAt ? Date.parse(chat.lastReadAt) : 0;
        const done = marked.current?.chatId === chat.id ? marked.current.at : 0;
        if (at <= Math.max(mine, done)) return;
        const timer = setTimeout(() => {
            marked.current = { chatId: chat.id, at };
            mutate({ chatId: chat.id, lastReadAt: newest.createdAt });
        }, MARK_READ_DEBOUNCE_MS);
        return () => clearTimeout(timer);
    }, [chat, newest, myId, visible, mutate]);
}

export function ChatRoomGo() {
    const searchParams = useSearchParams();
    const chatId = searchParams.get("chatId") ?? "";
    const router = useRouter();
    const { t } = useLanguage();

    useEffect(() => {
        if (!chatId) router.replace("/app/chat");
    }, [chatId, router]);

    if (!chatId) return null;
    if (!isGoId(chatId)) {
        return (
            <div className="flex flex-1 flex-col items-center justify-center gap-3 p-8 text-center text-sm text-muted-foreground" data-testid="chat-legacy-link">
                <p>{t("chat.room.legacyLink")}</p>
                <Button variant="outline" size="sm" asChild>
                    <Link href="/app/chat" prefetch={false}>
                        {t("chat.room.backToConsole")}
                    </Link>
                </Button>
            </div>
        );
    }
    return <Room key={chatId} chatId={chatId} />;
}

function Room({ chatId }: { chatId: string }) {
    const router = useRouter();
    const client = useQueryClient();
    const { t, language } = useLanguage();
    const { data: myId = null } = useMe(meId);
    const canSend = useCan("chat:send").allowed;
    const fallback = useStreamFallbackPoll();
    const chat = useQuery({ ...chatQuery(chatId), ...fallback });
    // Never stale while the stream writes new messages in; polled (60 s) only while the stream is down.
    const messages = useInfiniteQuery({ ...chatMessagesQuery(chatId), ...fallback });
    const send = useSendChatMessage();
    const action = useChatAction();
    const [input, setInput] = useState("");
    const bottomRef = useRef<HTMLDivElement>(null);

    // Join chat:{id} once Go has shown the chat to the caller (an unreadable id is never put on the stream).
    useRealtimeTopic(chat.isSuccess ? chatTopic(chatId) : null);

    const list = useMemo(() => messagesOldestFirst(messages.data), [messages.data]);
    const newest = useMemo(() => [...list].reverse().find((m) => m.local === undefined), [list]);
    const lastId = list.length > 0 ? list[list.length - 1].id : "";

    // The divider sits above the first driver message newer than the caller's read state at entry.
    // Captured once per room (the component is keyed by the chat): later reads move the state, not the divider.
    const [baseline, setBaseline] = useState<number | null>(null);
    if (baseline === null && chat.data) setBaseline(chat.data.lastReadAt ? Date.parse(chat.data.lastReadAt) : 0);
    const firstUnread = useMemo(() => {
        if (baseline === null) return -1;
        return list.findIndex((m) => m.senderRole === "driver" && Date.parse(m.createdAt) > baseline);
    }, [list, baseline]);

    useMarkReadOnView(chat.data, newest, myId);

    useEffect(() => {
        bottomRef.current?.scrollIntoView?.({ behavior: "smooth" });
    }, [lastId]);

    const submit = useCallback(
        (text: string, clientMessageId: string = newIdempotencyKey()) => {
            send.mutate(
                { chatId, text, clientMessageId, senderUserId: myId },
                {
                    onError: (e) => {
                        const code = isApiError(e) ? e.code : "";
                        toast.error(code === "already_exists" ? t("chat.room.openChatExists") : apiErrorText(e, t));
                    },
                }
            );
        },
        [chatId, myId, send, t]
    );

    const onSubmit = () => {
        const text = input.trim();
        if (!text) return;
        setInput("");
        submit(text);
    };

    const run = (a: ChatAction) =>
        action.mutate(
            { chatId, action: a },
            {
                onSuccess: () => {
                    if (a === "close") router.push("/app/chat");
                },
                onError: (e) => toast.error(isApiError(e) && e.code === "already_exists" ? t("chat.room.openChatExists") : apiErrorText(e, t)),
            }
        );

    if (chat.isPending) {
        return (
            <div className="flex min-h-[50vh] items-center justify-center" role="status" aria-busy="true">
                <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" aria-hidden />
                <span className="sr-only">{t("common.loading")}</span>
            </div>
        );
    }
    // A chat that does not exist or is outside the reader's scope (404), or a load error with a retry (UI3D): said on
    // the page, with the way back to the chat console, instead of a silent bounce or a bare red line.
    if (chat.isError) {
        return <RecordState error={chat.error} onRetry={() => void chat.refetch()} retrying={chat.isFetching} />;
    }

    const c = chat.data;
    const driverName = c.driverName || t("chat.room.driverFallback");
    const initial = driverName.charAt(0).toUpperCase() || "?";
    const mine = myId !== null && c.assignedAdminUserId === myId;
    const closed = c.status === "closed";
    const busy = action.isPending;
    const driverRead = c.driverLastReadAt ? Date.parse(c.driverLastReadAt) : null;

    return (
        <div className="flex h-full min-h-0 flex-col">
            <div className="shrink-0 border-b p-4">
                <PageHeader
                    title={t("nav.chat")}
                    recordName={driverName}
                    back={{ href: "/app/chat", label: t("chat.room.backToConsole") }}
                    description={
                        <span data-testid="chat-status">
                            {t(`chat.status.${c.status}`, c.status)}
                            {c.assignedAdminUserId && !mine && c.assignedAdminName ? ` · ${t("chat.room.assignedTo", { name: c.assignedAdminName })}` : ""}
                        </span>
                    }
                    actions={
                        !canSend ? null : closed ? (
                            <Button variant="outline" size="sm" onClick={() => run("reopen")} disabled={busy} className="gap-1">
                                <RotateCcw className="h-4 w-4" aria-hidden />
                                {t("chat.room.reopen")}
                            </Button>
                        ) : c.assignedAdminUserId === null ? (
                            <Button variant="outline" size="sm" onClick={() => run("take")} disabled={busy} className="gap-1">
                                <Hand className="h-4 w-4" aria-hidden />
                                {t("chat.room.take")}
                            </Button>
                        ) : mine ? (
                            <>
                                <Button variant="outline" size="sm" onClick={() => run("return")} disabled={busy} className="gap-1">
                                    <RotateCcw className="h-4 w-4" aria-hidden />
                                    {t("chat.room.returnToQueue")}
                                </Button>
                                <Button variant="default" size="sm" onClick={() => run("close")} disabled={busy} className="gap-1">
                                    <CheckCircle className="h-4 w-4" aria-hidden />
                                    {t("chat.room.close")}
                                </Button>
                            </>
                        ) : null
                    }
                />
            </div>

            <Card className="mx-4 mb-4 flex min-h-0 flex-1 flex-col rounded-lg">
                <CardContent className="min-h-0 flex-1 space-y-3 overflow-y-auto p-4" data-testid="chat-messages">
                    {messages.hasNextPage && (
                        <div className="flex justify-center">
                            <Button variant="ghost" size="sm" onClick={() => void messages.fetchNextPage()} disabled={messages.isFetchingNextPage}>
                                {messages.isFetchingNextPage ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                                {t("chat.room.olderMessages")}
                            </Button>
                        </div>
                    )}
                    {messages.isPending && (
                        <div className="flex justify-center py-8" role="status" aria-busy="true">
                            <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
                        </div>
                    )}
                    {messages.isError && <p className="text-center text-sm text-destructive">{apiErrorText(messages.error, t)}</p>}
                    {messages.isSuccess && list.length === 0 && <p className="py-8 text-center text-sm text-muted-foreground">{t("chat.room.empty")}</p>}
                    {list.map((m, index) => {
                        const day = localDayKey(m.createdAt);
                        const showDay = index === 0 || day !== localDayKey(list[index - 1].createdAt);
                        const staff = m.senderRole !== "driver";
                        const broadcast = m.messageType === "broadcast";
                        const href = safeHref(m.imageUrl);
                        const read = staff && m.local === undefined && driverRead !== null && driverRead >= Date.parse(m.createdAt);
                        return (
                            <Fragment key={m.clientMessageId ? `c:${m.clientMessageId}` : m.id}>
                                {showDay && (
                                    <div className="flex justify-center py-2" role="separator">
                                        <span className="rounded-full bg-muted/80 px-3 py-1 text-xs text-muted-foreground">
                                            {dayLabel(m.createdAt, language, t("chat.dateToday"), t("chat.dateYesterday"))}
                                        </span>
                                    </div>
                                )}
                                {firstUnread === index && (
                                    <div className="flex items-center gap-3 py-2" role="separator" aria-label={t("chat.unreadDivider")}>
                                        <div className="h-px flex-1 bg-primary/35" />
                                        <span className="shrink-0 text-xs font-medium text-primary">{t("chat.unreadDivider")}</span>
                                        <div className="h-px flex-1 bg-primary/35" />
                                    </div>
                                )}
                                <div className={`flex gap-2 ${staff ? "justify-end" : "justify-start"}`} data-testid="chat-message">
                                    {!staff && (
                                        <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-muted text-sm font-medium text-muted-foreground" title={driverName}>
                                            {initial}
                                        </div>
                                    )}
                                    <div className={`flex max-w-[75%] flex-col ${staff ? "items-end" : "items-start"}`}>
                                        {!staff && <span className="mb-0.5 px-1 text-xs text-muted-foreground">{m.senderName || driverName}</span>}
                                        <div className={`rounded-lg px-3 py-2 ${staff ? "bg-primary text-primary-foreground" : "bg-muted"} ${m.local === "failed" ? "opacity-70" : ""}`}>
                                            <p className="whitespace-pre-wrap text-sm wrap-break-word">
                                                {broadcast && (
                                                    <span className="mr-1" aria-hidden>
                                                        📢{" "}
                                                    </span>
                                                )}
                                                {m.text}
                                            </p>
                                            {href && (
                                                <a href={href} target="_blank" rel="noopener noreferrer" className="mt-1 block text-xs underline">
                                                    {t("chat.room.viewImage")}
                                                </a>
                                            )}
                                            <p className={`mt-1 text-xs ${staff ? "text-primary-foreground/80" : "text-muted-foreground"}`}>
                                                {clockLabel(m.createdAt, language)}
                                                {m.local === "sending" && <span className="ml-1.5">· {t("chat.room.sending")}</span>}
                                                {m.local === "syncing" && <span className="ml-1.5">· {t("chat.room.syncing")}</span>}
                                                {read && <span className="ml-1.5 opacity-90">· {t("chat.read")}</span>}
                                            </p>
                                        </div>
                                        {m.local === "failed" && m.clientMessageId && (
                                            <div className="mt-1 flex items-center gap-2 text-xs text-destructive" data-testid="chat-send-failed">
                                                <span>{t("chat.room.sendFailed")}</span>
                                                <button type="button" className="underline" onClick={() => submit(m.text ?? "", m.clientMessageId as string)}>
                                                    {t("chat.room.retry")}
                                                </button>
                                                <button type="button" className="underline" onClick={() => discardLocalMessage(client, chatId, m.clientMessageId as string)}>
                                                    {t("chat.room.discard")}
                                                </button>
                                            </div>
                                        )}
                                    </div>
                                </div>
                            </Fragment>
                        );
                    })}
                    <div ref={bottomRef} />
                </CardContent>
                {canSend && (
                    <CardHeader className="border-t pt-4">
                        <form
                            className="flex gap-2"
                            onSubmit={(e) => {
                                e.preventDefault();
                                onSubmit();
                            }}
                        >
                            <Input
                                value={input}
                                onChange={(e) => setInput(e.target.value)}
                                placeholder={closed ? t("chat.room.placeholderClosed") : t("chat.room.placeholder")}
                                maxLength={MAX_MESSAGE_LENGTH}
                                className="flex-1"
                                aria-label={t("chat.room.placeholder")}
                            />
                            <Button type="submit" disabled={!input.trim()} aria-label={t("chat.room.send")}>
                                <Send className="h-4 w-4" />
                            </Button>
                        </form>
                    </CardHeader>
                )}
            </Card>
        </div>
    );
}
