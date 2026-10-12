"use client";

// The chat room (T50): `GET /v1/chats/{id}` and its messages in keyset pages of 50 over Go, new messages
// pushed by SSE `chat:{id}`.
import { Suspense } from "react";
import dynamic from "next/dynamic";

import { PageLoading } from "@/features/platform/components/PageLoading";

const GoPage = dynamic(() => import("@/features/chat/components/ChatRoomGo").then((m) => m.ChatRoomGo), { loading: () => <PageLoading /> });

export default function AdminChatRoomPage() {
    return (
        <Suspense fallback={<PageLoading />}>
            <GoPage />
        </Suspense>
    );
}
