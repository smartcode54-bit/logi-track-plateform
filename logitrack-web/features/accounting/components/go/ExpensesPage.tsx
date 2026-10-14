"use client";

/**
 * Fuel and other expenses over Go (T42, P3; Appendix E §E.2.3 rows "fuel" and "other"; developer-spec.md
 * §10.10, R35).
 *
 * `['expenses',{type,status,category,driverId,truckId,from,to}]` (`GET /v1/expenses`, 100 per page) with
 * every filter on the server: the page reads only its own type (the Firestore page read every expense of
 * every type and both master lists twice, `expenses.ts:90-101`), the driver and truck pickers search on
 * the server, and the rows render through TanStack Table + Virtual (W9; the Firestore page rendered every
 * row, `fuel/page.tsx:726`, `other/page.tsx:458`). Bangchak retail prices are `['fuel','bangchak',locale]`
 * (`GET /v1/fuel/retail`): a language toggle reads the other language once. Totals and km/L cover the rows
 * loaded so far, and the page says so while more pages exist.
 */
import { useMemo, useState } from "react";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { createColumnHelper } from "@tanstack/react-table";
import { endOfMonth, format, startOfMonth, subDays } from "date-fns";
import { enUS, th as thDateLocale } from "date-fns/locale";
import { Fuel, Plus, RefreshCw, Upload } from "lucide-react";

import { useLanguage } from "@/context/language";
import { PageHeader } from "@/components/page-header";
import { useCan } from "@/features/auth/api/useMe";
import { LoadMore } from "@/components/load-more";
import { VirtualTable } from "@/components/virtual-table";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { DateOnlyRangePicker } from "@/components/ui/date-range-picker";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

import {
    EXPENSE_CATEGORIES,
    EXPENSE_STATUSES,
    expenseRows,
    expensesQuery,
    expenseTotals,
    fuelEfficiency,
    type ExpenseDTO,
    type ExpenseFilter,
    type ExpenseStatus,
    type ExpenseType,
} from "../../api/expenses.go";
import { fuelRetailGoQuery } from "../../api/fuel.go";
import { AddExpenseDialog } from "./AddExpenseDialog";
import { categoryLabel, ExpenseDetailDialog, ExpenseStatusBadge } from "./ExpenseDetailDialog";
import { ApiErrorAlert, Field, formatDateTime, formatThb, SearchPicker, type PickValue } from "./shared";
import { TollImportDialog } from "./TollImportDialog";

const col = createColumnHelper<ExpenseDTO>();

function BangchakCard() {
    const { t, language } = useLanguage();
    const prices = useQuery(fuelRetailGoQuery(language === "th" ? "th" : "en"));
    return (
        <Card>
            <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                <CardTitle className="flex items-center gap-2 text-base">
                    <Fuel className="h-4 w-4 text-amber-500" />
                    {t("accounting.bangchak.title")}
                </CardTitle>
                <Button size="sm" variant="ghost" onClick={() => void prices.refetch()} disabled={prices.isFetching} aria-label={t("accounting.bangchak.refresh")}>
                    <RefreshCw className={`h-4 w-4 ${prices.isFetching ? "animate-spin" : ""}`} />
                </Button>
            </CardHeader>
            <CardContent className="space-y-2">
                {prices.isError ? (
                    <p className="text-sm text-destructive">{t("accounting.bangchak.errorLoad")}</p>
                ) : (prices.data?.items ?? []).length === 0 ? (
                    <p className="text-sm text-muted-foreground">{prices.isPending ? t("common.loading") : t("accounting.bangchak.noData")}</p>
                ) : (
                    <div className="flex flex-wrap gap-2">
                        {prices.data!.items.map((i) => (
                            <Badge key={i.nameTh} variant="outline" className="font-normal">
                                {(language === "th" ? i.nameTh : i.nameEn) || i.nameTh} · ฿{i.price.toLocaleString(undefined, { maximumFractionDigits: 2 })}
                            </Badge>
                        ))}
                    </div>
                )}
                <p className="text-xs text-muted-foreground">
                    {prices.data?.fetchedAt ? `${t("accounting.bangchak.fetchedAt")}: ${formatDateTime(prices.data.fetchedAt)} · ` : ""}
                    {t("accounting.bangchak.disclaimer")}
                </p>
            </CardContent>
        </Card>
    );
}

