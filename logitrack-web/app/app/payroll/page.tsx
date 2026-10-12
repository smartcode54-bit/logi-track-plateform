"use client";

// Payroll runs: `GET /v1/payroll` (keyset, 60 s poll), `POST /v1/jobs/payroll.run` followed through
// `['jobs',id]`, approve and status moves over Go while the `hr` web flag is on; the Firestore page otherwise (T46, issue #66; developer-spec.md §10.13). Each source is its own chunk.
import dynamic from "next/dynamic";

import { DomainPage, PageLoading } from "@/features/platform/components/DomainPage";

const GoPage = dynamic(() => import("@/features/driver-compensation/components/go/PayrollPage").then((m) => m.PayrollPage), { loading: () => <PageLoading /> });
const FirestorePage = dynamic(() => import("@/features/driver-compensation/legacy/LegacyPayrollPage"), { loading: () => <PageLoading /> });

export default function AdminPayrollPage() {
    return <DomainPage domain="hr" go={GoPage} firestore={FirestorePage} />;
}
