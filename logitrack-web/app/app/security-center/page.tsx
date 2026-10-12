"use client";

// Security Center overview: `GET /v1/security/overview`, `/v1/stats/users-by-role`, `/v1/security-events` and
// the active users (`GET /v1/users`, 60 s poll) over Go (T54S, pulled forward at the owner's request;
// developer-spec.md §10.13, §12.2). The page is its own chunk.
import dynamic from "next/dynamic";

import { PageLoading } from "@/features/platform/components/PageLoading";

const GoPage = dynamic(() => import("@/features/security-center/components/SecurityOverviewPage").then((m) => m.SecurityOverviewPage), { loading: () => <PageLoading /> });

export default function SecurityCenterOverviewPage() {
    return <GoPage />;
}
