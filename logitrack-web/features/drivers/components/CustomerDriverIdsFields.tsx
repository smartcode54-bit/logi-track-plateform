"use client";

import { DriverFormMessage as FormMessage } from "./DriverFormMessage";
import { useState } from "react";
import { useQueries } from "@tanstack/react-query";
import type { UseFormReturn } from "react-hook-form";
import { CustomerSearchSelect } from "@/components/entity-pickers";
import { QueryError, QueryLoading } from "@/components/page-states";
import { FormControl, FormField, FormItem, FormLabel } from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useLanguage } from "@/context/language";
import { customerQuery } from "@/features/customers/api/customers";
import { useCan } from "@/features/auth/api/useMe";
import { isApiError } from "@/lib/apiError";
import type { Driver } from "@/validate/driverSchema";
import type { CustomerDriverIdDTO } from "../api/drivers";

export function CustomerDriverIdsFields({ form, existing }: { form: UseFormReturn<Driver>; existing: CustomerDriverIdDTO[] }) {
    const { t } = useLanguage();
    const { allowed: canReadCustomer } = useCan("fleet:manage_customers");
    const [added, setAdded] = useState<string[]>([]);
    const ids = [...new Set([...existing.map((c) => c.customerId), ...added])];
    const customers = useQueries({ queries: ids.map((id) => ({ ...customerQuery(id), enabled: canReadCustomer })) });

    return (
        <div className="space-y-6">
            {canReadCustomer ? <div className="space-y-2">
                <Label htmlFor="driver-id-customer">{t("drivers.form.addCustomer")}</Label>
                <CustomerSearchSelect id="driver-id-customer" value="" onValueChange={(id) => { if (id) setAdded((prev) => [...prev, id]); }} />
            </div> : null}
            {customers.map((query, index) => {
                const held = existing.filter((c) => c.customerId === ids[index]);
                const code = query.data?.code ?? held[0]?.customerCode;
                const forbidden = isApiError(query.error) && query.error.status === 403;
                const fields = new Map(held.map((c) => [c.idTypeKey,
                    c.idTypeKey === "appId" ? t("drivers.form.spxAppId")
                        : c.idTypeKey === "workId" ? t("drivers.form.spxWorkId")
                            : t("drivers.form.customerIdType", { key: c.idTypeKey }),
                ]));
                for (const type of query.data?.driverIdTypes ?? []) fields.set(type.key, type.label);
                return (
                    <div key={ids[index]} className="space-y-4 rounded-lg bg-muted p-4">
                        {canReadCustomer && query.isPending ? <QueryLoading /> : null}
                        {!canReadCustomer || forbidden ? <p className="text-sm text-muted-foreground">{t("drivers.form.customerSettingsRestricted")}</p>
                            : query.isError ? <QueryError label={t("drivers.form.customerLoadError")} onRetry={() => void query.refetch()} retrying={query.isFetching} /> : null}
                        {code ? <h3 className="font-semibold">{query.data?.name ?? held[0]?.customerName} ({code})</h3> : null}
                        {query.isSuccess && fields.size === 0 ? <p className="text-sm text-muted-foreground">{t("drivers.form.noCustomerIdTypes")}</p> : null}
                        <div className="grid gap-4 md:grid-cols-2">
                            {code ? [...fields].map(([key, label]) => (
                                <FormField key={key} control={form.control} name={`customerDriverIds.${code}.${key}`} defaultValue="" render={({ field }) => (
                                    <FormItem>
                                        <FormLabel>{label}</FormLabel>
                                        <FormControl><Input {...field} value={field.value ?? ""} /></FormControl>
                                        <FormMessage />
                                    </FormItem>
                                )} />
                            )) : null}
                        </div>
                    </div>
                );
            })}
        </div>
    );
}
