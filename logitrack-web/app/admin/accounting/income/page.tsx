"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { endOfDay, format, isWithinInterval, startOfDay, subDays } from "date-fns";
import { collection, doc, getDoc, getDocs, limit, orderBy, query, where, documentId } from "firebase/firestore";
import { httpsCallable } from "firebase/functions";
import * as XLSX from "xlsx";
import {
    AlertTriangle,
    ChevronLeft,
    ChevronRight,
    DollarSign,
    Download,
    Hash,
    Loader2,
    RefreshCw,
    TrendingUp,
    Wand2,
} from "lucide-react";
import { db, functions } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { useLanguage } from "@/context/language";
import { useAuth } from "@/context/auth";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { getCustomers } from "@/features/customers/api/customers";
import type { Customer } from "@/validate/customerSchema";
import { SOC_DESTINATIONS, normalizeSocIdToKey } from "@/validate/taskSchema";
import {
    extractHubId,
    normalizeDestinationCode,
    computeTripBilling,
    fetchRateEntriesForCustomers,
    fetchFuelAdjustmentsForCustomers,
    type BillingRateEntry,
    type FuelRateAdjustment,
} from "@/lib/billingRates";
import type { Task } from "@/validate/taskSchema";
import type { TripRecord } from "@/validate/tripRecordSchema";
import { primaryHubLabelFromFirestoreData } from "@/lib/hubDisplay";

type HubNameMap = Map<string, string>;

interface CustomerSummary {
    customerId: string;
    customerName: string;
    tripCount: number;
    totalIncome: number;
}

interface IncomeRow {
    id: string;
    spxTripId?: string;
    billingEstimateThb?: number;
    billingBaseRateThb?: number;
    billingRateImportId?: string;
    billingLookupHubId?: string;
    billingLookupDestination?: string;
    billingRateMultiplier?: number;
    billingAddThbPerTrip?: number;
    billingFuelAdjustmentId?: string;
    billingEffectiveFromDateStr?: string;
    billingCustomerId?: string;
    deliveredTimestamp?: Date;
}

interface MissingBillingRow {
    id: string;
    spxTripId?: string;
    taskId?: string;
    deliveredTimestamp?: Date;
    createdAt?: Date;
    sourceHub?: string;
    destination?: string;
    truckType?: string;
    customerId?: string;
    customerName?: string;
    lookupHubId?: string;
    lookupDestination?: string;
    lookupVehicleClass?: string;
    computedRate?: number;
    failureReason?: string;
}

function toDate(value: unknown): Date | undefined {
    if (!value) return undefined;
    if (value instanceof Date) return value;
    if (typeof (value as { toDate?: () => Date }).toDate === "function") {
        return (value as { toDate: () => Date }).toDate();
    }
    if (typeof value === "string" || typeof value === "number") {
        const d = new Date(value);
        return Number.isNaN(d.getTime()) ? undefined : d;
    }
    return undefined;
}

function toNumber(value: unknown): number | undefined {
    const n = Number(value);
    return Number.isFinite(n) ? n : undefined;
}

function getDestinationDisplayName(code: string | undefined | null, hubNameMap: HubNameMap): string {
    if (!code) return "";
    const trimmed = code.trim();
    const upper = trimmed.toUpperCase();
    const norm = normalizeDestinationCode(trimmed);
    const fromMap = (k: string) => hubNameMap.get(k);
    if (norm && fromMap(norm)) return fromMap(norm)!;
    if (fromMap(upper)) return fromMap(upper)!;
    if (trimmed !== upper && fromMap(trimmed)) return fromMap(trimmed)!;
    const socKey = normalizeSocIdToKey(upper);
    if (socKey && (SOC_DESTINATIONS as Record<string, string>)[socKey]) {
        return (SOC_DESTINATIONS as Record<string, string>)[socKey];
    }
    const dashIdx = upper.indexOf("-");
    if (dashIdx > 0) {
        return upper.slice(dashIdx + 1);
    }
    return norm || upper;
}

function resolveHubName(hubId: string | undefined | null, hubNameMap: HubNameMap): string {
    if (!hubId) return "";
    const name = hubNameMap.get(hubId);
    return name || hubId;
}

/** Extra keys so billing destination codes (e.g. SPK890103) resolve to hub rows whose source_id includes a Thai suffix. */
function extraDestinationLookupKeys(sourceId: string): string[] {
    const u = sourceId.trim().toUpperCase();
    const norm = normalizeDestinationCode(sourceId);
    if (!norm || norm === u) return [];
    if (/^SPK-[A-Z0-9]+$/.test(u)) return [];
    return [norm];
}

interface BackfillBillingResponse {
    scanned: number;
    eligible: number;
    written: number;
    skipped: number;
    failed: number;
    failures: { tripId: string; error?: string }[];
    capped: boolean;
}

function parseLocalDateOnly(dateStr: string): Date | null {
    if (!dateStr.trim()) return null;
    const d = new Date(`${dateStr.trim()}T12:00:00`);
    return Number.isNaN(d.getTime()) ? null : d;
}

function chunkArray<T>(arr: T[], size: number): T[][] {
    const chunks: T[][] = [];
    for (let i = 0; i < arr.length; i += size) {
        chunks.push(arr.slice(i, i + size));
    }
    return chunks;
}

let _hubNameCache: HubNameMap | null = null;

function clearHubCache() {
    _hubNameCache = null;
}

