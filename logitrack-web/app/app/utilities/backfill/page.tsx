"use client";

// Billing backfills: `POST /v1/jobs/billing.backfill-trips|backfill-standby` followed through `['jobs', id]`
// over Go (T42, P3; developer-spec.md §10.13, Appendix E §E.2.3). The page is its own chunk.
import dynamic from "next/dynamic";

import { PageLoading } from "@/features/platform/components/PageLoading";

const GoPage = dynamic(() => import("@/features/accounting/components/go/BackfillUtilitiesPage").then((m) => m.BackfillUtilitiesPage), { loading: () => <PageLoading /> });

export default function BackfillRoute() {
    return <GoPage />;
}
