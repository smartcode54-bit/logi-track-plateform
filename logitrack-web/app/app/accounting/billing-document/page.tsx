"use client";
import { PagePermissionGuard } from "@/components/page-permission-guard";
import { CAPABILITIES } from "@/lib/capabilities";
import { useEffect, useMemo, useState } from "react";
import {
    collection,
    getDocs,
    query,
    where,
    Timestamp,
    getDoc,
    doc,
    type QuerySnapshot,
    type DocumentData,
} from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { useLanguage } from "@/context/language";
import { getCustomers } from "@/features/customers/api/customers";
import type { Customer } from "@/validate/customerSchema";
import { primaryHubLabelFromFirestoreData } from "@/lib/hubDisplay";
import { normalizeDestinationCode } from "@/lib/billingRates";
import { getCustomerServiceFees } from "@/app/app/accounting/actions.client";
import {
    downloadBillingZip,
    type BillingTripRow,
    type BillingCustomer,
    type BillingPeriod,
    type BillingProviderInfo,
} from "@/lib/billingDocument";
import { saveBillingStatement } from "@/lib/billingStatement";
import { getOwnerCompany } from "@/features/companies/api/companies";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Download, FileText, Loader2, RefreshCw } from "lucide-react";
import { format } from "date-fns";
import { WITHHOLDING_TAX_RATE } from "@/lib/billingConfig";
import { toast } from "sonner";
import { useAuth } from "@/context/auth";

// ─── helpers ──────────────────────────────────────────────────────────────────

