"use client";

// Incident reports: `GET /v1/incidents?from&to` (keyset, SSE `incident.created`) over Go while the `operations` web flag is on; the Firestore page otherwise (T34, P2;
// developer-spec.md §10.13, Appendix E §E.2.1). Each source is its own chunk.
import dynamic from "next/dynamic";

import { DomainPage, PageLoading } from "@/features/platform/components/DomainPage";

const GoPage = dynamic(() => import("@/features/incident-reports/components/go/IncidentReportsPage").then((m) => m.IncidentReportsPage), { loading: () => <PageLoading /> });
const FirestorePage = dynamic(() => import("@/features/incident-reports/legacy/LegacyIncidentReportsPage"), { loading: () => <PageLoading /> });

export default function IncidentReportsRoute() {
    return <DomainPage domain="operations" go={GoPage} firestore={FirestorePage} />;
}
