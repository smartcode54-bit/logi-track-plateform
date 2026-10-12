"use client";

// Mobile clients: `GET /v1/app-installations[/stats]` over Go (T54S, pulled forward at the owner's request;
// developer-spec.md §10.13, §12.2). The page is its own chunk.
import dynamic from "next/dynamic";

import { PageLoading } from "@/features/platform/components/PageLoading";

const GoPage = dynamic(() => import("@/features/mobile-release/components/MobileClientsPage").then((m) => m.MobileClientsPage), { loading: () => <PageLoading /> });

export default function MobileClientsRoute() {
    return <GoPage />;
}
