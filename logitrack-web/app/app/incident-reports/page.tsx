"use client";

// Incident reports: `GET /v1/incidents?from&to` (keyset, SSE `incident.created`) over Go (T34, P2;
// developer-spec.md §10.13, Appendix E §E.2.1). The page is its own chunk.
import dynamic from "next/dynamic";

import { PageLoading } from "@/features/platform/components/PageLoading";

const GoPage = dynamic(() => import("@/features/incident-reports/components/go/IncidentReportsPage").then((m) => m.IncidentReportsPage), { loading: () => <PageLoading /> });

export default function IncidentReportsRoute() {
    return <GoPage />;
}
