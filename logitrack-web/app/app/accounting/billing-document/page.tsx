"use client";

// Billing Document: `GET /v1/billing/rows` (+ diagnostics, missing billing date) and `POST
// /v1/billing/statements`, documents rendered by Go over Go (T42, P3; developer-spec.md §10.13, Appendix E
// §E.2.3). The page is its own chunk.
import dynamic from "next/dynamic";

import { PageLoading } from "@/features/platform/components/PageLoading";

const GoPage = dynamic(() => import("@/features/accounting/components/go/BillingDocumentPage").then((m) => m.BillingDocumentPage), { loading: () => <PageLoading /> });

export default function BillingDocumentRoute() {
    return <GoPage />;
}
