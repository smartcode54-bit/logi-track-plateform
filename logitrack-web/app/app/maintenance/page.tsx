"use client";

// Maintenance overview: `GET /v1/maintenance` (keyset pages, server status and type filters) over Go (T42,
// P3; developer-spec.md §10.13, Appendix E §E.2.3). The page is its own chunk.
import dynamic from "next/dynamic";

import { PageLoading } from "@/features/platform/components/PageLoading";

const GoPage = dynamic(() => import("@/features/maintenance/components/go/MaintenanceOverviewPage").then((m) => m.MaintenanceOverviewPage), { loading: () => <PageLoading /> });

export default function MaintenanceOverviewRoute() {
    return <GoPage />;
}
