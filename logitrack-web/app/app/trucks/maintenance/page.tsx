"use client";

// A truck's maintenance: `GET /v1/maintenance?truckId`, `POST`/`PATCH /v1/maintenance` over Go (T42, P3;
// developer-spec.md §10.13, Appendix E §E.2.3). The page is its own chunk.
import dynamic from "next/dynamic";

import { PageLoading } from "@/features/platform/components/PageLoading";

const GoPage = dynamic(() => import("@/features/maintenance/components/go/TruckMaintenancePage").then((m) => m.TruckMaintenancePage), { loading: () => <PageLoading /> });

export default function TruckMaintenanceRoute() {
    return <GoPage />;
}
