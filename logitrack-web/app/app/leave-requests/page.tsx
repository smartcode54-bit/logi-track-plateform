"use client";

// Leave requests: `GET /v1/leave-requests` in keyset pages of 50 (60 s poll; no listener on the whole
// collection) and `POST /v1/leave-requests/{id}/approve|reject|cancel` over Go (T46, issue #66;
// developer-spec.md §10.13). The page is its own chunk.
import dynamic from "next/dynamic";

import { PageLoading } from "@/features/platform/components/PageLoading";

const GoPage = dynamic(() => import("@/features/leave/components/LeaveRequestsPage").then((m) => m.LeaveRequestsPage), { loading: () => <PageLoading /> });

export default function AdminLeaveRequestsPage() {
    return <GoPage />;
}
