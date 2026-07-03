"use client";
import { PagePermissionGuard } from "@/components/page-permission-guard";
import { CAPABILITIES } from "@/lib/capabilities";
import { useEffect, useMemo, useState } from "react";
import { useLanguage } from "@/context/language";
import { getCustomers } from "@/features/customers/api/customers";
import type { Customer } from "@/validate/customerSchema";
import {
    downloadBillingZip,
    type BillingTripRow,
    type BillingCustomer,
    type BillingPeriod,
    type BillingProviderInfo,
} from "@/lib/billingDocument";
import { saveBillingStatement } from "@/lib/billingStatement";
import { getOwnerCompany } from "@/features/companies/api/companies";
import { getCustomerServiceFees, fetchBillingTripRows } from "@/features/accounting";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Checkbox } from "@/components/ui/checkbox";
import { Download, FileText, Loader2, RefreshCw } from "lucide-react";
import { format } from "date-fns";
import { WITHHOLDING_TAX_RATE } from "@/lib/billingConfig";
import { toast } from "sonner";
import { useAuth } from "@/context/auth";

// ─── helpers ──────────────────────────────────────────────────────────────────

function formatThb(n: number) {
    return new Intl.NumberFormat("th-TH", { minimumFractionDigits: 2, maximumFractionDigits: 2 }).format(n);
}

// ─── Page ────────────────────────────────────────────────────────────────────

const currentYear = new Date().getFullYear();
const YEARS = Array.from({ length: 3 }, (_, i) => currentYear - i);
const MONTHS = [
    { value: 1, label: "มกราคม / January" },
    { value: 2, label: "กุมภาพันธ์ / February" },
    { value: 3, label: "มีนาคม / March" },
    { value: 4, label: "เมษายน / April" },
    { value: 5, label: "พฤษภาคม / May" },
    { value: 6, label: "มิถุนายน / June" },
    { value: 7, label: "กรกฎาคม / July" },
    { value: 8, label: "สิงหาคม / August" },
    { value: 9, label: "กันยายน / September" },
    { value: 10, label: "ตุลาคม / October" },
    { value: 11, label: "พฤศจิกายน / November" },
    { value: 12, label: "ธันวาคม / December" },
];

