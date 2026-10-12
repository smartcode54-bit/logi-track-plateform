"use client";

// Expense audit: `GET /v1/expenses?status` (fuel and other in one server query, status filtered by Go) over
// Go (T42, P3; developer-spec.md §10.13, Appendix E §E.2.3). The page is its own chunk.
import dynamic from "next/dynamic";

import { PageLoading } from "@/features/platform/components/PageLoading";

const GoPage = dynamic(() => import("@/features/accounting/components/go/ExpenseAuditPage").then((m) => m.ExpenseAuditPage), { loading: () => <PageLoading /> });

export default function AuditRoute() {
    return <GoPage />;
}
