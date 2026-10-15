"use client";

/** Stored server billing amounts, loaded-row totals and Bangkok delivery times. */
import { useMemo, useRef, useState } from "react";
import { useInfiniteQuery, useQuery, useQueryClient, type UseQueryResult } from "@tanstack/react-query";
import { createColumnHelper } from "@tanstack/react-table";
import { enUS, th as thDateLocale } from "date-fns/locale";
import { AlertTriangle, Download, Loader2, MoreHorizontal, Pencil, RefreshCw, Wand2 } from "lucide-react";
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

import { billingRowsQuery, DEFAULT_ROWS_PARAMS, type BillingRowsDTO, type UnpricedRowDTO } from "../../api/billing.go";
import { computeTripBilling, patchTripDeliveredAt, setTripManualPrice } from "../../api/billingWrites.go";
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
import { ForceRepriceDialog } from "./ForceRepriceDialog";
import { bangkokDateTimeInput, bangkokInputInstant, bangkokExcelDateTime, formatBangkokDateTime, formatBillingDay, bangkokMonthRange, bangkokLast30Days } from "../../lib/billingUiDates";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { JobCategoryBadge } from "./badges";
import { ApiErrorAlert, formatThbSatang as formatThb, MONTHS, monthName, yearOptions } from "./shared";

const col = createColumnHelper<IncomeRow>();

function PriceDetails({ row: r }: { row: IncomeRow }) {
    const { t } = useLanguage();
    const facts: [string, string][] = [];
    const add = (key: string, value: string | number | null | undefined) => { if (value != null && value !== "") facts.push([t(key), String(value)]); };
    add("accounting.ui4.rateSource", r.rateSource);
    add("accounting.standbyRates.table.effectiveFrom", r.rateEffectiveFromDate);
    add("accounting.ui4.rateEntry", r.rateEntryId);
    add("accounting.income.missing.table.lookupHub", r.lookupHubCode);
    add("accounting.income.missing.table.lookupDestination", r.lookupDestinationCode);
    add("accounting.income.table.truckType", r.vehicleClass);
    if (r.jobCategory) add("accounting.income.table.jobCategory", t(`accounting.billingDocument.badge.jobCategory${r.jobCategory === "PRIMARY" ? "Primary" : r.jobCategory === "SUPPLEMENTARY" ? "Supplementary" : "Unknown"}`));
    if (r.baseRateThb != null) add("accounting.income.table.baseRate", formatThb(r.baseRateThb));
    if (r.rateMultiplier != null) add("accounting.income.table.ruleMultiplier", `${Number(((r.rateMultiplier - 1) * 100).toFixed(4))}%`);
    if (r.addThbPerTrip != null) add("accounting.income.table.addPerTrip", formatThb(r.addThbPerTrip));
    add("accounting.ui4.fuelEffective", r.fuelEffectiveFromDate);
    if (r.referenceFuelPriceThb != null) add("accounting.ui4.referenceFuelPrice", formatThb(r.referenceFuelPriceThb));
    if (r.fuelBandLowerThb != null || r.fuelBandUpperThb != null) add("accounting.ui4.fuelBand", `${formatThb(r.fuelBandLowerThb)}–${formatThb(r.fuelBandUpperThb)}`);
    if (r.stopChargeThb != null) add("accounting.ui4.stopCharges", formatThb(r.stopChargeThb));
    if (facts.length === 0 && !r.stops?.length) return "—";
    return <details className="min-w-44 text-xs"><summary className="cursor-pointer max-md:min-h-11">{t("accounting.ui4.priceDetails")}</summary>
        <dl className="mt-2 space-y-1">{facts.map(([label, value]) => <div key={label}><dt className="text-muted-foreground">{label}</dt><dd className="break-words tabular-nums">{value}</dd></div>)}</dl>
        {r.stops?.length ? <div className="mt-2"><p>{t("accounting.ui4.stopCharges")}</p><ul>{r.stops.map((stop) => <li key={stop.stopIndex}>{stop.stopIndex} · {stop.destinationCode} · {formatThb(stop.baseRateThb)} → {formatThb(stop.finalRateThb)}</li>)}</ul></div> : null}
    </details>;
}

/** A list the export may write: not wanted, or loaded to its last page without an error. */
export function walkComplete(q: { status: string; isError: boolean; hasNextPage: boolean }, wanted: boolean): boolean {
    return !wanted || (q.status === "success" && !q.isError && !q.hasNextPage);
}

type ManualPriceRow = Pick<IncomeRow, "tripId" | "label" | "origin" | "destination" | "estimateThb" | "jobCategory">;

