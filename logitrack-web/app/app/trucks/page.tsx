"use client";
import { PagePermissionGuard } from "@/components/page-permission-guard"
import { CAPABILITIES } from "@/lib/capabilities"

import TrucksListDashboard from "@/features/trucks/components/TrucksListDashboard";

export default function TrucksPage() {
    return (
        <PagePermissionGuard capability={CAPABILITIES.fleet_view_trucks}>
            <TrucksListDashboard />
        </PagePermissionGuard>
    );
}
