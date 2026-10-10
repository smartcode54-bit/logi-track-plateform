"use client";

/**
 * Pickers of the users and tenants pages (T18): customer scopes (`GET /v1/customers?fields=minimal`),
 * a driver to link (`GET /v1/drivers?fields=minimal&q=`), and a tenant (`GET /v1/tenants`, platform
 * admins). Lists come from Go; an endpoint that is not live yet shows its error text.
 */
import { useMemo, useState } from "react";
import { Loader2, Search } from "lucide-react";

import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useLanguage } from "@/context/language";
import { tenantName } from "@/features/auth/api/me";
import { useAllTenants } from "@/features/tenants/api/tenants";
import { apiErrorText } from "@/lib/apiError";
import { useCustomerOptions, useDriverOptions } from "../api/users";

/** Billing parties of customer scope: at most 20 per user (Appendix B §B.2.5, `too_many_scopes`). */
export const MAX_SCOPES = 20;

export function CustomerScopePicker({
    value,
    onChange,
    disabled,
}: {
    value: string[];
    onChange: (billingPartyIds: string[]) => void;
    disabled?: boolean;
}) {
    const { t } = useLanguage();
    const [q, setQ] = useState("");
    const { data, isPending, error } = useCustomerOptions(true);
    const selected = useMemo(() => new Set(value), [value]);
    const shown = useMemo(() => {
        const term = q.trim().toLowerCase();
        return (data ?? []).filter((c) => !term || c.name.toLowerCase().includes(term) || (c.code ?? "").toLowerCase().includes(term));
    }, [data, q]);

    const toggle = (id: string, on: boolean) => {
        if (on && !selected.has(id) && value.length >= MAX_SCOPES) return;
        onChange(on ? [...value, id] : value.filter((v) => v !== id));
    };

    if (error) return <p className="text-xs text-destructive">{apiErrorText(error, t)}</p>;
    return (
        <div className="space-y-2">
            <div className="relative">
                <Search className="absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
                <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("users.scope.searchCustomers")} className="h-8 pl-7 text-xs" disabled={disabled} />
            </div>
            <div className="max-h-40 overflow-y-auto rounded-md border p-2 space-y-1">
                {isPending ? (
                    <Loader2 className="mx-auto h-4 w-4 animate-spin text-muted-foreground" />
                ) : shown.length === 0 ? (
                    <p className="text-xs text-muted-foreground">{t("users.scope.noCustomers")}</p>
                ) : (
                    shown.map((c) => (
                        <label key={c.billingPartyId} className="flex items-center gap-2 text-sm">
                            <Checkbox
                                checked={selected.has(c.billingPartyId)}
                                onCheckedChange={(v) => toggle(c.billingPartyId, v === true)}
                                disabled={disabled}
                            />
                            <span>
                                {c.name}
                                {c.code ? <span className="ml-1 text-xs text-muted-foreground">({c.code})</span> : null}
                            </span>
                        </label>
                    ))
                )}
            </div>
            <p className="text-xs text-muted-foreground">{t("users.scope.selected", { count: value.length, max: MAX_SCOPES })}</p>
        </div>
    );
}

export function DriverPicker({
    value,
    onChange,
    disabled,
}: {
    value: string;
    onChange: (driverId: string, label: string) => void;
    disabled?: boolean;
}) {
    const { t } = useLanguage();
    const [q, setQ] = useState("");
    const { data, isFetching, error } = useDriverOptions(q, true);

    return (
        <div className="space-y-2">
            <div className="relative">
                <Search className="absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
                <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("users.driverLink.search")} className="h-8 pl-7 text-xs" disabled={disabled} />
            </div>
            {error ? (
                <p className="text-xs text-destructive">{apiErrorText(error, t)}</p>
            ) : (
                <div className="max-h-40 overflow-y-auto rounded-md border p-1">
                    {isFetching && !data ? (
                        <Loader2 className="mx-auto my-2 h-4 w-4 animate-spin text-muted-foreground" />
                    ) : (data ?? []).length === 0 ? (
                        <p className="p-1 text-xs text-muted-foreground">{t("users.driverLink.noDrivers")}</p>
                    ) : (
                        (data ?? []).map((d) => (
                            <button
                                key={d.id}
                                type="button"
                                disabled={disabled}
                                onClick={() => onChange(d.id, d.displayName ?? d.id)}
                                className={`block w-full rounded px-2 py-1 text-left text-sm hover:bg-muted ${value === d.id ? "bg-muted font-medium" : ""}`}
                            >
                                {d.displayName || d.id}
                            </button>
                        ))
                    )}
                </div>
            )}
        </div>
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
            <SelectContent position="popper" className="z-[1005]">
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
