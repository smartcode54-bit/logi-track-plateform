"use client";

// System status: `GET /v1/security/status` over Go (T54S, pulled forward at the owner's request;
// developer-spec.md §10.13, §12.2). The page is its own chunk.
import dynamic from "next/dynamic";

import { PageLoading } from "@/features/platform/components/PageLoading";

const GoPage = dynamic(() => import("@/features/security-center/components/SystemStatusPage").then((m) => m.SystemStatusPage), { loading: () => <PageLoading /> });

export default function SystemStatusRoute() {
    return <GoPage />;
}