export default function AccountingIncomePage() {
    const { t } = useLanguage();
    const auth = useAuth();
    const isAdmin = auth?.customClaims?.admin === true;
    const [loading, setLoading] = useState(true);
    const [rows, setRows] = useState<IncomeRow[]>([]);
    const [missingRows, setMissingRows] = useState<MissingBillingRow[]>([]);
    const [customers, setCustomers] = useState<(Customer & { id: string })[]>([]);
    const [filterCustomerId, setFilterCustomerId] = useState("all");
    const [filterDateFrom, setFilterDateFrom] = useState("");
    const [filterDateTo, setFilterDateTo] = useState("");
    const [backfillFrom, setBackfillFrom] = useState(() => format(subDays(new Date(), 89), "yyyy-MM-dd"));
    const [backfillTo, setBackfillTo] = useState(() => format(new Date(), "yyyy-MM-dd"));
    const [backfillRunning, setBackfillRunning] = useState(false);
    const [backfillResult, setBackfillResult] = useState<BackfillBillingResponse | null>(null);
    const [backfillError, setBackfillError] = useState<string | null>(null);
    const [activeTab, setActiveTab] = useState<string>("with-billing");
    const [currentPage, setCurrentPage] = useState(1);
    const [pageSize, setPageSize] = useState(25);
    const [showSummary, setShowSummary] = useState(false);
    const [hubNameMap, setHubNameMap] = useState<HubNameMap>(new Map());
    // Missing billing tab state
    const [missingFilterStatus, setMissingFilterStatus] = useState<"all" | "canFix" | "needRateCard">("all");
    const [missingPage, setMissingPage] = useState(1);
    const [missingPageSize, setMissingPageSize] = useState(25);

    const loadData = async () => {
        setLoading(true);
        try {
            const customerList = await getCustomers();
            setCustomers(customerList as (Customer & { id: string })[]);
            const customerMap = new Map<string, Customer & { id: string }>();
            (customerList as (Customer & { id: string })[]).forEach((c) => customerMap.set(c.id, c));

            let hubNames = _hubNameCache;
            if (!hubNames) {
                const hubsSnap = await getDocs(collection(db, COLLECTIONS.HUBS));
                hubNames = new Map<string, string>();
                hubsSnap.docs.forEach((hubDoc) => {
                    const hd = hubDoc.data() as Record<string, unknown>;
                    const sourceId = String(hd.source_id ?? hd.hubId ?? hd.hubCode ?? "").trim();
                    if (!sourceId) return;
                    const label = primaryHubLabelFromFirestoreData(hd);
                    const displayName = label.trim() || sourceId;
                    const keys = new Set<string>();
                    keys.add(sourceId);
                    keys.add(sourceId.toUpperCase());
                    for (const extra of extraDestinationLookupKeys(sourceId)) {
                        keys.add(extra);
                    }
                    for (const k of keys) {
                        if (k) hubNames!.set(k, displayName);
                    }
                });
                _hubNameCache = hubNames;
            }
            setHubNameMap(hubNames);

            const snap = await getDocs(
                query(
                    collection(db, COLLECTIONS.TRIP_RECORDS),
                    where("status", "==", "delivered"),
                    orderBy("createdAt", "desc"),
                    limit(500)
                )
            );

            const withBilling: IncomeRow[] = [];
            const withoutBilling: { tripDoc: { id: string; data: Record<string, unknown> } }[] = [];

            snap.docs.forEach((docSnap) => {
                const d = docSnap.data();
                if (typeof d.billingEstimateThb === "number") {
                    withBilling.push({
                        id: docSnap.id,
                        spxTripId: d.spxTripId ? String(d.spxTripId) : undefined,
                        billingEstimateThb: toNumber(d.billingEstimateThb),
                        billingBaseRateThb: toNumber(d.billingBaseRateThb),
                        billingRateImportId: d.billingRateImportId ? String(d.billingRateImportId) : undefined,
                        billingLookupHubId: d.billingLookupHubId ? String(d.billingLookupHubId) : undefined,
                        billingLookupDestination: d.billingLookupDestination
                            ? String(d.billingLookupDestination)
                            : undefined,
                        billingRateMultiplier: toNumber(d.billingRateMultiplier),
                        billingAddThbPerTrip: toNumber(d.billingAddThbPerTrip),
                        billingFuelAdjustmentId: d.billingFuelAdjustmentId
                            ? String(d.billingFuelAdjustmentId)
                            : undefined,
                        billingEffectiveFromDateStr: d.billingEffectiveFromDateStr
                            ? String(d.billingEffectiveFromDateStr)
                            : undefined,
                        billingCustomerId: d.billingCustomerId ? String(d.billingCustomerId) : undefined,
                        deliveredTimestamp: toDate(d.deliveredTimestamp),
                    });
                } else {
                    withoutBilling.push({ tripDoc: { id: docSnap.id, data: d as Record<string, unknown> } });
                }
            });

            setRows(withBilling);

            if (withoutBilling.length > 0) {
                const taskIds = withoutBilling
                    .map((t) => t.tripDoc.data.taskId)
                    .filter((id): id is string => typeof id === "string" && id.trim().length > 0);
                const uniqueTaskIds = [...new Set(taskIds)];

                const taskMap = new Map<string, Task>();
                if (uniqueTaskIds.length > 0) {
                    const taskChunks = chunkArray(uniqueTaskIds, 30);
                    const taskSnapshots = await Promise.all(
                        taskChunks.map(chunk =>
                            getDocs(query(collection(db, COLLECTIONS.TASKS), where(documentId(), "in", chunk)))
                        )
                    );
                    taskSnapshots.forEach(snap => {
                        snap.docs.forEach(d => {
                            taskMap.set(d.id, d.data() as Task);
                        });
                    });
                }

                const customerIdsFromTasks = new Set<string>();
                taskMap.forEach((task) => {
                    if (task.sourceHubLinkedCustomerId) customerIdsFromTasks.add(task.sourceHubLinkedCustomerId);
                    if (task.destinationLinkedCustomerId) customerIdsFromTasks.add(task.destinationLinkedCustomerId);
                });

                let rateEntries: BillingRateEntry[] = [];
                let fuelAdjustments: FuelRateAdjustment[] = [];
                if (customerIdsFromTasks.size > 0) {
                    [rateEntries, fuelAdjustments] = await Promise.all([
                        fetchRateEntriesForCustomers(db, [...customerIdsFromTasks]),
                        fetchFuelAdjustmentsForCustomers(db, [...customerIdsFromTasks]),
                    ]);
                }

                const missingBillingRows: MissingBillingRow[] = withoutBilling.map(({ tripDoc }) => {
                    const d = tripDoc.data;
                    const taskId = typeof d.taskId === "string" ? d.taskId : undefined;
                    const task = taskId ? taskMap.get(taskId) : undefined;

                    const sourceHub = task?.sourceHub ?? undefined;
                    const destination = task?.destination ?? undefined;
                    const truckType = task?.truckType ?? undefined;
                    const customerId = task?.sourceHubLinkedCustomerId ?? task?.destinationLinkedCustomerId ?? undefined;
                    const customer = customerId ? customerMap.get(customerId) : undefined;

                    const lookupHubId = extractHubId(sourceHub);
                    const lookupDestination = normalizeDestinationCode(destination);
                    const lookupVehicleClass = (truckType ?? "4WJ").trim().toUpperCase();

                    let computedRate: number | undefined;
                    let failureReason: string | undefined;

                    if (!taskId) {
                        failureReason = t("accounting.income.missing.noTaskId");
                    } else if (!task) {
                        failureReason = t("accounting.income.missing.taskNotFound");
                    } else if (!customerId) {
                        failureReason = t("accounting.income.missing.noCustomer");
                    } else {
                        const tripRecord: TripRecord = {
                            deliveredTimestamp: d.deliveredTimestamp,
                            createdAt: d.createdAt,
                        } as TripRecord;
                        const billing = computeTripBilling(tripRecord, task, rateEntries, fuelAdjustments);
                        if (billing) {
                            computedRate = billing.finalRateThb;
                        } else {
                            const matchingRates = rateEntries.filter(
                                (r) =>
                                    r.customerId === customerId &&
                                    r.hubId === lookupHubId &&
                                    r.destinationCode === lookupDestination
                            );
                            if (matchingRates.length === 0) {
                                failureReason = t("accounting.income.missing.noRateEntry", {
                                    hubId: lookupHubId || "—",
                                    destination: lookupDestination || "—",
                                    vehicleClass: lookupVehicleClass,
                                });
                            } else {
                                failureReason = t("accounting.income.missing.vehicleClassMismatch", {
                                    vehicleClass: lookupVehicleClass,
                                });
                            }
                        }
                    }

                    return {
                        id: tripDoc.id,
                        spxTripId: d.spxTripId ? String(d.spxTripId) : undefined,
                        taskId,
                        deliveredTimestamp: toDate(d.deliveredTimestamp),
                        createdAt: toDate(d.createdAt),
                        sourceHub,
                        destination,
                        truckType,
                        customerId,
                        customerName: customer?.name ?? customer?.code ?? customerId,
                        lookupHubId,
                        lookupDestination,
                        lookupVehicleClass,
                        computedRate,
                        failureReason,
                    };
                });

                setMissingRows(missingBillingRows);
            } else {
                setMissingRows([]);
            }
        } finally {
            setLoading(false);
        }
    };

    useEffect(() => {
        void loadData();
    }, []);

    const customerNameById = useMemo(() => {
        const m = new Map<string, string>();
        customers.forEach((c) => m.set(c.id, c.name?.trim() || c.code || c.id));
        return m;
    }, [customers]);

    const filteredRows = useMemo(() => {
        let list = rows;
        if (filterCustomerId !== "all") {
            list = list.filter((r) => r.billingCustomerId === filterCustomerId);
        }
        if (filterDateFrom.trim() && filterDateTo.trim()) {
            const fromRaw = parseLocalDateOnly(filterDateFrom);
            const toRaw = parseLocalDateOnly(filterDateTo);
            if (fromRaw && toRaw) {
                const from = startOfDay(fromRaw);
                const to = endOfDay(toRaw);
                if (!Number.isNaN(from.getTime()) && !Number.isNaN(to.getTime())) {
                    list = list.filter((r) => {
                        const d = r.deliveredTimestamp;
                        if (!d) return false;
                        return isWithinInterval(d, { start: from, end: to });
                    });
                }
            }
        }
        return list;
    }, [rows, filterCustomerId, filterDateFrom, filterDateTo]);

    const totalIncome = useMemo(
        () => filteredRows.reduce((sum, row) => sum + (row.billingEstimateThb ?? 0), 0),
        [filteredRows]
    );
    const avgIncome = filteredRows.length ? totalIncome / filteredRows.length : 0;

    // Pagination
    const totalPages = Math.ceil(filteredRows.length / pageSize);
    const paginatedRows = useMemo(() => {
        const startIdx = (currentPage - 1) * pageSize;
        return filteredRows.slice(startIdx, startIdx + pageSize);
    }, [filteredRows, currentPage, pageSize]);

    // Reset to page 1 when filters change
    useEffect(() => {
        setCurrentPage(1);
    }, [filterCustomerId, filterDateFrom, filterDateTo]);

    // Computable trips (green rows in Missing Billing tab)
    const computableMissingRows = useMemo(
        () => missingRows.filter((r) => r.computedRate != null && !r.failureReason),
        [missingRows]
    );
    const uncomputableMissingRows = useMemo(
        () => missingRows.filter((r) => r.computedRate == null || r.failureReason),
        [missingRows]
    );

    // Filtered missing rows based on status filter
    const filteredMissingRows = useMemo(() => {
        if (missingFilterStatus === "canFix") return computableMissingRows;
        if (missingFilterStatus === "needRateCard") return uncomputableMissingRows;
        return missingRows;
    }, [missingRows, computableMissingRows, uncomputableMissingRows, missingFilterStatus]);

    // Paginated missing rows
    const missingTotalPages = Math.ceil(filteredMissingRows.length / missingPageSize);
    const paginatedMissingRows = useMemo(() => {
        const startIdx = (missingPage - 1) * missingPageSize;
        return filteredMissingRows.slice(startIdx, startIdx + missingPageSize);
    }, [filteredMissingRows, missingPage, missingPageSize]);

    // Reset missing page when filter changes
    useEffect(() => {
        setMissingPage(1);
    }, [missingFilterStatus]);

    // Group missing rows by failure reason for stats
    const missingReasonStats = useMemo(() => {
        const stats = new Map<string, number>();
        missingRows.forEach((r) => {
            const key = r.failureReason || (r.computedRate != null ? "canCompute" : "unknown");
            stats.set(key, (stats.get(key) ?? 0) + 1);
        });
        return stats;
    }, [missingRows]);

    // Customer summary
    const customerSummary = useMemo((): CustomerSummary[] => {
        const summaryMap = new Map<string, CustomerSummary>();
        filteredRows.forEach((row) => {
            const cid = row.billingCustomerId ?? "unknown";
            const existing = summaryMap.get(cid);
            if (existing) {
                existing.tripCount += 1;
                existing.totalIncome += row.billingEstimateThb ?? 0;
            } else {
                summaryMap.set(cid, {
                    customerId: cid,
                    customerName: customerNameById.get(cid) ?? cid,
                    tripCount: 1,
                    totalIncome: row.billingEstimateThb ?? 0,
                });
            }
        });
        return Array.from(summaryMap.values()).sort((a, b) => b.totalIncome - a.totalIncome);
    }, [filteredRows, customerNameById]);

    // Export to Excel
    const exportToExcel = () => {
        const exportData = filteredRows.map((row) => ({
            [t("accounting.income.table.tripId")]: row.spxTripId || row.id,
            [t("accounting.income.table.customerName")]: row.billingCustomerId
                ? customerNameById.get(row.billingCustomerId) ?? row.billingCustomerId
                : "",
            [t("accounting.income.export.originCode")]: row.billingLookupHubId ?? "",
            [t("accounting.income.export.originName")]: row.billingLookupHubId
                ? resolveHubName(row.billingLookupHubId, hubNameMap)
                : "",
            [t("accounting.income.export.destinationCode")]: row.billingLookupDestination ?? "",
            [t("accounting.income.export.destinationName")]: row.billingLookupDestination
                ? getDestinationDisplayName(row.billingLookupDestination, hubNameMap)
                : "",
            [t("accounting.income.table.baseRate")]: row.billingBaseRateThb ?? 0,
            [t("accounting.income.table.ruleMultiplier")]: row.billingRateMultiplier ?? 1,
            [t("accounting.income.table.addPerTrip")]: row.billingAddThbPerTrip ?? 0,
            [t("accounting.income.table.finalRate")]: row.billingEstimateThb ?? 0,
            [t("accounting.income.table.deliveredAt")]: row.deliveredTimestamp
                ? format(row.deliveredTimestamp, "dd/MM/yyyy HH:mm")
                : "",
        }));

        const ws = XLSX.utils.json_to_sheet(exportData);
        const wb = XLSX.utils.book_new();
        XLSX.utils.book_append_sheet(wb, ws, "Income");

        // Add summary sheet
        const summaryData = customerSummary.map((s) => ({
            [t("accounting.income.summary.customer")]: s.customerName,
            [t("accounting.income.stats.tripCount")]: s.tripCount,
            [t("accounting.income.stats.totalIncome")]: s.totalIncome,
        }));
        const wsSummary = XLSX.utils.json_to_sheet(summaryData);
        XLSX.utils.book_append_sheet(wb, wsSummary, "Summary");

        const dateStr = format(new Date(), "yyyyMMdd_HHmmss");
        XLSX.writeFile(wb, `income_report_${dateStr}.xlsx`);
    };

    // Export missing billing to Excel
    const exportMissingToExcel = () => {
        const exportData = filteredMissingRows.map((row) => ({
            [t("accounting.income.table.tripId")]: row.spxTripId || row.id,
            [t("accounting.income.missing.table.customer")]: row.customerName ?? "",
            [t("accounting.income.export.originCode")]: row.lookupHubId ?? "",
            [t("accounting.income.export.originName")]: row.lookupHubId
                ? resolveHubName(row.lookupHubId, hubNameMap)
                : "",
            [t("accounting.income.export.destinationCode")]: row.lookupDestination ?? "",
            [t("accounting.income.export.destinationName")]: row.lookupDestination
                ? getDestinationDisplayName(row.lookupDestination, hubNameMap)
                : "",
            [t("accounting.income.missing.table.vehicleClass")]: row.lookupVehicleClass ?? "",
            [t("accounting.income.table.deliveredAt")]: row.deliveredTimestamp
                ? format(row.deliveredTimestamp, "dd/MM/yyyy HH:mm")
                : "",
            [t("accounting.income.missing.table.computedRate")]: row.computedRate ?? "",
            [t("accounting.income.missing.export.status")]: row.computedRate != null && !row.failureReason
                ? t("accounting.income.backfill.canFix")
                : t("accounting.income.backfill.needRateCard"),
            [t("accounting.income.missing.table.reason")]: row.failureReason ?? (row.computedRate != null ? t("accounting.income.missing.canCompute") : ""),
        }));

        const ws = XLSX.utils.json_to_sheet(exportData);
        const wb = XLSX.utils.book_new();
        XLSX.utils.book_append_sheet(wb, ws, "Missing Billing");

        const dateStr = format(new Date(), "yyyyMMdd_HHmmss");
        XLSX.writeFile(wb, `missing_billing_${dateStr}.xlsx`);
    };

    const setQuickRangeThisMonth = () => {
        const now = new Date();
        const start = new Date(now.getFullYear(), now.getMonth(), 1);
        setFilterDateFrom(format(start, "yyyy-MM-dd"));
        setFilterDateTo(format(now, "yyyy-MM-dd"));
    };

    const setQuickRangeLast30 = () => {
        const now = new Date();
        setFilterDateFrom(format(subDays(now, 29), "yyyy-MM-dd"));
        setFilterDateTo(format(now, "yyyy-MM-dd"));
    };

    const resetDateFilters = () => {
        setFilterDateFrom("");
        setFilterDateTo("");
    };

    const copyFilterDatesToBackfill = () => {
        if (filterDateFrom.trim() && filterDateTo.trim()) {
            setBackfillFrom(filterDateFrom.trim());
            setBackfillTo(filterDateTo.trim());
        }
    };

    const runBackfillSnapshots = async () => {
        setBackfillRunning(true);
        setBackfillError(null);
        setBackfillResult(null);
        try {
            const fn = httpsCallable<
                { fromDateStr: string; toDateStr: string; maxScan?: number; maxWrite?: number },
                BackfillBillingResponse
            >(functions, "backfillTripBillingSnapshots");
            const { data } = await fn({
                fromDateStr: backfillFrom.trim(),
                toDateStr: backfillTo.trim(),
                maxScan: 500,
                maxWrite: 200,
            });
            setBackfillResult(data);
            await loadData();
        } catch (err: unknown) {
            const code =
                err && typeof err === "object" && "code" in err ? String((err as { code: string }).code) : "";
            const msg =
                err && typeof err === "object" && "message" in err
                    ? String((err as { message?: string }).message ?? "")
                    : "";
            const trimmed = msg.trim();
            setBackfillError(
                code === "functions/permission-denied"
                    ? t("accounting.income.backfill.denied")
                    : trimmed || t("accounting.income.backfill.error")
            );
        } finally {
            setBackfillRunning(false);
        }
    };

    const runBackfillForComputable = async () => {
        if (computableMissingRows.length === 0) return;
        setBackfillRunning(true);
        setBackfillError(null);
        setBackfillResult(null);
        
        const fn = httpsCallable<
            { tripId: string },
            { ok: boolean; skipped?: boolean; billingEstimateThb?: number; error?: string }
        >(functions, "computeTripBillingSnapshot");

        let written = 0;
        let skipped = 0;
        let failed = 0;
        const failures: { tripId: string; error?: string }[] = [];

        for (const row of computableMissingRows) {
            try {
                const { data } = await fn({ tripId: row.id });
                if (data.ok && !data.skipped && data.billingEstimateThb != null) {
                    written++;
                } else if (data.skipped) {
                    skipped++;
                } else {
                    failed++;
                    if (failures.length < 25) {
                        failures.push({ tripId: row.id, error: data.error });
                    }
                }
            } catch (err: unknown) {
                failed++;
                const msg = err && typeof err === "object" && "message" in err
                    ? String((err as { message?: string }).message ?? "")
                    : "Unknown error";
                if (failures.length < 25) {
                    failures.push({ tripId: row.id, error: msg });
                }
            }
        }

        setBackfillResult({
            scanned: computableMissingRows.length,
            eligible: computableMissingRows.length,
            written,
            skipped,
            failed,
            failures,
            capped: false,
        });
        setBackfillRunning(false);
        await loadData();
    };

    if (loading) {
        return (
            <div className="flex h-64 items-center justify-center">
                <Loader2 className="h-8 w-8 animate-spin text-primary" />
            </div>
        );
    }

    return (
        <div className="container mx-auto p-6 space-y-6 max-w-[1600px]">
            <div className="flex flex-col md:flex-row md:items-center justify-between gap-4">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">{t("accounting.income.title")}</h1>
                    <p className="text-muted-foreground mt-1">{t("accounting.income.subtitle")}</p>
                </div>
                <div className="flex gap-2">
                    <Button
                        variant="outline"
                        onClick={exportToExcel}
                        disabled={filteredRows.length === 0}
                    >
                        <Download className="h-4 w-4 mr-2" />
                        {t("accounting.income.export")}
                    </Button>
                    <Button variant="outline" onClick={() => {
                        clearHubCache();
                        void loadData();
                    }}>
                        <RefreshCw className="h-4 w-4 mr-2" />
                        {t("common.refresh")}
                    </Button>
                </div>
            </div>

            <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                <Card>
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium">{t("accounting.income.stats.totalIncome")}</CardTitle>
                        <DollarSign className="h-4 w-4 text-muted-foreground" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold">฿{Math.round(totalIncome).toLocaleString()}</div>
                    </CardContent>
                </Card>
                <Card>
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium">{t("accounting.income.stats.tripCount")}</CardTitle>
                        <Hash className="h-4 w-4 text-muted-foreground" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold">{filteredRows.length.toLocaleString()}</div>
                    </CardContent>
                </Card>
                <Card>
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium">{t("accounting.income.stats.avgIncome")}</CardTitle>
                        <TrendingUp className="h-4 w-4 text-muted-foreground" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold">฿{Math.round(avgIncome).toLocaleString()}</div>
                    </CardContent>
                </Card>
            </div>

            {/* Customer Summary */}
            {customerSummary.length > 0 && (
                <Card>
                    <CardHeader className="pb-3">
                        <div className="flex items-center justify-between">
                            <CardTitle className="text-base">{t("accounting.income.summary.title")}</CardTitle>
                            <Button
                                variant="ghost"
                                size="sm"
                                onClick={() => setShowSummary(!showSummary)}
                            >
                                {showSummary ? t("common.hide") : t("common.show")}
                            </Button>
                        </div>
                    </CardHeader>
                    {showSummary && (
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
                                    {customerSummary.map((s) => (
                                        <TableRow key={s.customerId}>
                                            <TableCell className="font-medium">{s.customerName}</TableCell>
                                            <TableCell className="text-right">{s.tripCount.toLocaleString()}</TableCell>
                                            <TableCell className="text-right font-semibold">
                                                ฿{Math.round(s.totalIncome).toLocaleString()}
                                            </TableCell>
                                            <TableCell className="text-right text-muted-foreground">
                                                {totalIncome > 0
                                                    ? `${((s.totalIncome / totalIncome) * 100).toFixed(1)}%`
                                                    : "—"}
                                            </TableCell>
                                        </TableRow>
                                    ))}
                                </TableBody>
                            </Table>
                        </CardContent>
                    )}
                </Card>
            )}

            {isAdmin && (
                <Card>
                    <CardHeader>
                        <CardTitle className="text-base flex items-center gap-2">
                            <Wand2 className="h-4 w-4" />
                            {t("accounting.income.backfill.title")}
                        </CardTitle>
                        <p className="text-sm text-muted-foreground font-normal">
                            {t("accounting.income.backfill.subtitle")}
                        </p>
                    </CardHeader>
                    <CardContent className="space-y-4">
                        {/* Quick stats about missing billing */}
                        {missingRows.length > 0 && (
                            <div className="flex flex-wrap gap-4 p-3 rounded-md bg-muted/50 text-sm">
                                <div>
                                    <span className="text-muted-foreground">{t("accounting.income.backfill.missingTotal")}:</span>{" "}
                                    <span className="font-semibold">{missingRows.length}</span>
                                </div>
                                <div>
                                    <span className="text-green-600 dark:text-green-400">
                                        {t("accounting.income.backfill.canFix")}:
                                    </span>{" "}
                                    <span className="font-semibold text-green-600 dark:text-green-400">
                                        {computableMissingRows.length}
                                    </span>
                                </div>
                                <div>
                                    <span className="text-destructive">
                                        {t("accounting.income.backfill.needRateCard")}:
                                    </span>{" "}
                                    <span className="font-semibold text-destructive">
                                        {uncomputableMissingRows.length}
                                    </span>
                                </div>
                                {computableMissingRows.length > 0 && (
                                    <Button
                                        type="button"
                                        size="sm"
                                        variant="default"
                                        disabled={backfillRunning}
                                        onClick={() => void runBackfillForComputable()}
                                        className="ml-auto"
                                    >
                                        {backfillRunning ? (
                                            <Loader2 className="h-4 w-4 mr-2 animate-spin" />
                                        ) : (
                                            <Wand2 className="h-4 w-4 mr-2" />
                                        )}
                                        {t("accounting.income.backfill.fixComputable", { count: computableMissingRows.length })}
                                    </Button>
                                )}
                            </div>
                        )}

                        <div className="flex flex-wrap gap-4 items-end">
                            <div className="flex flex-col gap-1.5">
                                <Label>{t("accounting.income.backfill.from")}</Label>
                                <Input
                                    type="date"
                                    value={backfillFrom}
                                    onChange={(e) => setBackfillFrom(e.target.value)}
                                />
                            </div>
                            <div className="flex flex-col gap-1.5">
                                <Label>{t("accounting.income.backfill.to")}</Label>
                                <Input
                                    type="date"
                                    value={backfillTo}
                                    onChange={(e) => setBackfillTo(e.target.value)}
                                />
                            </div>
                            <Button
                                type="button"
                                variant="outline"
                                size="sm"
                                onClick={copyFilterDatesToBackfill}
                                disabled={!filterDateFrom.trim() || !filterDateTo.trim()}
                            >
                                {t("accounting.income.backfill.useFilterDates")}
                            </Button>
                            <Button
                                type="button"
                                variant="secondary"
                                disabled={backfillRunning}
                                onClick={() => void runBackfillSnapshots()}
                            >
                                {backfillRunning ? (
                                    <Loader2 className="h-4 w-4 mr-2 animate-spin" />
                                ) : (
                                    <Wand2 className="h-4 w-4 mr-2" />
                                )}
                                {t("accounting.income.backfill.runByDate")}
                            </Button>
                        </div>
                        <p className="text-xs text-muted-foreground">{t("accounting.income.backfill.limitsHint")}</p>
                        {backfillError && <p className="text-sm text-destructive">{backfillError}</p>}
                        {backfillResult && (
                            <div className="rounded-md border bg-muted/30 px-3 py-2 text-sm space-y-1">
                                <p>
                                    {t("accounting.income.backfill.resultSummary", {
                                        scanned: backfillResult.scanned,
                                        eligible: backfillResult.eligible,
                                        written: backfillResult.written,
                                        skipped: backfillResult.skipped,
                                        failed: backfillResult.failed,
                                    })}
                                </p>
                                {backfillResult.capped && (
                                    <p className="text-amber-600 dark:text-amber-500">
                                        {t("accounting.income.backfill.cappedHint")}
                                    </p>
                                )}
                                {backfillResult.failures.length > 0 && (
                                    <ul className="list-disc pl-5 text-xs text-muted-foreground max-h-32 overflow-y-auto">
                                        {backfillResult.failures.map((f) => (
                                            <li key={f.tripId}>
                                                {f.tripId}
                                                {f.error ? ` — ${f.error}` : ""}
                                            </li>
                                        ))}
                                    </ul>
                                )}
                            </div>
                        )}
                    </CardContent>
                </Card>
            )}

            <Card>
                <CardContent className="pt-6 space-y-4">
                    <Tabs value={activeTab} onValueChange={setActiveTab}>
                        <TabsList>
                            <TabsTrigger value="with-billing" className="gap-2">
                                {t("accounting.income.tabs.withBilling")}
                                <Badge variant="secondary" className="ml-1">
                                    {filteredRows.length}
                                </Badge>
                            </TabsTrigger>
                            <TabsTrigger value="missing-billing" className="gap-2">
                                <AlertTriangle className="h-4 w-4 text-amber-500" />
                                {t("accounting.income.tabs.missingBilling")}
                                <Badge variant="destructive" className="ml-1">
                                    {missingRows.length}
                                </Badge>
                            </TabsTrigger>
                        </TabsList>

                        <div className="rounded-lg border p-4 flex flex-wrap gap-4 items-end mt-4">
                            <div className="flex flex-col gap-1.5 min-w-[200px]">
                                <Label>{t("accounting.income.filter.customer")}</Label>
                                <Select value={filterCustomerId} onValueChange={setFilterCustomerId}>
                                    <SelectTrigger>
                                        <SelectValue />
                                    </SelectTrigger>
                                    <SelectContent>
                                        <SelectItem value="all">{t("accounting.filter.all")}</SelectItem>
                                        {customers.map((c) => (
                                            <SelectItem key={c.id} value={c.id}>
                                                {c.code} — {c.name}
                                            </SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>
                            </div>
                            <div className="flex flex-col gap-1.5">
                                <Label>{t("accounting.income.filter.deliveredFrom")}</Label>
                                <Input
                                    type="date"
                                    value={filterDateFrom}
                                    onChange={(e) => setFilterDateFrom(e.target.value)}
                                />
                            </div>
                            <div className="flex flex-col gap-1.5">
                                <Label>{t("accounting.income.filter.deliveredTo")}</Label>
                                <Input
                                    type="date"
                                    value={filterDateTo}
                                    onChange={(e) => setFilterDateTo(e.target.value)}
                                />
                            </div>
                            <div className="flex flex-wrap gap-2">
                                <Button type="button" variant="secondary" size="sm" onClick={setQuickRangeThisMonth}>
                                    {t("accounting.filter.thisMonth")}
                                </Button>
                                <Button type="button" variant="secondary" size="sm" onClick={setQuickRangeLast30}>
                                    {t("accounting.filter.last30Days")}
                                </Button>
                                <Button type="button" variant="outline" size="sm" onClick={resetDateFilters}>
                                    {t("accounting.filter.reset")}
                                </Button>
                            </div>
                        </div>

                        <TabsContent value="with-billing" className="mt-4 space-y-4">
                            <Table>
                                <TableHeader>
                                    <TableRow>
                                        <TableHead>{t("accounting.income.table.tripId")}</TableHead>
                                        <TableHead>{t("accounting.income.table.customerName")}</TableHead>
                                        <TableHead>{t("accounting.income.table.origin")}</TableHead>
                                        <TableHead>{t("accounting.income.table.destination")}</TableHead>
                                        <TableHead className="text-right">{t("accounting.income.table.baseRate")}</TableHead>
                                        <TableHead className="text-right">{t("accounting.income.table.ruleMultiplier")}</TableHead>
                                        <TableHead className="text-right">{t("accounting.income.table.addPerTrip")}</TableHead>
                                        <TableHead className="text-right">{t("accounting.income.table.finalRate")}</TableHead>
                                        <TableHead>{t("accounting.income.table.deliveredAt")}</TableHead>
                                        <TableHead>{t("accounting.income.table.rateImportId")}</TableHead>
                                        <TableHead>{t("accounting.income.table.fuelRuleRef")}</TableHead>
                                    </TableRow>
                                </TableHeader>
                                <TableBody>
                                    {paginatedRows.map((row) => (
                                        <TableRow key={row.id}>
                                            <TableCell className="font-mono text-xs">{row.spxTripId || row.id}</TableCell>
                                            <TableCell>
                                                {row.billingCustomerId
                                                    ? customerNameById.get(row.billingCustomerId) ?? row.billingCustomerId
                                                    : "—"}
                                            </TableCell>
                                            <TableCell className="text-sm">
                                                {row.billingLookupHubId
                                                    ? resolveHubName(row.billingLookupHubId, hubNameMap)
                                                    : "—"}
                                            </TableCell>
                                            <TableCell className="text-sm">
                                                {row.billingLookupDestination
                                                    ? getDestinationDisplayName(row.billingLookupDestination, hubNameMap)
                                                    : "—"}
                                            </TableCell>
                                            <TableCell className="text-right">
                                                {row.billingBaseRateThb != null
                                                    ? `฿${row.billingBaseRateThb.toLocaleString()}`
                                                    : "-"}
                                            </TableCell>
                                            <TableCell className="text-right font-mono text-xs">
                                                {row.billingRateMultiplier != null ? `*${row.billingRateMultiplier.toFixed(2)}` : "—"}
                                            </TableCell>
                                            <TableCell className="text-right text-xs">
                                                {row.billingAddThbPerTrip != null
                                                    ? `฿${row.billingAddThbPerTrip.toLocaleString()}`
                                                    : "—"}
                                            </TableCell>
                                            <TableCell className="text-right font-semibold">
                                                {row.billingEstimateThb != null
                                                    ? `฿${row.billingEstimateThb.toLocaleString()}`
                                                    : "-"}
                                            </TableCell>
                                            <TableCell>
                                                {row.deliveredTimestamp
                                                    ? format(row.deliveredTimestamp, "dd/MM/yyyy HH:mm")
                                                    : "-"}
                                            </TableCell>
                                            <TableCell className="font-mono text-xs">
                                                {row.billingRateImportId || "-"}
                                            </TableCell>
                                            <TableCell className="text-xs max-w-[140px] truncate">
                                                {row.billingFuelAdjustmentId
                                                    ? `${row.billingFuelAdjustmentId.slice(0, 8)}…`
                                                    : "—"}
                                                {row.billingEffectiveFromDateStr
                                                    ? ` · ${row.billingEffectiveFromDateStr}`
                                                    : ""}
                                            </TableCell>
                                        </TableRow>
                                    ))}
                                    {filteredRows.length === 0 && (
                                        <TableRow>
                                            <TableCell colSpan={11} className="h-24 text-center text-muted-foreground">
                                                {t("accounting.income.noRecords")}
                                            </TableCell>
                                        </TableRow>
                                    )}
                                </TableBody>
                            </Table>

                            {/* Pagination */}
                            {filteredRows.length > 0 && (
                                <div className="flex items-center justify-between border-t pt-4">
                                    <div className="flex items-center gap-2 text-sm text-muted-foreground">
                                        <span>{t("accounting.income.pagination.showing")}</span>
                                        <Select
                                            value={String(pageSize)}
                                            onValueChange={(v) => {
                                                setPageSize(Number(v));
                                                setCurrentPage(1);
                                            }}
                                        >
                                            <SelectTrigger className="w-[70px] h-8">
                                                <SelectValue />
                                            </SelectTrigger>
                                            <SelectContent>
                                                <SelectItem value="10">10</SelectItem>
                                                <SelectItem value="25">25</SelectItem>
                                                <SelectItem value="50">50</SelectItem>
                                                <SelectItem value="100">100</SelectItem>
                                            </SelectContent>
                                        </Select>
                                        <span>
                                            {t("accounting.income.pagination.of", {
                                                total: filteredRows.length.toLocaleString(),
                                            })}
                                        </span>
                                    </div>
                                    <div className="flex items-center gap-2">
                                        <span className="text-sm text-muted-foreground">
                                            {t("accounting.income.pagination.page", {
                                                current: currentPage,
                                                total: totalPages,
                                            })}
                                        </span>
                                        <Button
                                            variant="outline"
                                            size="icon"
                                            className="h-8 w-8"
                                            onClick={() => setCurrentPage((p) => Math.max(1, p - 1))}
                                            disabled={currentPage <= 1}
                                        >
                                            <ChevronLeft className="h-4 w-4" />
                                        </Button>
                                        <Button
                                            variant="outline"
                                            size="icon"
                                            className="h-8 w-8"
                                            onClick={() => setCurrentPage((p) => Math.min(totalPages, p + 1))}
                                            disabled={currentPage >= totalPages}
                                        >
                                            <ChevronRight className="h-4 w-4" />
                                        </Button>
                                    </div>
                                </div>
                            )}
                        </TabsContent>

                        <TabsContent value="missing-billing" className="mt-4 space-y-4">
                            {/* Info banner */}
                            <div className="rounded-md border-l-4 border-amber-500 bg-amber-500/10 p-3">
                                <p className="text-sm text-amber-700 dark:text-amber-400">
                                    {t("accounting.income.missing.description")}
                                </p>
                            </div>

                            {/* Filters and actions */}
                            <div className="flex flex-wrap items-center gap-4">
                                <div className="flex items-center gap-2">
                                    <Label className="text-sm">{t("accounting.income.missing.filterStatus")}:</Label>
                                    <Select
                                        value={missingFilterStatus}
                                        onValueChange={(v) => setMissingFilterStatus(v as "all" | "canFix" | "needRateCard")}
                                    >
                                        <SelectTrigger className="w-[180px] h-8">
                                            <SelectValue />
                                        </SelectTrigger>
                                        <SelectContent>
                                            <SelectItem value="all">
                                                {t("accounting.filter.all")} ({missingRows.length})
                                            </SelectItem>
                                            <SelectItem value="canFix">
                                                <span className="text-green-600 dark:text-green-400">
                                                    {t("accounting.income.backfill.canFix")} ({computableMissingRows.length})
                                                </span>
                                            </SelectItem>
                                            <SelectItem value="needRateCard">
                                                <span className="text-destructive">
                                                    {t("accounting.income.backfill.needRateCard")} ({uncomputableMissingRows.length})
                                                </span>
                                            </SelectItem>
                                        </SelectContent>
                                    </Select>
                                </div>
                                <Button
                                    variant="outline"
                                    size="sm"
                                    onClick={exportMissingToExcel}
                                    disabled={filteredMissingRows.length === 0}
                                >
                                    <Download className="h-4 w-4 mr-2" />
                                    {t("accounting.income.export")}
                                </Button>
                            </div>

                            {/* Stats summary */}
                            {missingRows.length > 0 && (
                                <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
                                    <div className="rounded-md border p-3">
                                        <div className="text-xs text-muted-foreground">{t("accounting.income.backfill.missingTotal")}</div>
                                        <div className="text-lg font-semibold">{missingRows.length}</div>
                                    </div>
                                    <div className="rounded-md border p-3 border-green-500/30 bg-green-500/5">
                                        <div className="text-xs text-green-600 dark:text-green-400">{t("accounting.income.backfill.canFix")}</div>
                                        <div className="text-lg font-semibold text-green-600 dark:text-green-400">{computableMissingRows.length}</div>
                                    </div>
                                    <div className="rounded-md border p-3 border-destructive/30 bg-destructive/5">
                                        <div className="text-xs text-destructive">{t("accounting.income.backfill.needRateCard")}</div>
                                        <div className="text-lg font-semibold text-destructive">{uncomputableMissingRows.length}</div>
                                    </div>
                                    <div className="rounded-md border p-3">
                                        <div className="text-xs text-muted-foreground">{t("accounting.income.missing.estimatedValue")}</div>
                                        <div className="text-lg font-semibold">
                                            ฿{computableMissingRows.reduce((sum, r) => sum + (r.computedRate ?? 0), 0).toLocaleString()}
                                        </div>
                                    </div>
                                </div>
                            )}

                            {/* Table */}
                            <Table>
                                <TableHeader>
                                    <TableRow>
                                        <TableHead>{t("accounting.income.table.tripId")}</TableHead>
                                        <TableHead>{t("accounting.income.missing.table.customer")}</TableHead>
                                        <TableHead>{t("accounting.income.missing.table.lookupHub")}</TableHead>
                                        <TableHead>{t("accounting.income.missing.table.lookupDestination")}</TableHead>
                                        <TableHead>{t("accounting.income.missing.table.vehicleClass")}</TableHead>
                                        <TableHead>{t("accounting.income.table.deliveredAt")}</TableHead>
                                        <TableHead>{t("accounting.income.missing.table.computedRate")}</TableHead>
                                        <TableHead>{t("accounting.income.missing.table.reason")}</TableHead>
                                    </TableRow>
                                </TableHeader>
                                <TableBody>
                                    {paginatedMissingRows.map((row) => (
                                        <TableRow key={row.id}>
                                            <TableCell className="font-mono text-xs">{row.spxTripId || row.id}</TableCell>
                                            <TableCell className="text-sm">{row.customerName ?? "—"}</TableCell>
                                            <TableCell className="text-sm">
                                                {row.lookupHubId
                                                    ? resolveHubName(row.lookupHubId, hubNameMap)
                                                    : "—"}
                                            </TableCell>
                                            <TableCell className="text-sm">
                                                {row.lookupDestination
                                                    ? getDestinationDisplayName(row.lookupDestination, hubNameMap)
                                                    : "—"}
                                            </TableCell>
                                            <TableCell className="font-mono text-xs">{row.lookupVehicleClass || "—"}</TableCell>
                                            <TableCell>
                                                {row.deliveredTimestamp
                                                    ? format(row.deliveredTimestamp, "dd/MM/yyyy HH:mm")
                                                    : "-"}
                                            </TableCell>
                                            <TableCell className="text-right font-semibold">
                                                {row.computedRate != null ? (
                                                    <span className="text-green-600 dark:text-green-400">
                                                        ฿{row.computedRate.toLocaleString()}
                                                    </span>
                                                ) : (
                                                    "—"
                                                )}
                                            </TableCell>
                                            <TableCell className="text-xs max-w-[250px]">
                                                {row.failureReason ? (
                                                    <span className="text-destructive">{row.failureReason}</span>
                                                ) : row.computedRate != null ? (
                                                    <span className="text-green-600 dark:text-green-400">
                                                        {t("accounting.income.missing.canCompute")}
                                                    </span>
                                                ) : (
                                                    "—"
                                                )}
                                            </TableCell>
                                        </TableRow>
                                    ))}
                                    {filteredMissingRows.length === 0 && (
                                        <TableRow>
                                            <TableCell colSpan={8} className="h-24 text-center text-muted-foreground">
                                                {t("accounting.income.missing.noMissing")}
                                            </TableCell>
                                        </TableRow>
                                    )}
                                </TableBody>
                            </Table>

                            {/* Pagination */}
                            {filteredMissingRows.length > 0 && (
                                <div className="flex items-center justify-between border-t pt-4">
                                    <div className="flex items-center gap-2 text-sm text-muted-foreground">
                                        <span>{t("accounting.income.pagination.showing")}</span>
                                        <Select
                                            value={String(missingPageSize)}
                                            onValueChange={(v) => {
                                                setMissingPageSize(Number(v));
                                                setMissingPage(1);
                                            }}
                                        >
                                            <SelectTrigger className="w-[70px] h-8">
                                                <SelectValue />
                                            </SelectTrigger>
                                            <SelectContent>
                                                <SelectItem value="10">10</SelectItem>
                                                <SelectItem value="25">25</SelectItem>
                                                <SelectItem value="50">50</SelectItem>
                                                <SelectItem value="100">100</SelectItem>
                                            </SelectContent>
                                        </Select>
                                        <span>
                                            {t("accounting.income.pagination.of", {
                                                total: filteredMissingRows.length.toLocaleString(),
                                            })}
                                        </span>
                                    </div>
                                    <div className="flex items-center gap-2">
                                        <span className="text-sm text-muted-foreground">
                                            {t("accounting.income.pagination.page", {
                                                current: missingPage,
                                                total: missingTotalPages,
                                            })}
                                        </span>
                                        <Button
                                            variant="outline"
                                            size="icon"
                                            className="h-8 w-8"
                                            onClick={() => setMissingPage((p) => Math.max(1, p - 1))}
                                            disabled={missingPage <= 1}
                                        >
                                            <ChevronLeft className="h-4 w-4" />
                                        </Button>
                                        <Button
                                            variant="outline"
                                            size="icon"
                                            className="h-8 w-8"
                                            onClick={() => setMissingPage((p) => Math.min(missingTotalPages, p + 1))}
                                            disabled={missingPage >= missingTotalPages}
                                        >
                                            <ChevronRight className="h-4 w-4" />
                                        </Button>
                                    </div>
                                </div>
                            )}
                        </TabsContent>
                    </Tabs>
                </CardContent>
            </Card>
        </div>
    );
}
