"use client";

// Leave requests: `GET /v1/leave-requests` in keyset pages of 50 (60 s poll; no listener on the whole
// collection) and `POST /v1/leave-requests/{id}/approve|reject|cancel` over Go while the `hr` web flag is on; the
// Firestore page otherwise (T46, issue #66; developer-spec.md §10.13). Each source is its own chunk.
import dynamic from "next/dynamic";

import { DomainPage, PageLoading } from "@/features/platform/components/DomainPage";

const GoPage = dynamic(() => import("@/features/leave/components/LeaveRequestsPage").then((m) => m.LeaveRequestsPage), { loading: () => <PageLoading /> });
const FirestorePage = dynamic(() => import("@/features/leave/legacy/LegacyLeaveRequestsPage"), { loading: () => <PageLoading /> });

export default function AdminLeaveRequestsPage() {
    return <DomainPage domain="hr" go={GoPage} firestore={FirestorePage} />;
}
