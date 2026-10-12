"use client";

// The chat console's home and broadcast composer (T50): `GET/POST/DELETE /v1/broadcasts` over Go while the
// `comms` web flag is on, the Firestore page otherwise. Each source is its own chunk.
import { Suspense } from "react";
import dynamic from "next/dynamic";

import { DomainPage, PageLoading } from "@/features/platform/components/DomainPage";

const GoPage = dynamic(() => import("@/features/chat/components/ChatHomeGo").then((m) => m.ChatHomeGo), { loading: () => <PageLoading /> });
const FirestorePage = dynamic(() => import("@/features/chat/legacy/LegacyChatHome"), { loading: () => <PageLoading /> });

export default function AdminChatPage() {
    return (
        <Suspense fallback={<PageLoading />}>
            <DomainPage domain="comms" go={GoPage} firestore={FirestorePage} />
        </Suspense>
    );
}
