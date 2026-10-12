"use client";

// Standby records: `GET /v1/standby?from&to` (keyset, SSE `standby.*`) over Go (T34, P2; developer-spec.md
// §10.13, Appendix E §E.2.1). The page is its own chunk.
import dynamic from "next/dynamic";

import { PageLoading } from "@/features/platform/components/PageLoading";

const GoPage = dynamic(() => import("@/features/standby/components/go/StandbyRecordsPage").then((m) => m.StandbyRecordsPage), { loading: () => <PageLoading /> });

export default function StandbyRecordsRoute() {
    return <GoPage />;
}