function ExpensesPage({ type }: { type: ExpenseType }) {
    const { t, language } = useLanguage();
    const isFuel = type === "fuel";
    const canEdit = useCan(isFuel ? "accounting:edit_fuel" : "accounting:edit_other").allowed;
    const [driver, setDriver] = useState<PickValue | null>(null);
    const [truck, setTruck] = useState<PickValue | null>(null);
    const [status, setStatus] = useState<ExpenseStatus | "all">("all");
    const [category, setCategory] = useState<string>("all");
    const [from, setFrom] = useState("");
    const [to, setTo] = useState("");
    const [detail, setDetail] = useState<ExpenseDTO | null>(null);
    const [adding, setAdding] = useState(false);
    const [importing, setImporting] = useState(false);

    const filter: ExpenseFilter = {
        type,
        status: status === "all" ? undefined : status,
        category: isFuel || category === "all" ? undefined : category,
        driverId: driver?.id,
        truckId: truck?.id,
        from: from || undefined,
        to: to || undefined,
    };
    const list = useInfiniteQuery(expensesQuery(filter));
    const rows = expenseRows(list.data?.pages);
    const totals = useMemo(() => expenseTotals(rows), [rows]);
    const efficiency = useMemo(() => (isFuel ? fuelEfficiency(rows) : new Map()), [rows, isFuel]);
    const avgKmpl = useMemo(() => {
        const values = [...efficiency.values()].map((e) => e.kmPerLiter).filter((v): v is number => v != null && v > 0);
        return values.length ? values.reduce((a, b) => a + b, 0) / values.length : null;
    }, [efficiency]);

    const thisMonth = () => {
        const now = new Date();
        setFrom(format(startOfMonth(now), "yyyy-MM-dd"));
        setTo(format(endOfMonth(now), "yyyy-MM-dd"));
    };
    const last30 = () => {
        const now = new Date();
        setFrom(format(subDays(now, 29), "yyyy-MM-dd"));
        setTo(format(now, "yyyy-MM-dd"));
    };
    const reset = () => {
        setFrom("");
        setTo("");
        setDriver(null);
        setTruck(null);
        setStatus("all");
        setCategory("all");
    };

    const columns = useMemo(
        () => [
            col.accessor("date", { header: () => t("accounting.table.date"), cell: (c) => formatDateTime(c.getValue()), meta: { cellClassName: "whitespace-nowrap text-xs" } }),
            col.accessor("licensePlate", { header: () => t("accounting.table.licensePlate"), cell: (c) => c.getValue() ?? "—", meta: { cellClassName: "font-mono text-xs" } }),
            col.accessor("driverName", { header: () => t("accounting.table.driver"), cell: (c) => c.getValue() ?? "—", meta: { cellClassName: "text-xs" } }),
            ...(isFuel
                ? [
                      col.accessor("volumeLiters", {
                          header: () => t("accounting.table.volume"),
                          cell: (c) => (c.getValue() == null ? "—" : c.getValue()!.toLocaleString(undefined, { maximumFractionDigits: 2 })),
                          meta: { headerClassName: "text-right", cellClassName: "text-right text-xs" },
                      }),
                      col.accessor("pricePerLiter", {
                          header: () => t("accounting.table.pricePerLiter"),
                          cell: (c) => (c.getValue() == null ? "—" : c.getValue()!.toFixed(2)),
                          meta: { headerClassName: "text-right", cellClassName: "text-right text-xs" },
                      }),
                      col.accessor("odometerKm", {
                          header: () => t("accounting.table.odometer"),
                          cell: (c) => (c.getValue() == null ? "—" : c.getValue()!.toLocaleString()),
                          meta: { headerClassName: "text-right", cellClassName: "text-right text-xs" },
                      }),
                      col.display({
                          id: "kmpl",
                          header: () => t("accounting.table.kmPerLiter"),
                          cell: ({ row: { original: r } }) => {
                              const e = efficiency.get(r.id);
                              if (!e || e.kmPerLiter == null) return <span className="text-muted-foreground">—</span>;
                              return <span className={e.flag === "out-of-range" ? "font-medium text-red-600" : ""}>{e.kmPerLiter.toFixed(1)}</span>;
                          },
                          meta: { headerClassName: "text-right", cellClassName: "text-right text-xs" },
                      }),
                  ]
                : [
                      col.accessor("category", { header: () => t("accounting.table.category"), cell: (c) => categoryLabel(c.getValue(), t), meta: { cellClassName: "text-xs" } }),
                      col.display({
                          id: "description",
                          header: () => t("accounting.table.description"),
                          cell: ({ row: { original: r } }) => r.description || [r.tollLocation, r.tollLane].filter(Boolean).join(" / ") || "—",
                          meta: { cellClassName: "max-w-[220px] truncate text-xs" },
                      }),
                  ]),
            col.accessor("amountThb", {
                header: () => t("accounting.table.amount"),
                cell: (c) => formatThb(c.getValue()),
                meta: { headerClassName: "text-right", cellClassName: "text-right font-mono" },
            }),
            col.accessor("status", { header: () => t("accounting.audit.tableStatus"), cell: (c) => <ExpenseStatusBadge status={c.getValue()} /> }),
        ],
        [t, isFuel, efficiency]
    );

    return (
        <div className="flex flex-col gap-section">
            <PageHeader
                title={t(isFuel ? "nav.fuel" : "nav.otherExpenses")}
                description={isFuel ? t("accounting.fuel.subtitle") : t("accounting.other.subtitle")}
                actions={
                    <>
                        {!isFuel && canEdit ? (
                            <Button variant="outline" onClick={() => setImporting(true)}>
                                <Upload aria-hidden className="mr-2 h-4 w-4" />
                                {t("accounting.tollImport.button")}
                            </Button>
                        ) : null}
                        <Button variant="outline" onClick={() => void list.refetch()} disabled={list.isFetching}>
                            <RefreshCw aria-hidden className={`mr-2 h-4 w-4 ${list.isFetching ? "animate-spin" : ""}`} />
                            {t("common.refresh")}
                        </Button>
                        {canEdit ? (
                            <Button onClick={() => setAdding(true)}>
                                <Plus aria-hidden className="mr-2 h-4 w-4" />
                                {isFuel ? t("accounting.fuel.addButton") : t("accounting.other.addButton")}
                            </Button>
                        ) : null}
                    </>
                }
            />

            {isFuel ? <BangchakCard /> : null}

            <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
                <Card>
                    <CardContent className="pt-4">
                        <p className="text-xs text-muted-foreground">{t("accounting.dashboard.totalAmount")}</p>
                        <p className="text-2xl font-bold">{formatThb(totals.amount)}</p>
                    </CardContent>
                </Card>
                <Card>
                    <CardContent className="pt-4">
                        <p className="text-xs text-muted-foreground">{t("accounting.dashboard.recordCount")}</p>
                        <p className="text-2xl font-bold">{totals.count.toLocaleString()}</p>
                    </CardContent>
                </Card>
                <Card>
                    <CardContent className="pt-4">
                        <p className="text-xs text-muted-foreground">{t("accounting.dashboard.avgAmount")}</p>
                        <p className="text-2xl font-bold">{formatThb(totals.count ? totals.amount / totals.count : 0)}</p>
                    </CardContent>
                </Card>
                {isFuel ? (
                    <Card>
                        <CardContent className="pt-4">
                            <p className="text-xs text-muted-foreground">{t("accounting.dashboard.avgKmPerLiter")}</p>
                            <p className="text-2xl font-bold">{avgKmpl == null ? "—" : avgKmpl.toFixed(1)}</p>
                        </CardContent>
                    </Card>
                ) : null}
            </div>
            {list.hasNextPage ? <p className="text-xs text-muted-foreground">{t("accounting.go.statsOfLoaded")}</p> : null}

            <Card>
                <CardContent className="flex flex-wrap items-end gap-4 pt-4">
                    <Field label={t("accounting.filter.driver")}>
                        <SearchPicker kind="driver" value={driver} onChange={setDriver} placeholder={t("accounting.filter.all")} clearLabel={t("accounting.filter.all")} />
                    </Field>
                    <Field label={t("accounting.filter.vehicle")}>
                        <SearchPicker kind="truck" value={truck} onChange={setTruck} placeholder={t("accounting.filter.all")} clearLabel={t("accounting.filter.all")} />
                    </Field>
                    <Field label={t("accounting.audit.statusFilter")}>
                        <Select value={status} onValueChange={(v) => setStatus(v as ExpenseStatus | "all")}>
                            <SelectTrigger className="w-40">
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value="all">{t("accounting.audit.statusAll")}</SelectItem>
                                {EXPENSE_STATUSES.map((s) => (
                                    <SelectItem key={s} value={s}>
                                        {t(`accounting.audit.status${s[0].toUpperCase()}${s.slice(1)}`)}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </Field>
                    {!isFuel ? (
                        <Field label={t("accounting.table.category")}>
                            <Select value={category} onValueChange={setCategory}>
                                <SelectTrigger className="w-44">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                    <SelectItem value="all">{t("accounting.filter.all")}</SelectItem>
                                    {EXPENSE_CATEGORIES.map((c) => (
                                        <SelectItem key={c} value={c}>
                                            {categoryLabel(c, t)}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </Field>
                    ) : null}
                    <Field label={t("accounting.go.filter.dateRange")}>
                        <DateOnlyRangePicker
                            from={from}
                            to={to}
                            onChange={(f, tt) => {
                                setFrom(f);
                                setTo(tt);
                            }}
                            locale={language === "th" ? thDateLocale : enUS}
                            className="w-[230px]"
                        />
                    </Field>
                    <div className="flex flex-wrap gap-2">
                        <Button type="button" variant="secondary" size="sm" onClick={thisMonth}>
                            {t("accounting.filter.thisMonth")}
                        </Button>
                        <Button type="button" variant="secondary" size="sm" onClick={last30}>
                            {t("accounting.filter.last30Days")}
                        </Button>
                        <Button type="button" variant="outline" size="sm" onClick={reset}>
                            {t("accounting.filter.reset")}
                        </Button>
                    </div>
                </CardContent>
            </Card>

            <ApiErrorAlert error={list.error} />

            <Card>
                <CardContent className="space-y-3 p-0 pb-3">
                    <VirtualTable
                        data={rows}
                        columns={columns}
                        getRowId={(r) => r.id}
                        aria-label={isFuel ? t("accounting.fuel.title") : t("accounting.other.title")}
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

            {detail ? <ExpenseDetailDialog expense={detail} onClose={() => setDetail(null)} /> : null}
            <AddExpenseDialog type={type} open={adding} onOpenChange={setAdding} />
            {!isFuel ? <TollImportDialog open={importing} onOpenChange={setImporting} /> : null}
        </div>
    );
}

export function FuelExpensesPage() {
    return <ExpensesPage type="fuel" />;
}

export function OtherExpensesPage() {
    return <ExpensesPage type="other" />;
}
