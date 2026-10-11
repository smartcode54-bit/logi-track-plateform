"use client";

// Security audit: the keyset `GET /v1/security-events` over Go while the `security` web flag is on; the
// Firestore page otherwise (T54S, pulled forward at the owner's request; developer-spec.md §10.13, §12.2).
// Each source is its own chunk.
import dynamic from "next/dynamic";

import { DomainPage, PageLoading } from "@/features/platform/components/DomainPage";

const GoPage = dynamic(() => import("@/features/security-center/components/SecurityAuditPage").then((m) => m.SecurityAuditPage), { loading: () => <PageLoading /> });
const FirestorePage = dynamic(() => import("@/features/security-center/legacy/LegacyAuditPage"), { loading: () => <PageLoading /> });

export default function SecurityAuditRoute() {
    return <DomainPage domain="security" go={GoPage} firestore={FirestorePage} />;
}
