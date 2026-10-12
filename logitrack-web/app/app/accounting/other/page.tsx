"use client";

// Other expenses: `GET /v1/expenses?type=other` (server filters, keyset pages) and the toll import over Go
// (T42, P3; developer-spec.md §10.13, Appendix E §E.2.3). The page is its own chunk.
import dynamic from "next/dynamic";

import { PageLoading } from "@/features/platform/components/PageLoading";

const GoPage = dynamic(() => import("@/features/accounting/components/go/ExpensesPage").then((m) => m.OtherExpensesPage), { loading: () => <PageLoading /> });

export default function OtherRoute() {
    return <GoPage />;
}
