"use client";

// Expense audit: `GET /v1/expenses?status` (fuel and other in one server query, status filtered by Go) over Go while the `billing` web flag is on; the Firestore page otherwise (T42, P3;
// developer-spec.md §10.13, Appendix E §E.2.3). Each source is its own chunk.
import dynamic from "next/dynamic";

import { DomainPage, PageLoading } from "@/features/platform/components/DomainPage";

const GoPage = dynamic(() => import("@/features/accounting/components/go/ExpenseAuditPage").then((m) => m.ExpenseAuditPage), { loading: () => <PageLoading /> });
const FirestorePage = dynamic(() => import("@/features/accounting/legacy/LegacyAuditPage"), { loading: () => <PageLoading /> });

export default function AuditRoute() {
    return <DomainPage domain="billing" go={GoPage} firestore={FirestorePage} />;
}
