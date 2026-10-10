"use client";

// Security Center tenants page (T18 owner addition): platform admins onboard carrier tenants and their
// tenant_admins. Gated by platform:manage_tenants (logitrack-api/internal/authz/webroutes.go).
import { TenantsPage } from "@/features/tenants/components/TenantsPage";

export default function SecurityCenterTenantsPage() {
    return <TenantsPage />;
}
