"use client";

import DriverMonitorDashboard from "@/features/drivers/components/DriverMonitorDashboard";
import { PagePermissionGuard } from "@/components/page-permission-guard";
import { CAPABILITIES } from "@/lib/capabilities";

export default function DriverMonitorPage() {
    return (
        <PagePermissionGuard capability={CAPABILITIES.operations_view_driver_monitor}>
            <DriverMonitorDashboard />
        </PagePermissionGuard>
    );
}