export default function BillingDocumentPage() {
    const { t } = useLanguage();
    const auth = useAuth();

    const now = new Date();
    const [selectedMonth, setSelectedMonth] = useState(now.getMonth() + 1);
    const [selectedYear, setSelectedYear] = useState(now.getFullYear());
    const [selectedCustomerId, setSelectedCustomerId] = useState<string>("all");

    const [customers, setCustomers] = useState<Customer[]>([]);
    const [trips, setTrips] = useState<BillingTripRow[]>([]);

    // ── Type toggles (which charge types to include in billing) ──────────────
    const [includeTrips,     setIncludeTrips]     = useState(true);
    const [includeStandby,   setIncludeStandby]   = useState(true);
    const [includeMultiDrop, setIncludeMultiDrop] = useState(true);

    const [loading, setLoading] = useState(false);
    const [generating, setGenerating] = useState(false);
    const [ownerProvider, setOwnerProvider] = useState<BillingProviderInfo | undefined>(undefined);

    // Load customers + owner company once
    useEffect(() => {
        getCustomers().then(setCustomers).catch(console.error);
        // Load owner company for PDF branding (stamp, signature, etc.)
        getOwnerCompany()
            .then((company) => {
                if (company) {
                    setOwnerProvider({
                        name: company.nameTh,
                        shortName: company.shortName,
                        address: company.address,
                        taxId: company.taxId,
                        bankName: company.bankName,
                        accountNumber: company.accountNumber,
                        accountName: company.accountName,
                        withholdingTaxRate: company.withholdingTaxRate,
                        stampUrl: company.stampUrl,
                        signatureUrl: company.signatureUrl,
                        signatoryName: company.signatoryName,
                    });
                }
            })
            .catch((e) => console.warn("[billing] getOwnerCompany failed (using default provider):", e));
    }, []);

    async function loadTrips() {
        setLoading(true);
        try {
            const rows = await fetchBillingTripRows(selectedCustomerId, { month: selectedMonth, year: selectedYear });
            setTrips(rows);
        } catch (e) {
            console.error("[billing] fetchBillingTripRows failed:", e);
            toast.error(t("accounting.billingDocument.loadError", "Failed to load trips — please try again."));
        } finally {
            setLoading(false);
        }
    }

    const filteredTrips = useMemo(() => {
        return trips.filter((r) => {
            if (selectedCustomerId !== "all" && r.billingCustomerId !== selectedCustomerId) return false;
            if (r.rowType === "trip"           && !includeTrips)     return false;
            if (r.rowType === "standby"        && !includeStandby)   return false;
            if (r.rowType === "multidrop_stop" && !includeMultiDrop) return false;
            return true;
        });
    }, [trips, selectedCustomerId, includeTrips, includeStandby, includeMultiDrop]);

    // Count per type (before type-toggle filter, but after customer filter) for checkbox labels
    const typeCounts = useMemo(() => {
        const base = selectedCustomerId === "all" ? trips : trips.filter((r) => r.billingCustomerId === selectedCustomerId);
        return {
            trips:     base.filter((r) => r.rowType === "trip").length,
            standby:   base.filter((r) => r.rowType === "standby").length,
            multiDrop: base.filter((r) => r.rowType === "multidrop_stop").length,
        };
    }, [trips, selectedCustomerId]);

    const breakdown = useMemo(() => {
        const tripRows      = filteredTrips.filter((r) => r.rowType === "trip");
        const standbyRows   = filteredTrips.filter((r) => r.rowType === "standby");
        const multiDropRows = filteredTrips.filter((r) => r.rowType === "multidrop_stop");
        const sum = (arr: BillingTripRow[]) => arr.reduce((s, r) => s + r.billingEstimateThb, 0);
        return {
            tripOnlyCount:   tripRows.length,
            tripSubtotal:    sum(tripRows),
            standbyCount:    standbyRows.length,
            standbySubtotal: sum(standbyRows),
            multiDropCount:  multiDropRows.length,
            multiDropSubtotal: sum(multiDropRows),
        };
    }, [filteredTrips]);

    const grandTotal = useMemo(() => filteredTrips.reduce((s, t) => s + t.billingEstimateThb, 0), [filteredTrips]);
    const withholdingTax = Math.round(grandTotal * WITHHOLDING_TAX_RATE * 100) / 100;
    const totalNet = grandTotal - withholdingTax;

    const selectedCustomer = useMemo<BillingCustomer | null>(() => {
        if (selectedCustomerId === "all") return null;
        const c = customers.find((c) => c.id === selectedCustomerId);
        if (!c) return null;
        return {
            id: c.id!,
            name: c.name,
            address: c.address,
            taxId: c.taxId,
            branchType: c.branchType,
            branchNumber: c.branchNumber,
            contactName: c.contactName,
            contactPhone: c.contactPhone,
            paymentTermsDays: c.paymentTermsDays,
            invoiceNote: c.invoiceNote,
        };
    }, [selectedCustomerId, customers]);

    async function handleDownload() {
        if (!selectedCustomer) return;
        setGenerating(true);
        try {
            const period: BillingPeriod = { month: selectedMonth, year: selectedYear };

            // Save billing statement (registry) before download
            const customerForStatement = customers.find((c) => c.id === selectedCustomer.id);
            let invoiceNumber: string | undefined;
            try {
                toast.loading(t("accounting.billingDocument.save.saving"));
                invoiceNumber = await saveBillingStatement({
                    customerId: selectedCustomer.id,
                    customerName: selectedCustomer.name,
                    customerCode: customerForStatement?.code ?? selectedCustomer.id,
                    period,
                    totalAmount: grandTotal,
                    withholdingTax,
                    netAmount: totalNet,
                    tripCount: filteredTrips.length,
                    ...breakdown,
                    paymentTermsDays: selectedCustomer.paymentTermsDays,
                    generatedBy: auth?.currentUser?.uid,
                });
                toast.dismiss();
                toast.success(t("accounting.billingDocument.save.saved", { invoiceNumber }));
            } catch (saveErr) {
                toast.dismiss();
                console.error("[billing] Failed to save statement:", saveErr);
                toast.error(t("accounting.billingDocument.save.error"));
                // Still proceed with download even if statement save fails
            }

            await downloadBillingZip(filteredTrips, selectedCustomer, period, invoiceNumber, ownerProvider);
        } finally {
            setGenerating(false);
        }
    }

    const canDownload = selectedCustomerId !== "all" && filteredTrips.length > 0;

    return (
        <PagePermissionGuard capability={CAPABILITIES.accounting_billing_document}>
            <div className="p-6 space-y-6">
                <div>
                    <h1 className="text-2xl font-bold">{t("nav.billingDocument")}</h1>
                    <p className="text-muted-foreground text-sm mt-1">
                        {t("accounting.billingDocument.subtitle")}
                    </p>
                </div>

                {/* ── Filters ── */}
                <Card>
                    <CardHeader><CardTitle className="text-base">{t("accounting.billingDocument.filters.title")}</CardTitle></CardHeader>
                    <CardContent className="flex flex-wrap gap-4 items-end">
                        <div className="space-y-1">
                            <Label>{t("accounting.billingDocument.filters.month")}</Label>
                            <Select value={String(selectedMonth)} onValueChange={(v) => setSelectedMonth(Number(v))}>
                                <SelectTrigger className="w-52">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                    {MONTHS.map((m) => (
                                        <SelectItem key={m.value} value={String(m.value)}>{m.label}</SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>

                        <div className="space-y-1">
                            <Label>{t("accounting.billingDocument.filters.year")}</Label>
                            <Select value={String(selectedYear)} onValueChange={(v) => setSelectedYear(Number(v))}>
                                <SelectTrigger className="w-32">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                    {YEARS.map((y) => (
                                        <SelectItem key={y} value={String(y)}>{y}</SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>

                        <div className="space-y-1">
                            <Label>{t("accounting.billingDocument.filters.customer")}</Label>
                            <Select value={selectedCustomerId} onValueChange={setSelectedCustomerId}>
                                <SelectTrigger className="w-52">
                                    <SelectValue placeholder={t("accounting.billingDocument.filters.customer")} />
                                </SelectTrigger>
                                <SelectContent>
                                    <SelectItem value="all">{t("accounting.billingDocument.filters.allCustomers")}</SelectItem>
                                    {customers.map((c) => (
                                        <SelectItem key={c.id} value={c.id!}>{c.name}</SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>

                        {/* ── Charge type toggles ── */}
                        {trips.length > 0 && (
                            <div className="space-y-1.5 border-l pl-4 ml-2">
                                <Label className="text-xs text-muted-foreground">ค่าบริการที่รวมในบิล</Label>
                                <div className="flex flex-col gap-2">
                                    <label className="flex items-center gap-2 cursor-pointer text-sm">
                                        <Checkbox
                                            checked={includeTrips}
                                            onCheckedChange={(v) => setIncludeTrips(!!v)}
                                        />
                                        <span className="flex items-center gap-1.5">
                                            <span className="w-2 h-2 rounded-full bg-blue-500 inline-block" />
                                            เที่ยวปกติ
                                            <Badge variant="secondary" className="text-xs px-1.5 py-0">{typeCounts.trips}</Badge>
                                        </span>
                                    </label>
                                    <label className="flex items-center gap-2 cursor-pointer text-sm">
                                        <Checkbox
                                            checked={includeStandby}
                                            onCheckedChange={(v) => setIncludeStandby(!!v)}
                                        />
                                        <span className="flex items-center gap-1.5">
                                            <span className="w-2 h-2 rounded-full bg-orange-500 inline-block" />
                                            Standby
                                            <Badge variant="secondary" className="text-xs px-1.5 py-0">{typeCounts.standby}</Badge>
                                        </span>
                                    </label>
                                    <label className="flex items-center gap-2 cursor-pointer text-sm">
                                        <Checkbox
                                            checked={includeMultiDrop}
                                            onCheckedChange={(v) => setIncludeMultiDrop(!!v)}
                                        />
                                        <span className="flex items-center gap-1.5">
                                            <span className="w-2 h-2 rounded-full bg-purple-500 inline-block" />
                                            Multi-drop stops
                                            <Badge variant="secondary" className="text-xs px-1.5 py-0">{typeCounts.multiDrop}</Badge>
                                        </span>
                                    </label>
                                </div>
                            </div>
                        )}

                        <Button onClick={loadTrips} disabled={loading} variant="outline">
                            {loading ? <Loader2 className="h-4 w-4 animate-spin mr-2" /> : <RefreshCw className="h-4 w-4 mr-2" />}
                            {t("accounting.billingDocument.filters.load")}
                        </Button>
                    </CardContent>
                </Card>

                {/* ── Summary cards ── */}
                {trips.length > 0 && (
                    <div className="space-y-3">
                        {/* Breakdown by type */}
                        <div className="grid grid-cols-3 gap-3">
                            <Card className="border-l-4 border-l-blue-500">
                                <CardContent className="pt-4 pb-3">
                                    <p className="text-xs text-muted-foreground">เที่ยวปกติ</p>
                                    <p className="text-xl font-bold">{breakdown.tripOnlyCount} เที่ยว</p>
                                    <p className="text-sm font-mono text-blue-600">{formatThb(breakdown.tripSubtotal)}</p>
                                </CardContent>
                            </Card>
                            <Card className="border-l-4 border-l-orange-500">
                                <CardContent className="pt-4 pb-3">
                                    <p className="text-xs text-muted-foreground">Standby</p>
                                    <p className="text-xl font-bold">{breakdown.standbyCount} ครั้ง</p>
                                    <p className="text-sm font-mono text-orange-600">{formatThb(breakdown.standbySubtotal)}</p>
                                </CardContent>
                            </Card>
                            <Card className="border-l-4 border-l-purple-500">
                                <CardContent className="pt-4 pb-3">
                                    <p className="text-xs text-muted-foreground">Multi-drop stops</p>
                                    <p className="text-xl font-bold">{breakdown.multiDropCount} จุด</p>
                                    <p className="text-sm font-mono text-purple-600">{formatThb(breakdown.multiDropSubtotal)}</p>
                                </CardContent>
                            </Card>
                        </div>
                        {/* Totals */}
                        <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
                            <Card>
                                <CardContent className="pt-4">
                                    <p className="text-xs text-muted-foreground">{t("accounting.billingDocument.summary.tripCount")}</p>
                                    <p className="text-2xl font-bold">{filteredTrips.length}</p>
                                </CardContent>
                            </Card>
                            <Card>
                                <CardContent className="pt-4">
                                    <p className="text-xs text-muted-foreground">{t("accounting.billingDocument.summary.total")}</p>
                                    <p className="text-2xl font-bold">{formatThb(grandTotal)}</p>
                                </CardContent>
                            </Card>
                            <Card>
                                <CardContent className="pt-4">
                                    <p className="text-xs text-muted-foreground">{t("accounting.billingDocument.summary.withholdingTax")}</p>
                                    <p className="text-2xl font-bold text-red-600">-{formatThb(withholdingTax)}</p>
                                </CardContent>
                            </Card>
                            <Card>
                                <CardContent className="pt-4">
                                    <p className="text-xs text-muted-foreground">{t("accounting.billingDocument.summary.netTotal")}</p>
                                    <p className="text-2xl font-bold text-green-600">{formatThb(totalNet)}</p>
                                </CardContent>
                            </Card>
                        </div>
                    </div>
                )}

                {/* ── Download button ── */}
                {filteredTrips.length > 0 && (
                    <Card>
                        <CardContent className="pt-4 flex items-center justify-between">
                            <div className="flex items-center gap-2 text-sm text-muted-foreground">
                                <FileText className="h-4 w-4" />
                                <span>
                                    {t("accounting.billingDocument.download.filesInfo")} <code>invoice_summary.pdf</code>, <code>invoice_detail.xlsx</code>, <code>receipt.pdf</code>
                                </span>
                            </div>
                            <Button
                                onClick={handleDownload}
                                disabled={!canDownload || generating}
                            >
                                {generating ? <Loader2 className="h-4 w-4 animate-spin mr-2" /> : <Download className="h-4 w-4 mr-2" />}
                                Download Billing Package (.zip)
                            </Button>
                        </CardContent>
                        {!canDownload && selectedCustomerId === "all" && (
                            <CardContent className="pt-0">
                                <p className="text-xs text-amber-600">{t("accounting.billingDocument.download.selectCustomerWarning")}</p>
                            </CardContent>
                        )}
                    </Card>
                )}

                {/* ── Trip preview table ── */}
                {filteredTrips.length > 0 ? (
                    <Card>
                        <CardHeader><CardTitle className="text-base">{t("accounting.billingDocument.table.title", { count: filteredTrips.length })}</CardTitle></CardHeader>
                        <CardContent className="p-0">
                            <Table>
                                <TableHeader>
                                    <TableRow>
                                        <TableHead>{t("accounting.billingDocument.table.tripNumber")}</TableHead>
                                        <TableHead>{t("accounting.billingDocument.table.deliveredDate")}</TableHead>
                                        <TableHead>{t("accounting.billingDocument.table.route")}</TableHead>
                                        <TableHead>{t("accounting.billingDocument.table.vehicleType")}</TableHead>
                                        <TableHead>{t("accounting.billingDocument.table.driver")}</TableHead>
                                        <TableHead>{t("accounting.billingDocument.table.jobCategory")}</TableHead>
                                        <TableHead className="text-right">{t("accounting.billingDocument.table.billingAmount")}</TableHead>
                                    </TableRow>
                                </TableHeader>
                                <TableBody>
                                    {filteredTrips.map((trip) => {
                                        // hubDisplayName / destinationDisplayName already resolved by fetchBillingTripRows at load time.
                                        // J&T: show the source-hub CODE (SPK-GW) to match the Excel export (ADR-0005).
                                        const isJntCustomer = /j&t|jnt|j and t/i.test(selectedCustomer?.name ?? "");
                                        const originDisplay = isJntCustomer
                                            ? (trip.originHubCode || trip.billingLookupHubId || trip.hubDisplayName || "-")
                                            : (trip.hubDisplayName ?? trip.billingLookupHubId ?? "-");
                                        const destDisplay   = trip.destinationDisplayName ?? trip.billingLookupDestination ?? "-";
                                        return (
                                        <TableRow key={trip.id} className={trip.rowType === "standby" ? "bg-amber-950/20" : trip.rowType === "multidrop_stop" ? "bg-blue-950/10" : undefined}>
                                            <TableCell className="font-mono text-xs">
                                                <div className="flex flex-col gap-1">
                                                    <span>{trip.spxTripId ?? trip.id.slice(0, 8)}</span>
                                                    {trip.rowType === "standby" && (
                                                        <Badge variant="outline" className="w-fit text-amber-400 border-amber-600 text-[10px] px-1 py-0">
                                                            {t("accounting.billingDocument.badge.standby")}
                                                        </Badge>
                                                    )}
                                                    {trip.rowType === "multidrop_stop" && (
                                                        <Badge variant="outline" className="w-fit text-purple-400 border-purple-600 text-[10px] px-1 py-0">
                                                            {t("accounting.billingDocument.badge.stopN", { n: trip.stopIndex ?? 0 })}
                                                        </Badge>
                                                    )}
                                                    {trip.rowType === "trip" && (
                                                        <Badge variant="outline" className="w-fit text-blue-400 border-blue-600 text-[10px] px-1 py-0">
                                                            {t("accounting.billingDocument.badge.trip")}
                                                        </Badge>
                                                    )}
                                                </div>
                                            </TableCell>
                                            <TableCell className="text-xs">
                                                {trip.deliveredTimestamp ? format(trip.deliveredTimestamp, "dd/MM/yyyy HH:mm") : "-"}
                                            </TableCell>
                                            <TableCell className="text-xs">
                                                {[originDisplay, destDisplay].filter(Boolean).join(" → ")}
                                            </TableCell>
                                            <TableCell>
                                                {trip.vehicleClass ? <Badge variant="outline">{trip.vehicleClass}</Badge> : "-"}
                                            </TableCell>
                                            <TableCell className="text-xs">{trip.driverName ?? "-"}</TableCell>
                                            <TableCell>
                                                {trip.jobCategory === "SUPPLEMENTARY" ? (
                                                    <Badge variant="outline" className="text-amber-400 border-amber-600">
                                                        {t("accounting.billingDocument.badge.jobCategorySupplementary")}
                                                    </Badge>
                                                ) : (
                                                    <Badge variant="outline">
                                                        {t("accounting.billingDocument.badge.jobCategoryPrimary")}
                                                    </Badge>
                                                )}
                                            </TableCell>
                                            <TableCell className="text-right font-mono">
                                                {formatThb(trip.billingEstimateThb)}
                                            </TableCell>
                                        </TableRow>
                                        );
                                    })}
                                </TableBody>
                            </Table>
                        </CardContent>
                    </Card>
                ) : !loading && (
                    <Card>
                        <CardContent className="pt-6 text-center text-muted-foreground text-sm">
                            {t("accounting.billingDocument.empty")}
                        </CardContent>
                    </Card>
                )}
            </div>
        </PagePermissionGuard>
    );
}
