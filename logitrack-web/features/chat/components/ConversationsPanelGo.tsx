"use client";

/**
 * The conversations panel over Go (T50; Appendix E §E.2.1, §E.5 rows 1-2): the queue and the caller's chats
 * are `['chats','queued' | 'mine']` (keyset pages of 50; closed chats left out and unread counts computed by
 * Go), kept current by `tenant:{tid}:chats` (60 s poll only while the stream is down), instead of two
 * listeners on whole collections. Starting a chat picks a driver in the shared driver picker (UI2), which
 * searches on the server (`['drivers',{fields:'minimal',q}]`, debounced) instead of filtering 200 drivers
 * read in the browser.
 */
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useInfiniteQuery } from "@tanstack/react-query";
import { Inbox, Loader2, Megaphone, MessageCircle, Search, User } from "lucide-react";

import { useLanguage } from "@/context/language";
import { useCan } from "@/features/auth/api/useMe";
import { apiErrorText } from "@/lib/apiError";
import { useStreamFallbackPoll } from "@/lib/realtime/polling";
import { DriverSelect } from "@/components/entity-pickers";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";

import { chatListQuery, chatRows, type ChatDTO, type ChatListFilter } from "../api/chats.go";
import { chatTimeLabel } from "./chatFormat";



/** Starts a chat: the shared driver picker (UI2) over the server search; picking a driver opens the chat. */
function DriverSearch() {
    const { t } = useLanguage();
    const router = useRouter();
    return (
        <DriverSelect
            value=""
            onValueChange={(id) => {
                if (id) router.push(`/app/chat/with-driver?driverId=${encodeURIComponent(id)}`);
            }}
            placeholder={t("chat.panel.searchDriver")}
            searchPlaceholder={t("chat.panel.searchDriver")}
            emptyText={t("chat.panel.noDriver")}
            aria-label={t("chat.panel.searchDriver")}
            icon={<Search aria-hidden className="h-4 w-4 text-muted-foreground" />}
            className="text-sm"
            data-testid="chat-driver-search"
        />
    );
}

