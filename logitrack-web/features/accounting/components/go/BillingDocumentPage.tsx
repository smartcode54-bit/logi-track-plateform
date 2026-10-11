"use client";

/**
 * Billing Document over Go (T42, P3; Appendix E §E.2.3 row "billing-document"; developer-spec.md §6.14,
 * §9.9, §10.9; ADR 0005, 0008, 0027, 0028; R18, R62, R69, R89).
 *
 * One request per answer instead of the six-hop browser chain (`billing.ts:737`):
 * `['billing','rows',cust,y,m,{types,categories,plate,vehicleClass,reviewMonth}]` over
 * `GET /v1/billing/rows`, plus `['billing','standbyDiag',cust,y,m]` and `['billing','missingDate',cust,y,m]`,
 * all started by "Load" (no focus refetch, 5 min). The invoice set is the server's (`inInvoiceSet`,
 * `invoice` totals, the lines `POST /v1/billing/statements` inserts); the review filters (plate, vehicle
 * class, review month) narrow only the preview (`inPreview`, `preview` totals) and block the statement
 * while active. Unpriced rows carry their reason (R62). "Create statement" numbers and saves it in one
 * transaction; Go renders the PDF, the detail XLSX and the ZIP into `statement_documents` (R69), which the
 * documents dialog downloads (filenames with the customer's real code, WHT from the statement, R18). No
 * PDF or spreadsheet code runs in the browser.
 *
 * Repairs (a forced recompute, a standby customer) call the T38 routes (`../../api/billingWrites.go.ts`)
 * and invalidate only this month's keys.
 */
import { useMemo, useState } from "react";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { createColumnHelper } from "@tanstack/react-table";
import { AlertTriangle, FileText, Loader2, RefreshCw, Wrench, X } from "lucide-react";
import { toast } from "sonner";

import { useLanguage } from "@/context/language";
import { useCan } from "@/features/auth/api/useMe";
import { apiErrorText } from "@/lib/apiError";
import { VirtualTable } from "@/components/virtual-table";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

import {
    ALL_PARTIES,
    billingRowsQuery,
    createStatement,
    DEFAULT_ROWS_PARAMS,
    FILTER_NONE,
    INVOICE_CATEGORIES,
    INVOICE_TYPES,
    invalidateBillingMonth,
    missingBillingDateQuery,
    standbyDiagnosticsQuery,
    type BillingRowDTO,
    type BillingRowsDTO,
    type BillingRowsParams,
    type StandbyIssueDTO,
    type StatementDTO,
} from "../../api/billing.go";
import { assignStandbyCustomer, computeStandbyBilling, computeTripBilling } from "../../api/billingWrites.go";

import { JobCategoryBadge } from "./badges";
import { ApiErrorAlert, CustomerSelect, formatDateTime, formatDay, formatThb, MONTHS, monthName, useCustomerPicks, yearOptions } from "./shared";
import { StatementDocumentsDialog } from "./StatementDocumentsDialog";

interface LoadedPeriod {
    customerId: string;
    year: number;
    month: number;
}

/** Makes the period's billing keys stale (a repair whose last call failed before it could). */
const computeTripBillingInvalidate = (p: LoadedPeriod) => invalidateBillingMonth(p.customerId, p.year, p.month);

const TYPE_COLORS: Record<string, string> = { trip: "bg-blue-500", standby: "bg-orange-500", multidrop: "bg-purple-500" };

/** The `reviewMonth` option label: `MMMM yyyy` of a `yyyy-MM` value, or "no date". */
function reviewMonthLabel(value: string, language: string, none: string): string {
    if (value === FILTER_NONE) return none;
    const m = /^(\d{4})-(\d{2})$/.exec(value);
    return m ? `${monthName(Number(m[2]), language)} ${m[1]}` : value;
}

function toggle(list: readonly string[], value: string, on: boolean): string[] {
    return on ? [...new Set([...list, value])] : list.filter((v) => v !== value);
}

