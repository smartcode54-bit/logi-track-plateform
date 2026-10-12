"use client";

// Standby records: `GET /v1/standby?from&to` (keyset, SSE `standby.*`) over Go while the `operations` web flag is on; the Firestore page otherwise (T34, P2;
// developer-spec.md §10.13, Appendix E §E.2.1). Each source is its own chunk.
import dynamic from "next/dynamic";

import { DomainPage, PageLoading } from "@/features/platform/components/DomainPage";

const GoPage = dynamic(() => import("@/features/standby/components/go/StandbyRecordsPage").then((m) => m.StandbyRecordsPage), { loading: () => <PageLoading /> });
const FirestorePage = dynamic(() => import("@/features/standby/legacy/LegacyStandbyRecordsPage"), { loading: () => <PageLoading /> });

export default function StandbyRecordsRoute() {
    return <DomainPage domain="operations" go={GoPage} firestore={FirestorePage} />;
}
