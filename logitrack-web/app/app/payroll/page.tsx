"use client";

// Payroll runs: `GET /v1/payroll` (keyset, 60 s poll), `POST /v1/jobs/payroll.run` followed through
// `['jobs',id]`, approve and status moves over Go (T46, issue #66; developer-spec.md §10.13). The page is its
// own chunk.
import dynamic from "next/dynamic";

import { PageLoading } from "@/features/platform/components/PageLoading";

const GoPage = dynamic(() => import("@/features/driver-compensation/components/go/PayrollPage").then((m) => m.PayrollPage), { loading: () => <PageLoading /> });

export default function AdminPayrollPage() {
    return <GoPage />;
}