function StandbyIssuesPanel({
    issues,
    customers,
    period,
    canRepair,
}: {
    issues: StandbyIssueDTO[];
    customers: { id: string; code: string; name: string }[];
    period: LoadedPeriod;
    canRepair: boolean;
}) {
    const { t } = useLanguage();
    const [open, setOpen] = useState(false);
    const [pick, setPick] = useState<Record<string, string>>({});
    const [busy, setBusy] = useState<string | null>(null);
    if (issues.length === 0) return null;
    const unpriced = issues.filter((i) => i.reason !== "no_ended_at").length;
    const missingEndedAt = issues.length - unpriced;
    const fix = async (issue: StandbyIssueDTO) => {
        setBusy(issue.id);
        try {
            const month = { customerId: period.customerId, year: period.year, month: period.month };
            if (issue.reason === "no_customer") {
                const customerId = pick[issue.id];
                if (!customerId) return;
                await assignStandbyCustomer(issue.id, customerId, month);
                toast.success(t("accounting.billingDocument.standbyIssues.fixedNoAmount"));
            } else {
                const out = await computeStandbyBilling(issue.id, { month });
                if (out.blocked || out.blockedInvoiceNumber || out.invoiceNumber) {
                    toast.error(t("accounting.billingDocument.standbyIssues.blocked", { invoiceNumber: out.invoiceNumber ?? out.blockedInvoiceNumber ?? "" }));
                } else if (out.ok && out.billingEstimateThb != null) {
                    toast.success(t("accounting.billingDocument.standbyIssues.fixed", { amount: out.billingEstimateThb.toLocaleString() }));
                } else {
                    toast.error(`${t("accounting.billingDocument.standbyIssues.fixFailed")} ${out.error ?? ""}`);
                }
            }
        } catch (e) {
            toast.error(`${t("accounting.billingDocument.standbyIssues.fixFailed")} ${apiErrorText(e, t)}`);
        } finally {
            setBusy(null);
        }
    };
    return (
        <Card className="border-amber-500/60" data-testid="standby-issues">
            <CardContent className="space-y-3 pt-4">
                <div className="flex items-start justify-between gap-2">
                    <div className="flex items-start gap-2">
                        <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-500" />
                        <div className="text-sm">
                            <p className="font-medium">{t("accounting.billingDocument.standbyIssues.title", { count: issues.length })}</p>
                            <p className="text-muted-foreground">{t("accounting.billingDocument.standbyIssues.body", { unpriced, missingEndedAt })}</p>
                        </div>
                    </div>
                    <Button variant="ghost" size="sm" onClick={() => setOpen((v) => !v)}>
                        {open ? t("accounting.billingDocument.standbyIssues.hide") : t("accounting.billingDocument.standbyIssues.show")}
                    </Button>
                </div>
                {open ? (
                    <div className="overflow-x-auto">
                        <Table>
                            <TableHeader>
                                <TableRow>
                                    <TableHead>{t("accounting.billingDocument.standbyIssues.col.reason")}</TableHead>
                                    <TableHead>{t("accounting.billingDocument.standbyIssues.col.driver")}</TableHead>
                                    <TableHead>{t("accounting.billingDocument.standbyIssues.col.route")}</TableHead>
                                    <TableHead>{t("accounting.billingDocument.standbyIssues.col.endedAt")}</TableHead>
                                    <TableHead>{t("accounting.billingDocument.standbyIssues.col.fix")}</TableHead>
                                </TableRow>
                            </TableHeader>
                            <TableBody>
                                {issues.map((i) => (
                                    <TableRow key={`${i.reason}-${i.id}`}>
                                        <TableCell className="text-xs">
                                            {t(`accounting.billingDocument.standbyIssues.reason.${i.reason}`, i.reason)}
                                            {i.dryRunEstimateThb != null ? <span className="block text-muted-foreground">≈ {formatThb(i.dryRunEstimateThb)}</span> : null}
                                        </TableCell>
                                        <TableCell className="text-xs">{i.driverName ?? "—"}</TableCell>
                                        <TableCell className="text-xs">{[i.startLocation, i.endLocation].filter(Boolean).join(" → ") || "—"}</TableCell>
                                        <TableCell className="text-xs">
                                            {i.endedAt ? formatDateTime(i.endedAt) : t("accounting.billingDocument.standbyIssues.noEndedAtCell", { createdAt: formatDateTime(i.createdAt) })}
                                        </TableCell>
                                        <TableCell className="text-xs">
                                            {!canRepair ? (
                                                <span className="text-muted-foreground">{t("accounting.billingDocument.repair.adminOnly")}</span>
                                            ) : i.reason === "no_ended_at" ? (
                                                <span className="text-muted-foreground">{t("accounting.billingDocument.standbyIssues.notRepairableHere")}</span>
                                            ) : (
                                                <div className="flex items-center gap-2">
                                                    {i.reason === "no_customer" ? (
                                                        <Select value={pick[i.id] ?? ""} onValueChange={(v) => setPick((p) => ({ ...p, [i.id]: v }))}>
                                                            <SelectTrigger className="h-8 w-44">
                                                                <SelectValue placeholder={t("accounting.billingDocument.standbyIssues.pickCustomer")} />
                                                            </SelectTrigger>
                                                            <SelectContent className="z-[1005]" position="popper">
                                                                {customers.map((c) => (
                                                                    <SelectItem key={c.id} value={c.id}>
                                                                        {c.code} — {c.name}
                                                                    </SelectItem>
                                                                ))}
                                                            </SelectContent>
                                                        </Select>
                                                    ) : null}
                                                    <Button
                                                        size="sm"
                                                        variant="outline"
                                                        disabled={busy !== null || (i.reason === "no_customer" && !pick[i.id])}
                                                        onClick={() => void fix(i)}
                                                    >
                                                        {busy === i.id ? <Loader2 className="h-3 w-3 animate-spin" /> : t("accounting.billingDocument.standbyIssues.priceNow")}
                                                    </Button>
                                                </div>
                                            )}
                                        </TableCell>
                                    </TableRow>
                                ))}
                            </TableBody>
                        </Table>
                    </div>
                ) : null}
            </CardContent>
        </Card>
    );
}

