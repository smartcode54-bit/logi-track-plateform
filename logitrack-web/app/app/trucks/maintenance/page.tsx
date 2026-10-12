"use client";

// A truck's maintenance: `GET /v1/maintenance?truckId`, `POST`/`PATCH /v1/maintenance` over Go while the `billing` web flag is on; the Firestore page otherwise (T42, P3;
// developer-spec.md §10.13, Appendix E §E.2.3). Each source is its own chunk.
import dynamic from "next/dynamic";

import { DomainPage, PageLoading } from "@/features/platform/components/DomainPage";

const GoPage = dynamic(() => import("@/features/maintenance/components/go/TruckMaintenancePage").then((m) => m.TruckMaintenancePage), { loading: () => <PageLoading /> });
const FirestorePage = dynamic(() => import("@/features/maintenance/legacy/LegacyMaintenanceDashboardPage"), { loading: () => <PageLoading /> });

export default function TruckMaintenanceRoute() {
    return <DomainPage domain="billing" go={GoPage} firestore={FirestorePage} />;
}
