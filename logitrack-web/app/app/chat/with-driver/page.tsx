"use client";

// Opens a driver's chat (T50): one `POST /v1/chats {driverId}` over Go while the `comms` web flag is on, the
// Firestore lookups otherwise.
import { Suspense } from "react";
import dynamic from "next/dynamic";

import { DomainPage, PageLoading } from "@/features/platform/components/DomainPage";

const GoPage = dynamic(() => import("@/features/chat/components/ChatWithDriverGo").then((m) => m.ChatWithDriverGo), { loading: () => <PageLoading /> });
const FirestorePage = dynamic(() => import("@/features/chat/legacy/LegacyChatWithDriverPage"), { loading: () => <PageLoading /> });

export default function ChatWithDriverPage() {
    return (
        <Suspense fallback={<PageLoading />}>
            <DomainPage domain="comms" go={GoPage} firestore={FirestorePage} />
        </Suspense>
    );
}
