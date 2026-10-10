"use client";

/**
 * The select of the tenant switcher (components/tenant-switcher.tsx), in its own chunk: only a principal
 * with more than one tenant renders it, so the Radix select stays out of the `/app` layout's initial JS
 * (developer-spec.md §10.11).
 */
import { Building2, Loader2 } from "lucide-react";

import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useLanguage } from "@/context/language";
import { tenantName, type MeTenant } from "@/features/auth/api/me";

export interface TenantSwitcherMenuProps {
    tenants: MeTenant[];
    active: string;
    switching: boolean;
    onChoose: (tenantId: string) => void;
}

export default function TenantSwitcherMenu({ tenants, active, switching, onChoose }: TenantSwitcherMenuProps) {
    const { t, language } = useLanguage();
    return (
        <div className="flex items-center gap-2" data-testid="tenant-switcher">
            {switching ? (
                <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" aria-hidden />
            ) : (
                <Building2 className="h-4 w-4 text-muted-foreground" aria-hidden />
            )}
            <Select value={active} onValueChange={onChoose} disabled={switching}>
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
