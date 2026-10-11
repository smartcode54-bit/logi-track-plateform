"use client";

// Billing results: `GET /v1/billing/statements` with the stored documents of `statement_documents` over Go while the `billing` web flag is on; the Firestore page otherwise (T42, P3;
// developer-spec.md §10.13, Appendix E §E.2.3). Each source is its own chunk.
import dynamic from "next/dynamic";

import { DomainPage, PageLoading } from "@/features/platform/components/DomainPage";

const GoPage = dynamic(() => import("@/features/accounting/components/go/BillingResultPage").then((m) => m.BillingResultPage), { loading: () => <PageLoading /> });
const FirestorePage = dynamic(() => import("@/features/accounting/legacy/LegacyBillingResultPage"), { loading: () => <PageLoading /> });

export default function BillingResultRoute() {
    return <DomainPage domain="billing" go={GoPage} firestore={FirestorePage} />;
}
