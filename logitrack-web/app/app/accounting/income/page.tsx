"use client";

// Income: `GET /v1/trips?axis=delivered&from&to` and `GET /v1/standby?from&to` (server date filter, keyset pages) over Go while the `billing` web flag is on; the Firestore page otherwise (T42, P3;
// developer-spec.md §10.13, Appendix E §E.2.3). Each source is its own chunk.
import dynamic from "next/dynamic";

import { DomainPage, PageLoading } from "@/features/platform/components/DomainPage";

const GoPage = dynamic(() => import("@/features/accounting/components/go/IncomePage").then((m) => m.IncomePage), { loading: () => <PageLoading /> });
const FirestorePage = dynamic(() => import("@/features/accounting/legacy/LegacyIncomePage"), { loading: () => <PageLoading /> });

export default function IncomeRoute() {
    return <DomainPage domain="billing" go={GoPage} firestore={FirestorePage} />;
}
