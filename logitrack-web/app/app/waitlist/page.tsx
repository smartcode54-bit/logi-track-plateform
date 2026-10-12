"use client";

// Waitlist: `GET /v1/waitlist` (keyset) and `DELETE /v1/waitlist/{id}` over Go while the `dashboard` web
// flag is on, the flag of the waitlist collection's coexistence domain and of the sidebar count; the
// Firestore page otherwise (T54R, issue #74; developer-spec.md §10.13). Each source is its own chunk.
import dynamic from "next/dynamic";

import { DomainPage, PageLoading } from "@/features/platform/components/DomainPage";

const GoPage = dynamic(() => import("@/features/waitlist/components/WaitlistPage").then((m) => m.WaitlistPage), { loading: () => <PageLoading /> });
const FirestorePage = dynamic(() => import("@/features/waitlist/legacy/LegacyWaitlistPage"), { loading: () => <PageLoading /> });

export default function AdminWaitlistPage() {
    return <DomainPage domain="dashboard" go={GoPage} firestore={FirestorePage} />;
}
