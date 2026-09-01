"use client";

import { PagePermissionGuard } from "@/components/page-permission-guard";
import { CAPABILITIES } from "@/lib/capabilities";
import { useEffect, useMemo, useState } from "react";
import { useLanguage } from "@/context/language";
import { getCustomers } from "@/features/customers/api/customers";
import type { CustomerData } from "@/features/customers/api/customers";
import { fetchShopeeExpressReportTrips, type ShopeeReportTripRow } from "@/features/accounting";
import { getOwnerCompany } from "@/features/companies/api/companies";
import {
    downloadShopeeExpressReportPdf,
} from "@/lib/shopeeExpressReport";
import type { BillingCustomer, BillingPeriod, BillingProviderInfo } from "@/lib/billingDocument";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Download, FileText, Loader2, RefreshCw, AlertTriangle } from "lucide-react";
import { format } from "date-fns";
import { toast } from "sonner";

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

export default function ShopeeExpressReportPage() {
    const { t } = useLanguage();
    const now = new Date();

    const [selectedMonth, setSelectedMonth] = useState(now.getMonth() + 1);
    const [selectedYear, setSelectedYear] = useState(now.getFullYear());
    const [selectedCustomerId, setSelectedCustomerId] = useState<string>("");

    const [customers, setCustomers] = useState<CustomerData[]>([]);
    const [rows, setRows] = useState<ShopeeReportTripRow[]>([]);
    const [loading, setLoading] = useState(false);
    const [generating, setGenerating] = useState(false);
    const [loaded, setLoaded] = useState(false);
    const [ownerProvider, setOwnerProvider] = useState<BillingProviderInfo | undefined>(undefined);

    // Load customers (default-select TTP) + owner company for PDF branding
    useEffect(() => {
        getCustomers()
            .then((list) => {
                setCustomers(list);
                const ttp = list.find((c) => (c.code ?? "").trim().toUpperCase() === "TTP");
                if (ttp?.id) setSelectedCustomerId(ttp.id);
                else if (list[0]?.id) setSelectedCustomerId(list[0].id);
            })
            .catch(console.error);
        getOwnerCompany()
            .then((company) => {
                if (company) {
                    setOwnerProvider({
                        name: company.nameTh,
                        shortName: company.shortName,
                        address: company.address,
                        taxId: company.taxId,
                        withholdingTaxRate: company.withholdingTaxRate,
                        stampUrl: company.stampUrl,
                        signatureUrl: company.signatureUrl,
                        signatoryName: company.signatoryName,
                    });
                }
            })
            .catch((e) => console.warn("[shopee-report] getOwnerCompany failed:", e));
    }, []);

    const selectedCustomer = useMemo(
        () => customers.find((c) => c.id === selectedCustomerId) ?? null,
        [customers, selectedCustomerId]
    );

    async function loadTrips() {
        if (!selectedCustomerId) return;
        setLoading(true);
        try {
            const data = await fetchShopeeExpressReportTrips(selectedCustomerId, {
                month: selectedMonth,
                year: selectedYear,
            });
            setRows(data);
            setLoaded(true);
        } catch (e) {
            console.error("[shopee-report] fetchShopeeExpressReportTrips failed:", e);
            toast.error(t("accounting.shopeeReport.loadError", "โหลดข้อมูลไม่สำเร็จ กรุณาลองใหม่"));
        } finally {
            setLoading(false);
        }
    }

    const driverCount = useMemo(() => {
        const set = new Set(rows.map((r) => r.driverId?.trim() || r.driverName));
        return set.size;
    }, [rows]);
    const totalPhotos = useMemo(() => rows.reduce((s, r) => s + r.signedRunsheetPhotos.length, 0), [rows]);
    const missingCount = useMemo(() => rows.filter((r) => r.signedRunsheetPhotos.length === 0).length, [rows]);

    async function handleDownload() {
        if (!selectedCustomer || rows.length === 0) return;
        setGenerating(true);
        try {
            const period: BillingPeriod = { month: selectedMonth, year: selectedYear };
            const billingCustomer: BillingCustomer = {
                id: selectedCustomer.id,
                name: selectedCustomer.name,
                address: selectedCustomer.address,
                taxId: selectedCustomer.taxId,
            };
            await downloadShopeeExpressReportPdf(
                rows,
                billingCustomer,
                period,
                ownerProvider,
                selectedCustomer.code
            );
        } catch (e) {
            console.error("[shopee-report] generate PDF failed:", e);
            toast.error(t("accounting.shopeeReport.generateError", "สร้าง PDF ไม่สำเร็จ"));
        } finally {
            setGenerating(false);
        }
    }

    return (
        <PagePermissionGuard capability={CAPABILITIES.accounting_shopee_report}>
            <div className="p-6 space-y-6">
                <div>
                    <h1 className="text-2xl font-bold">{t("nav.shopeeExpressReport")}</h1>
                    <p className="text-muted-foreground text-sm mt-1">
                        {t("accounting.shopeeReport.subtitle")}
                    </p>
                </div>

                {/* ── Filters ── */}
                <Card>
                    <CardHeader>
                        <CardTitle className="text-base">{t("accounting.shopeeReport.filters.title")}</CardTitle>
                    </CardHeader>
                    <CardContent className="flex flex-wrap gap-4 items-end">
                        <div className="space-y-1">
                            <Label>{t("accounting.shopeeReport.filters.month")}</Label>
                            <Select value={String(selectedMonth)} onValueChange={(v) => setSelectedMonth(Number(v))}>
                                <SelectTrigger className="w-52"><SelectValue /></SelectTrigger>
                                <SelectContent>
                                    {MONTHS.map((m) => (
                                        <SelectItem key={m.value} value={String(m.value)}>{m.label}</SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>

                        <div className="space-y-1">
                            <Label>{t("accounting.shopeeReport.filters.year")}</Label>
                            <Select value={String(selectedYear)} onValueChange={(v) => setSelectedYear(Number(v))}>
                                <SelectTrigger className="w-32"><SelectValue /></SelectTrigger>
                                <SelectContent>
                                    {YEARS.map((y) => (
                                        <SelectItem key={y} value={String(y)}>{y}</SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>

                        <div className="space-y-1">
                            <Label>{t("accounting.shopeeReport.filters.customer")}</Label>
                            <Select value={selectedCustomerId} onValueChange={setSelectedCustomerId}>
                                <SelectTrigger className="w-56">
                                    <SelectValue placeholder={t("accounting.shopeeReport.filters.customer")} />
                                </SelectTrigger>
                                <SelectContent>
                                    {customers.map((c) => (
                                        <SelectItem key={c.id} value={c.id}>
                                            {c.code ? `${c.code} — ${c.name}` : c.name}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>

                        <Button onClick={loadTrips} disabled={loading || !selectedCustomerId} variant="outline">
                            {loading ? <Loader2 className="h-4 w-4 animate-spin mr-2" /> : <RefreshCw className="h-4 w-4 mr-2" />}
                            {t("accounting.shopeeReport.filters.load")}
                        </Button>
                    </CardContent>
                </Card>

                {/* ── Summary cards ── */}
                {loaded && rows.length > 0 && (
                    <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
                        <Card>
                            <CardContent className="pt-4">
                                <p className="text-xs text-muted-foreground">{t("accounting.shopeeReport.summary.trips")}</p>
                                <p className="text-2xl font-bold">{rows.length}</p>
                            </CardContent>
                        </Card>
                        <Card>
                            <CardContent className="pt-4">
                                <p className="text-xs text-muted-foreground">{t("accounting.shopeeReport.summary.drivers")}</p>
                                <p className="text-2xl font-bold">{driverCount}</p>
                            </CardContent>
                        </Card>
                        <Card>
                            <CardContent className="pt-4">
                                <p className="text-xs text-muted-foreground">{t("accounting.shopeeReport.summary.signedPhotos")}</p>
                                <p className="text-2xl font-bold text-green-600">{totalPhotos}</p>
                            </CardContent>
                        </Card>
                        <Card className={missingCount > 0 ? "border-l-4 border-l-amber-500" : undefined}>
                            <CardContent className="pt-4">
                                <p className="text-xs text-muted-foreground">{t("accounting.shopeeReport.summary.missing")}</p>
                                <p className={`text-2xl font-bold ${missingCount > 0 ? "text-amber-600" : ""}`}>{missingCount}</p>
                            </CardContent>
                        </Card>
                    </div>
                )}

                {/* ── Missing warning ── */}
                {loaded && missingCount > 0 && (
                    <Card className="border-amber-500/50">
                        <CardContent className="pt-4 flex items-center gap-2 text-sm text-amber-600">
                            <AlertTriangle className="h-4 w-4 shrink-0" />
                            <span>{t("accounting.shopeeReport.missingWarning", { count: String(missingCount) })}</span>
                        </CardContent>
                    </Card>
                )}

                {/* ── Download ── */}
                {loaded && rows.length > 0 && (
                    <Card>
                        <CardContent className="pt-4 flex items-center justify-between">
                            <div className="flex items-center gap-2 text-sm text-muted-foreground">
                                <FileText className="h-4 w-4" />
                                <span>{t("accounting.shopeeReport.download.info")}</span>
                            </div>
                            <Button onClick={handleDownload} disabled={generating}>
                                {generating ? <Loader2 className="h-4 w-4 animate-spin mr-2" /> : <Download className="h-4 w-4 mr-2" />}
                                {t("accounting.shopeeReport.download.button")}
                            </Button>
                        </CardContent>
                    </Card>
                )}

                {/* ── Preview table ── */}
                {loaded && rows.length > 0 ? (
                    <Card>
                        <CardHeader>
                            <CardTitle className="text-base">{t("accounting.shopeeReport.table.title", { count: String(rows.length) })}</CardTitle>
                        </CardHeader>
                        <CardContent className="p-0">
                            <Table>
                                <TableHeader>
                                    <TableRow>
                                        <TableHead>{t("accounting.shopeeReport.table.deliveredDate")}</TableHead>
                                        <TableHead>{t("accounting.shopeeReport.table.tripNumber")}</TableHead>
                                        <TableHead>{t("accounting.shopeeReport.table.route")}</TableHead>
                                        <TableHead>{t("accounting.shopeeReport.table.vehicleType")}</TableHead>
                                        <TableHead>{t("accounting.shopeeReport.table.plate")}</TableHead>
                                        <TableHead>{t("accounting.shopeeReport.table.driver")}</TableHead>
                                        <TableHead className="text-center">{t("accounting.shopeeReport.table.runsheets")}</TableHead>
                                    </TableRow>
                                </TableHeader>
                                <TableBody>
                                    {rows.map((r) => (
                                        <TableRow key={r.id}>
                                            <TableCell className="text-xs">
                                                {r.deliveredTimestamp ? format(r.deliveredTimestamp, "dd/MM/yyyy HH:mm") : "-"}
                                            </TableCell>
                                            <TableCell className="font-mono text-xs">{r.spxTripId ?? r.id.slice(0, 8)}</TableCell>
                                            <TableCell className="text-xs">
                                                {[r.originDisplay, r.destinationDisplay].filter((v) => v && v !== "-").join(" → ") || "-"}
                                            </TableCell>
                                            <TableCell>{r.vehicleClass ? <Badge variant="outline">{r.vehicleClass}</Badge> : "-"}</TableCell>
                                            <TableCell className="text-xs">{r.truckLicensePlate ?? "-"}</TableCell>
                                            <TableCell className="text-xs">{r.driverName}</TableCell>
                                            <TableCell className="text-center">
                                                {r.signedRunsheetPhotos.length > 0 ? (
                                                    <Badge variant="outline" className="text-green-600 border-green-500">
                                                        {r.signedRunsheetPhotos.length}
                                                    </Badge>
                                                ) : (
                                                    <Badge variant="outline" className="text-amber-600 border-amber-500">0</Badge>
                                                )}
                                            </TableCell>
                                        </TableRow>
                                    ))}
                                </TableBody>
                            </Table>
                        </CardContent>
                    </Card>
                ) : loaded && !loading ? (
                    <Card>
                        <CardContent className="pt-6 text-center text-muted-foreground text-sm">
                            {t("accounting.shopeeReport.empty")}
                        </CardContent>
                    </Card>
                ) : null}
            </div>
        </PagePermissionGuard>
    );
}
