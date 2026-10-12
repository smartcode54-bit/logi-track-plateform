"use client";

// Driver monitor: `GET /v1/trips/monitor` (server join, keyset 200, facets; SSE `trip.*` / `task.*` 2 s) over Go while the `operations` web flag is on; the Firestore page otherwise (T34, P2;
// developer-spec.md §10.13, Appendix E §E.2.1). Each source is its own chunk.
import dynamic from "next/dynamic";

import { DomainPage, PageLoading } from "@/features/platform/components/DomainPage";

const GoPage = dynamic(() => import("@/features/drivers/components/go/DriverMonitorPage").then((m) => m.DriverMonitorPage), { loading: () => <PageLoading /> });
const FirestorePage = dynamic(() => import("@/features/drivers/legacy/LegacyDriverMonitorPage"), { loading: () => <PageLoading /> });

export default function DriverMonitorRoute() {
    return <DomainPage domain="operations" go={GoPage} firestore={FirestorePage} />;
}
