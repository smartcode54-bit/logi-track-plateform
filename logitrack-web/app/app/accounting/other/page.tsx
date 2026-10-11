"use client";

// Other expenses: `GET /v1/expenses?type=other` (server filters, keyset pages) and the toll import over Go while the `billing` web flag is on; the Firestore page otherwise (T42, P3;
// developer-spec.md §10.13, Appendix E §E.2.3). Each source is its own chunk.
import dynamic from "next/dynamic";

import { DomainPage, PageLoading } from "@/features/platform/components/DomainPage";

const GoPage = dynamic(() => import("@/features/accounting/components/go/ExpensesPage").then((m) => m.OtherExpensesPage), { loading: () => <PageLoading /> });
const FirestorePage = dynamic(() => import("@/features/accounting/legacy/LegacyOtherPage"), { loading: () => <PageLoading /> });

export default function OtherRoute() {
    return <DomainPage domain="billing" go={GoPage} firestore={FirestorePage} />;
}
