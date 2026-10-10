"use client";

/**
 * Security Center tenants page for platform admins (T18 owner addition, 2026-10-10: multi-tenant from
 * the first release). Lists tenants (`GET /v1/tenants`, keyset), creates carrier tenants, edits their
 * names and status, and assigns or removes their `tenant_admin`s. The route is gated by
 * `platform:manage_tenants` (proxy.ts, lib/routeCapabilities.ts); Go checks every call again.
 */
import { useMemo, useState } from "react";
import { Building2, Edit, Loader2, MoreHorizontal, Plus, RefreshCw, UserCog } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useAuth } from "@/context/auth";
import { useLanguage } from "@/context/language";
import { hasCapability } from "@/features/auth/api/me";
import { apiErrorText } from "@/lib/apiError";
import { TENANT_KINDS, TENANT_STATUSES, useInvalidateTenants, useTenants, type TenantDTO } from "../api/tenants";
import { TenantAdminsDialog } from "./TenantAdminsDialog";
import { TenantFormDialog } from "./TenantFormDialog";

const STATUS_STYLE: Record<string, string> = {
    active: "bg-green-600 hover:bg-green-600",
    pending: "bg-amber-500 hover:bg-amber-500",
    suspended: "bg-gray-500 hover:bg-gray-500",
};

