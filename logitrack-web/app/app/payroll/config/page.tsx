"use client";

// Compensation config versions: `GET /v1/payroll/config` (`?at=` for the version in effect) and
// `POST /v1/payroll/config` (append-only) over Go while the `hr` web flag is on; the Firestore page otherwise (T46, issue #66; developer-spec.md §10.13). Each source is its own chunk.
import dynamic from "next/dynamic";

import { DomainPage, PageLoading } from "@/features/platform/components/DomainPage";

const GoPage = dynamic(() => import("@/features/driver-compensation/components/go/PayrollConfigPage").then((m) => m.PayrollConfigPage), { loading: () => <PageLoading /> });
const FirestorePage = dynamic(() => import("@/features/driver-compensation/legacy/LegacyPayrollConfigPage"), { loading: () => <PageLoading /> });

export default function AdminPayrollConfigPage() {
    return <DomainPage domain="hr" go={GoPage} firestore={FirestorePage} />;
}