function ChatList({ filter }: { filter: ChatListFilter }) {
    const { t, language } = useLanguage();
    const list = useInfiniteQuery({ ...chatListQuery(filter), ...useStreamFallbackPoll() });
    const rows = chatRows(list.data?.pages);
    if (list.isPending) {
        return (
            <div className="flex justify-center py-8" role="status" aria-busy="true">
                <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
            </div>
        );
    }
    if (list.isError && rows.length === 0) {
        return <p className="p-4 text-sm text-destructive">{apiErrorText(list.error, t)}</p>;
    }
    if (rows.length === 0) {
        return (
            <div className="py-12 text-center text-muted-foreground">
                <MessageCircle className="mx-auto mb-2 h-12 w-12 opacity-50" />
                <p>{t("chat.panel.noChats")}</p>
            </div>
        );
    }
    return (
        <>
            <ul className="divide-y divide-border" data-testid={`chat-list-${filter}`}>
                {rows.map((c) => (
                    <ChatRow key={c.id} chat={c} language={language} noMessages={t("chat.panel.noMessages")} />
                ))}
            </ul>
            {list.hasNextPage && (
                <div className="flex justify-center p-3">
                    <Button variant="outline" size="sm" onClick={() => void list.fetchNextPage()} disabled={list.isFetchingNextPage}>
                        {list.isFetchingNextPage ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                        {t("chat.panel.loadMore")}
                    </Button>
                </div>
            )}
        </>
    );
}

function ChatRow({ chat, language, noMessages }: { chat: ChatDTO; language: string; noMessages: string }) {
    const { t } = useLanguage();
    return (
        <li>
            <Link href={`/app/chat/room?chatId=${encodeURIComponent(chat.id)}`} prefetch={false} className="flex items-center gap-4 p-4 transition-colors hover:bg-muted/50">
                <div className="flex h-10 w-10 items-center justify-center rounded-full bg-muted">
                    <User className="h-5 w-5 text-muted-foreground" />
                </div>
                <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                        <span className="truncate font-medium">{chat.driverName || t("chat.room.driverFallback")}</span>
                        {chat.priority === "urgent" && (
                            <Badge variant="destructive" className="shrink-0">
                                {t("chat.panel.urgent")}
                            </Badge>
                        )}
                    </div>
                    <p className="truncate text-sm text-muted-foreground">
                        {chat.lastMessageType === "broadcast" && <span aria-hidden>📢 </span>}
                        {chat.lastMessagePreview || noMessages}
                    </p>
                </div>
                <div className="flex shrink-0 flex-col items-end gap-1">
                    <span className="text-xs text-muted-foreground">{chatTimeLabel(chat.lastMessageAt, language)}</span>
                    {chat.unreadForMe > 0 && (
                        <Badge className="h-5 min-w-5 justify-center px-1.5 text-xs" aria-label={t("chat.panel.unread", { count: chat.unreadForMe })}>
                            {chat.unreadForMe > 99 ? "99+" : chat.unreadForMe}
                        </Badge>
                    )}
                </div>
            </Link>
        </li>
    );
}

function CountBadge({ filter }: { filter: ChatListFilter }) {
    // The same cache entry as the list (no extra request): the loaded rows, "+" while more pages exist.
    const list = useInfiniteQuery({ ...chatListQuery(filter), ...useStreamFallbackPoll() });
    const n = chatRows(list.data?.pages).length;
    if (n === 0) return null;
    return (
        <Badge variant="secondary" className="ml-1 px-1 text-xs">
            {n}
            {list.hasNextPage ? "+" : ""}
        </Badge>
    );
}

export function ConversationsPanelGo() {
    const { t } = useLanguage();
    const canBroadcast = useCan("broadcasts:view").allowed;
    return (
        <div className="flex h-full flex-col">
            <div className="space-y-2 border-b p-3">
                <h2 className="text-sm font-semibold">{t("chat.shell.conversations")}</h2>
                <DriverSearch />
                {canBroadcast && (
                    <Button variant="default" size="sm" className="w-full gap-2" asChild>
                        <Link href="/app/chat?view=broadcast" prefetch={false}>
                            <Megaphone className="h-4 w-4" />
                            {t("chat.panel.newBroadcast")}
                        </Link>
                    </Button>
                )}
            </div>
            <Tabs defaultValue="queued" className="flex min-h-0 flex-1 flex-col">
                <TabsList className="mx-2 mt-2 grid w-full shrink-0 grid-cols-2">
                    <TabsTrigger value="queued" className="gap-1 text-xs">
                        <Inbox className="h-3.5 w-3.5" />
                        {t("chat.panel.queued")}
                        <CountBadge filter="queue" />
                    </TabsTrigger>
                    <TabsTrigger value="mychats" className="gap-1 text-xs">
                        <MessageCircle className="h-3.5 w-3.5" />
                        {t("chat.panel.mine")}
                        <CountBadge filter="mine" />
                    </TabsTrigger>
                </TabsList>
                <TabsContent value="queued" className="mt-2 min-h-0 flex-1 data-[state=inactive]:hidden">
                    <Card className="flex h-full flex-col rounded-none border-0 shadow-none">
                        <CardHeader className="px-3 py-2">
                            <CardTitle className="text-xs font-medium text-muted-foreground">{t("chat.panel.queuedHint")}</CardTitle>
                        </CardHeader>
                        <CardContent className="min-h-0 flex-1 overflow-y-auto p-0">
                            <ChatList filter="queue" />
                        </CardContent>
                    </Card>
                </TabsContent>
                <TabsContent value="mychats" className="mt-2 min-h-0 flex-1 data-[state=inactive]:hidden">
                    <Card className="flex h-full flex-col rounded-none border-0 shadow-none">
                        <CardHeader className="px-3 py-2">
                            <CardTitle className="text-xs font-medium text-muted-foreground">{t("chat.panel.mineHint")}</CardTitle>
                        </CardHeader>
                        <CardContent className="min-h-0 flex-1 overflow-y-auto p-0">
                            <ChatList filter="mine" />
                        </CardContent>
                    </Card>
                </TabsContent>
            </Tabs>
        </div>
    );
}
