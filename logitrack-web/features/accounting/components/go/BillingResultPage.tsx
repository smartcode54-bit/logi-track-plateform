"use client";

/**
 * Billing statements over Go (T42, P3; Appendix E §E.2.3 row "billing-result"; developer-spec.md §6.14,
 * §9.9; R18, R69, R89).
 *
 * - `['statements',{customerId,status,year,month}]`: `GET /v1/billing/statements` with every filter on the
 *   server (the Firestore page filtered the period in the browser and read the creator of each row from
 *   `users`; Go embeds `generatedByName`).
 * - The documents are the stored ones of `statement_documents` (`GET /v1/billing/statements/{id}`, the
 *   `['statements','detail',id]` key): a download re-serves the file rendered when the statement was made
 *   (the Firestore page rebuilt it from live data with `fetchBillingTripRows`), named with the customer's
 *   real code, its withholding tax the rate snapshotted on the statement (R18). The receipt is rendered by
 *   Go when the statement is marked paid. No document code runs in the browser.
 * - Status changes, delete (draft only) and regenerate invalidate `['statements']`; SSE `statement.*` and
 *   `documents.rendered` do the same in other tabs, and a detail polls every 5 s while a document renders
 *   and the stream is closed.
 */
import { useMemo, useState } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";
import { differenceInDays, parseISO } from "date-fns";
import { AlertTriangle, Clock, FileText, Loader2, MoreHorizontal, RefreshCw, TrendingUp } from "lucide-react";
import { toast } from "sonner";

import { useLanguage } from "@/context/language";
import { useCan } from "@/features/auth/api/useMe";
import { apiErrorText } from "@/lib/apiError";
import { CustomerSearchSelect } from "@/components/entity-pickers";
import { LoadMore } from "@/components/load-more";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

import {
    canMoveStatement,
    deleteStatement,
    setStatementStatus,
    statementRows,
    statementsQuery,
    type StatementDTO,
    type StatementStatus,
} from "../../api/billing.go";
import { ApiErrorAlert, CenteredSpinner, formatDateTime, formatDay, MONTHS, monthName, yearOptions } from "./shared";
import { StatementDocumentsDialog } from "./StatementDocumentsDialog";

const STATUS_COLORS: Record<string, string> = {
    draft: "bg-gray-100 text-gray-700",
    sent: "bg-blue-100 text-blue-700",
    paid: "bg-green-100 text-green-700",
    cancelled: "bg-red-100 text-red-700",
    overdue: "bg-amber-100 text-amber-700",
};

function money(n: number) {
    return new Intl.NumberFormat("th-TH", { minimumFractionDigits: 2, maximumFractionDigits: 2 }).format(n);
}

/** `overdue` for a draft or sent statement past its due date. */
export function effectiveStatus(s: Pick<StatementDTO, "status" | "dueDate">, now = new Date()): string {
    if (s.status !== "draft" && s.status !== "sent") return s.status;
    if (s.dueDate && differenceInDays(now, parseISO(s.dueDate)) > 0) return "overdue";
    return s.status;
}

/** The statistics of the loaded statements (cancelled ones are not invoiced). */
export function statementStats(rows: StatementDTO[], now = new Date()) {
    let total = 0;
    let counted = 0;
    let cancelled = 0;
    let draft = 0;
    let sent = 0;
    let paid = 0;
    let overdue = 0;
    for (const s of rows) {
        if (s.status === "cancelled") {
            cancelled += 1;
            continue;
        }
        counted += 1;
        total += s.totalAmount;
        if (s.status === "draft") draft += 1;
        if (s.status === "sent") sent += 1;
        if (s.status === "paid") paid += 1;
        if (effectiveStatus(s, now) === "overdue") overdue += 1;
    }
    return { total, counted, cancelled, draft, sent, paid, overdue };
}

