"use client";

// Fuel price history: `GET /v1/fuel/daily?from` (paged, no 400-row cap) over Go while the `billing` web flag is on; the Firestore page otherwise (T42, P3;
// developer-spec.md §10.13, Appendix E §E.2.3). Each source is its own chunk.
import dynamic from "next/dynamic";

import { DomainPage, PageLoading } from "@/features/platform/components/DomainPage";

const GoPage = dynamic(() => import("@/features/accounting/components/go/FuelPriceHistoryPage").then((m) => m.FuelPriceHistoryPage), { loading: () => <PageLoading /> });
const FirestorePage = dynamic(() => import("@/features/accounting/legacy/LegacyFuelPriceHistoryPage"), { loading: () => <PageLoading /> });

export default function FuelPriceHistoryRoute() {
    return <DomainPage domain="billing" go={GoPage} firestore={FirestorePage} />;
}
