"use client";

// Driver penalties: `GET/POST /v1/penalties`, `POST /v1/penalties/{id}/cancel`, the active config's types and
// a server-searched driver picker, in parallel, over Go (T46, issue #66; developer-spec.md §10.13). The page
// is its own chunk.
import dynamic from "next/dynamic";

import { PageLoading } from "@/features/platform/components/PageLoading";

const GoPage = dynamic(() => import("@/features/driver-compensation/components/go/PenaltiesPage").then((m) => m.PenaltiesPage), { loading: () => <PageLoading /> });

export default function AdminPenaltiesPage() {
    return <GoPage />;
}
