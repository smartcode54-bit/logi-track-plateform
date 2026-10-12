"use client";

// Job assigning: `GET /v1/tasks?date` over both types (keyset, SSE `task.*`) over Go while the `operations` web flag is on; the Firestore page otherwise (T34, P2;
// developer-spec.md §10.13, Appendix E §E.2.1). Each source is its own chunk.
import dynamic from "next/dynamic";

import { DomainPage, PageLoading } from "@/features/platform/components/DomainPage";

const GoPage = dynamic(() => import("@/features/tasks/components/go/TaskBoardPage").then((m) => m.JobAssignBoardPage), { loading: () => <PageLoading /> });
const FirestorePage = dynamic(() => import("@/features/tasks/legacy/LegacyJobAssignPage"), { loading: () => <PageLoading /> });

export default function JobAssignRoute() {
    return <DomainPage domain="operations" go={GoPage} firestore={FirestorePage} />;
}
