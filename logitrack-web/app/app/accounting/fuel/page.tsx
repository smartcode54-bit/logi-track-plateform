"use client";

// Fuel expenses: `GET /v1/expenses?type=fuel` (server filters, keyset pages) over Go (T42, P3;
// developer-spec.md §10.13, Appendix E §E.2.3). The page is its own chunk.
import dynamic from "next/dynamic";

import { PageLoading } from "@/features/platform/components/PageLoading";

const GoPage = dynamic(() => import("@/features/accounting/components/go/ExpensesPage").then((m) => m.FuelExpensesPage), { loading: () => <PageLoading /> });

export default function FuelRoute() {
    return <GoPage />;
}
