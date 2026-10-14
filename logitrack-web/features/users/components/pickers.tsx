"use client";

/**
 * Pickers of the users and tenants pages (T18): customer scopes (`GET /v1/customers?fields=minimal&q`),
 * a driver to link (`GET /v1/drivers?fields=minimal&q=`), and a tenant (`GET /v1/tenants`, platform
 * admins). Lists come from Go and search on the server (UI2 shared pickers); an endpoint that is not live
 * yet shows its error text in the list.
 */
import { CustomerPartyMultiSelect, DriverSelect } from "@/components/entity-pickers";
import { nodeText } from "@/components/ui/search-select";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useLanguage } from "@/context/language";
import { tenantName } from "@/features/auth/api/me";
import { useAllTenants } from "@/features/tenants/api/tenants";
import { apiErrorText } from "@/lib/apiError";

/** Billing parties of customer scope: at most 20 per user (Appendix B §B.2.5, `too_many_scopes`). */
export const MAX_SCOPES = 20;

export function CustomerScopePicker({
    value,
    onChange,
    disabled,
    labels,
}: {
    value: string[];
    onChange: (billingPartyIds: string[]) => void;
    disabled?: boolean;
    /** Names of the parties already in the scope (the user's `scopes`), shown before the search loads. */
    labels?: Record<string, string>;
}) {
    const { t } = useLanguage();
    return (
        <div className="space-y-2">
            <CustomerPartyMultiSelect
                aria-label={t("users.scope.customerTitle")}
                value={value}
                onValueChange={(ids) => onChange(ids.slice(0, MAX_SCOPES))}
                max={MAX_SCOPES}
                selectedLabels={labels}
                disabled={disabled}
                placeholder={t("users.scope.searchCustomers")}
                searchPlaceholder={t("users.scope.searchCustomers")}
                emptyText={t("users.scope.noCustomers")}
            />
            <p className="text-xs text-muted-foreground">{t("users.scope.selected", { count: value.length, max: MAX_SCOPES })}</p>
        </div>
    );
}

export function DriverPicker({
    value,
    onChange,
    disabled,
    selectedLabel,
}: {
    value: string;
    onChange: (driverId: string, label: string) => void;
    disabled?: boolean;
    selectedLabel?: string;
}) {
    const { t } = useLanguage();
    return (
        <DriverSelect
            aria-label={t("users.driverLink.title")}
            value={value}
            selectedLabel={selectedLabel}
            onValueChange={(id, option) => onChange(id, option ? (option.text ?? nodeText(option.label)) : "")}
            disabled={disabled}
            placeholder={t("users.driverLink.search")}
            searchPlaceholder={t("users.driverLink.search")}
            emptyText={t("users.driverLink.noDrivers")}
        />
    );
}

export function TenantPicker({
    value,
    onChange,
    disabled,
    id,
}: {
    value: string;
    onChange: (tenantId: string) => void;
    disabled?: boolean;
    id?: string;
}) {
    const { t, language } = useLanguage();
    const { data, isPending, error } = useAllTenants(true);
    if (error) return <p className="text-xs text-destructive">{apiErrorText(error, t)}</p>;
    const tenants = (data ?? []).filter((x) => x.kind !== "quarantine");
    return (
        <Select value={value} onValueChange={onChange} disabled={disabled || isPending}>
            <SelectTrigger id={id}>
                <SelectValue placeholder={isPending ? t("users.loading") : t("users.form.tenantPlaceholder")} />
            </SelectTrigger>
            <SelectContent>
                {tenants.map((x) => (
                    <SelectItem key={x.id} value={x.id}>
                        {tenantName(x, language)}
                        {x.code ? <span className="ml-1 text-xs text-muted-foreground">({x.code})</span> : null}
                    </SelectItem>
                ))}
            </SelectContent>
        </Select>
    );
}