function ManualPriceDialog({ row, onClose }: { row: ManualPriceRow; onClose: () => void }) {
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
                        <Label htmlFor="manual-amount">{t("accounting.income.editBilling.manualRate")}</Label>
                        <Input id="manual-amount" type="number" step="any" min="0" value={amount} onChange={(e) => setAmount(e.target.value)} />
                        {!valid && amount.trim() !== "" ? <p className="text-xs text-destructive">{t("accounting.income.editBilling.manualRateInvalid")}</p> : null}
                    </div>
                    <div className="space-y-1">
                        <Label htmlFor="manual-category">{t("accounting.income.table.jobCategory")}</Label>
                        <Select value={category} onValueChange={(v) => setCategory(v === "SUPPLEMENTARY" ? "SUPPLEMENTARY" : "PRIMARY")}>
                            <SelectTrigger id="manual-category" className="max-md:min-h-11">
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
                    <Button variant="outline" className="max-md:min-h-11" onClick={onClose} disabled={saving}>
                        {t("common.cancel")}
                    </Button>
                    <Button className="max-md:min-h-11" onClick={() => void save()} disabled={!valid || saving}>
                        {saving ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                        {t("accounting.income.editBilling.save")}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}

/** Inline edit of a Bangkok delivered time, saved by `onSave`. */
function DateCellEditor({ initial, onSave, onCancel }: { initial: string; onSave: (v: string) => Promise<void>; onCancel: () => void }) {
    const { t } = useLanguage();
    const [value, setValue] = useState(initial);
    const [saving, setSaving] = useState(false);
    return (
        <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
            <Input aria-label={t("accounting.ui4.editDeliveredAt")} type="datetime-local" className="h-7 w-40 text-xs max-md:min-h-11" value={value} onChange={(e) => setValue(e.target.value)} />
            <Button
                size="sm"
                className="h-7 px-2 text-xs max-md:min-h-11"
                disabled={saving || value === "" || value === initial}
                onClick={async () => {
                    setSaving(true);
                    try {
                        await onSave(value);
                    } finally {
                        setSaving(false);
                    }
                }}
            >
                {saving ? <Loader2 className="h-3 w-3 animate-spin" /> : t("common.save")}
            </Button>
            <Button size="sm" variant="ghost" className="h-7 px-2 text-xs max-md:min-h-11" onClick={onCancel} disabled={saving}>
                {t("common.cancel")}
            </Button>
        </div>
    );
}

/** Shared by the priced table and both missing-price views, under the same edit capability. */
function DeliveredTimeCell({ tripId, deliveredAt, canEdit }: { tripId: string | null; deliveredAt: string | null; canEdit: boolean }) {
    const { t } = useLanguage();
    const client = useQueryClient();
    const [editing, setEditing] = useState(false);
    if (editing && canEdit && tripId) return <DateCellEditor initial={bangkokDateTimeInput(deliveredAt)} onCancel={() => setEditing(false)} onSave={async (v) => {
        try {
            await patchTripDeliveredAt(tripId, bangkokInputInstant(v), client, deliveredAt);
            setEditing(false);
        } catch (e) {
            toast.error(`${t("accounting.ui4.dateSaveFailed")} ${apiErrorText(e, t)}`);
        }
    }} />;
    return <div className="flex items-center gap-1 whitespace-nowrap text-xs">
        {formatBangkokDateTime(deliveredAt)}
        {canEdit && tripId ? <Button size="sm" variant="ghost" className="h-6 px-1 max-md:min-h-11 max-md:min-w-11" aria-label={t("accounting.ui4.editDeliveredAt")} onClick={() => setEditing(true)}><Pencil className="h-3 w-3" /></Button> : null}
    </div>;
}

type MissingEditProps = { canEditTrips: boolean; canOverride: boolean; onManual: (row: ManualPriceRow) => void };

function MissingManualAction({ row, onManual }: { row: ManualPriceRow; onManual: MissingEditProps["onManual"] }) {
    const { t } = useLanguage();
    return <DropdownMenu>
        <DropdownMenuTrigger asChild><Button size="icon" variant="ghost" className="max-md:min-h-11 max-md:min-w-11" aria-label={t("accounting.ui4.rowActions")}><MoreHorizontal className="h-4 w-4" /></Button></DropdownMenuTrigger>
        <DropdownMenuContent align="end"><DropdownMenuItem onSelect={() => onManual(row)}>{t("accounting.income.editBilling.action")}</DropdownMenuItem></DropdownMenuContent>
    </DropdownMenu>;
}

/**
 * The missing-billing tab of a user without `accounting:billing_document` (`GET /v1/billing/rows` needs
 * it): the loaded income rows that carry no price, with the reason stored on them. The rows come from the
 * trips and standby lists `accounting:view_income` already reads, so the tab never calls a route the role
 * cannot use.
 */
function LoadedMissingBillingTab({ rows, hasMore, customerName, canEditTrips, canOverride, onManual }: { rows: IncomeRow[]; hasMore: boolean; customerName: (id: string | null) => string } & MissingEditProps) {
    const { t } = useLanguage();
    const unpriced = useMemo(() => unpricedIncomeRows(rows), [rows]);
    return (
        <div className="space-y-4" data-testid="missing-billing-loaded">
            <p className="text-sm text-muted-foreground">{t("accounting.go.income.missingLoadedHint")}</p>
            {hasMore ? <p className="text-xs text-warning">{t("accounting.go.statsOfLoaded")}</p> : null}
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
                                {canOverride ? <TableHead /> : null}
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {unpriced.map((r) => (
                                <TableRow key={r.id}>
                                    <TableCell className="font-mono text-xs">
                                        {r.label || t("accounting.income.recordType.standby")}{r.recordType === "standby" ? ` · ${formatBangkokDateTime(r.deliveredAt)}` : ""}
                                        {r.recordType === "standby" ? (
                                            <Badge variant="outline" className="ml-1 px-1 py-0 text-xs">
                                                {t("accounting.income.recordType.standby")}
                                            </Badge>
                                        ) : null}
                                    </TableCell>
                                    <TableCell className="text-xs">{customerName(r.customerId)}</TableCell>
                                    <TableCell className="text-xs">{r.origin || "—"}</TableCell>
                                    <TableCell className="text-xs">{r.destination || "—"}</TableCell>
                                    <TableCell className="text-xs">{r.vehicleClass ?? "—"}</TableCell>
                                    <TableCell className="text-xs"><DeliveredTimeCell tripId={r.tripId} deliveredAt={r.deliveredAt} canEdit={canEditTrips && r.recordType !== "standby"} /></TableCell>
                                    <TableCell className="text-xs">
                                        {t(`accounting.billingDocument.unpriced.reason.${r.unpricedReason ?? "not_computed"}`, r.unpricedReason ?? "not_computed")}
                                    </TableCell>
                                    {canOverride ? <TableCell>{r.tripId && r.recordType !== "standby" ? <MissingManualAction row={r} onManual={onManual} /> : null}</TableCell> : null}
                                </TableRow>
                            ))}
                        </TableBody>
                    </Table>
                </div>
            )}
        </div>
    );
}

