"use client";

// Compensation config versions: `GET /v1/payroll/config` (`?at=` for the version in effect) and `POST
// /v1/payroll/config` (append-only) over Go (T46, issue #66; developer-spec.md §10.13). The page is its own
// chunk.
import dynamic from "next/dynamic";

import { PageLoading } from "@/features/platform/components/PageLoading";

const GoPage = dynamic(() => import("@/features/driver-compensation/components/go/PayrollConfigPage").then((m) => m.PayrollConfigPage), { loading: () => <PageLoading /> });

export default function AdminPayrollConfigPage() {
    return <GoPage />;
}