function IssuesPanel({
    data,
    missingCount,
    missingFailed,
    period,
    repairIds,
    canRepair,
}: {
    data: BillingRowsDTO;
    missingCount: number;
    missingFailed: boolean;
    period: LoadedPeriod;
    repairIds: string[];
    canRepair: boolean;
}) {
    const { t } = useLanguage();
    const [progress, setProgress] = useState<{ done: number; total: number } | null>(null);
    const [result, setResult] = useState<string | null>(null);
    const i = data.issues;
    const any =
        missingCount > 0 ||
        missingFailed ||
        i.suppPricedWithFuel > 0 ||
        i.planDateOutOfPeriod > 0 ||
        i.planDateDrifted > 0 ||
        i.standbyPlanOtherMonth > 0 ||
        i.noPlanDate > 0 ||
        i.suppManualPriceWithFuel > 0;
    if (!any) return null;
    const repair = async () => {
        const total = repairIds.length;
        let repriced = 0;
        let moved = 0;
        let unchanged = 0;
        let failed = 0;
        const blocked = new Set<string>();
        setProgress({ done: 0, total });
        setResult(null);
        for (let n = 0; n < total; n++) {
            const last = n === total - 1;
            try {
                const out = await computeTripBilling(repairIds[n], { forceRecompute: true, month: period, skipInvalidate: !last });
                if (out.blockedInvoiceNumber) blocked.add(out.blockedInvoiceNumber);
                else if (!out.ok) failed += 1;
                else if (out.billingDateMoved) moved += 1;
                else if (out.skipped) unchanged += 1;
                else repriced += 1;
            } catch {
                failed += 1;
                // The last call failed before it could invalidate: the month's keys still need it.
                if (last) await computeTripBillingInvalidate(period);
            }
            setProgress({ done: n + 1, total });
        }
        setProgress(null);
        setResult(
            t("accounting.billingDocument.repair.result", { repriced, moved, unchanged, failed }) +
                (blocked.size > 0 ? ` ${t("accounting.billingDocument.repair.blocked", { invoiceNumbers: [...blocked].join(", ") })}` : "")
        );
    };
    return (
        <Card className="border-amber-500/60" data-testid="billing-issues">
            <CardContent className="space-y-3 pt-4">
                <div className="flex items-start gap-2">
                    <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-500" />
                    <div className="space-y-1 text-sm">
                        <p className="font-medium">{t("accounting.billingDocument.repair.title")}</p>
                        <ul className="list-disc space-y-0.5 pl-5 text-muted-foreground">
                            {missingCount > 0 ? <li className="text-red-600">{t("accounting.billingDocument.repair.missingBillingDate", { count: missingCount })}</li> : null}
                            {missingFailed ? <li className="text-red-600">{t("accounting.billingDocument.repair.missingCheckFailed")}</li> : null}
                            {i.suppPricedWithFuel > 0 ? <li className="text-red-600">{t("accounting.billingDocument.repair.suppWithFuel", { count: i.suppPricedWithFuel })}</li> : null}
                            {i.planDateOutOfPeriod > 0 ? <li>{t("accounting.billingDocument.repair.outOfPeriod", { count: i.planDateOutOfPeriod })}</li> : null}
                            {i.planDateDrifted > 0 ? <li>{t("accounting.billingDocument.repair.drifted", { count: i.planDateDrifted })}</li> : null}
                            {i.standbyPlanOtherMonth > 0 ? <li>{t("accounting.billingDocument.repair.standbyOtherMonth", { count: i.standbyPlanOtherMonth })}</li> : null}
                            {i.noPlanDate > 0 ? <li>{t("accounting.billingDocument.repair.noPlanDate", { count: i.noPlanDate })}</li> : null}
                            {i.suppManualPriceWithFuel > 0 ? <li>{t("accounting.billingDocument.repair.suppManualPrice", { count: i.suppManualPriceWithFuel })}</li> : null}
                        </ul>
                        {data.basis === "plan" ? <p className="text-xs text-muted-foreground">{t("accounting.billingDocument.repair.limitation")}</p> : null}
                    </div>
                </div>
                {repairIds.length > 0 ? (
                    canRepair ? (
                        <Button size="sm" variant="outline" onClick={() => void repair()} disabled={progress !== null}>
                            {progress ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Wrench className="mr-2 h-4 w-4" />}
                            {progress
                                ? t("accounting.billingDocument.repair.running", { done: progress.done, total: progress.total })
                                : t("accounting.billingDocument.repair.button", { count: repairIds.length })}
                        </Button>
                    ) : (
                        <p className="text-xs text-muted-foreground">{t("accounting.billingDocument.repair.adminOnly")}</p>
                    )
                ) : null}
                {result ? <p className="text-xs text-muted-foreground">{result}</p> : null}
            </CardContent>
        </Card>
    );
}

