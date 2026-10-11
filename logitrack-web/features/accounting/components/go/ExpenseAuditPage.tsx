"use client";

/**
 * Expense audit over Go (T42, P3; Appendix E §E.2.3 row "audit"; developer-spec.md §10.7 W6).
 *
 * The status filter is a server filter: `['expenses',{status,type,category}]` over `GET /v1/expenses`
 * (no type = fuel and other in one query), so a status switch reads only that status (the Firestore page
 * re-downloaded every expense on each switch and filtered in the browser, `expenses.ts:270-272`,
 * `audit/page.tsx:106-108`). The tabs' counts are `meta.statusCounts` of the first page. Approve, reject
 * and bulk approve are `POST /v1/expenses/{id}/status` and `POST /v1/expenses/bulk-status`; approving a
 * fuel row with an odometer runs the PM check in the same transaction (no `checkMaintenanceAlert`
 * callable). Writes invalidate `['expenses']` and `['badges']`; SSE `expense.*` does it in other tabs.
 */
import { useMemo, useState } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";
import { createColumnHelper } from "@tanstack/react-table";
import { Check, Loader2, RefreshCw } from "lucide-react";
import { toast } from "sonner";

import { useLanguage } from "@/context/language";
import { useCan } from "@/features/auth/api/useMe";
import { apiErrorText } from "@/lib/apiError";
import { LoadMore } from "@/components/load-more";
import { VirtualTable } from "@/components/virtual-table";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";

import {
    bulkExpenseStatus,
    EXPENSE_CATEGORIES,
    expenseRows,
    expensesQuery,
    setExpenseStatus,
    statusCountsOf,
    type ExpenseDTO,
    type ExpenseFilter,
    type ExpenseStatus,
} from "../../api/expenses.go";
import { categoryLabel, ExpenseDetailDialog, ExpenseStatusBadge } from "./ExpenseDetailDialog";
import { ApiErrorAlert, Field, formatDateTime, formatThb } from "./shared";

const col = createColumnHelper<ExpenseDTO>();

/** The type filter: every type, one type, or one category of the other expenses. */
type TypeFilter = "all" | "fuel" | "other" | `other:${string}`;

export function auditFilter(status: ExpenseStatus | "all", type: TypeFilter): ExpenseFilter {
    const f: ExpenseFilter = { status: status === "all" ? undefined : status };
    if (type === "fuel" || type === "other") f.type = type;
    else if (type.startsWith("other:")) {
        f.type = "other";
        f.category = type.slice("other:".length);
    }
    return f;
}

