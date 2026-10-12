"use client";

// Holidays: `['holidays',year]` over `GET /v1/holidays?year` (a plain fetch, no listener, no refetch on a
// language toggle) and the holiday routes (`POST`, `PATCH`/`DELETE /v1/holidays/{id}`, `PUT /v1/holidays/generate`)
// over Go while the `hr` web flag is on; the Firestore page otherwise (T46, issue #66; developer-spec.md §10.13). Each source is its own chunk.
import dynamic from "next/dynamic";

import { DomainPage, PageLoading } from "@/features/platform/components/DomainPage";

const GoPage = dynamic(() => import("@/features/holidays/components/HolidaysPage").then((m) => m.HolidaysPage), { loading: () => <PageLoading /> });
const FirestorePage = dynamic(() => import("@/features/holidays/legacy/LegacyHolidaysPage"), { loading: () => <PageLoading /> });

export default function AdminHolidaysPage() {
    return <DomainPage domain="hr" go={GoPage} firestore={FirestorePage} />;
}
