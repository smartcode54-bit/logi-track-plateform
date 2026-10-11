"use client";

// Role & permission matrix: `GET`/`PUT /v1/roles/matrix` (one call) over Go while the `security` web flag is
// on; the Firestore page otherwise (T54S, pulled forward at the owner's request; developer-spec.md §10.13,
// §12.2). Each source is its own chunk.
import dynamic from "next/dynamic";

import { DomainPage, PageLoading } from "@/features/platform/components/DomainPage";

const GoPage = dynamic(() => import("@/features/security-center/components/RoleMatrixPage").then((m) => m.RoleMatrixPage), { loading: () => <PageLoading /> });
const FirestorePage = dynamic(() => import("@/features/security-center/legacy/LegacyRolesPage"), { loading: () => <PageLoading /> });

export default function RolePermissionMatrixPage() {
    return <DomainPage domain="security" go={GoPage} firestore={FirestorePage} />;
}