function UnpricedPanel({ data }: { data: BillingRowsDTO }) {
    const { t } = useLanguage();
    const [open, setOpen] = useState(false);
    if (data.unpriced.length === 0) return null;
    return (
        <Card data-testid="unpriced-rows">
            <CardContent className="space-y-3 pt-4">
                <div className="flex items-center justify-between gap-2">
                    <p className="text-sm font-medium">{t("accounting.billingDocument.unpriced.title", { count: data.unpriced.length })}</p>
                    <Button variant="ghost" size="sm" onClick={() => setOpen((v) => !v)}>
                        {open ? t("accounting.billingDocument.standbyIssues.hide") : t("accounting.billingDocument.standbyIssues.show")}
                    </Button>
                </div>
                <p className="text-xs text-muted-foreground">{t("accounting.billingDocument.unpriced.hint")}</p>
                {open ? (
                    <div className="max-h-80 overflow-auto">
                        <Table>
                            <TableHeader>
                                <TableRow>
                                    <TableHead>{t("accounting.billingDocument.table.tripNumber")}</TableHead>
                                    <TableHead>{t("accounting.billingDocument.standbyIssues.col.reason")}</TableHead>
                                    <TableHead>{t("accounting.billingDocument.table.route")}</TableHead>
                                    <TableHead>{t("accounting.billingDocument.table.vehicleType")}</TableHead>
                                    <TableHead>{t("accounting.billingDocument.table.deliveredDate")}</TableHead>
                                    <TableHead className="text-right">{t("accounting.billingDocument.unpriced.dryRun")}</TableHead>
                                </TableRow>
                            </TableHeader>
                            <TableBody>
                                {data.unpriced.map((u) => (
                                    <TableRow key={u.id}>
                                        <TableCell className="font-mono text-xs">{u.tripNo ?? u.id.slice(0, 8)}</TableCell>
                                        <TableCell className="text-xs">
                                            {t(`accounting.billingDocument.unpriced.reason.${u.reason}`, u.reason)}
                                            <span className="block text-[10px] text-muted-foreground">{t(`accounting.billingDocument.unpriced.source.${u.reasonSource}`, u.reasonSource)}</span>
                                        </TableCell>
                                        <TableCell className="text-xs">{[u.hubDisplayName, u.destinationDisplayName].filter(Boolean).join(" → ") || "—"}</TableCell>
                                        <TableCell className="text-xs">{u.vehicleClass ?? u.lookupVehicleClass ?? "—"}</TableCell>
                                        <TableCell className="text-xs">{formatDateTime(u.deliveredAt)}</TableCell>
                                        <TableCell className="text-right font-mono text-xs">{u.dryRunEstimateThb != null ? formatThb(u.dryRunEstimateThb) : "—"}</TableCell>
                                    </TableRow>
                                ))}
                            </TableBody>
                        </Table>
                    </div>
                ) : null}
            </CardContent>
        </Card>
    );
}

const col = createColumnHelper<BillingRowDTO>();

