"use client";
import { PagePermissionGuard } from "@/components/page-permission-guard";
import { CAPABILITIES } from "@/lib/capabilities";
import { useEffect, useMemo, useState } from "react";
import {
    collection,
    getDocs,
    getDocsFromServer,
    query,
    where,
    Timestamp,
    documentId,
    type QuerySnapshot,
    type DocumentData,
} from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { useLanguage } from "@/context/language";
import { getCustomers } from "@/features/customers/api/customers";
import type { Customer } from "@/validate/customerSchema";
import { billingHubLabelFromFirestoreData } from "@/lib/hubDisplay";
import { driverDisplayName } from "@/lib/driverName";
import { normalizeDestinationCode } from "@/lib/billingRates";
import { SOC_DESTINATIONS, normalizeSocIdToKey } from "@/validate/taskSchema";
import {
    downloadBillingZip,
    type BillingTripRow,
    type BillingCustomer,
    type BillingPeriod,
    type BillingProviderInfo,
} from "@/lib/billingDocument";
import { saveBillingStatement } from "@/lib/billingStatement";
import { getOwnerCompany } from "@/features/companies/api/companies";
import { getCustomerServiceFees } from "@/features/accounting";
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

/** Extra keys so billing destination codes (e.g. SPK890103) resolve to hub rows whose source_id includes a Thai suffix. */
function extraDestinationLookupKeys(sourceId: string): string[] {
    const u = sourceId.trim().toUpperCase();
    const norm = normalizeDestinationCode(sourceId);
    if (!norm || norm === u) return [];
    if (/^SPK-[A-Z0-9]+$/.test(u)) return [];
    return [norm];
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
    // Reverse map: any hub name/label/code (UPPERCASE) → source_id code. Used to render the
    // J&T origin as a hub CODE even for trips whose billingLookupHubId snapshot stored a NAME.
    const [hubCodeMap, setHubCodeMap] = useState<Map<string, string>>(new Map());

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

    // Load hub display names once
    useEffect(() => {
        getDocs(collection(db, COLLECTIONS.HUBS)).then((snap) => {
            const map = new Map<string, string>();
            const codeMap = new Map<string, string>();
            snap.forEach((d) => {
                const data = d.data();
                const label = billingHubLabelFromFirestoreData(data);
                const sourceId = String(data.source_id ?? data.hubId ?? data.hubCode ?? "").trim();
                // Map all possible code fields so any hub reference format resolves to a display name
                if (data.source_id) map.set(String(data.source_id).trim().toUpperCase(), label);
                if (data.hubId)     map.set(String(data.hubId).trim().toUpperCase(), label);
                if (data.hubCode)   map.set(String(data.hubCode).trim().toUpperCase(), label);
                // Normalized destination keys (e.g. "SPK890103-ลาดกระบัง26" → "SPK890103")
                // so billing codes that drop the Thai suffix still resolve to the name.
                for (const extra of extraDestinationLookupKeys(sourceId)) {
                    map.set(extra, label);
                }
                map.set(d.id, label);
                map.set(d.id.toUpperCase(), label);

                // Reverse: name/label/code → source_id code (for the J&T origin-code display rule).
                if (sourceId) {
                    const codeKey = sourceId.toUpperCase();
                    codeMap.set(codeKey, sourceId);
                    codeMap.set(label.trim().toUpperCase(), sourceId);
                    for (const nameField of [data.source_name_en, data.source_name_th, data.hubName, data.hubTHName]) {
                        const name = typeof nameField === "string" ? nameField.trim() : "";
                        if (name) codeMap.set(name.toUpperCase(), sourceId);
                    }
                    codeMap.set(d.id.toUpperCase(), sourceId);
                }
            });
            setHubNameMap(map);
            setHubCodeMap(codeMap);
        }).catch(console.error);
    }, []);

    const resolveDisplayName = (code: string | undefined): string => {
        if (!code) return "-";
        const trimmed = code.trim();
        if (!trimmed) return "-";
        const upper = trimmed.toUpperCase();
        // 1. Try raw code first (e.g. "SPK-GW" → "J&T EXPRESS บางปู")
        if (hubNameMap.get(upper)) return hubNameMap.get(upper)!;
        if (trimmed !== upper && hubNameMap.get(trimmed)) return hubNameMap.get(trimmed)!;
        // 2. Try normalized code (strips suffix after dash for SOC codes)
        const norm = normalizeDestinationCode(trimmed);
        if (norm && norm !== upper && hubNameMap.get(norm)) return hubNameMap.get(norm)!;
        // 3. SOC destinations (SOCE/SOCN/SOCW/SPKxxxxxx)
        const socKey = normalizeSocIdToKey(upper);
        if (socKey && (SOC_DESTINATIONS as Record<string, string>)[socKey]) {
            return (SOC_DESTINATIONS as Record<string, string>)[socKey];
        }
        // 4. Fallback: return as-is (already a display name like "J&T EXPRESS บางปู")
        return trimmed;
    };

    /**
     * Resolve a source-hub reference (which may be a CODE like "SPK-GW" or a NAME like
     * "J&T EXPRESS บางปู", depending on what the trip's billing snapshot stored) to its
     * canonical hub CODE (source_id). Used for the J&T origin-code rule. Unknown values
     * pass through unchanged so already-correct codes are preserved.
     */
    const resolveHubCode = (value: string | undefined): string => {
        const trimmed = (value ?? "").trim();
        if (!trimmed) return trimmed;
        return hubCodeMap.get(trimmed.toUpperCase()) ?? trimmed;
    };


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
            const tripSnap = await getDocsFromServer(query(collection(db, COLLECTIONS.TRIP_RECORDS), ...tripConstraints));

            // ── Load standby_records (isolated — index may be building) ────────
            let standbySnap: QuerySnapshot<DocumentData> | null = null;
            try {
                const standbyConstraints = [
                    where("status", "==", "completed"),
                    where("endedAt", ">=", Timestamp.fromDate(start)),
                    where("endedAt", "<", Timestamp.fromDate(end)),
                ];
                standbySnap = await getDocsFromServer(query(collection(db, COLLECTIONS.STANDBY_RECORDS), ...standbyConstraints));
            } catch (e) {
                console.warn("[billing] standby_records query failed (index may be building):", e);
            }

            // ── Collect taskIds for batch lookup ──────────────────────────────
            const taskIds = new Set<string>();
            tripSnap.forEach((d) => { const tid = d.data().taskId; if (tid) taskIds.add(tid); });
            standbySnap?.forEach((d) => { const tid = d.data().taskId; if (tid) taskIds.add(tid); });

            // ── Fetch tasks (driverName/licensePlate/customer denormalized) ────
            type TaskInfo = { truckType?: string; driverName?: string; driverPhone?: string; truckLicensePlate?: string; billingCustomerId?: string; sourceHub?: string; destination?: string };
            const taskMap = new Map<string, TaskInfo>();
            // Batched fetch via documentId() "in" (chunks of 30) — no per-row cap, so
            // trip + standby task lookups don't overflow when a month has many trips.
            const taskIdChunks: string[][] = [];
            const allTaskIds = Array.from(taskIds);
            for (let i = 0; i < allTaskIds.length; i += 30) {
                taskIdChunks.push(allTaskIds.slice(i, i + 30));
            }
            await Promise.allSettled(taskIdChunks.map(async (chunk) => {
                const taskSnap = await getDocs(
                    query(collection(db, COLLECTIONS.TASKS), where(documentId(), "in", chunk))
                );
                taskSnap.forEach((taskDoc) => {
                    const t = taskDoc.data();
                    taskMap.set(taskDoc.id, {
                        truckType: t.truckType,
                        driverName: t.driverName,
                        driverPhone: t.driverPhone,
                        truckLicensePlate: t.licensePlate,
                        billingCustomerId: t.sourceHubLinkedCustomerId,
                        sourceHub: t.sourceHub,
                        destination: t.destination,
                    });
                });
            }));

            // ── Drivers: resolve names to Thai (reports use Thai driver names) ──
            // Keyed by both doc id and authId so a trip's driverId (authId) resolves.
            const driverNameByKey = new Map<string, string>();
            const driverSubByKey = new Map<string, string>(); // Sup (ผู้รับเหมา) ของคนขับ
            try {
                const driversSnap = await getDocs(collection(db, COLLECTIONS.DRIVERS));
                driversSnap.forEach((ds) => {
                    const dd = ds.data();
                    const name = driverDisplayName(dd, ds.id);
                    const sub = (dd.subcontractorName as string | undefined)?.trim();
                    driverNameByKey.set(ds.id, name);
                    if (sub) driverSubByKey.set(ds.id, sub);
                    const authId = (dd.authId ?? dd.authUid) as string | undefined;
                    if (authId) {
                        driverNameByKey.set(authId, name);
                        if (sub) driverSubByKey.set(authId, sub);
                    }
                });
            } catch (e) {
                console.warn("[billing] failed to load drivers for Thai name resolution:", e);
            }
            const resolveDriverName = (driverId: unknown, fallback?: string): string | undefined => {
                const key = String(driverId ?? "").trim();
                return (key && driverNameByKey.get(key)) || fallback;
            };
            const resolveSubcontractor = (driverId: unknown): string | undefined => {
                const key = String(driverId ?? "").trim();
                return key ? driverSubByKey.get(key) : undefined;
            };

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
                            driverName: resolveDriverName(data.driverId, taskInfo?.driverName),
                            driverPhone: taskInfo?.driverPhone,
                            subcontractorName: resolveSubcontractor(data.driverId),
                            jobCategory: data.jobCategory === "SUPPLEMENTARY" ? "SUPPLEMENTARY" : "PRIMARY",
                            truckLicensePlate: taskInfo?.truckLicensePlate,
                            hubDisplayName: resolveDisplayName(hubId),
                            originHubCode: resolveHubCode(hubId || (taskInfo?.sourceHub as string | undefined) || ""),
                            destinationDisplayName: resolveDisplayName(destCode),
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
                    driverName: resolveDriverName(data.driverId, taskInfo?.driverName),
                    driverPhone: taskInfo?.driverPhone,
                    subcontractorName: resolveSubcontractor(data.driverId),
                    jobCategory: data.jobCategory === "SUPPLEMENTARY" ? "SUPPLEMENTARY" : "PRIMARY",
                    truckLicensePlate: taskInfo?.truckLicensePlate,
                    hubDisplayName: resolveDisplayName(hubId),
                    originHubCode: resolveHubCode(hubId || (taskInfo?.sourceHub as string | undefined) || ""),
                    destinationDisplayName: resolveDisplayName(dest),
                    rowType: "trip",
                });
            });

            // ── Build standby rows (source: standby_records.billingEstimateThb) ─
            standbySnap?.forEach((d) => {
                const data = d.data();
                const billingAmt = Number(data.billingEstimateThb);
                if (!billingAmt) return; // backfill not yet run → skip

                // Customer filter — prefer billingCustomerId written by backfill function
                const cid = (data.billingCustomerId as string | undefined)?.trim() || undefined;
                if (selectedCustomerId !== "all" && cid !== selectedCustomerId) return;

                const taskInfo = data.taskId ? taskMap.get(data.taskId) : undefined;

                rows.push({
                    id: d.id,
                    taskId: data.taskId ?? undefined,
                    spxTripId: (data.spxTripId as string | undefined)
                        ?? (data.migratedFromSpxTripId as string | undefined)
                        ?? (data.migratedFromTripId as string | undefined)
                        ?? undefined,
                    deliveredTimestamp: toDate(data.endedAt) ?? toDate(data.startedAt) ?? undefined,
                    billingEstimateThb: billingAmt,
                    billingCustomerId: cid,
                    vehicleClass: taskInfo?.truckType,
                    driverName: resolveDriverName(data.driverId, taskInfo?.driverName),
                    driverPhone: taskInfo?.driverPhone,
                    subcontractorName: resolveSubcontractor(data.driverId),
                    truckLicensePlate: taskInfo?.truckLicensePlate,
                    hubDisplayName: resolveDisplayName(
                        (taskInfo?.sourceHub as string | undefined) ?? (data.startLocation as string | undefined)
                    ),
                    originHubCode: resolveHubCode(
                        (taskInfo?.sourceHub as string | undefined) ?? (data.startLocation as string | undefined) ?? ""
                    ),
                    destinationDisplayName: resolveDisplayName(
                        (taskInfo?.destination as string | undefined) ?? (data.endLocation as string | undefined)
                    ),
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
                                        <TableHead className="text-right">{t("accounting.billingDocument.table.billingAmount")}</TableHead>
                                    </TableRow>
                                </TableHeader>
                                <TableBody>
                                    {filteredTrips.map((trip) => {
                                        // hubDisplayName / destinationDisplayName already resolved by resolveDisplayName at load time.
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
