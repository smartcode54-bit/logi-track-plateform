"use client";

// Income: `GET /v1/trips?axis=delivered&from&to` and `GET /v1/standby?from&to` (server date filter, keyset
// pages) over Go (T42, P3; developer-spec.md §10.13, Appendix E §E.2.3). The page is its own chunk.
import dynamic from "next/dynamic";

import { PageLoading } from "@/features/platform/components/PageLoading";

const GoPage = dynamic(() => import("@/features/accounting/components/go/IncomePage").then((m) => m.IncomePage), { loading: () => <PageLoading /> });

export default function IncomeRoute() {
    return <GoPage />;
}
