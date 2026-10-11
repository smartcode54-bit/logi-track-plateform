"use client";

// Maintenance overview: `GET /v1/maintenance` (keyset pages, server status and type filters) over Go while the `billing` web flag is on; the Firestore page otherwise (T42, P3;
// developer-spec.md §10.13, Appendix E §E.2.3). Each source is its own chunk.
import dynamic from "next/dynamic";

import { DomainPage, PageLoading } from "@/features/platform/components/DomainPage";

const GoPage = dynamic(() => import("@/features/maintenance/components/go/MaintenanceOverviewPage").then((m) => m.MaintenanceOverviewPage), { loading: () => <PageLoading /> });
const FirestorePage = dynamic(() => import("@/features/maintenance/legacy/LegacyMaintenanceOverviewPage"), { loading: () => <PageLoading /> });

export default function MaintenanceOverviewRoute() {
    return <DomainPage domain="billing" go={GoPage} firestore={FirestorePage} />;
}
