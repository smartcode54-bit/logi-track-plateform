"use client";

// Billing Document: `GET /v1/billing/rows` (+ diagnostics, missing billing date) and `POST /v1/billing/statements`, documents rendered by Go over Go while the `billing` web flag is on; the Firestore page otherwise (T42, P3;
// developer-spec.md §10.13, Appendix E §E.2.3). Each source is its own chunk.
import dynamic from "next/dynamic";

import { DomainPage, PageLoading } from "@/features/platform/components/DomainPage";

const GoPage = dynamic(() => import("@/features/accounting/components/go/BillingDocumentPage").then((m) => m.BillingDocumentPage), { loading: () => <PageLoading /> });
const FirestorePage = dynamic(() => import("@/features/accounting/legacy/LegacyBillingDocumentPage"), { loading: () => <PageLoading /> });

export default function BillingDocumentRoute() {
    return <DomainPage domain="billing" go={GoPage} firestore={FirestorePage} />;
}
