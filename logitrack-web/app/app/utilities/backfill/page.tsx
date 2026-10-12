"use client";

// Billing backfills: `POST /v1/jobs/billing.backfill-trips|backfill-standby` followed through `['jobs', id]` over Go while the `billing` web flag is on; the Firestore page otherwise (T42, P3;
// developer-spec.md §10.13, Appendix E §E.2.3). Each source is its own chunk.
import dynamic from "next/dynamic";

import { DomainPage, PageLoading } from "@/features/platform/components/DomainPage";

const GoPage = dynamic(() => import("@/features/accounting/components/go/BackfillUtilitiesPage").then((m) => m.BackfillUtilitiesPage), { loading: () => <PageLoading /> });
const FirestorePage = dynamic(() => import("@/features/accounting/legacy/LegacyBackfillPage"), { loading: () => <PageLoading /> });

export default function BackfillRoute() {
    return <DomainPage domain="billing" go={GoPage} firestore={FirestorePage} />;
}
