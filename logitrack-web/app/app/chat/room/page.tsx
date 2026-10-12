"use client";

// The chat room (T50): `GET /v1/chats/{id}` and its messages in keyset pages of 50 over Go, new messages
// pushed by SSE `chat:{id}`, while the `comms` web flag is on; the Firestore listeners otherwise.
import { Suspense } from "react";
import dynamic from "next/dynamic";

import { DomainPage, PageLoading } from "@/features/platform/components/DomainPage";

const GoPage = dynamic(() => import("@/features/chat/components/ChatRoomGo").then((m) => m.ChatRoomGo), { loading: () => <PageLoading /> });
const FirestorePage = dynamic(() => import("@/features/chat/legacy/LegacyChatRoomPage"), { loading: () => <PageLoading /> });

export default function AdminChatRoomPage() {
    return (
        <Suspense fallback={<PageLoading />}>
            <DomainPage domain="comms" go={GoPage} firestore={FirestorePage} />
        </Suspense>
    );
}