export function BillingResultPage() {
    const { t, language } = useLanguage();
    const canManage = useCan("accounting:manage_statements").allowed;
    // The statements carry their customer's name and code; the filter searches customers on the server.
    const [customerId, setCustomerId] = useState("all");
    const [status, setStatus] = useState("all");
    const [month, setMonth] = useState("all");
    const [year, setYear] = useState("all");
    const filter = { customerId, status, year: year === "all" ? 0 : Number(year), month: month === "all" ? 0 : Number(month) };
    const list = useInfiniteQuery(statementsQuery(filter));
    const rows = statementRows(list.data?.pages);
    const stats = useMemo(() => statementStats(rows), [rows]);
    const [busyId, setBusyId] = useState<string | null>(null);
    const [docsFor, setDocsFor] = useState<StatementDTO | null>(null);

    const changeStatus = async (s: StatementDTO, next: StatementStatus) => {
        if (next === "cancelled" && !window.confirm(t("accounting.billingResult.confirmCancel"))) return;
        setBusyId(s.id);
        try {
            await setStatementStatus(s, next);
            if (next === "paid") toast.success(t("accounting.billingResult.documents.receiptQueued"));
        } catch (e) {
            toast.error(`${t("accounting.billingResult.action.updateError")} ${apiErrorText(e, t)}`);
        } finally {
            setBusyId(null);
        }
    };
    const remove = async (s: StatementDTO) => {
        if (!window.confirm(t("accounting.billingResult.confirmDelete"))) return;
        setBusyId(s.id);
        try {
            await deleteStatement(s);
        } catch (e) {
            toast.error(`${t("accounting.billingResult.action.deleteError")} ${apiErrorText(e, t)}`);
        } finally {
            setBusyId(null);
        }
    };

    return (
        <div className="space-y-6 p-6">
            <div>
                <h1 className="text-2xl font-bold">{t("nav.billingResult")}</h1>
                <p className="mt-1 text-sm text-muted-foreground">{t("accounting.billingResult.subtitle")}</p>
            </div>

            <Card>
                <CardContent className="pt-4">
                    <div className="flex flex-wrap items-end gap-4">
                        <div className="space-y-1">
                            <Label className="text-xs">{t("accounting.billingResult.filters.customer")}</Label>
                            <CustomerSearchSelect
                                aria-label={t("accounting.billingResult.filters.customer")}
                                value={customerId}
                                onValueChange={(id) => setCustomerId(id || "all")}
                                allValue="all"
                                allLabel={t("accounting.billingResult.filters.allCustomers")}
                                className="w-56"
                            />
                        </div>
                        <div className="space-y-1">
                            <Label className="text-xs">{t("accounting.billingResult.filters.status")}</Label>
                            <Select value={status} onValueChange={setStatus}>
                                <SelectTrigger className="w-36">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                    <SelectItem value="all">{t("accounting.billingResult.filters.allStatuses")}</SelectItem>
                                    {(["draft", "sent", "paid", "cancelled"] as const).map((s) => (
                                        <SelectItem key={s} value={s}>
                                            {t(`accounting.billingResult.status.${s}`)}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>
                        <div className="space-y-1">
                            <Label className="text-xs">{t("accounting.billingResult.filters.month")}</Label>
                            <Select value={month} onValueChange={setMonth} disabled={year === "all"}>
                                <SelectTrigger className="w-44">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                    <SelectItem value="all">{t("accounting.billingResult.filters.allPeriods")}</SelectItem>
                                    {MONTHS.map((m) => (
                                        <SelectItem key={m} value={String(m)}>
                                            {monthName(m, language)}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>
                        <div className="space-y-1">
                            <Label className="text-xs">{t("accounting.billingResult.filters.year")}</Label>
                            <Select
                                value={year}
                                onValueChange={(v) => {
                                    setYear(v);
                                    if (v === "all") setMonth("all");
                                }}
                            >
                                <SelectTrigger className="w-28">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                    <SelectItem value="all">{t("accounting.billingResult.filters.allPeriods")}</SelectItem>
                                    {yearOptions().map((y) => (
                                        <SelectItem key={y} value={String(y)}>
                                            {y}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>
                        <Button variant="outline" size="sm" onClick={() => void list.refetch()} disabled={list.isFetching} aria-label={t("common.refresh")}>
                            {list.isFetching ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCw className="h-4 w-4" />}
                        </Button>
                    </div>
                </CardContent>
            </Card>

            <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
                <Card>
                    <CardContent className="pt-4">
                        <div className="mb-1 flex items-center gap-2 text-xs text-muted-foreground">
                            <TrendingUp className="h-3.5 w-3.5" />
                            {t("accounting.billingResult.stats.total")}
                        </div>
                        <p className="text-xl font-bold">฿{money(stats.total)}</p>
                        <p className="text-xs text-muted-foreground">
                            {t("accounting.billingResult.stats.invoiceCount", { count: String(stats.counted) })}
                            {stats.cancelled > 0 ? ` · ${t("accounting.billingResult.stats.excludingCancelled", { count: String(stats.cancelled) })}` : ""}
                        </p>
                    </CardContent>
                </Card>
                <Card>
                    <CardContent className="pt-4">
                        <div className="mb-1 flex items-center gap-2 text-xs text-muted-foreground">
                            <FileText className="h-3.5 w-3.5" />
                            {t("accounting.billingResult.stats.draft")} / {t("accounting.billingResult.stats.sent")}
                        </div>
                        <p className="text-xl font-bold">{stats.draft + stats.sent}</p>
                    </CardContent>
                </Card>
                <Card>
                    <CardContent className="pt-4">
                        <div className="mb-1 flex items-center gap-2 text-xs text-muted-foreground">
                            <Clock className="h-3.5 w-3.5" />
                            {t("accounting.billingResult.stats.paid")}
                        </div>
                        <p className="text-xl font-bold text-green-600">{stats.paid}</p>
                    </CardContent>
                </Card>
                <Card>
                    <CardContent className="pt-4">
                        <div className="mb-1 flex items-center gap-2 text-xs text-muted-foreground">
                            <AlertTriangle className="h-3.5 w-3.5" />
                            {t("accounting.billingResult.stats.overdue")}
                        </div>
                        <p className={`text-xl font-bold ${stats.overdue > 0 ? "text-amber-600" : ""}`}>{stats.overdue}</p>
                    </CardContent>
                </Card>
            </div>
            {list.hasNextPage ? <p className="text-xs text-muted-foreground">{t("accounting.go.statsOfLoaded")}</p> : null}

            <ApiErrorAlert error={list.error} />

            <Card>
                <CardContent className="space-y-4 pt-4">
                    {list.isPending ? (
                        <CenteredSpinner />
                    ) : rows.length === 0 ? (
                        <p className="py-12 text-center text-sm text-muted-foreground">{t("accounting.billingResult.noData")}</p>
                    ) : (
                        <div className="overflow-x-auto">
                            <Table>
                                <TableHeader>
                                    <TableRow>
                                        <TableHead>{t("accounting.billingResult.table.invoiceNumber")}</TableHead>
                                        <TableHead>{t("accounting.billingResult.table.customer")}</TableHead>
                                        <TableHead>{t("accounting.billingResult.table.period")}</TableHead>
                                        <TableHead className="text-right">{t("accounting.billingResult.table.tripCount")}</TableHead>
                                        <TableHead className="text-right">{t("accounting.billingResult.table.totalAmount")}</TableHead>
                                        <TableHead className="text-right">{t("accounting.billingResult.table.withholdingTax")}</TableHead>
                                        <TableHead className="text-right">{t("accounting.billingResult.table.netAmount")}</TableHead>
                                        <TableHead>{t("accounting.billingResult.table.status")}</TableHead>
                                        <TableHead>{t("accounting.billingResult.table.dueDate")}</TableHead>
                                        <TableHead>{t("accounting.billingResult.table.generatedAt")}</TableHead>
                                        <TableHead>{t("accounting.billingResult.table.createdBy")}</TableHead>
                                        <TableHead>{t("accounting.billingResult.table.actions")}</TableHead>
                                    </TableRow>
                                </TableHeader>
                                <TableBody>
                                    {rows.map((s) => {
                                        const eff = effectiveStatus(s);
                                        const busy = busyId === s.id;
                                        return (
                                            <TableRow key={s.id} data-testid="statement-row">
                                                <TableCell className="font-mono text-sm font-medium">{s.invoiceNumber}</TableCell>
                                                <TableCell>
                                                    <div className="font-medium">{s.customerName ?? "—"}</div>
                                                    <div className="font-mono text-xs text-muted-foreground">{s.customerCode}</div>
                                                </TableCell>
                                                <TableCell className="font-mono text-sm">
                                                    {String(s.period.month).padStart(2, "0")}/{s.period.year}
                                                </TableCell>
                                                <TableCell className="text-right">{s.tripCount}</TableCell>
                                                <TableCell className="text-right font-mono">{money(s.totalAmount)}</TableCell>
                                                <TableCell className="text-right font-mono text-muted-foreground">
                                                    {money(s.withholdingTax)}
                                                    <span className="block text-[10px]">{(s.withholdingTaxRate * 100).toFixed(2).replace(/\.?0+$/, "")}%</span>
                                                </TableCell>
                                                <TableCell className="text-right font-mono font-medium">{money(s.netAmount)}</TableCell>
                                                <TableCell>
                                                    <Badge className={`text-xs ${STATUS_COLORS[eff] ?? ""}`} variant="outline">
                                                        {t(`accounting.billingResult.status.${eff}`, eff)}
                                                    </Badge>
                                                </TableCell>
                                                <TableCell className="text-sm">
                                                    <span className={eff === "overdue" ? "font-medium text-amber-600" : ""}>{formatDay(s.dueDate)}</span>
                                                </TableCell>
                                                <TableCell className="whitespace-nowrap text-sm text-muted-foreground">{formatDateTime(s.generatedAt)}</TableCell>
                                                <TableCell className="text-sm text-muted-foreground">{s.generatedByName ?? "—"}</TableCell>
                                                <TableCell>
                                                    {busy ? (
                                                        <Loader2 className="h-4 w-4 animate-spin" />
                                                    ) : (
                                                        <DropdownMenu>
                                                            <DropdownMenuTrigger asChild>
                                                                <Button variant="ghost" size="icon" className="h-8 w-8" aria-label={t("accounting.billingResult.table.actions")}>
                                                                    <MoreHorizontal className="h-4 w-4" />
                                                                </Button>
                                                            </DropdownMenuTrigger>
                                                            <DropdownMenuContent align="end">
                                                                <DropdownMenuItem onClick={() => setDocsFor(s)}>{t("accounting.billingResult.documents.open")}</DropdownMenuItem>
                                                                {canManage && canMoveStatement(s.status, "sent") ? (
                                                                    <DropdownMenuItem onClick={() => void changeStatus(s, "sent")}>
                                                                        {t("accounting.billingResult.action.markSent")}
                                                                    </DropdownMenuItem>
                                                                ) : null}
                                                                {canManage && canMoveStatement(s.status, "paid") ? (
                                                                    <DropdownMenuItem onClick={() => void changeStatus(s, "paid")}>
                                                                        {t("accounting.billingResult.action.markPaid")}
                                                                    </DropdownMenuItem>
                                                                ) : null}
                                                                {canManage && canMoveStatement(s.status, "cancelled") ? (
                                                                    <DropdownMenuItem className="text-destructive" onClick={() => void changeStatus(s, "cancelled")}>
                                                                        {t("accounting.billingResult.action.cancel")}
                                                                    </DropdownMenuItem>
                                                                ) : null}
                                                                {canManage && s.status === "draft" ? (
                                                                    <DropdownMenuItem className="text-destructive" onClick={() => void remove(s)}>
                                                                        {t("accounting.billingResult.action.delete")}
                                                                    </DropdownMenuItem>
                                                                ) : null}
                                                            </DropdownMenuContent>
                                                        </DropdownMenu>
                                                    )}
                                                </TableCell>
                                            </TableRow>
                                        );
                                    })}
                                </TableBody>
                            </Table>
                        </div>
                    )}
                    <LoadMore
                        loaded={rows.length}
                        hasNextPage={list.hasNextPage}
                        isFetchingNextPage={list.isFetchingNextPage}
                        error={list.isFetchNextPageError}
                        onLoadMore={() => void list.fetchNextPage()}
                    />
                </CardContent>
            </Card>

            {docsFor ? <StatementDocumentsDialog statement={docsFor} onClose={() => setDocsFor(null)} /> : null}
        </div>
    );
}
