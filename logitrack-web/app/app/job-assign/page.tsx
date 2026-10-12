"use client";

// Job assigning: `GET /v1/tasks?date` over both types (keyset, SSE `task.*`) over Go (T34, P2;
// developer-spec.md §10.13, Appendix E §E.2.1). The page is its own chunk.
import dynamic from "next/dynamic";

import { PageLoading } from "@/features/platform/components/PageLoading";

const GoPage = dynamic(() => import("@/features/tasks/components/go/TaskBoardPage").then((m) => m.JobAssignBoardPage), { loading: () => <PageLoading /> });

export default function JobAssignRoute() {
    return <GoPage />;
}
