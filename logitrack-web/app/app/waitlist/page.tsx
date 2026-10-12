"use client";

// Waitlist: `GET /v1/waitlist` (keyset) and `DELETE /v1/waitlist/{id}` over Go (T54R, issue #74;
// developer-spec.md §10.13). The page is its own chunk.
import dynamic from "next/dynamic";

import { PageLoading } from "@/features/platform/components/PageLoading";

const GoPage = dynamic(() => import("@/features/waitlist/components/WaitlistPage").then((m) => m.WaitlistPage), { loading: () => <PageLoading /> });

export default function AdminWaitlistPage() {
    return <GoPage />;
}
