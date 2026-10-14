"use client";

/**
 * The select of the tenant switcher (components/tenant-switcher.tsx), in its own chunk: only a principal
 * with more than one tenant renders it, so the Radix select stays out of the `/app` layout's initial JS
 * (developer-spec.md §10.11).
 */
import { Building2, Loader2 } from "lucide-react";

import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useLanguage } from "@/context/language";
import { useIsMobile } from "@/hooks/use-mobile";
import { tenantName, type MeTenant } from "@/features/auth/api/me";

export interface TenantSwitcherMenuProps {
    tenants: MeTenant[];
    active: string;
    switching: boolean;
    onChoose: (tenantId: string) => void;
}

export default function TenantSwitcherMenu({ tenants, active, switching, onChoose }: TenantSwitcherMenuProps) {
    const { t, language } = useLanguage();
    const isMobile = useIsMobile();
    const current = tenants.find((tenant) => tenant.id === active);
    const label = t("nav.switchTenantCurrent", { name: current ? tenantName(current, language) : active });
    return (
        <div className="flex items-center gap-2" data-testid="tenant-switcher">
            {switching ? <Loader2 className="hidden size-4 animate-spin text-muted-foreground md:block" aria-hidden /> : <Building2 className="hidden size-4 text-muted-foreground md:block" aria-hidden />}
            <Select value={active} onValueChange={onChoose} disabled={switching}>
                {/* The phone keeps the same searchable picker behind a named 44 px building icon. */}
                <SelectTrigger className="size-11 shrink-0 px-0 max-md:[&>span:first-child]:justify-center max-md:[&>span:last-child]:hidden md:h-8 md:w-[220px] md:px-3" aria-label={isMobile ? label : t("nav.switchTenant")}>
                    {switching ? <Loader2 className="size-5 animate-spin text-muted-foreground md:hidden" aria-hidden /> : <Building2 className="size-5 text-muted-foreground md:hidden" aria-hidden />}
                    <SelectValue className="hidden md:flex" placeholder={t("nav.switchTenant")} />
                </SelectTrigger>
                <SelectContent>
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
