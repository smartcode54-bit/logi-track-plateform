"use client";
import { PagePermissionGuard } from "@/components/page-permission-guard"
import { CAPABILITIES } from "@/lib/capabilities"

import dynamic from "next/dynamic";
import { Loader2 } from "lucide-react";

const MaintenanceOverview = dynamic(() => import("@/features/maintenance/components/MaintenanceOverview"), {
    ssr: false,
    loading: () => (
        <div className="flex items-center justify-center min-h-[400px]">
            <Loader2 className="h-8 w-8 animate-spin text-muted-foreground mr-2" />
            <span className="text-muted-foreground">Loading...</span>
        </div>
    ),
});

export default function MaintenanceDashboardPage() {
    return (
        <PagePermissionGuard capability={CAPABILITIES.fleet_manage_maintenance}>
            <MaintenanceOverview />
        </PagePermissionGuard>
    );
}
