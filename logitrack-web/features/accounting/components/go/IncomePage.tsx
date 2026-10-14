"use client";

/**
 * Income over Go (T42, P3; Appendix E §E.2.3 row "income"; developer-spec.md §10.7, §10.10; R62, R89).
 *
 * The date range is a server filter: `['income',{from,to,party,kind}]` over
 * `GET /v1/trips?axis=delivered&from&to&customerId` and `GET /v1/standby?sort=ended&from&to&customerId`
 * (keyset, 200 per page). The Firestore page read the newest 500 trips and filtered the dates in the
 * browser, so older trips of a range were silently missing (`income/page.tsx:336-342,597-621`). Prices
 * are the stored snapshots the server wrote (the `billing` block, `accounting:view_income`), never
 * computed here. The missing-billing tab is `GET /v1/billing/rows?customerId=all&year&month` (unpriced
 * rows with their reason) for `accounting:billing_document` holders, the route's capability; a user with
 * `accounting:view_income` only (the default operation_staff) gets the unpriced rows of the loaded range
 * instead, as the Firestore page did, never a 403. Totals cover the loaded rows; "Load all" and the export
 * walk every page of the range (xlsx loads on the export click), and the export stops rather than write a
 * partial range. The customer filter is a billing party searched on the server (`CustomerPartySelect`), and
 * the rows' customers are named by id (`useCustomerLookup`; the export resolves every walked row first).
 *
 * Writes call Go and invalidate `['income']` and the month's billing keys: recompute and manual price
 * (T38 routes), the plan date (`PATCH /v1/tasks/{id}`) and the delivered time (`PATCH /v1/trips/{id}`)
 * (T34 routes); `../../api/billingWrites.go.ts` names each route's owner.
 */
import { useMemo, useState } from "react";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { createColumnHelper } from "@tanstack/react-table";
import { endOfMonth, format, parseISO, startOfMonth, subDays } from "date-fns";
import { enUS, th as thDateLocale } from "date-fns/locale";
import { AlertTriangle, DollarSign, Download, Hash, Loader2, Pencil, RefreshCw, TrendingUp, Wand2 } from "lucide-react";
import { toast } from "sonner";

import { useLanguage } from "@/context/language";
import { PageHeader } from "@/components/page-header";
import { useCan } from "@/features/auth/api/useMe";
import { apiErrorText } from "@/lib/apiError";
import { CustomerPartySelect } from "@/components/entity-pickers";
import { LoadMore } from "@/components/load-more";
import { VirtualTable } from "@/components/virtual-table";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { DateOnlyRangePicker } from "@/components/ui/date-range-picker";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";

import { ALL_PARTIES, billingRowsQuery, DEFAULT_ROWS_PARAMS, type UnpricedRowDTO } from "../../api/billing.go";
import { computeTripBilling, patchTaskPlanDate, patchTripDeliveredAt, setTripManualPrice } from "../../api/billingWrites.go";
import {
    incomeCustomerRefs,
    incomeStandbyQuery,
    incomeSummary,
    incomeRows,
    incomeTripsQuery,
    unpricedIncomeRows,
    type IncomeRecordType,
    type IncomeRow,
} from "../../api/income.go";
import { fetchCustomerLookup, useCustomerLookup } from "../../api/useCustomerLookup";
import { BackfillPanel } from "./BackfillPanel";
import { JobCategoryBadge } from "./badges";
import { ApiErrorAlert, formatDateTime, formatDay, formatThb, MONTHS, monthName, yearOptions } from "./shared";

const col = createColumnHelper<IncomeRow>();

/** A list the export may write: not wanted, or loaded to its last page without an error. */
export function walkComplete(q: { status: string; isError: boolean; hasNextPage: boolean }, wanted: boolean): boolean {
    return !wanted || (q.status === "success" && !q.isError && !q.hasNextPage);
}