function MissingBillingTab({ year, month, party, setYear, setMonth, rows, canRecompute, canEditTrips, canOverride, onManual }: {
    party: string;
    year: number; month: number; setYear: (year: number) => void; setMonth: (month: number) => void;
    rows: UseQueryResult<BillingRowsDTO>;
    canRecompute: boolean;
} & MissingEditProps) {
    const { t, language } = useLanguage();
    const [busy, setBusy] = useState<string | null>(null);
    const unpriced = rows.data?.unpriced ?? [];
    const customers = useCustomerLookup(unpriced.map((u) => u.customerId ?? u.billingPartyId));
    const recompute = async (u: UnpricedRowDTO) => {
        if (!u.tripId) return;
        setBusy(u.id);
        try {
            const out = await computeTripBilling(u.tripId, { month: { customerId: party, year, month } });
            if (out.blocked) toast.error(t("accounting.go.income.blocked", { invoiceNumber: out.blockedInvoiceNumber ?? "" }));
            else if (!out.ok) toast.error(out.error ?? t("accounting.billingDocument.standbyIssues.fixFailed"));
            else toast.success(t(out.billingDateMoved ? "accounting.ui4.billingDateMoved" : out.skipped ? "accounting.ui4.repriceSkipped" : "accounting.ui4.repriceSucceeded"));
        } catch (e) {
            toast.error(apiErrorText(e, t));
        } finally {
            setBusy(null);
        }
    };
    return (
        <div className="space-y-4">
            <p className="text-sm text-muted-foreground">{t("accounting.ui4.missingMonthHint")}</p>
            <div className="flex flex-wrap items-end gap-4">
                <div className="space-y-1">
                    <Label htmlFor="income-missing-month">{t("accounting.billingDocument.filters.deliveredMonth")}</Label>
                    <Select value={String(month)} onValueChange={(v) => setMonth(Number(v))}>
                        <SelectTrigger id="income-missing-month" aria-label={t("accounting.billingDocument.filters.deliveredMonth")} className="w-44 max-md:min-h-11">
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
                    <Label htmlFor="income-missing-year">{t("accounting.billingDocument.filters.year")}</Label>
                    <Select value={String(year)} onValueChange={(v) => setYear(Number(v))}>
                        <SelectTrigger id="income-missing-year" aria-label={t("accounting.billingDocument.filters.year")} className="w-28 max-md:min-h-11">
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
                <Button aria-label={t("common.refresh")} variant="outline" size="sm" className="max-md:min-h-11 max-md:min-w-11" onClick={() => void rows.refetch()} disabled={rows.isFetching}>
                    <RefreshCw className={`h-4 w-4 ${rows.isFetching ? "animate-spin" : ""}`} />
                </Button>
            </div>
            <ApiErrorAlert error={rows.error} />
            {rows.isPending || rows.error ? null : unpriced.length === 0 ? (
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
                                {canRecompute || canOverride ? <TableHead /> : null}
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {unpriced.map((u) => (
                                <TableRow key={u.id}>
                                    <TableCell className="font-mono text-xs">
                                        {u.rowType === "standby" ? `${u.driverName || t("accounting.income.recordType.standby")} · ${formatBangkokDateTime(u.deliveredAt)}` : u.tripNo ?? u.id.slice(0, 8)}
                                        {u.rowType === "standby" ? (
                                            <Badge variant="outline" className="ml-1 px-1 py-0 text-xs">
                                                {t("accounting.income.recordType.standby")}
                                            </Badge>
                                        ) : null}
                                    </TableCell>
                                    <TableCell className="text-xs">{customers.label(u.customerId ?? u.billingPartyId)}</TableCell>
                                    <TableCell className="text-xs">{u.hubDisplayName || u.lookupHubCode || u.sourceHub || "—"}</TableCell>
                                    <TableCell className="text-xs">{u.destinationDisplayName || u.lookupDestinationCode || u.destination || "—"}</TableCell>
                                    <TableCell className="text-xs">{u.vehicleClass ?? u.lookupVehicleClass ?? "—"}</TableCell>
                                    <TableCell className="text-xs"><DeliveredTimeCell tripId={u.tripId} deliveredAt={u.deliveredAt} canEdit={canEditTrips && u.rowType !== "standby"} /></TableCell>
                                    <TableCell className="text-xs">
                                        {t(`accounting.billingDocument.unpriced.reason.${u.reason}`, u.reason)}
                                        <span className="block text-xs text-muted-foreground">{t(`accounting.billingDocument.unpriced.source.${u.reasonSource}`, u.reasonSource)}</span>
                                    </TableCell>
                                    <TableCell className="text-right font-mono text-xs">{u.dryRunEstimateThb != null ? formatThb(u.dryRunEstimateThb) : "—"}</TableCell>
                                    {canRecompute || canOverride ? (
                                        <TableCell>
                                            {canRecompute && u.tripId ? (
                                                <Button size="sm" variant="ghost" className="max-md:min-h-11 max-md:min-w-11" disabled={busy !== null} onClick={() => void recompute(u)} aria-label={t("accounting.go.income.recompute")}>
                                                    {busy === u.id ? <Loader2 className="h-3 w-3 animate-spin" aria-hidden /> : <Wand2 className="h-3 w-3" aria-hidden />}
                                                </Button>
                                            ) : null}
                                            {canOverride && u.tripId && u.rowType !== "standby" ? <MissingManualAction row={{ tripId: u.tripId, label: u.tripNo ?? "", origin: u.hubDisplayName || u.sourceHub || "", destination: u.destinationDisplayName || u.destination || "", estimateThb: null, jobCategory: u.jobCategory === "SUPPLEMENTARY" ? "SUPPLEMENTARY" : "PRIMARY" }} onManual={onManual} /> : null}
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
    const canBillingRows = useCan("accounting:billing_document").allowed;
    const client = useQueryClient();
    const currentMonth = bangkokMonthRange();
    const [from, setFrom] = useState(currentMonth.from);
    const [to, setTo] = useState(currentMonth.to);
    const [missingYear, setMissingYear] = useState(currentMonth.year);
    const [missingMonth, setMissingMonth] = useState(currentMonth.month);
    const [backfillOpen, setBackfillOpen] = useState(false);
    const backfillButton = useRef<HTMLButtonElement>(null);
    const [forceRow, setForceRow] = useState<IncomeRow | null>(null);
    // The trip and standby filters match the billing party, so the customer picker's value is the party id.
    const [party, setParty] = useState("all");
    const [recordType, setRecordType] = useState<"all" | IncomeRecordType>("all");
    const [tab, setTab] = useState("with-billing");
    const [showSummary, setShowSummary] = useState(true);
    const [manual, setManual] = useState<ManualPriceRow | null>(null);
    const [walking, setWalking] = useState(false);
    const [exporting, setExporting] = useState(false);
    const [busyRow, setBusyRow] = useState<string | null>(null);

    const filter = { from, to, party };
    const wantTrips = recordType !== "standby";
    const wantStandby = recordType === "all" || recordType === "standby";
    const trips = useInfiniteQuery(incomeTripsQuery(filter, wantTrips));
    const standby = useInfiniteQuery(incomeStandbyQuery(filter, wantStandby));

    const missingQuery = useQuery(billingRowsQuery(party, missingYear, missingMonth, DEFAULT_ROWS_PARAMS, canBillingRows));
    const monthStart = `${missingYear}-${String(missingMonth).padStart(2, "0")}-01`;
    const monthEnd = `${missingYear}-${String(missingMonth).padStart(2, "0")}-${new Date(Date.UTC(missingYear, missingMonth, 0)).getUTCDate()}`;
    const monthlyFilter = { from: monthStart, to: monthEnd, party };
    const missingTrips = useInfiniteQuery(incomeTripsQuery(monthlyFilter, !canBillingRows && wantTrips));
    const missingStandby = useInfiniteQuery(incomeStandbyQuery(monthlyFilter, !canBillingRows && wantStandby));
    // The loaded rows' customers by id (a standby record names its billing party only), one cached query each.
    const customers = useCustomerLookup(
        [...incomeCustomerRefs(wantTrips ? trips.data?.pages : undefined, wantStandby ? standby.data?.pages : undefined), ...incomeCustomerRefs(!canBillingRows ? missingTrips.data?.pages : undefined, !canBillingRows ? missingStandby.data?.pages : undefined)]
    );
    const rowOpts = { trips: wantTrips, standby: wantStandby, recordType, customerOfParty: (p: string | null) => customers.get(p)?.id ?? null };
    const rows = useMemo(
        () => incomeRows(trips.data?.pages, standby.data?.pages, rowOpts),
        // rowOpts is rebuilt per render from these values.
        // eslint-disable-next-line react-hooks/exhaustive-deps
        [trips.data, standby.data, wantTrips, wantStandby, recordType, customers]
    );
    const monthlyRows = incomeRows(missingTrips.data?.pages, missingStandby.data?.pages, rowOpts);
    const missingHasMore = (wantTrips && missingTrips.hasNextPage) || (wantStandby && missingStandby.hasNextPage);
    const missingFetchingMore = (wantTrips && missingTrips.isFetchingNextPage) || (wantStandby && missingStandby.isFetchingNextPage);
    const missingNextError = (wantTrips && missingTrips.isFetchNextPageError) || (wantStandby && missingStandby.isFetchNextPageError);
    const loadMoreMissing = async () => {
        await Promise.all([
            wantTrips && missingTrips.hasNextPage ? missingTrips.fetchNextPage({ cancelRefetch: false }) : null,
            wantStandby && missingStandby.hasNextPage ? missingStandby.fetchNextPage({ cancelRefetch: false }) : null,
        ]);
    };
    const missingPending = canBillingRows ? missingQuery.isPending : (wantTrips && missingTrips.isPending) || (wantStandby && missingStandby.isPending);
    const missingError = canBillingRows ? missingQuery.error : (wantTrips && missingTrips.error) || (wantStandby && missingStandby.error);
    const missingCount = missingPending || missingError ? undefined : canBillingRows ? missingQuery.data?.unpriced.length : unpricedIncomeRows(monthlyRows).length;
    const pending = (wantTrips && trips.isPending) || (wantStandby && standby.isPending);
    const error = (wantTrips && trips.error) || (wantStandby && standby.error);
    const ready = !pending && !error;
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
                    [t("accounting.income.filter.recordType")]: t(`accounting.income.recordType.${r.recordType === "multi_drop" ? "multiDrop" : r.recordType}`),
                    [t("accounting.income.table.customerName")]: names.label(r.customerId),
                    [t("accounting.income.table.origin")]: r.origin,
                    [t("accounting.income.table.destination")]: r.destination,
                    [t("accounting.income.table.truckType")]: r.vehicleClass ?? "",
                    [t("accounting.income.table.jobCategory")]: r.jobCategory ? t(`accounting.billingDocument.badge.jobCategory${r.jobCategory === "PRIMARY" ? "Primary" : r.jobCategory === "SUPPLEMENTARY" ? "Supplementary" : "Unknown"}`) : "",
                    [t("accounting.income.table.baseRate")]: r.baseRateThb ?? "",
                    [t("accounting.income.table.ruleMultiplier")]: r.rateMultiplier ?? "",
                    [t("accounting.income.table.addPerTrip")]: r.addThbPerTrip ?? "",
                    [t("accounting.income.table.finalRate")]: r.estimateThb ?? "",
                    [t("accounting.income.table.deliveredAt")]: bangkokExcelDateTime(r.deliveredAt),
                    [t("accounting.ui4.billingDate")]: r.billingDate ? r.billingDate.slice(0, 10) : "",
                }))
            );
            const wb = XLSX.utils.book_new();
            XLSX.utils.book_append_sheet(wb, sheet, t("accounting.ui4.exportSheet"));
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
            else toast.success(t(out.billingDateMoved ? "accounting.ui4.billingDateMoved" : out.skipped ? "accounting.ui4.repriceSkipped" : "accounting.ui4.repriceSucceeded"));
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
                        <span>{r.label || t("accounting.income.recordType.standby")}{r.recordType === "standby" ? ` · ${formatBangkokDateTime(r.deliveredAt)}` : ""}</span>
                        {r.recordType === "standby" ? (
                            <Badge variant="outline" className="w-fit px-1 py-0 text-xs">
                                {t("accounting.income.recordType.standby")}
                            </Badge>
                        ) : r.recordType === "multi_drop" ? (
                            <Badge variant="outline" className="w-fit px-1 py-0 text-xs">
                                {t("accounting.income.recordType.multiDrop")}
                            </Badge>
                        ) : null}
                        {r.manualOverride ? (
                            <Badge variant="outline" className="w-fit px-1 py-0 text-xs text-muted-foreground">
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
                cell: ({ row: { original: r } }) => <span className="font-semibold">{formatThb(r.estimateThb)}</span>,
                meta: { headerClassName: "text-right", cellClassName: "text-right tabular-nums font-semibold" },
            }),
            col.display({
                id: "delivered",
                header: () => t("accounting.income.table.deliveredAt"),
                cell: ({ row: { original: r } }) => <DeliveredTimeCell tripId={r.tripId} deliveredAt={r.deliveredAt} canEdit={canEditTrips && r.recordType !== "standby"} />,
            }),
            col.display({ id: "billingDate", header: () => t("accounting.ui4.billingDate"), cell: ({ row: { original: r } }) => formatBillingDay(r.billingDate), meta: { cellClassName: "text-xs whitespace-nowrap" } }),
            col.display({ id: "priceDetails", header: () => t("accounting.ui4.priceDetails"), cell: ({ row: { original: r } }) => <PriceDetails row={r} /> }),
            col.display({
                id: "actions",
                header: () => "",
                cell: ({ row: { original: r } }) =>
                    r.recordType === "standby" || !r.tripId || (!canRecompute && !canOverride) ? null : (
                        <DropdownMenu>
                            <DropdownMenuTrigger asChild><Button size="icon" variant="ghost" className="max-md:min-h-11 max-md:min-w-11" aria-label={t("accounting.ui4.rowActions")}><MoreHorizontal className="h-4 w-4" /></Button></DropdownMenuTrigger>
                            <DropdownMenuContent align="end">
                                {canRecompute ? <DropdownMenuItem disabled={busyRow !== null} onSelect={() => setForceRow(r)}>{t("accounting.go.income.recompute")}</DropdownMenuItem> : null}
                                {canOverride ? <DropdownMenuItem onSelect={() => setManual(r)}>{t("accounting.income.editBilling.action")}</DropdownMenuItem> : null}
                            </DropdownMenuContent>
                        </DropdownMenu>
                    ),
            }),
        ],
        // customerName reads customers; busyRow drives the actions.
        // eslint-disable-next-line react-hooks/exhaustive-deps
        [t, customers, busyRow, canRecompute, canOverride, canEditTrips]
    );


    return (
        <div className="flex flex-col gap-section">
            <PageHeader
                title={t("nav.income")}
                description={t("accounting.income.subtitle")}
                actions={
                    <>
                        {canRecompute ? <Button ref={backfillButton} className="max-md:min-h-11" variant="outline" onClick={() => setBackfillOpen(true)}><Wand2 aria-hidden className="mr-2 h-4 w-4" />{t("accounting.income.backfill.title")}</Button> : null}
                        <Button variant="outline" className="max-md:min-h-11" onClick={() => void exportExcel()} disabled={exporting || rows.length === 0}>
                            {exporting ? <Loader2 aria-hidden className="mr-2 h-4 w-4 animate-spin" /> : <Download aria-hidden className="mr-2 h-4 w-4" />}
                            {t("accounting.income.export")}
                        </Button>
                        <Button
                            variant="outline"
                            className="max-md:min-h-11"
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

            <div className="flex flex-wrap items-end gap-4">
                <div className="flex min-w-[200px] flex-col gap-1.5">
                    <Label htmlFor="income-customer">{t("accounting.income.filter.customer")}</Label>
                    <CustomerPartySelect
                        id="income-customer"
                        aria-label={t("accounting.income.filter.customer")}
                        value={party}
                        onValueChange={(p) => setParty(p || "all")}
                        allValue="all"
                        allLabel={t("accounting.filter.all")}
                    />
                </div>
                <div className="flex min-w-[180px] flex-col gap-1.5">
                    <Label htmlFor="income-record-type">{t("accounting.income.filter.recordType")}</Label>
                    <Select value={recordType} onValueChange={(v) => setRecordType(v as "all" | IncomeRecordType)}>
                        <SelectTrigger className="max-md:min-h-11" id="income-record-type" aria-label={t("accounting.income.filter.recordType")}>
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
                    <Label htmlFor="income-delivered-range">{t("accounting.income.filter.deliveredRange")}</Label>
                    <DateOnlyRangePicker
                        id="income-delivered-range"
                        aria-label={t("accounting.income.filter.deliveredRange")}
                        from={from}
                        to={to}
                        onChange={(f, tt) => {
                            setFrom(f);
                            setTo(tt);
                        }}
                        locale={dateLocale}
                        className="w-[230px] max-md:min-h-11"
                    />
                </div>
                <div className="flex flex-wrap gap-2">
                    <Button
                        type="button"
                        variant="secondary"
                        size="sm"
                        className="max-md:min-h-11"
                        onClick={() => {
                            const range = bangkokMonthRange();
                            setFrom(range.from);
                            setTo(range.to);
                        }}
                    >
                        {t("accounting.filter.thisMonth")}
                    </Button>
                    <Button
                        type="button"
                        variant="secondary"
                        size="sm"
                        className="max-md:min-h-11"
                        onClick={() => {
                            const range = bangkokLast30Days();
                            setFrom(range.from);
                            setTo(range.to);
                        }}
                    >
                        {t("accounting.filter.last30Days")}
                    </Button>
                </div>
            </div>

            <ApiErrorAlert error={error} />
            <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
                <Card>
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium">{t("accounting.income.stats.totalIncome")}</CardTitle>
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-semibold tabular-nums">{ready ? formatThb(summary.total) : "—"}</div>
                        <p className="text-xs text-muted-foreground">{t("accounting.ui4.loadedBasis")}</p>
                    </CardContent>
                </Card>
                <Card>
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium">{t("accounting.income.stats.tripCount")}</CardTitle>
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-semibold tabular-nums">{ready ? summary.count.toLocaleString() : "—"}</div>
                        <p className="text-xs text-muted-foreground">{t("accounting.ui4.loadedBasis")}</p>
                        <Button variant="link" className="h-auto whitespace-normal p-0 text-xs max-md:min-h-11" onClick={() => setTab("missing-billing")}>{t("accounting.ui4.priceMissingRows", { count: ready ? rows.length - priced.length : "—" })}</Button>
                    </CardContent>
                </Card>
                <Card>
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium">{t("accounting.income.stats.avgIncome")}</CardTitle>
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-semibold tabular-nums">{ready ? formatThb(summary.average) : "—"}</div>
                        <p className="text-xs text-muted-foreground">{t("accounting.ui4.loadedBasis")}</p>
                    </CardContent>
                </Card>
            </div>
            {hasMore ? (
                <div className="flex flex-wrap items-center gap-3 text-xs text-muted-foreground">
                    <AlertTriangle className="h-4 w-4 text-warning" />
                    <span>{t("accounting.go.statsOfLoaded")}</span>
                    <Button size="sm" variant="outline" className="max-md:min-h-11" onClick={() => void loadAll()} disabled={walking}>
                        {walking ? <Loader2 className="mr-2 h-3 w-3 animate-spin" /> : null}
                        {t("accounting.go.income.loadAll")}
                    </Button>
                </div>
            ) : null}

            {ready && summary.byCustomer.length > 0 ? (
                <Card>
                    <CardHeader className="pb-3">
                        <div className="flex items-center justify-between">
                            <CardTitle className="text-base">{t("accounting.income.summary.title")}</CardTitle>
                            <span className="text-xs text-muted-foreground">{t("accounting.ui4.loadedBasis")}</span>
                            <Button variant="ghost" size="sm" className="max-md:min-h-11" onClick={() => setShowSummary(!showSummary)}>
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
                                            <TableCell className="text-right font-semibold tabular-nums">{formatThb(s.total)}</TableCell>
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

            <Card>
                <CardContent className="space-y-4 pt-6">
                    <Tabs value={tab} onValueChange={setTab}>
                        <TabsList>
                            <TabsTrigger value="with-billing" className="gap-2 max-md:min-h-11">
                                {t("accounting.income.tabs.withBilling")}
                                <Badge variant="secondary" className="ml-1">
                                    {ready ? priced.length : "—"}
                                </Badge>
                            </TabsTrigger>
                            <TabsTrigger value="missing-billing" className="gap-2 max-md:min-h-11" data-testid="missing-billing-tab">
                                <AlertTriangle className="h-4 w-4 text-warning" />
                                {t("accounting.income.tabs.missingBilling")}
                                <Badge variant="outline"><span aria-hidden="true">{missingCount ?? "—"}</span><span className="sr-only">{t("accounting.ui4.missingCount", { count: missingCount ?? "—" })}</span></Badge>
                            </TabsTrigger>
                        </TabsList>

                        <TabsContent value="with-billing" className="mt-4 space-y-4">
                            <VirtualTable
                                data={priced}
                                columns={columns}
                                getRowId={(r) => r.id}
                                aria-label={t("nav.income")}
                                infinite={infinite}
                                loading={(wantTrips && trips.isPending) || (wantStandby && standby.isPending)}
                                error={Boolean(error)}
                                emptyState={<p className="py-8 text-center text-sm text-muted-foreground">{t("accounting.income.noRecords")}</p>}
                                resetScrollKey={JSON.stringify([filter, recordType])}
                                maxHeight={640}
                            />
                            <div className="max-md:[&_button]:min-h-11"><LoadMore
                                loaded={rows.length}
                                hasNextPage={Boolean(hasMore)}
                                isFetchingNextPage={fetchingMore}
                                error={infinite.isFetchNextPageError}
                                onLoadMore={() => void infinite.fetchNextPage()}
                            /></div>
                        </TabsContent>
                        <TabsContent value="missing-billing" className="mt-4">
                            {canBillingRows ? (
                                <MissingBillingTab party={party} canEditTrips={canEditTrips} canOverride={canOverride} onManual={setManual} year={missingYear} month={missingMonth} setYear={setMissingYear} setMonth={setMissingMonth} rows={missingQuery} canRecompute={canRecompute} />
                            ) : (
                                <div className="space-y-4">
                                    <p className="text-sm text-muted-foreground">{t("accounting.ui4.missingMonthHint")} · {monthName(missingMonth, language)} {missingYear}</p>
                                    <div className="flex flex-wrap gap-3">
                                        <Select value={String(missingMonth)} onValueChange={(v) => setMissingMonth(Number(v))}><SelectTrigger aria-label={t("accounting.filter.month")} className="w-44 max-md:min-h-11"><SelectValue /></SelectTrigger><SelectContent>{MONTHS.map((m) => <SelectItem key={m} value={String(m)}>{monthName(m, language)}</SelectItem>)}</SelectContent></Select>
                                        <Select value={String(missingYear)} onValueChange={(v) => setMissingYear(Number(v))}><SelectTrigger aria-label={t("accounting.filter.year")} className="w-28 max-md:min-h-11"><SelectValue /></SelectTrigger><SelectContent>{yearOptions().map((y) => <SelectItem key={y} value={String(y)}>{y}</SelectItem>)}</SelectContent></Select>
                                    </div>
                                    <ApiErrorAlert error={missingError} />
                                    {missingError && !missingNextError ? <Button variant="outline" className="max-md:min-h-11" onClick={() => {
                                        if (wantTrips) void missingTrips.refetch();
                                        if (wantStandby) void missingStandby.refetch();
                                    }}>{t("shell.retry")}</Button> : null}
                                    {!missingPending && (!missingError || monthlyRows.length > 0) ? <LoadedMissingBillingTab rows={monthlyRows} hasMore={Boolean(missingHasMore)} customerName={customerName} canEditTrips={canEditTrips} canOverride={canOverride} onManual={setManual} /> : <p className="text-sm text-muted-foreground">—</p>}
                                    {!missingPending ? <div className="max-md:[&_button]:min-h-11"><LoadMore loaded={monthlyRows.length} hasNextPage={Boolean(missingHasMore)} isFetchingNextPage={Boolean(missingFetchingMore)} error={Boolean(missingNextError)} onLoadMore={() => void loadMoreMissing()} /></div> : null}
                                </div>
                            )}
                        </TabsContent>
                    </Tabs>
                </CardContent>
            </Card>

            {backfillOpen ? <Dialog open onOpenChange={setBackfillOpen}><DialogContent onCloseAutoFocus={(event) => { event.preventDefault(); backfillButton.current?.focus(); }} className="max-h-[90dvh] overflow-y-auto sm:max-w-2xl"><DialogHeader><DialogTitle>{t("accounting.income.backfill.title")}</DialogTitle><DialogDescription>{t("accounting.income.backfill.subtitle")}</DialogDescription></DialogHeader><BackfillPanel defaultFrom={from} defaultTo={to} defaultCustomer={party} /></DialogContent></Dialog> : null}
            {forceRow ? <ForceRepriceDialog scope={{ from: formatBillingDay(forceRow.billingDate ?? bangkokDateTimeInput(forceRow.deliveredAt)), to: formatBillingDay(forceRow.billingDate ?? bangkokDateTimeInput(forceRow.deliveredAt)), customer: customerName(forceRow.customerId), kinds: `${t("accounting.go.backfill.trips")} · ${forceRow.label}`, limit: 1 }} onCancel={() => setForceRow(null)} onConfirm={() => { void recompute(forceRow); setForceRow(null); }} /> : null}
            {manual ? <ManualPriceDialog row={manual} onClose={() => setManual(null)} /> : null}
        </div>
    );
}
