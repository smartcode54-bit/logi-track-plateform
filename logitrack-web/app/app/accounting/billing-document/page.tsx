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
} from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { useLanguage } from "@/context/language";
import { getCustomers } from "@/features/customers/api/customers";
import type { Customer } from "@/validate/customerSchema";
import { primaryHubLabelFromFirestoreData } from "@/lib/hubDisplay";
import { normalizeDestinationCode } from "@/lib/billingRates";
import {
    downloadBillingZip,
    type BillingTripRow,
    type BillingCustomer,
    type BillingPeriod,
} from "@/lib/billingDocument";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Download, FileText, Loader2, RefreshCw } from "lucide-react";
import { format } from "date-fns";
import { WITHHOLDING_TAX_RATE } from "@/lib/billingConfig";

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

    const now = new Date();
    const [selectedMonth, setSelectedMonth] = useState(now.getMonth() + 1);
    const [selectedYear, setSelectedYear] = useState(now.getFullYear());
    const [selectedCustomerId, setSelectedCustomerId] = useState<string>("all");

    const [customers, setCustomers] = useState<Customer[]>([]);
    const [trips, setTrips] = useState<BillingTripRow[]>([]);
    const [hubNameMap, setHubNameMap] = useState<Map<string, string>>(new Map());

    const [loading, setLoading] = useState(false);
    const [generating, setGenerating] = useState(false);

    // Load customers once
    useEffect(() => {
        getCustomers().then(setCustomers).catch(console.error);
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

            const constraints = [
                where("status", "==", "delivered"),
                where("deliveredTimestamp", ">=", Timestamp.fromDate(start)),
                where("deliveredTimestamp", "<", Timestamp.fromDate(end)),
            ];
            if (selectedCustomerId !== "all") {
                constraints.push(where("billingCustomerId", "==", selectedCustomerId));
            }

            const snap = await getDocs(query(collection(db, COLLECTIONS.TRIP_RECORDS), ...constraints));

            // Collect taskIds for batch lookup
            const taskIds = new Set<string>();
            snap.forEach((d) => { const tid = d.data().taskId; if (tid) taskIds.add(tid); });

            // Fetch tasks (batch of up to 30 per getDocs)
            const taskMap = new Map<string, { truckType?: string; driverName?: string; driverPhone?: string; truckLicensePlate?: string }>();
            const taskIdArr = Array.from(taskIds).slice(0, 100);
            await Promise.allSettled(taskIdArr.map(async (tid) => {
                const taskSnap = await getDoc(doc(db, COLLECTIONS.TASKS, tid));
                if (!taskSnap.exists()) return;
                const t = taskSnap.data();
                const info: { truckType?: string; driverName?: string; driverPhone?: string; truckLicensePlate?: string } = {
                    truckType: t.truckType,
                };
                // Load driver info
                if (t.assignedDriverId) {
                    const driverSnap = await getDoc(doc(db, COLLECTIONS.DRIVERS, t.assignedDriverId)).catch(() => null);
                    if (driverSnap?.exists()) {
                        const d = driverSnap.data();
                        info.driverName = d.name ?? d.fullName;
                        info.driverPhone = d.phone ?? d.phoneNumber;
                    }
                }
                // Load truck license plate
                if (t.assignedTruckId) {
                    const truckSnap = await getDoc(doc(db, COLLECTIONS.TRUCKS, t.assignedTruckId)).catch(() => null);
                    if (truckSnap?.exists()) {
                        info.truckLicensePlate = truckSnap.data().licensePlate;
                    }
                }
                taskMap.set(tid, info);
            }));

            const rows: BillingTripRow[] = [];
            snap.forEach((d) => {
                const data = d.data();
                const billingEstimateThb = Number(data.billingEstimateThb);
                if (!billingEstimateThb) return; // skip trips without billing

                const taskInfo = data.taskId ? taskMap.get(data.taskId) : undefined;
                const hubId = data.billingLookupHubId ?? "";
                const dest = data.billingLookupDestination ?? "";

                rows.push({
                    id: d.id,
                    taskId: data.taskId,
                    spxTripId: data.spxTripId,
                    deliveredTimestamp: toDate(data.deliveredTimestamp),
                    billingEstimateThb,
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
        return { id: c.id!, name: c.name, address: c.address, taxId: c.taxId, branchType: c.branchType, branchNumber: c.branchNumber };
    }, [selectedCustomerId, customers]);

    async function handleDownload() {
        if (!selectedCustomer) return;
        setGenerating(true);
        try {
            const period: BillingPeriod = { month: selectedMonth, year: selectedYear };
            await downloadBillingZip(filteredTrips, selectedCustomer, period);
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
                        สร้างเอกสารวางบิล (Invoice + Excel + ใบเสร็จ) เป็น ZIP สำหรับส่งให้ลูกค้า
                    </p>
                </div>

                {/* ── Filters ── */}
                <Card>
                    <CardHeader><CardTitle className="text-base">เลือกช่วงเวลาและลูกค้า</CardTitle></CardHeader>
                    <CardContent className="flex flex-wrap gap-4 items-end">
                        <div className="space-y-1">
                            <Label>เดือน</Label>
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
                            <Label>ปี</Label>
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
                            <Label>ลูกค้า</Label>
                            <Select value={selectedCustomerId} onValueChange={setSelectedCustomerId}>
                                <SelectTrigger className="w-52">
                                    <SelectValue placeholder="เลือกลูกค้า" />
                                </SelectTrigger>
                                <SelectContent>
                                    <SelectItem value="all">ทั้งหมด</SelectItem>
                                    {customers.map((c) => (
                                        <SelectItem key={c.id} value={c.id!}>{c.name}</SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>

                        <Button onClick={loadTrips} disabled={loading} variant="outline">
                            {loading ? <Loader2 className="h-4 w-4 animate-spin mr-2" /> : <RefreshCw className="h-4 w-4 mr-2" />}
                            โหลดข้อมูล
                        </Button>
                    </CardContent>
                </Card>

                {/* ── Summary cards ── */}
                {trips.length > 0 && (
                    <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
                        <Card>
                            <CardContent className="pt-4">
                                <p className="text-xs text-muted-foreground">จำนวนเที่ยว</p>
                                <p className="text-2xl font-bold">{filteredTrips.length}</p>
                            </CardContent>
                        </Card>
                        <Card>
                            <CardContent className="pt-4">
                                <p className="text-xs text-muted-foreground">ยอดรวม (THB)</p>
                                <p className="text-2xl font-bold">{formatThb(grandTotal)}</p>
                            </CardContent>
                        </Card>
                        <Card>
                            <CardContent className="pt-4">
                                <p className="text-xs text-muted-foreground">หัก ณ ที่จ่าย 1%</p>
                                <p className="text-2xl font-bold text-red-600">-{formatThb(withholdingTax)}</p>
                            </CardContent>
                        </Card>
                        <Card>
                            <CardContent className="pt-4">
                                <p className="text-xs text-muted-foreground">ยอดสุทธิ (THB)</p>
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
                                    ไฟล์ที่จะสร้าง: <code>invoice_summary.pdf</code>, <code>invoice_detail.xlsx</code>, <code>receipt.pdf</code>
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
                                <p className="text-xs text-amber-600">⚠ กรุณาเลือกลูกค้าเพื่อ generate เอกสาร (ต้องระบุลูกค้าสำหรับ invoice)</p>
                            </CardContent>
                        )}
                    </Card>
                )}

                {/* ── Trip preview table ── */}
                {filteredTrips.length > 0 ? (
                    <Card>
                        <CardHeader><CardTitle className="text-base">รายการเที่ยว ({filteredTrips.length})</CardTitle></CardHeader>
                        <CardContent className="p-0">
                            <Table>
                                <TableHeader>
                                    <TableRow>
                                        <TableHead>เลขใบงาน</TableHead>
                                        <TableHead>วันที่ส่ง</TableHead>
                                        <TableHead>เส้นทาง</TableHead>
                                        <TableHead>ประเภทรถ</TableHead>
                                        <TableHead>คนขับ</TableHead>
                                        <TableHead className="text-right">ยอดวางบิล</TableHead>
                                    </TableRow>
                                </TableHeader>
                                <TableBody>
                                    {filteredTrips.map((trip) => (
                                        <TableRow key={trip.id}>
                                            <TableCell className="font-mono text-xs">
                                                {trip.spxTripId ?? trip.id.slice(0, 8)}
                                            </TableCell>
                                            <TableCell className="text-xs">
                                                {trip.deliveredTimestamp ? format(trip.deliveredTimestamp, "dd/MM/yyyy HH:mm") : "-"}
                                            </TableCell>
                                            <TableCell className="text-xs">
                                                {[trip.hubDisplayName ?? trip.billingLookupHubId, trip.destinationDisplayName ?? trip.billingLookupDestination].filter(Boolean).join(" → ")}
                                            </TableCell>
                                            <TableCell>
                                                {trip.vehicleClass ? <Badge variant="outline">{trip.vehicleClass}</Badge> : "-"}
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
                            เลือกเดือน/ปี/ลูกค้า แล้วกด &quot;โหลดข้อมูล&quot;
                        </CardContent>
                    </Card>
                )}
            </div>
        </PagePermissionGuard>
    );
}
