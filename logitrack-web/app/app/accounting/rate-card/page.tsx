"use client";

// Rate card: one key per table and customer (`['rateCard', table, customerId]`), each write invalidates only
// its key over Go (T42, P3; developer-spec.md §10.13, Appendix E §E.2.3). The page is its own chunk.
import dynamic from "next/dynamic";

import { PageLoading } from "@/features/platform/components/PageLoading";

const GoPage = dynamic(() => import("@/features/accounting/components/go/RateCardPage").then((m) => m.RateCardPage), { loading: () => <PageLoading /> });

export default function RateCardRoute() {
    return <GoPage />;
}
