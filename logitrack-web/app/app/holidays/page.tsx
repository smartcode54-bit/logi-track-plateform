"use client";

// Holidays: `['holidays',year]` over `GET /v1/holidays?year` (a plain fetch, no listener, no refetch on a
// language toggle) and the holiday routes (`POST`, `PATCH`/`DELETE /v1/holidays/{id}`, `PUT
// /v1/holidays/generate`) over Go (T46, issue #66; developer-spec.md §10.13). The page is its own chunk.
import dynamic from "next/dynamic";

import { PageLoading } from "@/features/platform/components/PageLoading";

const GoPage = dynamic(() => import("@/features/holidays/components/HolidaysPage").then((m) => m.HolidaysPage), { loading: () => <PageLoading /> });

export default function AdminHolidaysPage() {
    return <GoPage />;
}
