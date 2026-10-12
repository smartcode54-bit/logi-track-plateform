"use client";

// Dashboard: one `GET /v1/dashboard/summary` (with the users-by-role, vehicle-location, incident and
// badge reads) over Go while the `dashboard` web flag is on; the Firestore page otherwise (T54R, issue
// #74; developer-spec.md §10.13). Each source is its own chunk. The analytics (activity histogram) and
// package figures live here: `/app/analytics` and `/app/packages` have no page (§10.5).
import dynamic from "next/dynamic";

import { DomainPage, PageLoading } from "@/features/platform/components/DomainPage";

const GoPage = dynamic(() => import("@/features/dashboard/components/DashboardPage").then((m) => m.DashboardPage), { loading: () => <PageLoading /> });
const FirestorePage = dynamic(() => import("@/features/dashboard/legacy/LegacyDashboardPage"), { loading: () => <PageLoading /> });

export default function AdminDashboardPage() {
    return <DomainPage domain="dashboard" go={GoPage} firestore={FirestorePage} />;
}
