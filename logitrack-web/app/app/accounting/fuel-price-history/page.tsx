"use client";

// Fuel price history: `GET /v1/fuel/daily?from` (paged, no 400-row cap) over Go (T42, P3; developer-spec.md
// §10.13, Appendix E §E.2.3). The page is its own chunk.
import dynamic from "next/dynamic";

import { PageLoading } from "@/features/platform/components/PageLoading";

const GoPage = dynamic(() => import("@/features/accounting/components/go/FuelPriceHistoryPage").then((m) => m.FuelPriceHistoryPage), { loading: () => <PageLoading /> });

export default function FuelPriceHistoryRoute() {
    return <GoPage />;
}