function ManualPriceDialog({ row, onClose }: { row: IncomeRow; onClose: () => void }) {
    const { t } = useLanguage();
    const [amount, setAmount] = useState(row.estimateThb != null ? String(row.estimateThb) : "");
    const [category, setCategory] = useState<"PRIMARY" | "SUPPLEMENTARY">(row.jobCategory === "SUPPLEMENTARY" ? "SUPPLEMENTARY" : "PRIMARY");
    const [saving, setSaving] = useState(false);
    const value = Number(amount);
    const valid = amount.trim() !== "" && Number.isFinite(value) && value > 0;
    const save = async () => {
        if (!valid || !row.tripId) return;
        setSaving(true);
        try {
            await setTripManualPrice(row.tripId, { billingEstimateThb: value, jobCategory: category });
            onClose();
        } catch (e) {
            toast.error(`${t("accounting.income.editBilling.saveError")} ${apiErrorText(e, t)}`);
        } finally {
            setSaving(false);
        }
    };
    return (
        <Dialog open onOpenChange={(o) => !o && !saving && onClose()}>
            <DialogContent className="sm:max-w-md">
                <DialogHeader>
                    <DialogTitle>{t("accounting.income.editBilling.title")}</DialogTitle>
                    <DialogDescription>
                        {row.label} · {[row.origin, row.destination].filter(Boolean).join(" → ")}
                    </DialogDescription>
                </DialogHeader>
                <div className="space-y-3">
                    <div className="space-y-1">
                        <Label>{t("accounting.income.editBilling.manualRate")}</Label>
                        <Input type="number" step="any" min="0" value={amount} onChange={(e) => setAmount(e.target.value)} />
                        {!valid && amount.trim() !== "" ? <p className="text-xs text-destructive">{t("accounting.income.editBilling.manualRateInvalid")}</p> : null}
                    </div>
                    <div className="space-y-1">
                        <Label>{t("accounting.income.table.jobCategory")}</Label>
                        <Select value={category} onValueChange={(v) => setCategory(v === "SUPPLEMENTARY" ? "SUPPLEMENTARY" : "PRIMARY")}>
                            <SelectTrigger>
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value="PRIMARY">{t("accounting.billingDocument.badge.jobCategoryPrimary")}</SelectItem>
                                <SelectItem value="SUPPLEMENTARY">{t("accounting.billingDocument.badge.jobCategorySupplementary")}</SelectItem>
                            </SelectContent>
                        </Select>
                    </div>
                    <p className="text-xs text-muted-foreground">{t("accounting.go.income.manualHint")}</p>
                </div>
                <DialogFooter>
                    <Button variant="outline" onClick={onClose} disabled={saving}>
                        {t("common.cancel")}
                    </Button>
                    <Button onClick={() => void save()} disabled={!valid || saving}>
                        {saving ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                        {t("accounting.income.editBilling.save")}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}

/** Inline edit of a date cell (`date` or `datetime-local`), saved by `onSave`. */
function DateCellEditor({ initial, kind, onSave, onCancel }: { initial: string; kind: "date" | "datetime-local"; onSave: (v: string) => Promise<void>; onCancel: () => void }) {
    const { t } = useLanguage();
    const [value, setValue] = useState(initial);
    const [saving, setSaving] = useState(false);
    return (
        <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
            <Input type={kind} className="h-7 w-40 text-xs" value={value} onChange={(e) => setValue(e.target.value)} />
            <Button
                size="sm"
                className="h-7 px-2 text-xs"
                disabled={saving || value === ""}
                onClick={async () => {
                    setSaving(true);
                    try {
                        await onSave(value);
                    } finally {
                        setSaving(false);
                    }
                }}
            >
                {saving ? <Loader2 className="h-3 w-3 animate-spin" /> : t("accounting.income.planDate.save")}
            </Button>
            <Button size="sm" variant="ghost" className="h-7 px-2 text-xs" onClick={onCancel} disabled={saving}>
                {t("accounting.income.planDate.cancel")}
            </Button>
        </div>
    );
}

/**
 * The missing-billing tab of a user without `accounting:billing_document` (`GET /v1/billing/rows` needs
 * it): the loaded income rows that carry no price, with the reason stored on them. The rows come from the
 * trips and standby lists `accounting:view_income` already reads, so the tab never calls a route the role
 * cannot use.
 */
function LoadedMissingBillingTab({ rows, hasMore, customerName }: { rows: IncomeRow[]; hasMore: boolean; customerName: (id: string | null) => string }) {
    const { t } = useLanguage();
    const unpriced = useMemo(() => unpricedIncomeRows(rows), [rows]);
    return (
        <div className="space-y-4" data-testid="missing-billing-loaded">
            <p className="text-sm text-muted-foreground">{t("accounting.go.income.missingLoadedHint")}</p>
            {hasMore ? <p className="text-xs text-amber-600">{t("accounting.go.statsOfLoaded")}</p> : null}
            {unpriced.length === 0 ? (
                <p className="py-8 text-center text-sm text-muted-foreground">{t("accounting.income.missing.noMissing")}</p>
            ) : (
                <div className="max-h-[600px] overflow-auto rounded-md border">
                    <Table>
                        <TableHeader>
                            <TableRow>
                                <TableHead>{t("accounting.income.table.tripId")}</TableHead>
                                <TableHead>{t("accounting.income.missing.table.customer")}</TableHead>
                                <TableHead>{t("accounting.income.table.origin")}</TableHead>
                                <TableHead>{t("accounting.income.table.destination")}</TableHead>
                                <TableHead>{t("accounting.income.missing.table.vehicleClass")}</TableHead>
                                <TableHead>{t("accounting.income.table.deliveredAt")}</TableHead>
                                <TableHead>{t("accounting.income.missing.table.reason")}</TableHead>
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {unpriced.map((r) => (
                                <TableRow key={r.id}>
                                    <TableCell className="font-mono text-xs">
                                        {r.label}
                                        {r.recordType === "standby" ? (
                                            <Badge variant="outline" className="ml-1 px-1 py-0 text-[10px]">
                                                STANDBY
                                            </Badge>
                                        ) : null}
                                    </TableCell>
                                    <TableCell className="text-xs">{customerName(r.customerId)}</TableCell>
                                    <TableCell className="text-xs">{r.origin || "—"}</TableCell>
                                    <TableCell className="text-xs">{r.destination || "—"}</TableCell>
                                    <TableCell className="text-xs">{r.vehicleClass ?? "—"}</TableCell>
                                    <TableCell className="text-xs">{formatDateTime(r.deliveredAt)}</TableCell>
                                    <TableCell className="text-xs">
                                        {t(`accounting.billingDocument.unpriced.reason.${r.unpricedReason ?? "not_computed"}`, r.unpricedReason ?? "not_computed")}
                                    </TableCell>
                                </TableRow>
                            ))}
                        </TableBody>
                    </Table>
                </div>
            )}
        </div>
    );
}

function MissingBillingTab({ canRecompute }: { canRecompute: boolean }) {
    const { t, language } = useLanguage();
    const now = new Date();
    const [year, setYear] = useState(now.getFullYear());
    const [month, setMonth] = useState(now.getMonth() + 1);
    const [busy, setBusy] = useState<string | null>(null);
    // Rendered only for `accounting:billing_document` holders (the route's capability); W5 gate kept here too.
    const canRows = useCan("accounting:billing_document").allowed;
    const rows = useQuery(billingRowsQuery(ALL_PARTIES, year, month, DEFAULT_ROWS_PARAMS, canRows));
    const unpriced = rows.data?.unpriced ?? [];
    const customers = useCustomerLookup(unpriced.map((u) => u.customerId ?? u.billingPartyId));
    const recompute = async (u: UnpricedRowDTO) => {
        if (!u.tripId) return;
        setBusy(u.id);
        try {
            const out = await computeTripBilling(u.tripId, { month: { customerId: ALL_PARTIES, year, month } });
            if (out.blocked) toast.error(t("accounting.go.income.blocked", { invoiceNumber: out.blockedInvoiceNumber ?? "" }));
            else if (!out.ok) toast.error(out.error ?? t("accounting.billingDocument.standbyIssues.fixFailed"));
        } catch (e) {
            toast.error(apiErrorText(e, t));
        } finally {
            setBusy(null);
        }
    };
    return (
        <div className="space-y-4">
            <p className="text-sm text-muted-foreground">{t("accounting.go.income.missingHint")}</p>
            <div className="flex flex-wrap items-end gap-4">
                <div className="space-y-1">
                    <Label>{t("accounting.billingDocument.filters.deliveredMonth")}</Label>
                    <Select value={String(month)} onValueChange={(v) => setMonth(Number(v))}>
                        <SelectTrigger className="w-44">
                            <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                            {MONTHS.map((m) => (
                                <SelectItem key={m} value={String(m)}>
                                    {monthName(m, language)}
                                </SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                </div>
                <div className="space-y-1">
                    <Label>{t("accounting.billingDocument.filters.year")}</Label>
                    <Select value={String(year)} onValueChange={(v) => setYear(Number(v))}>
                        <SelectTrigger className="w-28">
                            <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                            {yearOptions().map((y) => (
                                <SelectItem key={y} value={String(y)}>
                                    {y}
                                </SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                </div>
                <Button aria-label={t("common.refresh")} variant="outline" size="sm" onClick={() => void rows.refetch()} disabled={rows.isFetching}>
                    <RefreshCw className={`h-4 w-4 ${rows.isFetching ? "animate-spin" : ""}`} />
                </Button>
            </div>
            <ApiErrorAlert error={rows.error} />
            {rows.isPending ? null : unpriced.length === 0 ? (
                <p className="py-8 text-center text-sm text-muted-foreground">{t("accounting.income.missing.noMissing")}</p>
            ) : (
                <div className="max-h-[600px] overflow-auto rounded-md border">
                    <Table>
                        <TableHeader>
                            <TableRow>
                                <TableHead>{t("accounting.income.table.tripId")}</TableHead>
                                <TableHead>{t("accounting.income.missing.table.customer")}</TableHead>
                                <TableHead>{t("accounting.income.missing.table.lookupHub")}</TableHead>
                                <TableHead>{t("accounting.income.missing.table.lookupDestination")}</TableHead>
                                <TableHead>{t("accounting.income.missing.table.vehicleClass")}</TableHead>
                                <TableHead>{t("accounting.income.table.deliveredAt")}</TableHead>
                                <TableHead>{t("accounting.income.missing.table.reason")}</TableHead>
                                <TableHead className="text-right">{t("accounting.income.missing.table.computedRate")}</TableHead>
                                {canRecompute ? <TableHead /> : null}
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {unpriced.map((u) => (
                                <TableRow key={u.id}>
                                    <TableCell className="font-mono text-xs">
                                        {u.tripNo ?? u.id.slice(0, 8)}
                                        {u.rowType === "standby" ? (
                                            <Badge variant="outline" className="ml-1 px-1 py-0 text-[10px]">
                                                STANDBY
                                            </Badge>
                                        ) : null}
                                    </TableCell>
                                    <TableCell className="text-xs">{customers.label(u.customerId ?? u.billingPartyId)}</TableCell>
                                    <TableCell className="text-xs">{u.hubDisplayName || u.lookupHubCode || u.sourceHub || "—"}</TableCell>
                                    <TableCell className="text-xs">{u.destinationDisplayName || u.lookupDestinationCode || u.destination || "—"}</TableCell>
                                    <TableCell className="text-xs">{u.vehicleClass ?? u.lookupVehicleClass ?? "—"}</TableCell>
                                    <TableCell className="text-xs">{formatDateTime(u.deliveredAt)}</TableCell>
                                    <TableCell className="text-xs">
                                        {t(`accounting.billingDocument.unpriced.reason.${u.reason}`, u.reason)}
                                        <span className="block text-[10px] text-muted-foreground">{t(`accounting.billingDocument.unpriced.source.${u.reasonSource}`, u.reasonSource)}</span>
                                    </TableCell>
                                    <TableCell className="text-right font-mono text-xs">{u.dryRunEstimateThb != null ? formatThb(u.dryRunEstimateThb) : "—"}</TableCell>
                                    {canRecompute ? (
                                        <TableCell>
                                            {u.tripId ? (
                                                <Button size="sm" variant="ghost" disabled={busy !== null} onClick={() => void recompute(u)}>
                                                    {busy === u.id ? <Loader2 className="h-3 w-3 animate-spin" /> : <Wand2 className="h-3 w-3" />}
                                                </Button>
                                            ) : null}
                                        </TableCell>
                                    ) : null}
                                </TableRow>
                            ))}
                        </TableBody>
                    </Table>
                </div>
            )}
        </div>
    );
}

export function IncomePage() {
    const { t, language } = useLanguage();
    const dateLocale = language === "th" ? thDateLocale : enUS;
    const canRecompute = useCan("accounting:recompute_force").allowed;
    const canOverride = useCan("accounting:override_price").allowed;
    const canEditTrips = useCan("operations:edit_trip_details").allowed;
    const canEditTasks = useCan("operations:manage_tasks").allowed;
    const canBillingRows = useCan("accounting:billing_document").allowed;
    const client = useQueryClient();
    const now = new Date();
    const [from, setFrom] = useState(format(startOfMonth(now), "yyyy-MM-dd"));
    const [to, setTo] = useState(format(endOfMonth(now), "yyyy-MM-dd"));
    // The trip and standby filters match the billing party, so the customer picker's value is the party id.
    const [party, setParty] = useState("all");
    const [recordType, setRecordType] = useState<"all" | IncomeRecordType>("all");
    const [tab, setTab] = useState("with-billing");
    const [showSummary, setShowSummary] = useState(true);
    const [editing, setEditing] = useState<{ id: string; field: "delivered" | "plan" } | null>(null);
    const [manual, setManual] = useState<IncomeRow | null>(null);
    const [walking, setWalking] = useState(false);
    const [exporting, setExporting] = useState(false);
    const [busyRow, setBusyRow] = useState<string | null>(null);

    const filter = { from, to, party };
    const wantTrips = recordType !== "standby";
    const wantStandby = recordType === "all" || recordType === "standby";
    const trips = useInfiniteQuery(incomeTripsQuery(filter, wantTrips));
    const standby = useInfiniteQuery(incomeStandbyQuery(filter, wantStandby));

    // The loaded rows' customers by id (a standby record names its billing party only), one cached query each.
    const customers = useCustomerLookup(
        incomeCustomerRefs(wantTrips ? trips.data?.pages : undefined, wantStandby ? standby.data?.pages : undefined)
    );
    const rowOpts = { trips: wantTrips, standby: wantStandby, recordType, customerOfParty: (p: string | null) => customers.get(p)?.id ?? null };
    const rows = useMemo(
        () => incomeRows(trips.data?.pages, standby.data?.pages, rowOpts),
        // rowOpts is rebuilt per render from these values.
        // eslint-disable-next-line react-hooks/exhaustive-deps
        [trips.data, standby.data, wantTrips, wantStandby, recordType, customers]
    );
    const priced = useMemo(() => rows.filter((r) => r.estimateThb != null), [rows]);
    const summary = useMemo(() => incomeSummary(priced), [priced]);
    const customerName = (id: string | null) => customers.label(id);

    const hasMore = (wantTrips && trips.hasNextPage) || (wantStandby && standby.hasNextPage);
    const fetchingMore = trips.isFetchingNextPage || standby.isFetchingNextPage;
    const infinite = {
        hasNextPage: Boolean(hasMore),
        isFetchingNextPage: fetchingMore,
        isFetching: trips.isFetching || standby.isFetching,
        isFetchNextPageError: trips.isFetchNextPageError || standby.isFetchNextPageError,
        fetchNextPage: async () => {
            await Promise.all([
                wantTrips && trips.hasNextPage ? trips.fetchNextPage({ cancelRefetch: false }) : null,
                wantStandby && standby.hasNextPage ? standby.fetchNextPage({ cancelRefetch: false }) : null,
            ]);
        },
    };

    /**
     * Walks every remaining page of the range (the totals and the export need them all); resolves the pages
     * and whether the walk reached the end of every wanted list (`fetchNextPage` resolves on an error, it
     * does not throw, so the result says it).
     */
    const loadAll = async () => {
        setWalking(true);
        let tr = trips;
        let sb = standby;
        try {
            for (let i = 0; i < 500; i++) {
                const next = [
                    wantTrips && tr.hasNextPage ? tr.fetchNextPage({ cancelRefetch: false }) : null,
                    wantStandby && sb.hasNextPage ? sb.fetchNextPage({ cancelRefetch: false }) : null,
                ];
                if (next.every((n) => n === null)) break;
                const [a, b] = await Promise.all(next);
                if (a) tr = a as typeof tr;
                if (b) sb = b as typeof sb;
                if ((a && a.isError) || (b && b.isError)) break;
            }
        } finally {
            setWalking(false);
        }
        return {
            trips: tr.data?.pages,
            standby: sb.data?.pages,
            complete: walkComplete(tr, wantTrips) && walkComplete(sb, wantStandby),
        };
    };

    const exportExcel = async () => {
        setExporting(true);
        try {
            // The rows of the whole range, from the pages the walk returned (this render's rows are stale).
            const pages = await loadAll();
            // A failed page, a list still loading or the walk's bound: never write a partial range silently.
            if (!pages.complete) {
                toast.error(t("accounting.go.income.exportIncomplete"));
                return;
            }
            // Every walked row's customer resolved before the file is written (pages past the screen too).
            const names = await fetchCustomerLookup(client, incomeCustomerRefs(wantTrips ? pages.trips : undefined, wantStandby ? pages.standby : undefined));
            const all = incomeRows(pages.trips, pages.standby, { ...rowOpts, customerOfParty: (p) => names.get(p)?.id ?? null });
            const XLSX = await import("xlsx");
            const sheet = XLSX.utils.json_to_sheet(
                all.map((r) => ({
                    [t("accounting.income.table.tripId")]: r.label,
                    [t("accounting.income.filter.recordType")]: r.recordType,
                    [t("accounting.income.table.customerName")]: names.label(r.customerId),
                    [t("accounting.income.table.origin")]: r.origin,
                    [t("accounting.income.table.destination")]: r.destination,
                    [t("accounting.income.table.truckType")]: r.vehicleClass ?? "",
                    [t("accounting.income.table.jobCategory")]: r.jobCategory ?? "",
                    [t("accounting.income.table.baseRate")]: r.baseRateThb ?? "",
                    [t("accounting.income.table.ruleMultiplier")]: r.rateMultiplier ?? "",
                    [t("accounting.income.table.addPerTrip")]: r.addThbPerTrip ?? "",
                    [t("accounting.income.table.finalRate")]: r.estimateThb ?? "",
                    [t("accounting.income.table.deliveredAt")]: r.deliveredAt ? format(parseISO(r.deliveredAt), "yyyy-MM-dd HH:mm") : "",
                    [t("accounting.income.table.planDate")]: r.billingDate ? r.billingDate.slice(0, 10) : "",
                    [t("accounting.income.table.rateImportId")]: r.rateImportId ?? "",
                }))
            );
            const wb = XLSX.utils.book_new();
            XLSX.utils.book_append_sheet(wb, sheet, "Income");
            XLSX.writeFile(wb, `income_${from}_${to}.xlsx`);
        } catch (e) {
            toast.error(apiErrorText(e, t));
        } finally {
            setExporting(false);
        }
    };

    const recompute = async (r: IncomeRow) => {
        if (!r.tripId || r.recordType === "standby") return;
        setBusyRow(r.id);
        try {
            const out = await computeTripBilling(r.tripId, { forceRecompute: canRecompute });
            if (out.blocked) toast.error(t("accounting.go.income.blocked", { invoiceNumber: out.blockedInvoiceNumber ?? "" }));
            else if (!out.ok) toast.error(out.error ?? t("accounting.billingDocument.standbyIssues.fixFailed"));
        } catch (e) {
            toast.error(apiErrorText(e, t));
        } finally {
            setBusyRow(null);
        }
    };

    const columns = useMemo(
        () => [
            col.display({
                id: "trip",
                header: () => t("accounting.income.table.tripId"),
                cell: ({ row: { original: r } }) => (
                    <div className="flex flex-col gap-1 font-mono text-xs">
                        <span>{r.label}</span>
                        {r.recordType === "standby" ? (
                            <Badge variant="outline" className="w-fit border-orange-300 bg-orange-50 px-1 py-0 text-[10px] text-orange-700">
                                STANDBY
                            </Badge>
                        ) : r.recordType === "multi_drop" ? (
                            <Badge variant="outline" className="w-fit border-blue-300 bg-blue-50 px-1 py-0 text-[10px] text-blue-700">
                                MULTI-DROP
                            </Badge>
                        ) : null}
                        {r.manualOverride ? (
                            <Badge variant="outline" className="w-fit px-1 py-0 text-[10px] text-muted-foreground">
                                {t("accounting.billingDocument.badge.suppManualPrice")}
                            </Badge>
                        ) : null}
                    </div>
                ),
            }),
            col.display({ id: "customer", header: () => t("accounting.income.table.customerName"), cell: ({ row: { original: r } }) => customerName(r.customerId), meta: { cellClassName: "text-xs" } }),
            col.accessor("origin", { header: () => t("accounting.income.table.origin"), cell: (c) => c.getValue() || "—", meta: { cellClassName: "text-xs" } }),
            col.accessor("destination", { header: () => t("accounting.income.table.destination"), cell: (c) => c.getValue() || "—", meta: { cellClassName: "text-xs" } }),
            col.accessor("vehicleClass", { header: () => t("accounting.income.table.truckType"), cell: (c) => c.getValue() ?? "—", meta: { cellClassName: "text-xs" } }),
            col.display({
                id: "category",
                header: () => t("accounting.income.table.jobCategory"),
                cell: ({ row: { original: r } }) => <JobCategoryBadge value={r.jobCategory} kind={r.recordType === "standby" ? "standby" : undefined} />,
            }),
            col.accessor("baseRateThb", { header: () => t("accounting.income.table.baseRate"), cell: (c) => formatThb(c.getValue()), meta: { headerClassName: "text-right", cellClassName: "text-right text-xs" } }),
            col.accessor("rateMultiplier", {
                header: () => t("accounting.income.table.ruleMultiplier"),
                cell: (c) => (c.getValue() != null ? `*${c.getValue()!.toFixed(2)}` : "—"),
                meta: { headerClassName: "text-right", cellClassName: "text-right font-mono text-xs" },
            }),
            col.accessor("addThbPerTrip", { header: () => t("accounting.income.table.addPerTrip"), cell: (c) => formatThb(c.getValue()), meta: { headerClassName: "text-right", cellClassName: "text-right text-xs" } }),
            col.display({
                id: "final",
                header: () => t("accounting.income.table.finalRate"),
                cell: ({ row: { original: r } }) =>
                    r.estimateThb != null ? (
                        <span className="font-semibold">{formatThb(r.estimateThb)}</span>
                    ) : (
                        <span className="text-xs text-amber-600">{t(`accounting.billingDocument.unpriced.reason.${r.unpricedReason ?? "not_computed"}`, r.unpricedReason ?? "—")}</span>
                    ),
                meta: { headerClassName: "text-right", cellClassName: "text-right" },
            }),
            col.display({
                id: "delivered",
                header: () => t("accounting.income.table.deliveredAt"),
                cell: ({ row: { original: r } }) =>
                    editing?.id === r.id && editing.field === "delivered" && r.tripId ? (
                        <DateCellEditor
                            kind="datetime-local"
                            initial={r.deliveredAt ? format(parseISO(r.deliveredAt), "yyyy-MM-dd'T'HH:mm") : ""}
                            onCancel={() => setEditing(null)}
                            onSave={async (v) => {
                                try {
                                    await patchTripDeliveredAt(r.tripId!, new Date(v).toISOString());
                                    setEditing(null);
                                } catch (e) {
                                    toast.error(`${t("accounting.income.planDate.saveFailed")} ${apiErrorText(e, t)}`);
                                }
                            }}
                        />
                    ) : (
                        <div className="flex items-center gap-1 whitespace-nowrap text-xs">
                            {formatDateTime(r.deliveredAt)}
                            {canEditTrips && r.tripId && r.recordType !== "standby" ? (
                                <Button size="sm" variant="ghost" className="h-6 px-1" aria-label={t("accounting.income.table.deliveredAt")} onClick={() => setEditing({ id: r.id, field: "delivered" })}>
                                    <Pencil className="h-3 w-3" />
                                </Button>
                            ) : null}
                        </div>
                    ),
            }),
            col.display({
                id: "plan",
                header: () => t("accounting.income.table.planDate"),
                cell: ({ row: { original: r } }) =>
                    editing?.id === r.id && editing.field === "plan" && r.taskId ? (
                        <DateCellEditor
                            kind="date"
                            initial={(r.billingDate ?? r.deliveredAt ?? "").slice(0, 10)}
                            onCancel={() => setEditing(null)}
                            onSave={async (v) => {
                                try {
                                    await patchTaskPlanDate(r.taskId!, v);
                                    toast.success(t("accounting.income.planDate.saved"));
                                    setEditing(null);
                                } catch (e) {
                                    toast.error(`${t("accounting.income.planDate.saveFailed")} ${apiErrorText(e, t)}`);
                                }
                            }}
                        />
                    ) : (
                        <div className="flex items-center gap-1 whitespace-nowrap text-xs">
                            {formatDay(r.billingDate)}
                            {canEditTasks && r.taskId && r.recordType !== "standby" ? (
                                <Button size="sm" variant="ghost" className="h-6 px-1" aria-label={t("accounting.income.table.planDate")} onClick={() => setEditing({ id: r.id, field: "plan" })}>
                                    <Pencil className="h-3 w-3" />
                                </Button>
                            ) : null}
                        </div>
                    ),
            }),
            col.accessor("rateImportId", { header: () => t("accounting.income.table.rateImportId"), cell: (c) => c.getValue() ?? "—", meta: { cellClassName: "font-mono text-xs" } }),
            col.display({
                id: "fuel",
                header: () => t("accounting.income.table.fuelRuleRef"),
                cell: ({ row: { original: r } }) =>
                    `${r.fuelAdjustmentId ? `${r.fuelAdjustmentId.slice(0, 8)}…` : "—"}${r.fuelEffectiveFromDate ? ` · ${r.fuelEffectiveFromDate}` : ""}`,
                meta: { cellClassName: "max-w-[140px] truncate text-xs" },
            }),
            col.display({
                id: "actions",
                header: () => "",
                cell: ({ row: { original: r } }) =>
                    r.recordType === "standby" || !r.tripId ? null : (
                        <div className="flex gap-1">
                            {canRecompute ? (
                                <Button size="sm" variant="ghost" className="h-7 px-1" aria-label={t("accounting.go.income.recompute")} title={t("accounting.go.income.recompute")} disabled={busyRow !== null} onClick={() => void recompute(r)}>
                                    {busyRow === r.id ? <Loader2 className="h-3 w-3 animate-spin" /> : <Wand2 className="h-3 w-3" />}
                                </Button>
                            ) : null}
                            {canOverride ? (
                                <Button size="sm" variant="ghost" className="h-7 px-1" aria-label={t("accounting.income.editBilling.action")} title={t("accounting.income.editBilling.action")} onClick={() => setManual(r)}>
                                    <Pencil className="h-3 w-3" />
                                </Button>
                            ) : null}
                        </div>
                    ),
            }),
        ],
        // customerName reads customers; editing and busyRow drive the cells.
        // eslint-disable-next-line react-hooks/exhaustive-deps
        [t, customers, editing, busyRow, canRecompute, canOverride, canEditTrips, canEditTasks]
    );

    const error = trips.error ?? standby.error;

    return (
        <div className="flex flex-col gap-section">
            <PageHeader
                title={t("nav.income")}
                description={t("accounting.income.subtitle")}
                actions={
                    <>
                        <Button variant="outline" onClick={() => void exportExcel()} disabled={exporting || rows.length === 0}>
                            {exporting ? <Loader2 aria-hidden className="mr-2 h-4 w-4 animate-spin" /> : <Download aria-hidden className="mr-2 h-4 w-4" />}
                            {t("accounting.income.export")}
                        </Button>
                        <Button
                            variant="outline"
                            onClick={() => {
                                void trips.refetch();
                                void standby.refetch();
                            }}
                        >
                            <RefreshCw aria-hidden className="mr-2 h-4 w-4" />
                            {t("common.refresh")}
                        </Button>
                    </>
                }
            />

            <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
                <Card>
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium">{t("accounting.income.stats.totalIncome")}</CardTitle>
                        <DollarSign className="h-4 w-4 text-muted-foreground" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold">฿{Math.round(summary.total).toLocaleString()}</div>
                    </CardContent>
                </Card>
                <Card>
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium">{t("accounting.income.stats.tripCount")}</CardTitle>
                        <Hash className="h-4 w-4 text-muted-foreground" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold">{summary.count.toLocaleString()}</div>
                    </CardContent>
                </Card>
                <Card>
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium">{t("accounting.income.stats.avgIncome")}</CardTitle>
                        <TrendingUp className="h-4 w-4 text-muted-foreground" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold">฿{Math.round(summary.average).toLocaleString()}</div>
                    </CardContent>
                </Card>
            </div>
            {hasMore ? (
                <div className="flex flex-wrap items-center gap-3 text-xs text-muted-foreground">
                    <AlertTriangle className="h-4 w-4 text-amber-500" />
                    <span>{t("accounting.go.statsOfLoaded")}</span>
                    <Button size="sm" variant="outline" onClick={() => void loadAll()} disabled={walking}>
                        {walking ? <Loader2 className="mr-2 h-3 w-3 animate-spin" /> : null}
                        {t("accounting.go.income.loadAll")}
                    </Button>
                </div>
            ) : null}

            {summary.byCustomer.length > 0 ? (
                <Card>
                    <CardHeader className="pb-3">
                        <div className="flex items-center justify-between">
                            <CardTitle className="text-base">{t("accounting.income.summary.title")}</CardTitle>
                            <Button variant="ghost" size="sm" onClick={() => setShowSummary(!showSummary)}>
                                {showSummary ? t("common.hide") : t("common.show")}
                            </Button>
                        </div>
                    </CardHeader>
                    {showSummary ? (
                        <CardContent>
                            <Table>
                                <TableHeader>
                                    <TableRow>
                                        <TableHead>{t("accounting.income.summary.customer")}</TableHead>
                                        <TableHead className="text-right">{t("accounting.income.summary.trips")}</TableHead>
                                        <TableHead className="text-right">{t("accounting.income.summary.total")}</TableHead>
                                        <TableHead className="text-right">{t("accounting.income.summary.percent")}</TableHead>
                                    </TableRow>
                                </TableHeader>
                                <TableBody>
                                    {summary.byCustomer.map((s) => (
                                        <TableRow key={s.customerId || "none"}>
                                            <TableCell className="font-medium">{customerName(s.customerId || null)}</TableCell>
                                            <TableCell className="text-right">{s.count.toLocaleString()}</TableCell>
                                            <TableCell className="text-right font-semibold">฿{Math.round(s.total).toLocaleString()}</TableCell>
                                            <TableCell className="text-right text-muted-foreground">
                                                {summary.total > 0 ? `${((s.total / summary.total) * 100).toFixed(1)}%` : "—"}
                                            </TableCell>
                                        </TableRow>
                                    ))}
                                </TableBody>
                            </Table>
                        </CardContent>
                    ) : null}
                </Card>
            ) : null}

            {canRecompute ? (
                <Card>
                    <CardHeader>
                        <CardTitle className="flex items-center gap-2 text-base">
                            <Wand2 className="h-4 w-4" />
                            {t("accounting.income.backfill.title")}
                        </CardTitle>
                        <p className="text-sm font-normal text-muted-foreground">{t("accounting.income.backfill.subtitle")}</p>
                    </CardHeader>
                    <CardContent>
                        <BackfillPanel defaultFrom={from} defaultTo={to} />
                    </CardContent>
                </Card>
            ) : null}

            <Card>
                <CardContent className="space-y-4 pt-6">
                    <Tabs value={tab} onValueChange={setTab}>
                        <TabsList>
                            <TabsTrigger value="with-billing" className="gap-2">
                                {t("accounting.income.tabs.withBilling")}
                                <Badge variant="secondary" className="ml-1">
                                    {rows.length}
                                </Badge>
                            </TabsTrigger>
                            <TabsTrigger value="missing-billing" className="gap-2" data-testid="missing-billing-tab">
                                <AlertTriangle className="h-4 w-4 text-amber-500" />
                                {t("accounting.income.tabs.missingBilling")}
                            </TabsTrigger>
                        </TabsList>

                        <TabsContent value="with-billing" className="mt-4 space-y-4">
                            <div className="flex flex-wrap items-end gap-4 rounded-lg border p-4">
                                <div className="flex min-w-[200px] flex-col gap-1.5">
                                    <Label>{t("accounting.income.filter.customer")}</Label>
                                    <CustomerPartySelect
                                        aria-label={t("accounting.income.filter.customer")}
                                        value={party}
                                        onValueChange={(p) => setParty(p || "all")}
                                        allValue="all"
                                        allLabel={t("accounting.filter.all")}
                                    />
                                </div>
                                <div className="flex min-w-[180px] flex-col gap-1.5">
                                    <Label>{t("accounting.income.filter.recordType")}</Label>
                                    <Select value={recordType} onValueChange={(v) => setRecordType(v as "all" | IncomeRecordType)}>
                                        <SelectTrigger>
                                            <SelectValue />
                                        </SelectTrigger>
                                        <SelectContent>
                                            <SelectItem value="all">{t("accounting.filter.all")}</SelectItem>
                                            <SelectItem value="trip">{t("accounting.income.recordType.trip")}</SelectItem>
                                            <SelectItem value="multi_drop">{t("accounting.income.recordType.multiDrop")}</SelectItem>
                                            <SelectItem value="standby">{t("accounting.income.recordType.standby")}</SelectItem>
                                        </SelectContent>
                                    </Select>
                                </div>
                                <div className="flex flex-col gap-1.5">
                                    <Label>{t("accounting.income.filter.deliveredRange")}</Label>
                                    <DateOnlyRangePicker
                                        from={from}
                                        to={to}
                                        onChange={(f, tt) => {
                                            setFrom(f);
                                            setTo(tt);
                                        }}
                                        locale={dateLocale}
                                        className="w-[230px]"
                                    />
                                </div>
                                <div className="flex flex-wrap gap-2">
                                    <Button
                                        type="button"
                                        variant="secondary"
                                        size="sm"
                                        onClick={() => {
                                            const n = new Date();
                                            setFrom(format(startOfMonth(n), "yyyy-MM-dd"));
                                            setTo(format(endOfMonth(n), "yyyy-MM-dd"));
                                        }}
                                    >
                                        {t("accounting.filter.thisMonth")}
                                    </Button>
                                    <Button
                                        type="button"
                                        variant="secondary"
                                        size="sm"
                                        onClick={() => {
                                            const n = new Date();
                                            setFrom(format(subDays(n, 29), "yyyy-MM-dd"));
                                            setTo(format(n, "yyyy-MM-dd"));
                                        }}
                                    >
                                        {t("accounting.filter.last30Days")}
                                    </Button>
                                </div>
                            </div>
                            <ApiErrorAlert error={error} />
                            <VirtualTable
                                data={rows}
                                columns={columns}
                                getRowId={(r) => r.id}
                                aria-label={t("accounting.income.title")}
                                infinite={infinite}
                                loading={(wantTrips && trips.isPending) || (wantStandby && standby.isPending)}
                                error={Boolean(error)}
                                emptyState={<p className="py-8 text-center text-sm text-muted-foreground">{t("accounting.income.noRecords")}</p>}
                                resetScrollKey={JSON.stringify([filter, recordType])}
                                maxHeight={640}
                            />
                            <LoadMore
                                loaded={rows.length}
                                hasNextPage={Boolean(hasMore)}
                                isFetchingNextPage={fetchingMore}
                                error={infinite.isFetchNextPageError}
                                onLoadMore={() => void infinite.fetchNextPage()}
                            />
                        </TabsContent>
                        <TabsContent value="missing-billing" className="mt-4">
                            {canBillingRows ? (
                                <MissingBillingTab canRecompute={canRecompute} />
                            ) : (
                                <LoadedMissingBillingTab rows={rows} hasMore={Boolean(hasMore)} customerName={customerName} />
                            )}
                        </TabsContent>
                    </Tabs>
                </CardContent>
            </Card>

            {manual ? <ManualPriceDialog row={manual} onClose={() => setManual(null)} /> : null}
        </div>
    );
}
