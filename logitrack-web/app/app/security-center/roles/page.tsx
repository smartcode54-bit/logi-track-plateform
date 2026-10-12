"use client";

// Role & permission matrix: `GET`/`PUT /v1/roles/matrix` (one call) over Go (T54S, pulled forward at the
// owner's request; developer-spec.md §10.13, §12.2). The page is its own chunk.
import dynamic from "next/dynamic";

import { PageLoading } from "@/features/platform/components/PageLoading";

const GoPage = dynamic(() => import("@/features/security-center/components/RoleMatrixPage").then((m) => m.RoleMatrixPage), { loading: () => <PageLoading /> });

export default function RolePermissionMatrixPage() {
    return <GoPage />;
}