export function BillingDocumentPage() {
    const { t, language } = useLanguage();
    const canRecompute = useCan("accounting:recompute_force").allowed;
    const canEditRates = useCan("accounting:edit_rate_card").allowed;
    const customers = useCustomerPicks();
    const now = new Date();
    const [month, setMonth] = useState(now.getMonth() + 1);
    const [year, setYear] = useState(now.getFullYear());
    const [customerId, setCustomerId] = useState<string>(ALL_PARTIES);
    const [loaded, setLoaded] = useState<LoadedPeriod | null>(null);
    const [params, setParams] = useState<BillingRowsParams>(DEFAULT_ROWS_PARAMS);
    const [showActualPickup, setShowActualPickup] = useState(false);
    const [saving, setSaving] = useState(false);
    const [saved, setSaved] = useState<StatementDTO | null>(null);

    const rows = useQuery({
        ...billingRowsQuery(loaded?.customerId ?? "", loaded?.year ?? 0, loaded?.month ?? 0, params, loaded !== null),
        placeholderData: keepPreviousData,
    });
    const diag = useQuery(standbyDiagnosticsQuery(loaded?.customerId ?? "", loaded?.year ?? 0, loaded?.month ?? 0, loaded !== null));
    const missing = useQuery(missingBillingDateQuery(loaded?.customerId ?? "", loaded?.year ?? 0, loaded?.month ?? 0, loaded !== null));
    const data = rows.data;

    const load = () => {
        const next = { customerId, year, month };
        if (loaded && loaded.customerId === next.customerId && loaded.year === next.year && loaded.month === next.month) {
            void rows.refetch();
            void diag.refetch();
            void missing.refetch();
        } else {
            setParams((p) => ({ ...p, plate: "", vehicleClass: "", reviewMonth: "" }));
            setLoaded(next);
        }
        setSaved(null);
    };

    const reviewActive = params.plate !== "" || params.vehicleClass !== "" || params.reviewMonth !== "";
    const stale = loaded !== null && (loaded.customerId !== customerId || loaded.year !== year || loaded.month !== month);
    const previewRows = useMemo(() => (data?.rows ?? []).filter((r) => r.inPreview), [data]);
    const selectedCustomer = customers.list.find((c) => c.id === customerId);
    const basisLabel =
        loaded?.customerId === ALL_PARTIES || data?.basis == null
            ? "all"
            : data.basis === "plan"
              ? "plan"
              : "delivered";
    const reviewAxisPlan = data?.basis !== "plan";
    const canCreate = !!data && data.documentable && !reviewActive && !stale && loaded?.customerId !== ALL_PARTIES;
    const totals = reviewActive ? data?.preview : data?.invoice;
    const whtRate = totals?.withholdingTaxRate;
    const repairIds = useMemo(() => {
        const ids = new Set<string>(data?.issues.repairTripIds ?? []);
        for (const m of missing.data ?? []) ids.add(m.tripId);
        return [...ids];
    }, [data, missing.data]);

    const create = async () => {
        if (!loaded || !data) return;
        setSaving(true);
        try {
            const s = await createStatement({
                customerId: loaded.customerId,
                year: loaded.year,
                month: loaded.month,
                types: [...params.types],
                categories: [...params.categories],
                showActualPickup,
            });
            setSaved(s);
            toast.success(t("accounting.billingDocument.save.saved", { invoiceNumber: s.invoiceNumber }));
        } catch (e) {
            toast.error(`${t("accounting.billingDocument.save.error")} ${apiErrorText(e, t)}`);
        } finally {
            setSaving(false);
        }
    };

    const columns = useMemo(
        () => [
            col.display({
                id: "trip",
                header: () => t("accounting.billingDocument.table.tripNumber"),
                cell: ({ row: { original: r } }) => (
                    <div className="flex flex-col gap-1 font-mono text-xs">
                        <span>{r.tripNo ?? r.id.slice(0, 8)}</span>
                        <Badge variant="outline" className="w-fit px-1 py-0 text-[10px]">
                            {r.rowType === "standby"
                                ? t("accounting.billingDocument.badge.standby")
                                : r.rowType === "multidrop_stop"
                                  ? t("accounting.billingDocument.badge.stopN", { n: r.stopIndex ?? 0 })
                                  : t("accounting.billingDocument.badge.trip")}
                        </Badge>
                    </div>
                ),
            }),
            col.display({
                id: "plan",
                header: () => t("accounting.billingDocument.table.planDate"),
                cell: ({ row: { original: r } }) =>
                    r.flags.includes("plan_date_drifted") ? (
                        <span className="text-amber-500" title={t("accounting.billingDocument.table.planDateDrift")}>
                            {formatDay(r.planDate)} ⚠
                        </span>
                    ) : (
                        formatDay(r.planDate)
                    ),
                meta: { cellClassName: "whitespace-nowrap text-xs" },
            }),
            col.display({
                id: "delivered",
                header: () => t("accounting.billingDocument.table.deliveredDate"),
                cell: ({ row: { original: r } }) => formatDateTime(r.deliveredAt),
                meta: { cellClassName: "whitespace-nowrap text-xs" },
            }),
            ...(showActualPickup
                ? [
                      col.display({
                          id: "pickup",
                          header: () => t("accounting.billingDocument.table.actualPickup"),
                          cell: ({ row: { original: r } }) => formatDateTime(r.actualPickupAt),
                          meta: { cellClassName: "whitespace-nowrap text-xs" },
                      }),
                  ]
                : []),
            col.display({
                id: "route",
                header: () => t("accounting.billingDocument.table.route"),
                cell: ({ row: { original: r } }) =>
                    [r.originHubCode || r.lookupHubCode || r.hubDisplayName, r.destinationDisplayName || r.lookupDestinationCode].filter(Boolean).join(" → ") || "—",
                meta: { cellClassName: "text-xs" },
            }),
            col.display({
                id: "class",
                header: () => t("accounting.billingDocument.table.vehicleType"),
                cell: ({ row: { original: r } }) => (r.vehicleClass ? <Badge variant="outline">{r.vehicleClass}</Badge> : "—"),
            }),
            col.display({
                id: "plate",
                header: () => t("accounting.billingDocument.filters.licensePlate"),
                cell: ({ row: { original: r } }) => r.truckLicensePlate ?? "—",
                meta: { cellClassName: "whitespace-nowrap text-xs" },
            }),
            col.display({
                id: "driver",
                header: () => t("accounting.billingDocument.table.driver"),
                cell: ({ row: { original: r } }) => r.driverName ?? "—",
                meta: { cellClassName: "text-xs" },
            }),
            col.display({
                id: "category",
                header: () => t("accounting.billingDocument.table.jobCategory"),
                cell: ({ row: { original: r } }) => (
                    <div className="flex flex-col gap-1">
                        <JobCategoryBadge value={r.jobCategory} kind={r.rowType} />
                        {r.flags.includes("supp_priced_with_fuel") ? (
                            <Badge variant="outline" className="w-fit border-red-500 px-1 py-0 text-[10px] text-red-600">
                                {t("accounting.billingDocument.badge.suppWithFuel")}
                            </Badge>
                        ) : null}
                        {r.flags.includes("supp_manual_price_with_fuel") ? (
                            <Badge variant="outline" className="w-fit px-1 py-0 text-[10px] text-muted-foreground">
                                {t("accounting.billingDocument.badge.suppManualPrice")}
                            </Badge>
                        ) : null}
                    </div>
                ),
            }),
            col.display({
                id: "amount",
                header: () => t("accounting.billingDocument.table.billingAmount"),
                cell: ({ row: { original: r } }) => (
                    <span className={r.inInvoiceSet ? "" : "text-muted-foreground line-through"} title={r.inInvoiceSet ? undefined : t("accounting.billingDocument.table.notInInvoice")}>
                        {formatThb(r.estimateThb)}
                    </span>
                ),
                meta: { headerClassName: "text-right", cellClassName: "text-right font-mono" },
            }),
        ],
        [t, showActualPickup]
    );

    const options = data?.filterOptions ?? {};
    const typeLabel: Record<string, string> = {
        trip: t("accounting.billingDocument.types.trip"),
        standby: t("accounting.billingDocument.types.standby"),
        multidrop: t("accounting.billingDocument.types.multidrop"),
    };
    const categoryLabel: Record<string, string> = {
        PRIMARY: t("accounting.billingDocument.badge.jobCategoryPrimary"),
        SUPPLEMENTARY: t("accounting.billingDocument.badge.jobCategorySupplementary"),
        unverified: t("accounting.billingDocument.badge.jobCategoryUnknown"),
    };

    return (
        <div className="space-y-6 p-6">
            <div>
                <h1 className="text-2xl font-bold">{t("nav.billingDocument")}</h1>
                <p className="mt-1 text-sm text-muted-foreground">{t("accounting.billingDocument.subtitle")}</p>
            </div>

            <Card>
                <CardHeader>
                    <CardTitle className="text-base">{t("accounting.billingDocument.filters.title")}</CardTitle>
                    <p className="text-xs text-muted-foreground">{t(`accounting.billingDocument.filters.periodHint.${basisLabel}`)}</p>
                </CardHeader>
                <CardContent className="flex flex-wrap items-end gap-4">
                    <div className="space-y-1">
                        <Label className="flex items-center gap-1.5">
                            {basisLabel === "plan" ? t("accounting.billingDocument.filters.planMonth") : t("accounting.billingDocument.filters.deliveredMonth")}
                            <Badge variant="secondary" className="px-1 py-0 text-[10px] font-normal">
                                {t("accounting.billingDocument.filters.billingPeriodTag")}
                            </Badge>
                        </Label>
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
                    <div className="space-y-1">
                        <Label>{t("accounting.billingDocument.filters.customer")}</Label>
                        <CustomerSelect
                            value={customerId}
                            onChange={setCustomerId}
                            customers={customers.list}
                            allValue={ALL_PARTIES}
                            allLabel={t("accounting.billingDocument.filters.allCustomers")}
                        />
                    </div>
                    <div className="space-y-1">
                        <Label>&nbsp;</Label>
                        <Button type="button" variant={showActualPickup ? "default" : "outline"} onClick={() => setShowActualPickup((v) => !v)}>
                            {showActualPickup ? t("accounting.billingDocument.actualPickup.hide") : t("accounting.billingDocument.actualPickup.show")}
                        </Button>
                    </div>
                    <Button onClick={load} disabled={rows.isFetching} variant="outline">
                        {rows.isFetching ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <RefreshCw className="mr-2 h-4 w-4" />}
                        {t("accounting.billingDocument.filters.load")}
                    </Button>
                </CardContent>

                {data ? (
                    <CardContent className="flex flex-wrap items-end gap-4 border-t pt-4">
                        <div className="space-y-1">
                            <Label>{t("accounting.billingDocument.filters.licensePlate")}</Label>
                            <Select value={params.plate || "__all__"} onValueChange={(v) => setParams((p) => ({ ...p, plate: v === "__all__" ? "" : v }))}>
                                <SelectTrigger className="w-48">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                    <SelectItem value="__all__">{t("accounting.billingDocument.filters.allLicensePlates")}</SelectItem>
                                    {(options.plates ?? []).map((o) => (
                                        <SelectItem key={o.value} value={o.value}>
                                            {o.value === FILTER_NONE ? t("accounting.billingDocument.filters.plateNotSpecified") : o.label} ({o.count})
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>
                        <div className="space-y-1">
                            <Label>{t("accounting.billingDocument.filters.vehicleClass")}</Label>
                            <Select
                                value={params.vehicleClass || "__all__"}
                                onValueChange={(v) => setParams((p) => ({ ...p, vehicleClass: v === "__all__" ? "" : v }))}
                            >
                                <SelectTrigger className="w-48">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                    <SelectItem value="__all__">{t("accounting.billingDocument.filters.allVehicleClasses")}</SelectItem>
                                    {(options.vehicleClasses ?? []).map((o) => (
                                        <SelectItem key={o.value} value={o.value}>
                                            {o.value === FILTER_NONE ? t("accounting.billingDocument.filters.vehicleClassNotSpecified") : o.label} ({o.count})
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>
                        <div className="space-y-1">
                            <Label className="flex items-center gap-1.5">
                                {reviewAxisPlan ? t("accounting.billingDocument.filters.planMonth") : t("accounting.billingDocument.filters.deliveredMonth")}
                                <Badge variant="outline" className="px-1 py-0 text-[10px] font-normal">
                                    {t("accounting.billingDocument.filters.reviewTag")}
                                </Badge>
                            </Label>
                            <Select
                                value={params.reviewMonth || "__all__"}
                                onValueChange={(v) => setParams((p) => ({ ...p, reviewMonth: v === "__all__" ? "" : v }))}
                            >
                                <SelectTrigger className="w-52">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                    <SelectItem value="__all__">{t("accounting.billingDocument.filters.allMonths")}</SelectItem>
                                    {(options.reviewMonths ?? []).map((o) => (
                                        <SelectItem key={o.value} value={o.value}>
                                            {reviewMonthLabel(o.value, language, t("accounting.billingDocument.filters.monthNotSpecified"))} ({o.count})
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>
                        {reviewActive ? (
                            <Button
                                variant="ghost"
                                size="sm"
                                className="h-9 text-muted-foreground"
                                onClick={() => setParams((p) => ({ ...p, plate: "", vehicleClass: "", reviewMonth: "" }))}
                            >
                                <X className="mr-1.5 h-4 w-4" />
                                {t("accounting.billingDocument.filters.clearReviewFilters")}
                            </Button>
                        ) : null}
                        <div className="ml-2 space-y-1.5 border-l pl-4">
                            <Label className="text-xs text-muted-foreground">{t("accounting.billingDocument.types.title")}</Label>
                            <div className="flex flex-col gap-2">
                                {INVOICE_TYPES.map((ty) => (
                                    <label key={ty} className="flex cursor-pointer items-center gap-2 text-sm">
                                        <Checkbox
                                            checked={params.types.includes(ty)}
                                            onCheckedChange={(v) => setParams((p) => ({ ...p, types: toggle(p.types, ty, v === true) }))}
                                        />
                                        <span className={`inline-block h-2 w-2 rounded-full ${TYPE_COLORS[ty]}`} />
                                        {typeLabel[ty]}
                                        <Badge variant="secondary" className="px-1.5 py-0 text-xs">
                                            {data.typeCounts[ty] ?? 0}
                                        </Badge>
                                    </label>
                                ))}
                            </div>
                        </div>
                        <div className="ml-2 space-y-1.5 border-l pl-4">
                            <Label className="text-xs text-muted-foreground">{t("accounting.billingDocument.table.jobCategory")}</Label>
                            <div className="flex flex-col gap-2">
                                {INVOICE_CATEGORIES.map((c) => (
                                    <label key={c} className={`flex cursor-pointer items-center gap-2 text-sm ${c === "unverified" ? "text-red-600" : ""}`}>
                                        <Checkbox
                                            checked={params.categories.includes(c)}
                                            onCheckedChange={(v) => setParams((p) => ({ ...p, categories: toggle(p.categories, c, v === true) }))}
                                        />
                                        {categoryLabel[c]}
                                        <Badge variant="secondary" className="px-1.5 py-0 text-xs">
                                            {data.categoryCounts[c] ?? 0}
                                        </Badge>
                                    </label>
                                ))}
                            </div>
                        </div>
                    </CardContent>
                ) : null}
            </Card>

            <ApiErrorAlert error={rows.error} title={t("accounting.billingDocument.loadError")} />
            {diag.error ? <ApiErrorAlert error={diag.error} title={t("accounting.billingDocument.standbyIssues.queryFailedTitle")} /> : null}

            {loaded && diag.data ? (
                <StandbyIssuesPanel
                    issues={[...diag.data.unpriced, ...diag.data.missingEndedAt]}
                    customers={customers.list}
                    period={loaded}
                    canRepair={canEditRates}
                />
            ) : null}
            {loaded && data ? (
                <IssuesPanel
                    data={data}
                    missingCount={missing.data?.length ?? 0}
                    missingFailed={missing.isError}
                    period={loaded}
                    repairIds={repairIds}
                    canRepair={canRecompute}
                />
            ) : null}
            {data ? <UnpricedPanel data={data} /> : null}

            {data && totals ? (
                <div className="space-y-3">
                    <div className="grid grid-cols-3 gap-3">
                        <Card className="border-l-4 border-l-blue-500">
                            <CardContent className="pb-3 pt-4">
                                <p className="text-xs text-muted-foreground">{typeLabel.trip}</p>
                                <p className="text-xl font-bold">{totals.tripOnlyCount}</p>
                                <p className="font-mono text-sm text-blue-600">{formatThb(totals.tripSubtotal)}</p>
                            </CardContent>
                        </Card>
                        <Card className="border-l-4 border-l-orange-500">
                            <CardContent className="pb-3 pt-4">
                                <p className="text-xs text-muted-foreground">{typeLabel.standby}</p>
                                <p className="text-xl font-bold">{totals.standbyCount}</p>
                                <p className="font-mono text-sm text-orange-600">{formatThb(totals.standbySubtotal)}</p>
                            </CardContent>
                        </Card>
                        <Card className="border-l-4 border-l-purple-500">
                            <CardContent className="pb-3 pt-4">
                                <p className="text-xs text-muted-foreground">{typeLabel.multidrop}</p>
                                <p className="text-xl font-bold">{totals.multiDropCount}</p>
                                <p className="font-mono text-sm text-purple-600">{formatThb(totals.multiDropSubtotal)}</p>
                            </CardContent>
                        </Card>
                    </div>
                    <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
                        <Card>
                            <CardContent className="pt-4">
                                <p className="text-xs text-muted-foreground">{t("accounting.billingDocument.summary.tripCount")}</p>
                                <p className="text-2xl font-bold">{totals.rowCount}</p>
                            </CardContent>
                        </Card>
                        <Card>
                            <CardContent className="pt-4">
                                <p className="text-xs text-muted-foreground">{t("accounting.billingDocument.summary.total")}</p>
                                <p className="text-2xl font-bold">{formatThb(totals.grandTotal)}</p>
                            </CardContent>
                        </Card>
                        <Card>
                            <CardContent className="pt-4">
                                <p className="text-xs text-muted-foreground">
                                    {whtRate != null
                                        ? t("accounting.billingDocument.summary.withholdingTaxRate", { rate: (whtRate * 100).toFixed(2).replace(/\.?0+$/, "") })
                                        : t("accounting.billingDocument.summary.withholdingTaxNoRate")}
                                </p>
                                <p className="text-2xl font-bold text-red-600">-{formatThb(totals.withholdingTax)}</p>
                            </CardContent>
                        </Card>
                        <Card>
                            <CardContent className="pt-4">
                                <p className="text-xs text-muted-foreground">{t("accounting.billingDocument.summary.netTotal")}</p>
                                <p className="text-2xl font-bold text-green-600">{formatThb(totals.net)}</p>
                            </CardContent>
                        </Card>
                    </div>
                    {reviewActive ? <p className="text-xs text-amber-600">{t("accounting.billingDocument.summary.previewTotals")}</p> : null}
                </div>
            ) : null}

            {data && data.rows.length > 0 ? (
                <Card>
                    <CardContent className="flex flex-wrap items-center justify-between gap-3 pt-4">
                        <div className="flex items-center gap-2 text-sm text-muted-foreground">
                            <FileText className="h-4 w-4" />
                            <span>{t("accounting.billingDocument.statement.files")}</span>
                        </div>
                        <Button onClick={() => void create()} disabled={!canCreate || saving}>
                            {saving ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <FileText className="mr-2 h-4 w-4" />}
                            {saving ? t("accounting.billingDocument.save.saving") : t("accounting.billingDocument.statement.create")}
                        </Button>
                    </CardContent>
                    {loaded?.customerId === ALL_PARTIES ? (
                        <CardContent className="pt-0">
                            <p className="text-xs text-amber-600">{t("accounting.billingDocument.download.selectCustomerWarning")}</p>
                        </CardContent>
                    ) : null}
                    {reviewActive ? (
                        <CardContent className="pt-0">
                            <p className="text-xs text-amber-600">{t("accounting.billingDocument.download.reviewFilterActive")}</p>
                        </CardContent>
                    ) : null}
                    {stale ? (
                        <CardContent className="pt-0">
                            <p className="text-xs text-amber-600">{t("accounting.billingDocument.download.staleBasis")}</p>
                        </CardContent>
                    ) : null}
                    {data.outsideReach > 0 ? (
                        <CardContent className="pt-0">
                            <p className="text-xs text-amber-600">{t("accounting.billingDocument.statement.outsideReach", { count: data.outsideReach })}</p>
                        </CardContent>
                    ) : null}
                    {selectedCustomer && !data.documentable && data.outsideReach === 0 && loaded?.customerId !== ALL_PARTIES ? (
                        <CardContent className="pt-0">
                            <p className="text-xs text-amber-600">{t("accounting.billingDocument.statement.nothingToBill")}</p>
                        </CardContent>
                    ) : null}
                </Card>
            ) : null}

            {data ? (
                <Card>
                    <CardHeader>
                        <CardTitle className="text-base">{t("accounting.billingDocument.table.title", { count: previewRows.length })}</CardTitle>
                    </CardHeader>
                    <CardContent className="p-0">
                        <VirtualTable
                            data={previewRows}
                            columns={columns}
                            getRowId={(r) => r.id}
                            aria-label={t("accounting.billingDocument.table.title", { count: previewRows.length })}
                            emptyState={<p className="py-8 text-center text-sm text-muted-foreground">{t("accounting.billingDocument.empty")}</p>}
                            resetScrollKey={JSON.stringify([loaded, params])}
                            maxHeight={640}
                        />
                    </CardContent>
                </Card>
            ) : !rows.isFetching ? (
                <Card>
                    <CardContent className="pt-6 text-center text-sm text-muted-foreground">{t("accounting.billingDocument.empty")}</CardContent>
                </Card>
            ) : null}

            {saved ? <StatementDocumentsDialog statement={saved} onClose={() => setSaved(null)} /> : null}
        </div>
    );
}
