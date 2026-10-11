"use client";

// Security Center overview: `GET /v1/security/overview`, `/v1/stats/users-by-role`, `/v1/security-events` and
// the active users (`GET /v1/users`, 60 s poll) over Go while the `security` web flag is on; the Firestore
// page otherwise (T54S, pulled forward at the owner's request; developer-spec.md §10.13, §12.2). Each source
// is its own chunk.
import dynamic from "next/dynamic";

import { DomainPage, PageLoading } from "@/features/platform/components/DomainPage";

const GoPage = dynamic(() => import("@/features/security-center/components/SecurityOverviewPage").then((m) => m.SecurityOverviewPage), { loading: () => <PageLoading /> });
const FirestorePage = dynamic(() => import("@/features/security-center/legacy/LegacySecurityOverviewPage"), { loading: () => <PageLoading /> });

export default function SecurityCenterOverviewPage() {
    return <DomainPage domain="security" go={GoPage} firestore={FirestorePage} />;
}
