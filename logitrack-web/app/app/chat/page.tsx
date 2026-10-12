"use client";

// The chat console's home and broadcast composer (T50): `GET/POST/DELETE /v1/broadcasts` over Go. The page is
// its own chunk.
import { Suspense } from "react";
import dynamic from "next/dynamic";

import { PageLoading } from "@/features/platform/components/PageLoading";

const GoPage = dynamic(() => import("@/features/chat/components/ChatHomeGo").then((m) => m.ChatHomeGo), { loading: () => <PageLoading /> });

export default function AdminChatPage() {
    return (
        <Suspense fallback={<PageLoading />}>
            <GoPage />
        </Suspense>
    );
}