function toDate(val: unknown): Date | undefined {
    if (!val) return undefined;
    if (val instanceof Date) return val;
    if (typeof (val as { toDate?: () => Date }).toDate === "function") {
        return (val as { toDate: () => Date }).toDate();
    }
    return undefined;
}

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
    const [hubNameMap, setHubNameMap] = useState<Map<string, string>>(new Map());

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

    // Load hub display names once
    useEffect(() => {
        getDocs(collection(db, COLLECTIONS.HUBS)).then((snap) => {
            const map = new Map<string, string>();
            snap.forEach((d) => {
                const data = d.data();
                const label = primaryHubLabelFromFirestoreData(data);
                if (data.source_id) map.set(String(data.source_id), label);
                map.set(d.id, label);
            });
            setHubNameMap(map);
        }).catch(console.error);
    }, []);

    async function loadTrips() {
        setLoading(true);
        try {
            const start = new Date(selectedYear, selectedMonth - 1, 1);
            const end = new Date(selectedYear, selectedMonth, 1);

            // ── Load trip_records ──────────────────────────────────────────────
            const tripConstraints = [
                where("status", "==", "delivered"),
                where("deliveredTimestamp", ">=", Timestamp.fromDate(start)),
                where("deliveredTimestamp", "<", Timestamp.fromDate(end)),
            ];
            if (selectedCustomerId !== "all") {
                tripConstraints.push(where("billingCustomerId", "==", selectedCustomerId));
            }
            const tripSnap = await getDocs(query(collection(db, COLLECTIONS.TRIP_RECORDS), ...tripConstraints));

            // ── Load standby_records (isolated — index may be building) ────────
            let standbySnap: QuerySnapshot<DocumentData> | null = null;
            try {
                const standbyConstraints = [
                    where("status", "==", "completed"),
                    where("startedAt", ">=", Timestamp.fromDate(start)),
                    where("startedAt", "<", Timestamp.fromDate(end)),
                ];
                standbySnap = await getDocs(query(collection(db, COLLECTIONS.STANDBY_RECORDS), ...standbyConstraints));
            } catch (e) {
                console.warn("[billing] standby_records query failed (index may be building):", e);
            }

            // ── Collect taskIds for batch lookup ──────────────────────────────
            const taskIds = new Set<string>();
            tripSnap.forEach((d) => { const tid = d.data().taskId; if (tid) taskIds.add(tid); });
            standbySnap?.forEach((d) => { const tid = d.data().taskId; if (tid) taskIds.add(tid); });

            // ── Fetch tasks (driverName/licensePlate/customer denormalized) ────
            type TaskInfo = { truckType?: string; driverName?: string; driverPhone?: string; truckLicensePlate?: string; billingCustomerId?: string };
            const taskMap = new Map<string, TaskInfo>();
            await Promise.allSettled(Array.from(taskIds).slice(0, 200).map(async (tid) => {
                const taskSnap = await getDoc(doc(db, COLLECTIONS.TASKS, tid));
                if (!taskSnap.exists()) return;
                const t = taskSnap.data();
                taskMap.set(tid, {
                    truckType: t.truckType,
                    driverName: t.driverName,
                    driverPhone: t.driverPhone,
                    truckLicensePlate: t.licensePlate,
                    billingCustomerId: t.sourceHubLinkedCustomerId,
                });
            }));

            // ── Standby fee rate (per trip, per customer) ──────────────────────
            let allServiceFees: Awaited<ReturnType<typeof getCustomerServiceFees>> = [];
            try {
                allServiceFees = await getCustomerServiceFees(
                    selectedCustomerId !== "all" ? selectedCustomerId : undefined
                );
            } catch (e) {
                console.warn("[billing] getCustomerServiceFees failed:", e);
            }
            const standbyFeeMap = new Map<string, number>();
            for (const fee of allServiceFees) {
                if (fee.feeType === "standby" && fee.unit === "per_trip") {
                    standbyFeeMap.set(fee.customerId, fee.amountThb);
                }
            }

            const rows: BillingTripRow[] = [];

            // ── Build trip rows ────────────────────────────────────────────────
            tripSnap.forEach((d) => {
                const data = d.data();
                if (!Number(data.billingEstimateThb)) return; // skip no-billing trips

                const taskInfo = data.taskId ? taskMap.get(data.taskId) : undefined;
                const hubId = data.billingLookupHubId ?? "";

                // Multidrop: expand breakdown into one row per billed stop
                if (data.billingIsMultiDelivery && Array.isArray(data.billingMultiDeliveryBreakdown) && data.billingMultiDeliveryBreakdown.length > 0) {
                    for (const stop of data.billingMultiDeliveryBreakdown as { stopIndex: number; destination: string; baseRateThb: number; finalRateThb: number }[]) {
                        if (!stop.finalRateThb) continue;
                        const destCode = stop.destination ?? "";
                        rows.push({
                            id: `${d.id}_s${stop.stopIndex}`,
                            taskId: data.taskId,
                            spxTripId: data.spxTripId ? `${data.spxTripId}-s${stop.stopIndex}` : undefined,
                            deliveredTimestamp: toDate(data.deliveredTimestamp),
                            billingEstimateThb: stop.finalRateThb,
                            billingBaseRateThb: stop.baseRateThb || undefined,
                            billingLookupHubId: hubId,
                            billingLookupDestination: destCode,
                            billingCustomerId: data.billingCustomerId,
                            vehicleClass: taskInfo?.truckType,
                            driverName: taskInfo?.driverName,
                            driverPhone: taskInfo?.driverPhone,
                            truckLicensePlate: taskInfo?.truckLicensePlate,
                            hubDisplayName: hubNameMap.get(hubId) ?? hubId,
                            destinationDisplayName: hubNameMap.get(normalizeDestinationCode(destCode) ?? destCode) ?? destCode,
                            rowType: "multidrop_stop",
                            stopIndex: stop.stopIndex,
                        });
                    }
                    return;
                }

                // Single-delivery trip
                const dest = data.billingLookupDestination ?? "";
                rows.push({
                    id: d.id,
                    taskId: data.taskId,
                    spxTripId: data.spxTripId,
                    deliveredTimestamp: toDate(data.deliveredTimestamp),
                    billingEstimateThb: Number(data.billingEstimateThb),
                    billingBaseRateThb: Number(data.billingBaseRateThb) || undefined,
                    billingLookupHubId: hubId,
                    billingLookupDestination: dest,
                    billingRateMultiplier: Number(data.billingRateMultiplier) || undefined,
                    billingAddThbPerTrip: Number(data.billingAddThbPerTrip) || undefined,
                    billingCustomerId: data.billingCustomerId,
                    vehicleClass: taskInfo?.truckType,
                    driverName: taskInfo?.driverName,
                    driverPhone: taskInfo?.driverPhone,
                    truckLicensePlate: taskInfo?.truckLicensePlate,
                    hubDisplayName: hubNameMap.get(hubId) ?? hubId,
                    destinationDisplayName: hubNameMap.get(normalizeDestinationCode(dest) ?? dest) ?? dest,
                    rowType: "trip",
                });
            });

            // ── Build standby rows ─────────────────────────────────────────────
            standbySnap?.forEach((d) => {
                const data = d.data();
                const taskInfo = data.taskId ? taskMap.get(data.taskId) : undefined;
                const recordCustomerId = taskInfo?.billingCustomerId;

                // Filter by selected customer
                if (selectedCustomerId !== "all" && recordCustomerId !== selectedCustomerId) return;

                const effectiveCustomerId = recordCustomerId ?? selectedCustomerId;
                const feeAmount = typeof effectiveCustomerId === "string" ? standbyFeeMap.get(effectiveCustomerId) : undefined;
                if (!feeAmount) return; // no standby rate configured → skip

                rows.push({
                    id: d.id,
                    taskId: data.taskId ?? undefined,
                    deliveredTimestamp: toDate(data.endedAt) ?? toDate(data.startedAt) ?? undefined,
                    billingEstimateThb: feeAmount,
                    billingCustomerId: effectiveCustomerId !== "all" ? effectiveCustomerId : undefined,
                    vehicleClass: taskInfo?.truckType,
                    driverName: taskInfo?.driverName,
                    driverPhone: taskInfo?.driverPhone,
                    truckLicensePlate: taskInfo?.truckLicensePlate,
                    hubDisplayName: data.startLocation ?? "-",
                    destinationDisplayName: data.endLocation ?? "-",
                    rowType: "standby",
                });
            });

            rows.sort((a, b) => (a.deliveredTimestamp?.getTime() ?? 0) - (b.deliveredTimestamp?.getTime() ?? 0));
            setTrips(rows);
        } finally {
            setLoading(false);
        }
    }

    const filteredTrips = useMemo(() => {
        if (selectedCustomerId === "all") return trips;
        return trips.filter((t) => t.billingCustomerId === selectedCustomerId);
    }, [trips, selectedCustomerId]);

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

                        <Button onClick={loadTrips} disabled={loading} variant="outline">
                            {loading ? <Loader2 className="h-4 w-4 animate-spin mr-2" /> : <RefreshCw className="h-4 w-4 mr-2" />}
                            {t("accounting.billingDocument.filters.load")}
                        </Button>
                    </CardContent>
                </Card>

                {/* ── Summary cards ── */}
                {trips.length > 0 && (
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
                                        <TableHead className="text-right">{t("accounting.billingDocument.table.billingAmount")}</TableHead>
                                    </TableRow>
                                </TableHeader>
                                <TableBody>
                                    {filteredTrips.map((trip) => (
                                        <TableRow key={trip.id} className={trip.rowType === "standby" ? "bg-amber-950/20" : trip.rowType === "multidrop_stop" ? "bg-blue-950/10" : undefined}>
                                            <TableCell className="font-mono text-xs">
                                                {trip.rowType === "standby"
                                                    ? <Badge variant="outline" className="text-amber-400 border-amber-600">{t("accounting.billingDocument.badge.standby")}</Badge>
                                                    : trip.rowType === "multidrop_stop"
                                                        ? <span className="text-muted-foreground">{t("accounting.billingDocument.badge.stopN", { n: trip.stopIndex ?? 0 })}</span>
                                                        : (trip.spxTripId ?? trip.id.slice(0, 8))
                                                }
                                            </TableCell>
                                            <TableCell className="text-xs">
                                                {trip.deliveredTimestamp ? format(trip.deliveredTimestamp, "dd/MM/yyyy HH:mm") : "-"}
                                            </TableCell>
                                            <TableCell className="text-xs">
                                                {[trip.hubDisplayName ?? trip.billingLookupHubId, trip.destinationDisplayName ?? trip.billingLookupDestination].filter(Boolean).join(" → ")}
                                            </TableCell>
                                            <TableCell>
                                                {trip.rowType === "standby"
                                                    ? <Badge variant="outline" className="text-amber-400 border-amber-600 text-xs">{t("accounting.billingDocument.badge.standby")}</Badge>
                                                    : trip.vehicleClass ? <Badge variant="outline">{trip.vehicleClass}</Badge> : "-"
                                                }
                                            </TableCell>
                                            <TableCell className="text-xs">{trip.driverName ?? "-"}</TableCell>
                                            <TableCell className="text-right font-mono">
                                                {formatThb(trip.billingEstimateThb)}
                                            </TableCell>
                                        </TableRow>
                                    ))}
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