export function ExpenseAuditPage() {
    const { t } = useLanguage();
    const canAudit = useCan("accounting:audit_expense").allowed;
    const [status, setStatus] = useState<ExpenseStatus | "all">("pending");
    const [type, setType] = useState<TypeFilter>("all");
    const [selected, setSelected] = useState<Set<string>>(new Set());
    const [detail, setDetail] = useState<ExpenseDTO | null>(null);
    const [busy, setBusy] = useState<string | null>(null);

    const filter = auditFilter(status, type);
    const list = useInfiniteQuery(expensesQuery(filter));
    const rows = expenseRows(list.data?.pages);
    const counts = statusCountsOf(list.data?.pages);
    const pending = useMemo(() => rows.filter((r) => r.status === "pending"), [rows]);
    const selectedPending = pending.filter((r) => selected.has(r.id));

    const changeFilter = (next: () => void) => {
        setSelected(new Set());
        next();
    };
    const approveOne = async (r: ExpenseDTO) => {
        setBusy(r.id);
        try {
            const out = await setExpenseStatus(r.id, "approved");
            if (out.pmCheck?.created) toast.info(t("accounting.go.expense.pmBooked"));
        } catch (e) {
            toast.error(apiErrorText(e, t));
        } finally {
            setBusy(null);
        }
    };
    const approveSelected = async () => {
        if (selectedPending.length === 0) return;
        setBusy("bulk");
        try {
            const out = await bulkExpenseStatus(
                selectedPending.map((r) => r.id),
                "approved"
            );
            const booked = out.filter((r) => r.pmCheck?.created).length;
            if (booked > 0) toast.info(t("accounting.go.audit.pmBooked", { count: booked }));
            setSelected(new Set());
        } catch (e) {
            toast.error(apiErrorText(e, t));
        } finally {
            setBusy(null);
        }
    };

    const columns = useMemo(
        () => [
            ...(canAudit
                ? [
                      col.display({
                          id: "select",
                          header: () => (
                              <Checkbox
                                  aria-label={t("accounting.audit.selectAll")}
                                  checked={pending.length > 0 && selectedPending.length === pending.length ? true : selectedPending.length > 0 ? "indeterminate" : false}
                                  onCheckedChange={(v) => setSelected(v === true ? new Set(pending.map((r) => r.id)) : new Set())}
                                  disabled={pending.length === 0}
                              />
                          ),
                          cell: ({ row: { original: r } }) =>
                              r.status === "pending" ? (
                                  <Checkbox
                                      aria-label={r.id}
                                      checked={selected.has(r.id)}
                                      onClick={(e) => e.stopPropagation()}
                                      onCheckedChange={(v) =>
                                          setSelected((prev) => {
                                              const next = new Set(prev);
                                              if (v === true) next.add(r.id);
                                              else next.delete(r.id);
                                              return next;
                                          })
                                      }
                                  />
                              ) : null,
                      }),
                  ]
                : []),
            col.accessor("date", { header: () => t("accounting.table.date"), cell: (c) => formatDateTime(c.getValue()), meta: { cellClassName: "whitespace-nowrap text-xs" } }),
            col.display({
                id: "type",
                header: () => t("accounting.audit.tableType"),
                cell: ({ row: { original: r } }) => (
                    <Badge variant="outline" className="text-xs">
                        {r.type === "fuel" ? t("accounting.audit.typeFuel") : `${t("accounting.audit.typeOther")}${r.category ? ` · ${categoryLabel(r.category, t)}` : ""}`}
                    </Badge>
                ),
            }),
            col.accessor("licensePlate", { header: () => t("accounting.table.licensePlate"), cell: (c) => c.getValue() ?? "—", meta: { cellClassName: "font-mono text-xs" } }),
            col.accessor("driverName", { header: () => t("accounting.table.driver"), cell: (c) => c.getValue() ?? "—", meta: { cellClassName: "text-xs" } }),
            col.accessor("amountThb", { header: () => t("accounting.table.amount"), cell: (c) => formatThb(c.getValue()), meta: { headerClassName: "text-right", cellClassName: "text-right font-mono" } }),
            col.accessor("status", { header: () => t("accounting.audit.tableStatus"), cell: (c) => <ExpenseStatusBadge status={c.getValue()} /> }),
            ...(canAudit
                ? [
                      col.display({
                          id: "actions",
                          header: () => t("accounting.table.actions"),
                          cell: ({ row: { original: r } }) =>
                              r.status === "pending" ? (
                                  <Button
                                      size="sm"
                                      variant="ghost"
                                      onClick={(e) => {
                                          e.stopPropagation();
                                          void approveOne(r);
                                      }}
                                      disabled={busy !== null}
                                  >
                                      {busy === r.id ? <Loader2 className="h-4 w-4 animate-spin" /> : <Check className="h-4 w-4 text-green-600" />}
                                      <span className="ml-1">{t("accounting.audit.approve")}</span>
                                  </Button>
                              ) : null,
                      }),
                  ]
                : []),
        ],
        // approveOne reads only stable setters and the current busy flag.
        // eslint-disable-next-line react-hooks/exhaustive-deps
        [t, canAudit, selected, pending, selectedPending.length, busy]
    );

    const statusTab = (s: ExpenseStatus | "all", key: string) => (
        <TabsTrigger value={s} className="gap-2">
            {t(`accounting.audit.${key}`)}
            {s !== "all" ? (
                <Badge variant={s === "pending" ? "destructive" : "secondary"} className="ml-1">
                    {counts[s] ?? 0}
                </Badge>
            ) : null}
        </TabsTrigger>
    );

    return (
        <div className="container mx-auto max-w-[1600px] space-y-6 p-6">
            <div className="flex flex-col justify-between gap-4 md:flex-row md:items-center">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">{t("accounting.audit.title")}</h1>
                    <p className="mt-1 text-muted-foreground">{t("accounting.audit.subtitle")}</p>
                </div>
                <Button variant="outline" onClick={() => void list.refetch()} disabled={list.isFetching}>
                    <RefreshCw className={`mr-2 h-4 w-4 ${list.isFetching ? "animate-spin" : ""}`} />
                    {t("common.refresh")}
                </Button>
            </div>

            <Card>
                <CardContent className="flex flex-wrap items-end gap-4 pt-4">
                    <Tabs value={status} onValueChange={(v) => changeFilter(() => setStatus(v as ExpenseStatus | "all"))}>
                        <TabsList>
                            {statusTab("pending", "statusPending")}
                            {statusTab("approved", "statusApproved")}
                            {statusTab("rejected", "statusRejected")}
                            {statusTab("all", "statusAll")}
                        </TabsList>
                    </Tabs>
                    <Field label={t("accounting.audit.typeFilter")}>
                        <Select value={type} onValueChange={(v) => changeFilter(() => setType(v as TypeFilter))}>
                            <SelectTrigger className="w-56">
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value="all">{t("accounting.audit.typeAll")}</SelectItem>
                                <SelectItem value="fuel">{t("accounting.audit.typeFuel")}</SelectItem>
                                <SelectItem value="other">{t("accounting.audit.typeOtherAll")}</SelectItem>
                                {EXPENSE_CATEGORIES.map((c) => (
                                    <SelectItem key={c} value={`other:${c}`}>
                                        {t("accounting.audit.typeOther")} · {categoryLabel(c, t)}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </Field>
                    {canAudit && pending.length > 0 ? (
                        <div className="ml-auto flex items-center gap-2">
                            <span className="text-sm text-muted-foreground">{t("accounting.audit.selectedCount", { count: selectedPending.length })}</span>
                            <Button variant="outline" size="sm" onClick={() => setSelected(new Set(pending.map((r) => r.id)))}>
                                {t("accounting.audit.selectAll")}
                            </Button>
                            <Button variant="ghost" size="sm" onClick={() => setSelected(new Set())} disabled={selectedPending.length === 0}>
                                {t("accounting.audit.clearSelection")}
                            </Button>
                            <Button size="sm" className="bg-green-600 hover:bg-green-700" onClick={() => void approveSelected()} disabled={selectedPending.length === 0 || busy !== null}>
                                {busy === "bulk" ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Check className="mr-2 h-4 w-4" />}
                                {t("accounting.audit.approveSelected")}
                            </Button>
                        </div>
                    ) : null}
                </CardContent>
            </Card>

            <ApiErrorAlert error={list.error} />

            <Card>
                <CardContent className="space-y-3 p-0 pb-3">
                    <VirtualTable
                        data={rows}
                        columns={columns}
                        getRowId={(r) => r.id}
                        aria-label={t("accounting.audit.title")}
                        infinite={list}
                        loading={list.isPending}
                        error={list.isError}
                        emptyState={<p className="py-8 text-center text-sm text-muted-foreground">{t("accounting.go.expense.empty")}</p>}
                        resetScrollKey={JSON.stringify(filter)}
                        onRowClick={setDetail}
                        rowClassName="cursor-pointer"
                    />
                    <div className="px-4">
                        <LoadMore
                            loaded={rows.length}
                            hasNextPage={list.hasNextPage}
                            isFetchingNextPage={list.isFetchingNextPage}
                            error={list.isFetchNextPageError}
                            onLoadMore={() => void list.fetchNextPage()}
                        />
                    </div>
                </CardContent>
            </Card>

            {detail ? <ExpenseDetailDialog expense={detail} audit onClose={() => setDetail(null)} /> : null}
        </div>
    );
}
