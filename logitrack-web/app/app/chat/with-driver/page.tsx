"use client";

// Opens a driver's chat (T50): one `POST /v1/chats {driverId}` over Go.
import { Suspense } from "react";
import dynamic from "next/dynamic";

import { PageLoading } from "@/features/platform/components/PageLoading";

const GoPage = dynamic(() => import("@/features/chat/components/ChatWithDriverGo").then((m) => m.ChatWithDriverGo), { loading: () => <PageLoading /> });

export default function ChatWithDriverPage() {
    return (
        <Suspense fallback={<PageLoading />}>
            <GoPage />
        </Suspense>
    );
}
