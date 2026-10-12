"use client";

// The chat console (T50, issue #70; developer-spec.md §10.13): over Go (`GET /v1/chats*`, `/v1/broadcasts*`,
// SSE `tenant:{tid}:chats` and the room's `chat:{id}`). The layout is its own chunk.
import dynamic from "next/dynamic";

import { PageLoading } from "@/features/platform/components/PageLoading";

const GoLayout = dynamic(() => import("@/features/chat/components/ChatLayoutGo").then((m) => m.ChatLayoutGo), { loading: () => <PageLoading /> });

export default function ChatLayout({ children }: { children: React.ReactNode }) {
    return <GoLayout>{children}</GoLayout>;
}
