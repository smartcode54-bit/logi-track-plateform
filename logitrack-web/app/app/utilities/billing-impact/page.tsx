"use client";

// Billing impact report: `POST /v1/jobs/billing.impact-report` followed through `['jobs', id]` over Go while the `billing` web flag is on; the Firestore page otherwise (T42, P3;
// developer-spec.md §10.13, Appendix E §E.2.3). Each source is its own chunk.
import dynamic from "next/dynamic";

import { DomainPage, PageLoading } from "@/features/platform/components/DomainPage";

const GoPage = dynamic(() => import("@/features/accounting/components/go/BillingImpactPage").then((m) => m.BillingImpactPage), { loading: () => <PageLoading /> });
const FirestorePage = dynamic(() => import("@/features/accounting/legacy/LegacyBillingImpactPage"), { loading: () => <PageLoading /> });

export default function BillingImpactRoute() {
    return <DomainPage domain="billing" go={GoPage} firestore={FirestorePage} />;
}
