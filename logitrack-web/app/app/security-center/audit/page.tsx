"use client";

// Security audit: the keyset `GET /v1/security-events` over Go (T54S, pulled forward at the owner's request;
// developer-spec.md §10.13, §12.2). The page is its own chunk.
import dynamic from "next/dynamic";

import { PageLoading } from "@/features/platform/components/PageLoading";

const GoPage = dynamic(() => import("@/features/security-center/components/SecurityAuditPage").then((m) => m.SecurityAuditPage), { loading: () => <PageLoading /> });

export default function SecurityAuditRoute() {
    return <GoPage />;
}
