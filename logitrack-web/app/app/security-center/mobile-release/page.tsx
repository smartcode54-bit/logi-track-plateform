"use client";

// Mobile release: `GET /v1/app-releases` and `PUT /v1/app-releases/floor` over Go (ADR 0007 guards kept)
// while the `mobile_release` web flag is on; the Firestore page otherwise (T54S, pulled forward at the
// owner's request; developer-spec.md §10.13, §12.2). Each source is its own chunk.
import dynamic from "next/dynamic";

import { DomainPage, PageLoading } from "@/features/platform/components/DomainPage";

const GoPage = dynamic(() => import("@/features/mobile-release/components/MobileReleasePage").then((m) => m.MobileReleasePage), { loading: () => <PageLoading /> });
const FirestorePage = dynamic(() => import("@/features/mobile-release/legacy/LegacyMobileReleasePage"), { loading: () => <PageLoading /> });

export default function MobileReleaseRoute() {
    return <DomainPage domain="mobile_release" go={GoPage} firestore={FirestorePage} />;
}
