"use client";

// Shopee Express report: `GET /v1/billing/shopee-report` and the server-rendered PDF over Go while the `billing` web flag is on; the Firestore page otherwise (T42, P3;
// developer-spec.md §10.13, Appendix E §E.2.3). Each source is its own chunk.
import dynamic from "next/dynamic";

import { DomainPage, PageLoading } from "@/features/platform/components/DomainPage";

const GoPage = dynamic(() => import("@/features/accounting/components/go/ShopeeReportPage").then((m) => m.ShopeeReportPage), { loading: () => <PageLoading /> });
const FirestorePage = dynamic(() => import("@/features/accounting/legacy/LegacyShopeeReportPage"), { loading: () => <PageLoading /> });

export default function ShopeeReportRoute() {
    return <DomainPage domain="billing" go={GoPage} firestore={FirestorePage} />;
}
