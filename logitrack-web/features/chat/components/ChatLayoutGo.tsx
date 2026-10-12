"use client";

/**
 * The chat console over Go (T50, issue #70; the only console since TW7): the shell with the
 * Go conversations panel and driver panel. The room's driver comes from `['chat', id]`, the cache entry the
 * room reads too (one request, instead of the layout's own read of the chat document, `chat/layout.tsx:69`).
 */
import type { ReactNode } from "react";
import { usePathname, useSearchParams } from "next/navigation";
import { useQuery } from "@tanstack/react-query";

import { useStreamFallbackPoll } from "@/lib/realtime/polling";
import { chatQuery } from "../api/chats.go";
import { ChatShell } from "./ChatShell";
import { ConversationsPanelGo } from "./ConversationsPanelGo";
import { DriverPanelGo } from "./DriverPanelGo";

export function ChatLayoutGo({ children }: { children: ReactNode }) {
    const pathname = usePathname();
    const searchParams = useSearchParams();
    const chatId = pathname?.includes("/room") ? (searchParams?.get("chatId") ?? "") : "";
    const chat = useQuery({ ...chatQuery(chatId), ...useStreamFallbackPoll() });
    return (
        <ChatShell conversations={<ConversationsPanelGo />} driverPanel={<DriverPanelGo chatSelected={chatId !== ""} driverId={chat.data?.driverId ?? null} />}>
            {children}
        </ChatShell>
    );
}
