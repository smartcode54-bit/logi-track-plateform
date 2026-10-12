"use client";

// Shopee Express report: `GET /v1/billing/shopee-report` and the server-rendered PDF over Go (T42, P3;
// developer-spec.md §10.13, Appendix E §E.2.3). The page is its own chunk.
import dynamic from "next/dynamic";

import { PageLoading } from "@/features/platform/components/PageLoading";

const GoPage = dynamic(() => import("@/features/accounting/components/go/ShopeeReportPage").then((m) => m.ShopeeReportPage), { loading: () => <PageLoading /> });

export default function ShopeeReportRoute() {
    return <GoPage />;
}
