"use client";

// The chat console (T50, issue #70; developer-spec.md §10.13): over Go while the `comms` web flag is on
// (`GET /v1/chats*`, `/v1/broadcasts*`, SSE `tenant:{tid}:chats` and the room's `chat:{id}`), the Firestore
// console otherwise. Each source is its own chunk; the pages below switch the same way.
import dynamic from "next/dynamic";

import { useDomainPageSource } from "@/features/platform/api/useWebFlags";
import { PageLoading } from "@/features/platform/components/DomainPage";

const GoLayout = dynamic(() => import("@/features/chat/components/ChatLayoutGo").then((m) => m.ChatLayoutGo), { loading: () => <PageLoading /> });
const FirestoreLayout = dynamic(() => import("@/features/chat/legacy/LegacyChatLayout"), { loading: () => <PageLoading /> });

export default function ChatLayout({ children }: { children: React.ReactNode }) {
    const source = useDomainPageSource("comms");
    if (source === undefined) return <PageLoading />;
    const Layout = source === "go" ? GoLayout : FirestoreLayout;
    return <Layout>{children}</Layout>;
}
