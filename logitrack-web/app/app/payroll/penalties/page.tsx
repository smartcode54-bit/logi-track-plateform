"use client";

// Driver penalties: `GET/POST /v1/penalties`, `POST /v1/penalties/{id}/cancel`, the active config's types
// and a server-searched driver picker, in parallel, over Go while the `hr` web flag is on; the Firestore page otherwise (T46, issue #66; developer-spec.md §10.13). Each source is its own chunk.
import dynamic from "next/dynamic";

import { DomainPage, PageLoading } from "@/features/platform/components/DomainPage";

const GoPage = dynamic(() => import("@/features/driver-compensation/components/go/PenaltiesPage").then((m) => m.PenaltiesPage), { loading: () => <PageLoading /> });
const FirestorePage = dynamic(() => import("@/features/driver-compensation/legacy/LegacyPenaltiesPage"), { loading: () => <PageLoading /> });

export default function AdminPenaltiesPage() {
    return <DomainPage domain="hr" go={GoPage} firestore={FirestorePage} />;
}
