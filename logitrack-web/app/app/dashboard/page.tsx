"use client";

// Dashboard: one `GET /v1/dashboard/summary` (with the users-by-role, vehicle-location, incident and badge
// reads) over Go (T54R, issue #74; developer-spec.md §10.13). The page is its own chunk. The analytics
// (activity histogram) and package figures live here: `/app/analytics` and `/app/packages` have no page
// (§10.5).
import dynamic from "next/dynamic";

import { PageLoading } from "@/features/platform/components/PageLoading";

const GoPage = dynamic(() => import("@/features/dashboard/components/DashboardPage").then((m) => m.DashboardPage), { loading: () => <PageLoading /> });

export default function AdminDashboardPage() {
    return <GoPage />;
}
