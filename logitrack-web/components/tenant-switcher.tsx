"use client";

/**
 * Tenant switcher of the app header (T18 owner addition, 2026-10-10; R83). Shown only when
 * `GET /v1/me/tenants` lists more than one tenant. A switch calls `POST /api/auth/tenant`, which
 * replaces `lt_at`; `['me']`, the Firebase bridge and every tenant-scoped query follow
 * (context/auth.tsx `switchTenant`), and the route is checked again by the edge gate (`router.refresh`).
 */
import { useState } from "react";
import { useRouter } from "next/navigation";
import { Building2, Loader2 } from "lucide-react";
import { toast } from "sonner";

import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useAuth } from "@/context/auth";
import { useLanguage } from "@/context/language";
import { tenantName } from "@/features/auth/api/me";
import { useMyTenants } from "@/features/auth/api/useMe";
import { apiErrorText } from "@/lib/apiError";

export function TenantSwitcher() {
    const auth = useAuth();
    const router = useRouter();
    const { t, language } = useLanguage();
    const signedIn = Boolean(auth?.me);
    const { data: tenants } = useMyTenants(signedIn);
    const [switching, setSwitching] = useState(false);

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

    return (
        <div className="flex items-center gap-2" data-testid="tenant-switcher">
            {switching ? (
                <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" aria-hidden />
            ) : (
                <Building2 className="h-4 w-4 text-muted-foreground" aria-hidden />
            )}
            <Select value={active} onValueChange={(v) => void choose(v)} disabled={switching}>
                <SelectTrigger className="h-8 w-[140px] md:w-[220px]" aria-label={t("nav.switchTenant")}>
                    <SelectValue placeholder={t("nav.switchTenant")} />
                </SelectTrigger>
                <SelectContent position="popper" className="z-[1005]">
                    {tenants.map((tenant) => (
                        <SelectItem key={tenant.id} value={tenant.id} disabled={tenant.status !== undefined && tenant.status !== "active"}>
                            {tenantName(tenant, language)}
                            <span className="ml-2 text-xs text-muted-foreground">{t(`users.tenantRole.${tenant.role}`)}</span>
                        </SelectItem>
                    ))}
                </SelectContent>
            </Select>
        </div>
    );
}
