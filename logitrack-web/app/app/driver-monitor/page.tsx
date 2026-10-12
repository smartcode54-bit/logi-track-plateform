"use client";

// Driver monitor: `GET /v1/trips/monitor` (server join, keyset 200, facets; SSE `trip.*` / `task.*` 2 s) over
// Go (T34, P2; developer-spec.md §10.13, Appendix E §E.2.1). The page is its own chunk.
import dynamic from "next/dynamic";

import { PageLoading } from "@/features/platform/components/PageLoading";

const GoPage = dynamic(() => import("@/features/drivers/components/go/DriverMonitorPage").then((m) => m.DriverMonitorPage), { loading: () => <PageLoading /> });

export default function DriverMonitorRoute() {
    return <GoPage />;
}
