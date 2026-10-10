"use client";

/**
 * Tenant switcher of the app header (T18 owner addition, 2026-10-10; R83). Shown only when
 * `GET /v1/me/tenants` lists more than one tenant; the select itself is a lazily loaded chunk
 * (components/tenant-switcher-menu.tsx), so single-tenant principals never download it. A switch
 * calls `POST /api/auth/tenant`, which replaces `lt_at`; `['me']`, the Firebase bridge and every
 * tenant-scoped query follow (context/auth.tsx `switchTenant`, lib/queryClient.ts `watchPrincipal`),
 * and the route is checked again by the edge gate (`router.refresh`).
 */
import { useState } from "react";
import dynamic from "next/dynamic";
import { useRouter } from "next/navigation";
import { toast } from "sonner";

import { useAuth } from "@/context/auth";
import { useLanguage } from "@/context/language";
import { tenantName, type MeTenant } from "@/features/auth/api/me";
import { useMyTenants } from "@/features/auth/api/useMe";
import { apiErrorText } from "@/lib/apiError";

const TenantSwitcherMenu = dynamic(() => import("./tenant-switcher-menu"), { ssr: false, loading: () => null });

export function TenantSwitcher() {
    const auth = useAuth();
    const router = useRouter();
    const { t, language } = useLanguage();
    const userId = auth?.me?.id;
    const { data: fetched } = useMyTenants(Boolean(userId));
    const [switching, setSwitching] = useState(false);
    // A tenant switch resets `['me','tenants']` with every other query (`watchPrincipal`); the same
    // user's list stays on screen while it refetches, so the switcher does not blink out. Never another
    // user's list: it is kept per user id.
    const [last, setLast] = useState<{ userId: string; tenants: MeTenant[] } | null>(null);
    if (userId && fetched && (last?.userId !== userId || last.tenants !== fetched)) setLast({ userId, tenants: fetched });
    const tenants = fetched ?? (last && last.userId === userId ? last.tenants : undefined);

    if (!auth?.me || !tenants || tenants.length <= 1) return null;
    const active = auth.me.tenant?.id ?? "";

    const choose = async (tenantId: string) => {
        if (tenantId === active || switching) return;
        setSwitching(true);
        try {
            await auth.switchTenant(tenantId);
            const next = tenants.find((x) => x.id === tenantId);
            toast.success(t("nav.tenantSwitched", { name: next ? tenantName(next, language) : tenantId }));
            router.refresh();
        } catch (error) {
            toast.error(apiErrorText(error, t));
        } finally {
            setSwitching(false);
        }
    };

    return <TenantSwitcherMenu tenants={tenants} active={active} switching={switching} onChoose={(id) => void choose(id)} />;
}
