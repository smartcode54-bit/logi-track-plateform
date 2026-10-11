"use client";

// Rate card: one key per table and customer (`['rateCard', table, customerId]`), each write invalidates only its key over Go while the `billing` web flag is on; the Firestore page otherwise (T42, P3;
// developer-spec.md §10.13, Appendix E §E.2.3). Each source is its own chunk.
import dynamic from "next/dynamic";

import { DomainPage, PageLoading } from "@/features/platform/components/DomainPage";

const GoPage = dynamic(() => import("@/features/accounting/components/go/RateCardPage").then((m) => m.RateCardPage), { loading: () => <PageLoading /> });
const FirestorePage = dynamic(() => import("@/features/accounting/legacy/LegacyRateCardPage"), { loading: () => <PageLoading /> });

export default function RateCardRoute() {
    return <DomainPage domain="billing" go={GoPage} firestore={FirestorePage} />;
}
