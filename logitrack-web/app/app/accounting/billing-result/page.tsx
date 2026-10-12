"use client";

// Billing results: `GET /v1/billing/statements` with the stored documents of `statement_documents` over Go
// (T42, P3; developer-spec.md §10.13, Appendix E §E.2.3). The page is its own chunk.
import dynamic from "next/dynamic";

import { PageLoading } from "@/features/platform/components/PageLoading";

const GoPage = dynamic(() => import("@/features/accounting/components/go/BillingResultPage").then((m) => m.BillingResultPage), { loading: () => <PageLoading /> });

export default function BillingResultRoute() {
    return <GoPage />;
}
