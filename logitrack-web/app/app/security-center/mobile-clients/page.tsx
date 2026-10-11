"use client";

// Mobile clients: `GET /v1/app-installations[/stats]` over Go while the `mobile_release` web flag is on; the
// Firestore page otherwise (T54S, pulled forward at the owner's request; developer-spec.md §10.13, §12.2).
// Each source is its own chunk.
import dynamic from "next/dynamic";

import { DomainPage, PageLoading } from "@/features/platform/components/DomainPage";

const GoPage = dynamic(() => import("@/features/mobile-release/components/MobileClientsPage").then((m) => m.MobileClientsPage), { loading: () => <PageLoading /> });
const FirestorePage = dynamic(() => import("@/features/mobile-release/legacy/LegacyMobileClientsPage"), { loading: () => <PageLoading /> });

export default function MobileClientsRoute() {
    return <DomainPage domain="mobile_release" go={GoPage} firestore={FirestorePage} />;
}