export function TenantsPage() {
    const { t } = useLanguage();
    const auth = useAuth();
    const allowed = hasCapability(auth?.me, "platform:manage_tenants");
    const [kind, setKind] = useState("carrier");
    const [status, setStatus] = useState("");
    const tenants = useTenants({ kind, status }, allowed);
    const invalidate = useInvalidateTenants();
    const rows = useMemo(() => tenants.data?.pages.flatMap((p) => p.data ?? []) ?? [], [tenants.data]);

    const [form, setForm] = useState<{ tenant: TenantDTO | null } | null>(null);
    const [adminsOf, setAdminsOf] = useState<TenantDTO | null>(null);

    if (!allowed) {
        return <p className="p-6 text-sm text-muted-foreground">{t("tenants.accessDenied")}</p>;
    }

    return (
        <div className="p-6 space-y-6">
            <div className="flex flex-col md:flex-row justify-between items-start md:items-center gap-4">
                <div>
                    <h1 className="flex items-center gap-2 text-3xl font-bold tracking-tight">
                        <Building2 className="h-7 w-7" />
                        {t("tenants.title")}
                    </h1>
                    <p className="text-muted-foreground mt-2">{t("tenants.subtitle")}</p>
                </div>
                <div className="flex gap-2">
                    <Button variant="outline" className="gap-2" onClick={() => void tenants.refetch()} disabled={tenants.isFetching}>
                        {tenants.isFetching ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCw className="h-4 w-4" />}
                        {t("users.refresh")}
                    </Button>
                    <Button onClick={() => setForm({ tenant: null })}>
                        <Plus className="mr-2 h-4 w-4" />
                        {t("tenants.create")}
                    </Button>
                </div>
            </div>

            <div className="flex flex-wrap items-center gap-3">
                <Select value={kind || "all"} onValueChange={(v) => setKind(v === "all" ? "" : v)}>
                    <SelectTrigger className="w-[180px] h-9" aria-label={t("tenants.filter.kind")}>
                        <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value="all">{t("tenants.filter.kindAll")}</SelectItem>
                        {TENANT_KINDS.map((k) => (
                            <SelectItem key={k} value={k}>
                                {t(`tenants.kind.${k}`)}
                            </SelectItem>
                        ))}
                    </SelectContent>
                </Select>
                <Select value={status || "all"} onValueChange={(v) => setStatus(v === "all" ? "" : v)}>
                    <SelectTrigger className="w-[160px] h-9" aria-label={t("tenants.filter.status")}>
                        <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value="all">{t("tenants.filter.statusAll")}</SelectItem>
                        {TENANT_STATUSES.map((s) => (
                            <SelectItem key={s} value={s}>
                                {t(`tenants.status.${s}`)}
                            </SelectItem>
                        ))}
                    </SelectContent>
                </Select>
            </div>

            <Card className="bg-card border-border shadow-sm">
                <Table>
                    <TableHeader className="bg-muted/40">
                        <TableRow className="hover:bg-transparent">
                            <TableHead className="pl-6 text-xs font-semibold uppercase">{t("tenants.table.name")}</TableHead>
                            <TableHead className="text-xs font-semibold uppercase">{t("tenants.table.code")}</TableHead>
                            <TableHead className="text-xs font-semibold uppercase">{t("tenants.table.kind")}</TableHead>
                            <TableHead className="text-xs font-semibold uppercase">{t("tenants.table.status")}</TableHead>
                            <TableHead className="w-[52px] text-right text-xs font-semibold uppercase">{t("users.table.actions")}</TableHead>
                        </TableRow>
                    </TableHeader>
                    <TableBody>
                        {tenants.isPending ? (
                            <TableRow>
                                <TableCell colSpan={5} className="py-10 text-center">
                                    <Loader2 className="mx-auto h-6 w-6 animate-spin text-muted-foreground" />
                                </TableCell>
                            </TableRow>
                        ) : tenants.isError ? (
                            <TableRow>
                                <TableCell colSpan={5} className="py-10 text-center text-sm text-destructive" role="alert">
                                    {apiErrorText(tenants.error, t)}
                                </TableCell>
                            </TableRow>
                        ) : rows.length === 0 ? (
                            <TableRow>
                                <TableCell colSpan={5} className="py-10 text-center text-sm text-muted-foreground">
                                    {t("tenants.empty")}
                                </TableCell>
                            </TableRow>
                        ) : (
                            rows.map((tenant) => (
                                <TableRow key={tenant.id} data-testid="tenant-row">
                                    <TableCell className="pl-6">
                                        <div className="flex flex-col">
                                            <span className="text-sm font-semibold">{tenant.nameTh}</span>
                                            {tenant.nameEn ? <span className="text-xs text-muted-foreground">{tenant.nameEn}</span> : null}
                                        </div>
                                    </TableCell>
                                    <TableCell className="font-mono text-xs">{tenant.code ?? "—"}</TableCell>
                                    <TableCell>
                                        <Badge variant="outline">{t(`tenants.kind.${tenant.kind}`, tenant.kind)}</Badge>
                                    </TableCell>
                                    <TableCell>
                                        <Badge className={STATUS_STYLE[tenant.status]}>{t(`tenants.status.${tenant.status}`, tenant.status)}</Badge>
                                    </TableCell>
                                    <TableCell className="text-right">
                                        {tenant.kind === "quarantine" ? null : (
                                            <DropdownMenu>
                                                <DropdownMenuTrigger asChild>
                                                    <Button variant="ghost" size="icon" className="h-8 w-8" aria-label={t("users.table.actions")}>
                                                        <MoreHorizontal className="h-4 w-4" />
                                                    </Button>
                                                </DropdownMenuTrigger>
                                                <DropdownMenuContent align="end">
                                                    <DropdownMenuItem onSelect={() => setForm({ tenant })}>
                                                        <Edit className="mr-2 h-4 w-4" />
                                                        {t("tenants.edit")}
                                                    </DropdownMenuItem>
                                                    <DropdownMenuItem onSelect={() => setAdminsOf(tenant)}>
                                                        <UserCog className="mr-2 h-4 w-4" />
                                                        {t("tenants.admins.manage")}
                                                    </DropdownMenuItem>
                                                </DropdownMenuContent>
                                            </DropdownMenu>
                                        )}
                                    </TableCell>
                                </TableRow>
                            ))
                        )}
                    </TableBody>
                </Table>
            </Card>

            {tenants.hasNextPage ? (
                <div className="flex justify-end">
                    <Button variant="outline" onClick={() => void tenants.fetchNextPage()} disabled={tenants.isFetchingNextPage}>
                        {tenants.isFetchingNextPage ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                        {t("users.loadMore")}
                    </Button>
                </div>
            ) : null}

            {form ? (
                <TenantFormDialog
                    key={form.tenant?.id ?? "new"}
                    tenant={form.tenant}
                    open
                    onOpenChange={(open) => !open && setForm(null)}
                    onSaved={(saved) => {
                        void invalidate();
                        // A new carrier gets its first admin right away.
                        if (!form.tenant && saved) setAdminsOf(saved);
                    }}
                />
            ) : null}
            {adminsOf ? <TenantAdminsDialog tenant={adminsOf} onOpenChange={(open) => !open && setAdminsOf(null)} /> : null}
        </div>
    );
}
