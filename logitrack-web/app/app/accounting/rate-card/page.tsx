"use client";

import { useEffect, useMemo, useState } from "react";
import { format, isValid, parseISO } from "date-fns";
import { enUS, th as thDateLocale } from "date-fns/locale";
import * as XLSX from "xlsx";
import {
    Ban,
    Calculator,
    Copy,
    Download,
    Loader2,
    MoreHorizontal,
    Pencil,
    Plus,
    RefreshCw,
    Trash2,
    Upload,
} from "lucide-react";
import { useLanguage } from "@/context/language";
import { useAuth } from "@/context/auth";
import { usePermission } from "@/hooks/usePermission";
import { CAPABILITIES } from "@/lib/capabilities";
import { getCustomers } from "@/features/customers/api/customers";
import type { Customer } from "@/validate/customerSchema";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { DateOnlyRangePicker } from "@/components/ui/date-range-picker";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Combobox, type ComboboxOption } from "@/components/ui/combobox";
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
    createCustomerFuelRateAdjustment,
    createCustomerRateEntry,
    getCustomerFuelRateAdjustments,
    getCustomerRateEntries,
    getFuelMonthlySnapshots,
    getFuelDailySnapshot,
    voidCustomerFuelRateAdjustment,
    voidCustomerRateEntry,
    createCustomerServiceFee,
    deleteCustomerServiceFee,
    getCustomerServiceFees,
    updateCustomerServiceFee,
    normalizeRateEntryVehicleClasses,
    getStandbyRateEntries,
    createStandbyRateEntry,
    updateStandbyRateEntry,
    deleteStandbyRateEntry,
    type CustomerFuelRateAdjustmentRow,
    type CustomerRateEntryRow,
    type FuelMonthlySnapshotRow,
    type CustomerServiceFeeRow,
    type ServiceFeeType,
    type ServiceFeeUnit,
    type NormalizeVehicleClassResponse,
    type StandbyRateEntryRow,
} from "../actions.client";
import { RateCardImportDialog, type RateCardCustomerOption } from "@/features/accounting";
import { db, functions } from "@/firebase/client";
import { httpsCallable } from "firebase/functions";
import { collection, getDocs } from "firebase/firestore";
import { COLLECTIONS } from "@/lib/collections";
import {
    latestFleetDieselFromMonthlySnapshots,
    pickFleetBangchakDieselReference,
} from "@/lib/bangchakFleetReferenceFuel";
import { SOC_DESTINATIONS, TASK_TRUCK_TYPE_ENUM } from "@/validate/taskSchema";
import {
    computeFinalRateThb,
    computeFuelSurchargeThb,
    fuelBandRange,
    normalizeVehicleClass,
    selectFuelAdjustmentForBillingDate,
    type FuelRateAdjustment,
} from "@/lib/billingCompute";

interface HubOption {
    id: string;
    name?: string;
}

function parseDateInput(date: string): Date | null {
    if (!date) return null;
    const d = new Date(`${date}T00:00:00`);
    return Number.isNaN(d.getTime()) ? null : d;
}

function multiplierToPercentText(multiplier: number): string {
    const percent = (multiplier - 1) * 100;
    return `${percent >= 0 ? "+" : ""}${percent.toFixed(2)}%`;
}

function formatMultiplierTimes(multiplier: number): string {
    return `*${multiplier.toFixed(2)}`;
}

function fuelRowsToAdjustments(rows: CustomerFuelRateAdjustmentRow[]): FuelRateAdjustment[] {
    return rows.map((r) => ({
        id: r.id,
        customerId: r.customerId,
        effectiveFromMs: r.effectiveFrom.getTime(),
        rateMultiplier: r.rateMultiplier,
        addThbPerTrip: r.addThbPerTrip ?? 0,
    }));
}

function formatSignedThbDelta(base: number, finalRate: number): string {
    const d = Math.round((finalRate - base) * 100) / 100;
    if (d === 0) return "±฿0";
    return d > 0 ? `+฿${d.toLocaleString()}` : `-฿${Math.abs(d).toLocaleString()}`;
}

function parseMonthKeyDate(monthKey: string): Date | null {
    const m = /^(\d{4})-(\d{2})$/.exec(monthKey.trim());
    if (!m) return null;
    const y = Number(m[1]);
    const mo = Number(m[2]);
    if (!Number.isFinite(y) || mo < 1 || mo > 12) return null;
    return new Date(y, mo - 1, 1, 12, 0, 0, 0);
}

function formatFuelMonthPeriod(monthKey: string, language: "en" | "th"): { label: string; key: string } {
    const d = parseMonthKeyDate(monthKey);
    if (!d) return { label: monthKey, key: monthKey };
    const locale = language === "th" ? thDateLocale : enUS;
    return {
        label: format(d, "MMMM yyyy", { locale }),
        key: monthKey,
    };
}

function formatDieselRetailLine(row: FuelMonthlySnapshotRow): string {
    if (row.status === "error") return row.errorMessage?.trim() || "";
    const diesel = pickFleetBangchakDieselReference(row.items);
    if (diesel) {
        const u = diesel.unit?.trim() || "L";
        const nm = diesel.nameTh?.trim() || diesel.nameEn?.trim() || "";
        const pricePart = `฿${diesel.price.toLocaleString()} / ${u}`;
        return nm ? `${nm} · ${pricePart}` : pricePart;
    }
    const first = row.items[0];
    if (first) {
        const u = first.unit?.trim() || "L";
        const name = first.nameTh?.trim() || first.nameEn?.trim() || "";
        return name ? `${name} · ฿${first.price.toLocaleString()} / ${u}` : `฿${first.price.toLocaleString()} / ${u}`;
    }
    return "—";
}

function fuelSnapshotSourceLabel(source: string, t: (key: string) => string): string {
    const s = source.trim().toLowerCase();
    if (!s) return "—";
    if (s === "bangchak_api" || s.includes("bangchak")) {
        return t("accounting.rateCard.fuelMonthly.source.bangchak");
    }
    return source.trim();
}

/**
 * Legacy rate entries stored the truck master's full name ("6 Wheels", "4 WHEELS JUMBO"); tasks carry
 * the class code ("6WH", "4WJ") and billing matches on that. Use the same normalizer billing uses so
 * the table label matches the dropdown/filter values exactly; unknown legacy codes pass through
 * uppercased rather than rendering blank.
 */
function displayVehicleTypeCode(storedClass: string): string {
    return normalizeVehicleClass(storedClass);
}

/** Per-class badge colors so the Vehicle column is scannable; light + dark variants. */
function vehicleClassBadgeClasses(code: string): string {
    switch (code) {
        case "4W":
            return "bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-300";
        case "4WJ":
            return "bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-300";
        case "6WH":
            return "bg-orange-100 text-orange-800 dark:bg-orange-900/30 dark:text-orange-300";
        case "10WH":
            return "bg-purple-100 text-purple-800 dark:bg-purple-900/30 dark:text-purple-300";
        case "18WH":
            return "bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-300";
        case "VAN":
            return "bg-teal-100 text-teal-800 dark:bg-teal-900/30 dark:text-teal-300";
        default:
            return "bg-muted text-muted-foreground";
    }
}

/** Order dropdowns by the task enum (4W → VAN); unknown legacy codes fall to the end, then alpha. */
const VEHICLE_CLASS_ORDER: Record<string, number> = Object.fromEntries(
    TASK_TRUCK_TYPE_ENUM.map((code, i) => [code, i])
);
function compareVehicleClass(a: string, b: string): number {
    const ia = VEHICLE_CLASS_ORDER[a] ?? Number.MAX_SAFE_INTEGER;
    const ib = VEHICLE_CLASS_ORDER[b] ?? Number.MAX_SAFE_INTEGER;
    return ia !== ib ? ia - ib : a.localeCompare(b);
}

/**
 * Combined result of a "recompute billing" run. One button now drives two backfills — trips and
 * standby — because both land on the same invoice (ADR 0008 §5).
 */
interface RecomputeStats {
    scanned: number;
    eligible?: number;
    written: number;
    skipped: number;
    failed: number;
    /** Rows left alone because their period already carries a sent/paid invoice. */
    blocked?: number;
    blockedInvoices?: string[];
    /** Of `written`, how many were standby — so the admin can see standby was actually covered. */
    standbyWritten?: number;
    failures?: { tripId: string; error?: string }[];
}

export default function AccountingRateCardPage() {
    const { t, language } = useLanguage();
    const dateLocale = language === "th" ? thDateLocale : enUS;
    const { hasPermission: canEdit } = usePermission(CAPABILITIES.accounting_edit_rate_card);
    // Recorded as `voidedBy` so a retired announcement names who retired it.
    const currentUser = useAuth()?.currentUser ?? null;
    const [loading, setLoading] = useState(true);
    const [importOpen, setImportOpen] = useState(false);
    const [customers, setCustomers] = useState<RateCardCustomerOption[]>([]);
    const [hubs, setHubs] = useState<HubOption[]>([]);
    const [entries, setEntries] = useState<CustomerRateEntryRow[]>([]);
    const [fuelAdjustments, setFuelAdjustments] = useState<CustomerFuelRateAdjustmentRow[]>([]);
    const [fuelSnapshots, setFuelMonthlySnapshots] = useState<FuelMonthlySnapshotRow[]>([]);
    const [syncingFuelSnapshot, setSyncingFuelSnapshot] = useState(false);
    const [fuelMonthlySyncMessage, setFuelMonthlySyncMessage] = useState<string | null>(null);
    const [fuelMonthlySyncIsError, setFuelMonthlySyncIsError] = useState(false);
    const [fuelAdjustmentDialogOpen, setFuelAdjustmentDialogOpen] = useState(false);
    const [filterCustomerId, setFilterCustomerId] = useState("all");
    const [filterSourceHubId, setFilterSourceHubId] = useState("all");
    const [filterDestinationCode, setFilterDestinationCode] = useState("all");
    const [filterVehicleClass, setFilterVehicleClass] = useState("all");
    const [filterSearch, setFilterSearch] = useState("");
    const [entriesPage, setEntriesPage] = useState(1);
    const [entriesPerPage, setEntriesPerPage] = useState(15);
    const [showRateCalPreview, setShowRateCalPreview] = useState(false);
    const [rateCalAsOfStr, setRateCalAsOfStr] = useState(() => format(new Date(), "yyyy-MM-dd"));
    const [adjustmentError, setAdjustmentError] = useState<string | null>(null);
    const [savingAdjustment, setSavingAdjustment] = useState(false);
    // Voiding an announcement (ADR 0009 §1) — the only way to retire a round. There is no edit
    // path: a correction is announced as a new row with its own effective date.
    const [voidTarget, setVoidTarget] = useState<{
        kind: "entry" | "adjustment";
        id: string;
        label: string;
    } | null>(null);
    const [voidReason, setVoidReason] = useState("");
    const [voidSaving, setVoidSaving] = useState(false);
    const [voidError, setVoidError] = useState<string | null>(null);
    const [manualRateError, setManualRateError] = useState<string | null>(null);
    const [savingManualRate, setSavingManualRate] = useState(false);
    const [manualRateForm, setManualRateForm] = useState<{
        customerId: string;
        hubId: string;
        destinationCode: string;
        vehicleClass: string;
        rateThb: string;
        distanceKm: string;
        effectiveFrom: string;
        jobCategory: "PRIMARY" | "SUPPLEMENTARY";
    }>({
        customerId: "",
        hubId: "",
        destinationCode: "",
        vehicleClass: "4WJ",
        rateThb: "",
        distanceKm: "",
        effectiveFrom: "",
        jobCategory: "PRIMARY",
    });
    const [adjustmentForm, setAdjustmentForm] = useState({
        adjustmentType: "percent" as "percent" | "fixed_thb",
        customerId: "",
        effectiveFrom: "",
        ratePercent: "0",
        addThbPerTrip: "0",
        referenceFuelPrice: "",
        announcementNote: "",
        fuelBandEnabled: false,
        fuelBandBaselineFuelFloor: "41",
        fuelBandThbPerBaht: "10",
    });
    // Recompute billing state
    const [recomputeDialogOpen, setRecomputeDialogOpen] = useState(false);
    const [recomputeFromDate, setRecomputeFromDate] = useState(() => format(new Date(new Date().setDate(1)), "yyyy-MM-dd"));
    const [recomputeToDate, setRecomputeToDate] = useState(() => format(new Date(), "yyyy-MM-dd"));
    const [recomputeCustomerId, setRecomputeCustomerId] = useState("all");
    const [recomputeLoading, setRecomputeLoading] = useState(false);
    const [recomputeResult, setRecomputeResult] = useState<RecomputeStats | null>(null);
    const [recomputeError, setRecomputeError] = useState<string | null>(null);

    // Fuel price for the announcement's own effective date (ADR 0009 §5)
    const [dailyFuelPrice, setDailyFuelPrice] = useState<number | null>(null);
    const [dailyFuelState, setDailyFuelState] = useState<"idle" | "loading" | "found" | "missing">("idle");
    // Service Fees state
    const [serviceFees, setServiceFees] = useState<CustomerServiceFeeRow[]>([]);
    const [serviceFeeDialogOpen, setServiceFeeDialogOpen] = useState(false);
    const [serviceFeeForm, setServiceFeeForm] = useState({
        customerId: "",
        feeType: "extra_stop" as ServiceFeeType,
        customTypeName: "",
        amountThb: "",
        unit: "per_stop" as ServiceFeeUnit,
        note: "",
    });
    const [editingServiceFeeId, setEditingServiceFeeId] = useState<string | null>(null);
    const [savingServiceFee, setSavingServiceFee] = useState(false);
    const [deletingServiceFeeId, setDeletingServiceFeeId] = useState<string | null>(null);
    const [serviceFeeError, setServiceFeeError] = useState<string | null>(null);
    // Standby Rates state
    const [standbyRates, setStandbyRates] = useState<StandbyRateEntryRow[]>([]);
    const [standbyRateDialogOpen, setStandbyRateDialogOpen] = useState(false);
    const [editingStandbyRateId, setEditingStandbyRateId] = useState<string | null>(null);
    const [standbyRateForm, setStandbyRateForm] = useState({
        customerId: "",
        rateThb: "",
        effectiveFrom: format(new Date(), "yyyy-MM-dd"),
        note: "",
    });
    const [savingStandbyRate, setSavingStandbyRate] = useState(false);
    const [deletingStandbyRateId, setDeletingStandbyRateId] = useState<string | null>(null);
    const [standbyRateError, setStandbyRateError] = useState<string | null>(null);
    // Vehicle class normalization state
    const [normalizeInProgress, setNormalizeInProgress] = useState(false);
    const [normalizeResult, setNormalizeResult] = useState<NormalizeVehicleClassResponse | null>(null);
    const [normalizeError, setNormalizeError] = useState<string | null>(null);

    const customerNameById = useMemo(() => {
        const map = new Map<string, string>();
        customers.forEach((c) => map.set(c.id, `${c.code} - ${c.name}`));
        return map;
    }, [customers]);
    const customerOnlyNameById = useMemo(() => {
        const map = new Map<string, string>();
        customers.forEach((c) => map.set(c.id, c.name));
        return map;
    }, [customers]);
    const sourceNameByHubId = useMemo(() => {
        const map = new Map<string, string>();
        entries.forEach((entry) => {
            if (!map.has(entry.hubId) && entry.rawHubName?.trim()) {
                map.set(entry.hubId, entry.rawHubName.trim());
            }
        });
        return map;
    }, [entries]);
    const hubNameById = useMemo(() => {
        const map = new Map<string, string>();
        hubs.forEach((hub) => {
            if (hub.name?.trim()) {
                map.set(hub.id, hub.name.trim());
            }
        });
        return map;
    }, [hubs]);
    const formatSource = (hubId: string) => sourceNameByHubId.get(hubId) ?? hubNameById.get(hubId) ?? hubId;
    /** Same ordering/hints as origin + SOC labels for legacy destination-only codes (imports). */
    const formatDestination = (destinationCode: string) =>
        (SOC_DESTINATIONS as Record<string, string>)[destinationCode] ?? formatSource(destinationCode);

    const loadData = async () => {
        setLoading(true);
        try {
            const [customerRows, entryRows, fuelRows, snapshotRows, hubRows, serviceFeeRows, standbyRateRows] = await Promise.all([
                getCustomers(),
                getCustomerRateEntries(),
                getCustomerFuelRateAdjustments(),
                getFuelMonthlySnapshots(36),
                getDocs(collection(db, COLLECTIONS.HUBS)),
                getCustomerServiceFees(),
                getStandbyRateEntries(),
            ]);
            const mappedCustomers: RateCardCustomerOption[] = (customerRows as (Customer & { id: string })[]).map((c) => ({
                id: c.id,
                code: c.code,
                name: c.name,
            }));
            setCustomers(mappedCustomers);
            setEntries(entryRows);
            setFuelAdjustments(fuelRows);
            setFuelMonthlySnapshots(snapshotRows);
            setServiceFees(serviceFeeRows);
            setStandbyRates(standbyRateRows);
            setHubs(
                hubRows.docs.map((d) => {
                    const data = d.data();
                    const id = String(data.hubId ?? data.source_id ?? "").trim().toUpperCase();
                    const name = String(data.source_name_en ?? data.source_name_th ?? data.hubName ?? "").trim();
                    return { id, name: name || undefined };
                }).filter((x) => x.id)
            );
        } finally {
            setLoading(false);
        }
    };

    useEffect(() => {
        void loadData();
    }, []);

    /**
     * Load the diesel price of the announcement's **own** effective date (ADR 0009 §5).
     *
     * The monthly snapshot cannot answer this — it is overwritten on every sync, so entering a
     * round on the 20th that takes effect on the 16th would read the 20th's price and land in the
     * wrong band. When the day was never captured the field is left empty and the UI says so;
     * silently substituting another day's price is what this replaces.
     */
    useEffect(() => {
        const dateStr = adjustmentForm.effectiveFrom;
        if (!dateStr || !fuelAdjustmentDialogOpen) {
            setDailyFuelState("idle");
            setDailyFuelPrice(null);
            return;
        }
        let cancelled = false;
        setDailyFuelState("loading");
        void (async () => {
            try {
                const snap = await getFuelDailySnapshot(dateStr);
                if (cancelled) return;
                const row = snap ? pickFleetBangchakDieselReference(snap.items) : undefined;
                const price = row != null && Number.isFinite(row.price) ? row.price : null;
                if (price == null) {
                    setDailyFuelPrice(null);
                    setDailyFuelState("missing");
                    return;
                }
                setDailyFuelPrice(price);
                setDailyFuelState("found");
                setAdjustmentForm((p) => ({ ...p, referenceFuelPrice: String(price) }));
            } catch (err) {
                console.error(err);
                if (!cancelled) {
                    setDailyFuelPrice(null);
                    setDailyFuelState("missing");
                }
            }
        })();
        return () => {
            cancelled = true;
        };
    }, [adjustmentForm.effectiveFrom, fuelAdjustmentDialogOpen]);

    const filteredEntries = useMemo(() => {
        let list = entries;
        if (filterCustomerId !== "all") {
            list = list.filter((e) => e.customerId === filterCustomerId);
        }
        if (filterSourceHubId !== "all") {
            list = list.filter((e) => e.hubId === filterSourceHubId);
        }
        if (filterDestinationCode !== "all") {
            list = list.filter((e) => e.destinationCode === filterDestinationCode);
        }
        if (filterVehicleClass !== "all") {
            list = list.filter((e) => normalizeVehicleClass(e.vehicleClass) === filterVehicleClass);
        }
        if (filterSearch.trim()) {
            const q = filterSearch.trim().toLowerCase();
            list = list.filter(
                (e) =>
                    e.hubId.toLowerCase().includes(q) ||
                    e.destinationCode.toLowerCase().includes(q) ||
                    e.rawHubName.toLowerCase().includes(q) ||
                    e.importId.toLowerCase().includes(q)
            );
        }
        return list;
    }, [entries, filterCustomerId, filterSourceHubId, filterDestinationCode, filterVehicleClass, filterSearch]);
    /** Single option list for origin + manual/filter destination so hubs need not be split into two workflows. */
    const rateCardLocationOptions = useMemo(() => {
        const fromHubs = hubs.map((h) => h.id);
        const fromEntries = entries.map((e) => e.hubId);
        return Array.from(new Set([...fromHubs, ...fromEntries])).sort((a, b) => a.localeCompare(b));
    }, [hubs, entries]);
    /** When a customer is chosen: show all locations (master list) + customer's existing locations. */
    const manualLocationOptions = useMemo(() => {
        const cid = manualRateForm.customerId.trim();
        if (!cid) return rateCardLocationOptions;
        // Always include full master list + customer-specific ones
        const merged = new Set<string>([...rateCardLocationOptions]);
        for (const e of entries) {
            if (e.customerId !== cid) continue;
            merged.add(e.hubId);
            merged.add(e.destinationCode);
        }
        return Array.from(merged).sort((a, b) => a.localeCompare(b));
    }, [manualRateForm.customerId, entries, rateCardLocationOptions]);
    const filterLocationOptions = useMemo(() => {
        if (filterCustomerId === "all") return rateCardLocationOptions;
        // Always include full master list + customer-specific ones
        const merged = new Set<string>([...rateCardLocationOptions]);
        for (const e of entries) {
            if (e.customerId !== filterCustomerId) continue;
            merged.add(e.hubId);
            merged.add(e.destinationCode);
        }
        return Array.from(merged).sort((a, b) => a.localeCompare(b));
    }, [filterCustomerId, entries, rateCardLocationOptions]);
    const manualLocationComboboxOptions = useMemo<ComboboxOption[]>(
        () => manualLocationOptions.map((code) => ({ value: code, label: formatSource(code) })),
        [manualLocationOptions, t]
    );
    const filterLocationComboboxOptions = useMemo<ComboboxOption[]>(
        () => [
            { value: "all", label: t("accounting.filter.all") },
            ...filterLocationOptions.map((code) => ({ value: code, label: formatSource(code) })),
        ],
        [filterLocationOptions, t]
    );
    const vehicleOptions = useMemo(
        () => Array.from(new Set(entries.map((e) => normalizeVehicleClass(e.vehicleClass)))).sort(compareVehicleClass),
        [entries]
    );
    /**
     * The classes a rate card may be written against are the ones a task can carry
     * (TASK_TRUCK_TYPE_ENUM) — billing matches tasks.truckType to entry.vehicleClass, so offering the
     * truck master's names here ("Pickup", "6 Wheels") produced rate cards no task could ever match.
     * Classes already present in existing entries are kept so legacy rows stay selectable.
     */
    const manualVehicleClassOptions = useMemo(() => {
        const options = new Set<string>(TASK_TRUCK_TYPE_ENUM);
        vehicleOptions.forEach((v) => options.add(v));
        return Array.from(options).sort(compareVehicleClass);
    }, [vehicleOptions]);
    const entriesTotalPages = Math.max(1, Math.ceil(filteredEntries.length / entriesPerPage));
    const paginatedEntries = useMemo(() => {
        const start = (entriesPage - 1) * entriesPerPage;
        return filteredEntries.slice(start, start + entriesPerPage);
    }, [filteredEntries, entriesPage, entriesPerPage]);

    const entriesRangeStart = filteredEntries.length === 0 ? 0 : (entriesPage - 1) * entriesPerPage + 1;
    const entriesRangeEnd = Math.min(entriesPage * entriesPerPage, filteredEntries.length);

    const filteredAdjustments = useMemo(() => {
        if (filterCustomerId === "all") return fuelAdjustments;
        return fuelAdjustments.filter((r) => r.customerId === filterCustomerId);
    }, [fuelAdjustments, filterCustomerId]);

    const adjustmentsByCustomer = useMemo(() => {
        const m = new Map<string, CustomerFuelRateAdjustmentRow[]>();
        for (const a of fuelAdjustments) {
            const id = a.customerId.trim();
            if (!id) continue;
            const list = m.get(id) ?? [];
            list.push(a);
            m.set(id, list);
        }
        for (const list of m.values()) {
            list.sort((a, b) => b.effectiveFrom.getTime() - a.effectiveFrom.getTime());
        }
        return m;
    }, [fuelAdjustments]);

    const latestSyncedFleetFuel = useMemo(
        () => latestFleetDieselFromMonthlySnapshots(fuelSnapshots),
        [fuelSnapshots]
    );

    /**
     * Fuel rounds of the selected customer as ordered **half-open** intervals (ADR 0009 §2, §8):
     * a round runs `[effectiveFrom, nextEffectiveFrom)`, so no day belongs to two rounds and none
     * to zero. Voided rounds stay listed — they are part of the history — but are not treated as
     * boundaries, because pricing skips them.
     */
    const rateRounds = useMemo(() => {
        if (filterCustomerId === "all") return [];
        const rows = fuelAdjustments
            .filter((r) => r.customerId === filterCustomerId)
            .sort((a, b) => a.effectiveFrom.getTime() - b.effectiveFrom.getTime());
        const liveDates = rows.filter((r) => !r.voided).map((r) => r.effectiveFrom.getTime());
        return rows.map((r, i) => {
            const nextLive = liveDates.find((ms) => ms > r.effectiveFrom.getTime());
            const band =
                r.referenceFuelPriceThbPerLitre != null
                    ? fuelBandRange(r.referenceFuelPriceThbPerLitre)
                    : null;
            const add = r.addThbPerTrip ?? 0;
            const pct = (r.rateMultiplier - 1) * 100;
            return {
                id: r.id,
                label: `R${i + 1}`,
                voided: r.voided === true,
                fromStr: r.effectiveFromDateStr ?? format(r.effectiveFrom, "yyyy-MM-dd"),
                untilStr:
                    nextLive != null
                        ? format(new Date(nextLive), "yyyy-MM-dd")
                        : t("accounting.rateCard.rounds.openEnded"),
                bandLabel: band
                    ? `${band.lowerThb.toFixed(2)}–${band.upperThb.toFixed(2)}`
                    : "-",
                adjustmentLabel:
                    add !== 0
                        ? `${add > 0 ? "+" : ""}${add.toLocaleString()} ฿/เที่ยว`
                        : `${pct >= 0 ? "+" : ""}${pct.toFixed(2)}%`,
            };
        });
    }, [fuelAdjustments, filterCustomerId, t]);

    useEffect(() => {
        setEntriesPage(1);
    }, [filterCustomerId, filterSourceHubId, filterDestinationCode, filterVehicleClass, filterSearch, entriesPerPage]);

    useEffect(() => {
        if (entriesPage > entriesTotalPages) setEntriesPage(entriesTotalPages);
    }, [entriesPage, entriesTotalPages]);

    useEffect(() => {
        if (manualVehicleClassOptions.length === 0) return;
        if (!manualVehicleClassOptions.includes(manualRateForm.vehicleClass)) {
            setManualRateForm((prev) => ({ ...prev, vehicleClass: manualVehicleClassOptions[0] }));
        }
    }, [manualVehicleClassOptions, manualRateForm.vehicleClass]);

    useEffect(() => {
        setManualRateForm((prev) => {
            const hubOk = !prev.hubId || manualLocationOptions.includes(prev.hubId);
            const destOk = !prev.destinationCode || manualLocationOptions.includes(prev.destinationCode);
            if (hubOk && destOk) return prev;
            return {
                ...prev,
                hubId: hubOk ? prev.hubId : "",
                destinationCode: destOk ? prev.destinationCode : "",
            };
        });
    }, [manualLocationOptions]);

    useEffect(() => {
        setFilterSourceHubId((prev) => (prev === "all" || filterLocationOptions.includes(prev) ? prev : "all"));
        setFilterDestinationCode((prev) =>
            prev === "all" || filterLocationOptions.includes(prev) ? prev : "all"
        );
    }, [filterLocationOptions]);

    const handleCreateAdjustment = async () => {
        setAdjustmentError(null);
        const customerId = adjustmentForm.customerId.trim();
        const effectiveFrom = parseDateInput(adjustmentForm.effectiveFrom);
        const referenceFuelPrice = adjustmentForm.referenceFuelPrice
            ? Number(adjustmentForm.referenceFuelPrice)
            : undefined;

        const isPercentMode = adjustmentForm.adjustmentType === "percent";
        const ratePercent = isPercentMode ? Number(adjustmentForm.ratePercent) : 0;
        const rateMultiplier = isPercentMode ? 1 + ratePercent / 100 : 1;
        let addThbPerTrip = isPercentMode ? 0 : Number(adjustmentForm.addThbPerTrip);

        // Auto-calculate addThbPerTrip from the fuel band (fixed_thb mode only).
        // Delegated to the shared helper: the old inline `Math.floor` classified into [n, n+1)
        // while the contract band is (n, n+1], so a price of exactly x.00 was charged one band
        // too high for the whole round (ADR 0009 §3).
        if (!isPercentMode && adjustmentForm.fuelBandEnabled && referenceFuelPrice != null) {
            const baselineFloor = parseInt(adjustmentForm.fuelBandBaselineFuelFloor) || 41;
            const thbPerBaht = parseFloat(adjustmentForm.fuelBandThbPerBaht) || 10;
            addThbPerTrip = computeFuelSurchargeThb(referenceFuelPrice, baselineFloor, thbPerBaht);
        }

        if (
            !customerId ||
            !effectiveFrom ||
            (isPercentMode && !Number.isFinite(ratePercent)) ||
            !Number.isFinite(rateMultiplier) ||
            rateMultiplier <= 0 ||
            !Number.isFinite(addThbPerTrip)
        ) {
            setAdjustmentError(t("accounting.rateCard.fuelAdjustments.form.invalid"));
            return;
        }
        setSavingAdjustment(true);
        try {
            const payload = {
                customerId,
                effectiveFrom,
                rateMultiplier,
                addThbPerTrip,
                referenceFuelPriceThbPerLitre: referenceFuelPrice,
                announcementNote: adjustmentForm.announcementNote,
                fuelBandEnabled: adjustmentForm.fuelBandEnabled,
                fuelBandBaselineFuelFloor: adjustmentForm.fuelBandEnabled
                    ? parseInt(adjustmentForm.fuelBandBaselineFuelFloor)
                    : undefined,
                fuelBandThbPerBaht: adjustmentForm.fuelBandEnabled
                    ? parseFloat(adjustmentForm.fuelBandThbPerBaht)
                    : undefined,
            };
            // Always a new row — announcements are immutable (ADR 0009 §1).
            await createCustomerFuelRateAdjustment(payload);
            setFuelAdjustmentDialogOpen(false);
            handleCancelEditAdjustment();
            await loadData();
        } catch (err) {
            console.error(err);
            setAdjustmentError(t("accounting.rateCard.fuelAdjustments.form.error"));
        } finally {
            setSavingAdjustment(false);
        }
    };

    const handleRecomputeBilling = async () => {
        setRecomputeLoading(true);
        setRecomputeResult(null);
        setRecomputeError(null);
        try {
            const tripFn = httpsCallable(functions, "backfillTripBillingSnapshots", { timeout: 540000 });
            // Standby is priced from its own rate table but shows up on the same invoice, so a
            // "recompute after a rate change" that skipped it left the document half-updated
            // (ADR 0008 §5). Both run over the same window, both forced.
            const standbyFn = httpsCallable(functions, "backfillStandbyBillingSnapshots", { timeout: 540000 });
            const range = {
                fromDateStr: recomputeFromDate,
                toDateStr: recomputeToDate,
                forceRecompute: true,
                maxScan: 2000,
                maxWrite: 500,
            };

            const [tripRes, standbyRes] = await Promise.all([
                tripFn({ ...range, customerId: recomputeCustomerId !== "all" ? recomputeCustomerId : undefined }),
                standbyFn(range),
            ]);

            const trip = tripRes.data as RecomputeStats;
            const standby = standbyRes.data as RecomputeStats;
            // Present one combined figure — the admin recomputed "the invoice", not two collections.
            setRecomputeResult({
                scanned: trip.scanned + standby.scanned,
                eligible: (trip.eligible ?? 0) + (standby.eligible ?? 0),
                written: trip.written + standby.written,
                skipped: trip.skipped + standby.skipped,
                failed: trip.failed + standby.failed,
                blocked: (trip.blocked ?? 0) + (standby.blocked ?? 0),
                blockedInvoices: [
                    ...new Set([...(trip.blockedInvoices ?? []), ...(standby.blockedInvoices ?? [])]),
                ],
                standbyWritten: standby.written,
            });
        } catch (err) {
            setRecomputeError(err instanceof Error ? err.message : String(err));
        } finally {
            setRecomputeLoading(false);
        }
    };

    /**
     * Void an announcement (ADR 0009 §1). Replaces delete: the row is the record that a price *was
     * announced*, and every frozen trip amount computed from it points here for provenance.
     */
    const handleConfirmVoid = async () => {
        if (!canEdit || !voidTarget) return;
        const reason = voidReason.trim();
        if (!reason) return;
        setVoidSaving(true);
        setVoidError(null);
        try {
            const actor = currentUser?.email || currentUser?.uid || "unknown";
            if (voidTarget.kind === "adjustment") {
                await voidCustomerFuelRateAdjustment(voidTarget.id, reason, actor);
            } else {
                await voidCustomerRateEntry(voidTarget.id, reason, actor);
            }
            setVoidTarget(null);
            setVoidReason("");
            await loadData();
        } catch (err) {
            console.error(err);
            setVoidError(t("accounting.rateCard.void.error"));
        } finally {
            setVoidSaving(false);
        }
    };

    /**
     * Prefill the form from an existing round so the admin can announce a **corrected** one.
     * This used to update the row in place, which silently changed the meaning of every amount
     * already computed from it.
     */
    const handleAnnounceLikeAdjustment = (row: CustomerFuelRateAdjustmentRow) => {
        const ratePercent = ((row.rateMultiplier - 1) * 100).toFixed(2);
        const latest = latestFleetDieselFromMonthlySnapshots(fuelSnapshots);
        // Detect adjustment type: if addThbPerTrip is set (non-zero) or fuelBand is enabled → fixed_thb mode
        const adjustmentType: "percent" | "fixed_thb" =
            (row.fuelBandEnabled || (row.addThbPerTrip != null && row.addThbPerTrip !== 0))
                ? "fixed_thb"
                : "percent";
        setAdjustmentError(null);
        setAdjustmentForm({
            adjustmentType,
            customerId: row.customerId,
            // Blank on purpose: a correction is a NEW round and needs its own effective date,
            // and the daily-price lookup (§5) keys on that date.
            effectiveFrom: "",
            ratePercent,
            addThbPerTrip: row.addThbPerTrip != null ? String(row.addThbPerTrip) : "0",
            referenceFuelPrice:
                latest != null
                    ? String(latest.price)
                    : row.referenceFuelPriceThbPerLitre != null
                      ? String(row.referenceFuelPriceThbPerLitre)
                      : "",
            announcementNote: row.announcementNote ?? "",
            fuelBandEnabled: row.fuelBandEnabled ?? false,
            fuelBandBaselineFuelFloor: String(row.fuelBandBaselineFuelFloor ?? "41"),
            fuelBandThbPerBaht: String(row.fuelBandThbPerBaht ?? "10"),
        });
        setFuelAdjustmentDialogOpen(true);
    };

    const handleCancelEditAdjustment = () => {
        setAdjustmentError(null);
        setDailyFuelState("idle");
        setDailyFuelPrice(null);
        setAdjustmentForm({
            adjustmentType: "percent",
            customerId: "",
            effectiveFrom: "",
            ratePercent: "0",
            addThbPerTrip: "0",
            referenceFuelPrice: "",
            announcementNote: "",
            fuelBandEnabled: false,
            fuelBandBaselineFuelFloor: "41",
            fuelBandThbPerBaht: "10",
        });
    };

    const handleSyncBangchakFuelSnapshot = async () => {
        if (!canEdit) return;
        setFuelMonthlySyncMessage(null);
        setFuelMonthlySyncIsError(false);
        setSyncingFuelSnapshot(true);
        try {
            const fn = httpsCallable<
                Record<string, never>,
                { ok: boolean; monthKey: string; itemCount?: number; errorMessage?: string }
            >(functions, "syncBangchakFuelMonthlySnapshot");
            const { data } = await fn({});
            if (data.ok) {
                setFuelMonthlySyncIsError(false);
                setFuelMonthlySyncMessage(
                    t("accounting.rateCard.fuelMonthly.syncSuccess", {
                        monthKey: data.monthKey,
                        count: data.itemCount ?? 0,
                    })
                );
            } else {
                setFuelMonthlySyncIsError(true);
                setFuelMonthlySyncMessage(
                    data.errorMessage?.trim() || t("accounting.rateCard.fuelMonthly.syncFailed")
                );
            }
            await loadData();
        } catch (err: unknown) {
            console.error(err);
            setFuelMonthlySyncIsError(true);
            const code =
                err && typeof err === "object" && "code" in err ? String((err as { code: string }).code) : "";
            const rawMsg =
                err && typeof err === "object" && "message" in err
                    ? String((err as { message?: string }).message ?? "")
                    : "";
            const msgTrim = rawMsg.trim();
            const looksLikeUndeployedCallable =
                code === "functions/not-found" ||
                /\bnot\s+found\b/i.test(msgTrim) ||
                /\b404\b/.test(msgTrim);
            const looksLikeOpaqueFailure =
                code === "functions/internal" || /^internal$/i.test(msgTrim);

            setFuelMonthlySyncMessage(
                code === "functions/permission-denied"
                    ? t("accounting.rateCard.fuelMonthly.syncDenied")
                    : looksLikeUndeployedCallable || looksLikeOpaqueFailure
                      ? t("accounting.rateCard.fuelMonthly.syncDeployHint")
                      : msgTrim || t("accounting.rateCard.fuelMonthly.syncFailed")
            );
        } finally {
            setSyncingFuelSnapshot(false);
        }
    };

    const handleExportTemplate = () => {
        const rows = [
            ["Hub Name", "Location", "Destination", "4WJ", "Distance"],
            ["ABBON - บางบอน", "", "SOCE", 1306, 68, "*ลบรายการตัวอย่างออกก่อน"],
            ["ABBON - บางบอน", "", "SOCN", 1416, 82, ""],
            ["ABBON - บางบอน", "", "SOCW", 1059, 18, ""],
            ["SPK-GW", "", "SPK890103-ลาดกระบัง26", 1200, 44, ""],
            ["SPK-GW", "", "SPK890174-ห้วยขวาง10", 1200, 46, ""],
            ["SPK-GW", "", "SPK890153-บึงกุ่ม", 1200, 45, ""],
        ];
        const guideRows = [
            ["How to fill rate card template"],
            ["1) Hub Name: ใช้รูปแบบ 'รหัสฮับ - ชื่อฮับ' เช่น 'SPK-GW - J&T EXPRESS บางปู'"],
            ["2) Location: ปล่อยว่างได้ (ระบบไม่ใช้ในงานบัญชี)"],
            ["3) Destination: ใส่ปลายทางตามที่ใช้งานจริง เช่น SOCE/SOCN/SOCW หรือปลายทางลูกค้า"],
            ["4) 4WJ: ราคาฐานต่อเที่ยว (บาท)"],
            ["5) Distance: ระยะทาง (กม.) ใส่หรือปล่อยว่างได้"],
            [""],
            ["Important"],
            ["- ต้องเลือกลูกค้าก่อน Import ทุกครั้ง"],
            ["- ถ้าฮับไม่อยู่ในระบบ แถวนั้นจะถูกข้ามและขึ้นคำเตือน"],
            ["- หัวคอลัมน์ต้องอยู่แถวแรกของไฟล์"],
        ];

        const ws = XLSX.utils.aoa_to_sheet(rows);
        if (ws.F2) {
            ws.F2.s = {
                fill: {
                    patternType: "solid",
                    fgColor: { rgb: "FFF59D" },
                },
                font: {
                    bold: true,
                    color: { rgb: "D32F2F" },
                },
            };
        }
        const wsGuide = XLSX.utils.aoa_to_sheet(guideRows);
        const wb = XLSX.utils.book_new();
        XLSX.utils.book_append_sheet(wb, ws, "rate_card_template");
        XLSX.utils.book_append_sheet(wb, wsGuide, "instructions");
        XLSX.writeFile(wb, "rate_card_template.xlsx");
    };

    const handleCreateManualRate = async () => {
        setManualRateError(null);
        const customerId = manualRateForm.customerId.trim();
        const hubId = manualRateForm.hubId.trim().toUpperCase();
        const destinationCode = manualRateForm.destinationCode.trim().toUpperCase();
        const vehicleClass = manualRateForm.vehicleClass.trim().toUpperCase() || "4WJ";
        const rateThb = Number(manualRateForm.rateThb);
        const distanceKm = manualRateForm.distanceKm.trim() === "" ? undefined : Number(manualRateForm.distanceKm);
        const effectiveFrom = parseDateInput(manualRateForm.effectiveFrom) ?? new Date();

        if (!customerId || !hubId || !destinationCode || !Number.isFinite(rateThb)) {
            setManualRateError(t("accounting.rateCard.manualAdd.invalid"));
            return;
        }
        setSavingManualRate(true);
        try {
            await createCustomerRateEntry(
                customerId,
                {
                    hubId,
                    rawHubName: formatSource(hubId),
                    destinationCode,
                    vehicleClass,
                    rateThb,
                    distanceKm: Number.isFinite(distanceKm as number) ? distanceKm : undefined,
                    jobCategory: manualRateForm.jobCategory,
                },
                effectiveFrom
            );
            setManualRateForm((prev) => ({
                ...prev,
                hubId: "",
                destinationCode: "",
                vehicleClass: "4WJ",
                rateThb: "",
                distanceKm: "",
                effectiveFrom: "",
                jobCategory: "PRIMARY",
            }));
            await loadData();
        } catch (err) {
            console.error(err);
            setManualRateError(t("accounting.rateCard.manualAdd.error"));
        } finally {
            setSavingManualRate(false);
        }
    };

    /**
     * Prefill the manual-add form from an existing rate row so a corrected price is announced as a
     * NEW row with its own effective date (ADR 0009 §1) instead of overwriting the old one.
     */
    const handleAnnounceLikeEntry = (entry: CustomerRateEntryRow) => {
        setManualRateError(null);
        setManualRateForm({
            customerId: entry.customerId,
            hubId: entry.hubId,
            destinationCode: entry.destinationCode,
            vehicleClass: normalizeVehicleClass(entry.vehicleClass),
            rateThb: String(entry.rateThb),
            distanceKm: entry.distanceKm != null ? String(entry.distanceKm) : "",
            jobCategory: entry.jobCategory === "SUPPLEMENTARY" ? "SUPPLEMENTARY" : "PRIMARY",
            // Left blank on purpose — a correction needs its own effective date.
            effectiveFrom: "",
        });
    };

    const handleExportData = () => {
        const exportRows = filteredEntries.map((row) => ({
            [t("accounting.rateCard.table.customer")]: customerOnlyNameById.get(row.customerId) ?? row.customerId,
            [t("accounting.rateCard.export.customerCode")]: customers.find((c) => c.id === row.customerId)?.code ?? "",
            [t("accounting.rateCard.table.hubId")]: row.hubId,
            [t("accounting.rateCard.export.hubName")]: formatSource(row.hubId),
            [t("accounting.rateCard.table.destination")]: row.destinationCode,
            [t("accounting.rateCard.export.destinationName")]: formatDestination(row.destinationCode),
            [t("accounting.rateCard.table.vehicleClass")]: row.vehicleClass,
            [t("accounting.rateCard.table.rateThb")]: row.rateThb,
            [t("accounting.rateCard.table.distanceKm")]: row.distanceKm ?? "",
            [t("accounting.rateCard.table.effectiveFrom")]: format(row.effectiveFrom, "dd/MM/yyyy"),
            [t("accounting.rateCard.table.importId")]: row.importId,
        }));
        const ws = XLSX.utils.json_to_sheet(exportRows);
        const wb = XLSX.utils.book_new();
        XLSX.utils.book_append_sheet(wb, ws, "Rate Card");
        const dateStr = format(new Date(), "yyyyMMdd_HHmmss");
        XLSX.writeFile(wb, `rate_card_export_${dateStr}.xlsx`);
    };

    // Service Fees handlers
    const handleCreateServiceFee = async () => {
        setServiceFeeError(null);
        const customerId = serviceFeeForm.customerId.trim();
        const amountThb = Number(serviceFeeForm.amountThb);

        if (!customerId || !Number.isFinite(amountThb) || (serviceFeeForm.feeType === "custom" && !serviceFeeForm.customTypeName.trim())) {
            setServiceFeeError(t("accounting.serviceFees.form.invalid"));
            return;
        }

        setSavingServiceFee(true);
        try {
            if (editingServiceFeeId) {
                await updateCustomerServiceFee(editingServiceFeeId, {
                    customerId,
                    feeType: serviceFeeForm.feeType,
                    customTypeName: serviceFeeForm.feeType === "custom" ? serviceFeeForm.customTypeName.trim() : undefined,
                    amountThb,
                    unit: serviceFeeForm.unit,
                    note: serviceFeeForm.note.trim() || undefined,
                });
            } else {
                await createCustomerServiceFee({
                    customerId,
                    feeType: serviceFeeForm.feeType,
                    customTypeName: serviceFeeForm.feeType === "custom" ? serviceFeeForm.customTypeName.trim() : undefined,
                    amountThb,
                    unit: serviceFeeForm.unit,
                    note: serviceFeeForm.note.trim() || undefined,
                });
            }
            setServiceFeeForm({
                customerId: "",
                feeType: "extra_stop",
                customTypeName: "",
                amountThb: "",
                unit: "per_stop",
                note: "",
            });
            setEditingServiceFeeId(null);
            setServiceFeeDialogOpen(false);
            await loadData();
        } catch (err) {
            console.error(err);
            setServiceFeeError(t("accounting.serviceFees.form.error"));
        } finally {
            setSavingServiceFee(false);
        }
    };

    const handleDeleteServiceFee = async (id: string) => {
        if (!confirm(t("accounting.serviceFees.confirmDelete"))) return;
        setDeletingServiceFeeId(id);
        try {
            await deleteCustomerServiceFee(id);
            await loadData();
        } catch (err) {
            console.error(err);
        } finally {
            setDeletingServiceFeeId(null);
        }
    };

    const handleEditServiceFee = (fee: CustomerServiceFeeRow) => {
        setEditingServiceFeeId(fee.id);
        setServiceFeeForm({
            customerId: fee.customerId,
            feeType: fee.feeType,
            customTypeName: fee.customTypeName ?? "",
            amountThb: String(fee.amountThb),
            unit: fee.unit,
            note: fee.note ?? "",
        });
        setServiceFeeError(null);
        setServiceFeeDialogOpen(true);
    };

    const handleCancelEditServiceFee = () => {
        setEditingServiceFeeId(null);
        setServiceFeeForm({
            customerId: "",
            feeType: "extra_stop",
            customTypeName: "",
            amountThb: "",
            unit: "per_stop",
            note: "",
        });
        setServiceFeeError(null);
    };

    const filteredServiceFees = useMemo(() => {
        if (filterCustomerId === "all") return serviceFees;
        return serviceFees.filter((f) => f.customerId === filterCustomerId);
    }, [serviceFees, filterCustomerId]);

    const getServiceFeeTypeLabel = (feeType: ServiceFeeType, customTypeName?: string) => {
        if (feeType === "custom" && customTypeName?.trim()) {
            return customTypeName.trim();
        }
        const key = `accounting.serviceFees.type.${feeType}`;
        return t(key);
    };

    // Standby Rates handlers
    const handleSaveStandbyRate = async () => {
        setStandbyRateError(null);
        const customerId = standbyRateForm.customerId.trim();
        const rateThb = Number(standbyRateForm.rateThb);
        const effDate = standbyRateForm.effectiveFrom ? new Date(`${standbyRateForm.effectiveFrom}T00:00:00`) : null;

        if (!customerId || !Number.isFinite(rateThb) || rateThb < 0 || !effDate || isNaN(effDate.getTime())) {
            setStandbyRateError("กรุณากรอกข้อมูลให้ครบถ้วน");
            return;
        }

        setSavingStandbyRate(true);
        try {
            const input = { customerId, rateThb, effectiveFrom: effDate, note: standbyRateForm.note.trim() || undefined };
            if (editingStandbyRateId) {
                await updateStandbyRateEntry(editingStandbyRateId, input);
            } else {
                await createStandbyRateEntry(input);
            }
            setStandbyRateDialogOpen(false);
            setEditingStandbyRateId(null);
            setStandbyRateForm({ customerId: "", rateThb: "", effectiveFrom: format(new Date(), "yyyy-MM-dd"), note: "" });
            await loadData();
        } catch (err) {
            setStandbyRateError(err instanceof Error ? err.message : "เกิดข้อผิดพลาด");
        } finally {
            setSavingStandbyRate(false);
        }
    };

    const handleEditStandbyRate = (row: StandbyRateEntryRow) => {
        setEditingStandbyRateId(row.id);
        setStandbyRateForm({
            customerId: row.customerId,
            rateThb: String(row.rateThb),
            effectiveFrom: format(row.effectiveFrom, "yyyy-MM-dd"),
            note: row.note ?? "",
        });
        setStandbyRateError(null);
        setStandbyRateDialogOpen(true);
    };

    const handleDeleteStandbyRate = async (id: string) => {
        if (!confirm("ลบ Standby Rate นี้?")) return;
        setDeletingStandbyRateId(id);
        try {
            await deleteStandbyRateEntry(id);
            await loadData();
        } catch (err) {
            console.error(err);
        } finally {
            setDeletingStandbyRateId(null);
        }
    };

    const handleNormalizeVehicleClasses = async () => {
        if (!confirm("Normalize vehicle class values in all rate entries? This will convert full names (e.g., '4 WHEELS JUMBO') to short codes (e.g., '4WJ').")) {
            return;
        }
        setNormalizeInProgress(true);
        setNormalizeError(null);
        setNormalizeResult(null);
        try {
            const result = await normalizeRateEntryVehicleClasses(5000, 500);
            setNormalizeResult(result);
            if (result.updated > 0) {
                await loadData();
            }
        } catch (err) {
            console.error(err);
            setNormalizeError(err instanceof Error ? err.message : "Unknown error during normalization");
        } finally {
            setNormalizeInProgress(false);
        }
    };

    // Stats summary
    const entriesStats = useMemo(() => {
        const byCustomer = new Map<string, number>();
        entries.forEach((e) => {
            byCustomer.set(e.customerId, (byCustomer.get(e.customerId) ?? 0) + 1);
        });
        return {
            total: entries.length,
            customerCount: byCustomer.size,
            filtered: filteredEntries.length,
        };
    }, [entries, filteredEntries]);

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
                    <h1 className="text-3xl font-bold tracking-tight">{t("accounting.rateCard.title")}</h1>
                    <p className="text-muted-foreground mt-1">{t("accounting.rateCard.subtitle")}</p>
                </div>
                <div className="flex items-center gap-2">
                    <Button
                        variant="outline"
                        onClick={handleExportData}
                        disabled={filteredEntries.length === 0}
                    >
                        <Download className="h-4 w-4 mr-2" />
                        {t("accounting.rateCard.exportData")}
                    </Button>
                    <Button variant="outline" onClick={handleExportTemplate}>
                        <Download className="h-4 w-4 mr-2" />
                        {t("accounting.rateCard.exportTemplate")}
                    </Button>
                    <Button variant="outline" onClick={() => void loadData()} disabled={normalizeInProgress}>
                        <RefreshCw className="h-4 w-4 mr-2" />
                        {t("common.refresh")}
                    </Button>
                    {canEdit && (
                        <>
                            <Button
                                variant="outline"
                                onClick={() => void handleNormalizeVehicleClasses()}
                                disabled={normalizeInProgress || entries.length === 0}
                                title="Normalize vehicle class values (e.g., 4 WHEELS JUMBO → 4WJ)"
                            >
                                <Calculator className="h-4 w-4 mr-2" />
                                {normalizeInProgress ? (
                                    <>
                                        <Loader2 className="h-4 w-4 mr-2 animate-spin" />
                                        Normalizing...
                                    </>
                                ) : (
                                    "Normalize Vehicle Classes"
                                )}
                            </Button>
                            <Button onClick={() => setImportOpen(true)}>
                                <Upload className="h-4 w-4 mr-2" />
                                {t("accounting.rateCard.importButton")}
                            </Button>
                        </>
                    )}
                </div>
            </div>

            {/* Stats Summary */}
            <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                <Card>
                    <CardHeader className="pb-2">
                        <CardTitle className="text-sm font-medium">{t("accounting.rateCard.stats.total")}</CardTitle>
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold">{entriesStats.total.toLocaleString()}</div>
                        <p className="text-xs text-muted-foreground">
                            {t("accounting.rateCard.stats.entries")}
                        </p>
                    </CardContent>
                </Card>
                <Card>
                    <CardHeader className="pb-2">
                        <CardTitle className="text-sm font-medium">{t("accounting.rateCard.stats.customers")}</CardTitle>
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold">{entriesStats.customerCount.toLocaleString()}</div>
                        <p className="text-xs text-muted-foreground">
                            {t("accounting.rateCard.stats.customersWithRates")}
                        </p>
                    </CardContent>
                </Card>
                <Card>
                    <CardHeader className="pb-2">
                        <CardTitle className="text-sm font-medium">{t("accounting.rateCard.stats.filtered")}</CardTitle>
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold">{entriesStats.filtered.toLocaleString()}</div>
                        <p className="text-xs text-muted-foreground">
                            {t("accounting.rateCard.stats.matchingFilter")}
                        </p>
                    </CardContent>
                </Card>
            </div>

            {/* Normalization Result Alert */}
            {normalizeError && (
                <div className="bg-red-50 border border-red-200 rounded p-4">
                    <h3 className="font-semibold text-red-900">Normalization Error</h3>
                    <p className="text-red-800 mt-1">{normalizeError}</p>
                </div>
            )}

            {normalizeResult && (
                <div className="bg-blue-50 border border-blue-200 rounded p-4">
                    <h3 className="font-semibold text-blue-900">Vehicle Class Normalization Results</h3>
                    <div className="grid grid-cols-1 md:grid-cols-4 gap-3 mt-3">
                        <div>
                            <p className="text-xs text-muted-foreground">Scanned</p>
                            <p className="text-lg font-bold">{normalizeResult.scanned}</p>
                        </div>
                        <div>
                            <p className="text-xs text-muted-foreground">Needs Update</p>
                            <p className="text-lg font-bold">{normalizeResult.needsUpdate}</p>
                        </div>
                        <div>
                            <p className="text-xs text-muted-foreground">Updated</p>
                            <p className="text-lg font-bold text-green-600">{normalizeResult.updated}</p>
                        </div>
                        <div>
                            <p className="text-xs text-muted-foreground">Status</p>
                            <p className="text-sm mt-1">
                                {normalizeResult.capped ? "More work may remain" : "Complete"}
                            </p>
                        </div>
                    </div>
                    {normalizeResult.samples.length > 0 && (
                        <div className="mt-3 pt-3 border-t border-blue-200">
                            <p className="text-xs font-semibold text-blue-900 mb-2">Sample updates:</p>
                            <div className="space-y-1 text-xs">
                                {normalizeResult.samples.map((s) => (
                                    <div key={s.docId} className="text-blue-800">
                                        <code className="bg-white px-2 py-1 rounded">
                                            {s.old} → {s.new}
                                        </code>
                                    </div>
                                ))}
                            </div>
                        </div>
                    )}
                </div>
            )}

            {canEdit && (
                <Card>
                    <CardHeader>
                        <CardTitle>{t("accounting.rateCard.manualAdd.title")}</CardTitle>
                    </CardHeader>
                    <CardContent className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-3">
                        <div className="space-y-1.5">
                            <Label>{t("accounting.rateCard.import.selectCustomer")}</Label>
                            <Select
                                value={manualRateForm.customerId}
                                onValueChange={(v) => setManualRateForm((prev) => ({ ...prev, customerId: v }))}
                            >
                                <SelectTrigger>
                                    <SelectValue placeholder={t("accounting.rateCard.import.selectCustomerPlaceholder")} />
                                </SelectTrigger>
                                <SelectContent>
                                    {customers.map((c) => (
                                        <SelectItem key={c.id} value={c.id}>
                                            {c.code} - {c.name}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>
                        <div className="space-y-1.5">
                            <Label>{t("accounting.rateCard.table.hubId")}</Label>
                            <Combobox
                                options={manualLocationComboboxOptions}
                                value={manualRateForm.hubId}
                                onSelect={(value) => setManualRateForm((prev) => ({ ...prev, hubId: value || "" }))}
                                placeholder={t("accounting.rateCard.filterSource")}
                                searchPlaceholder={t("accounting.rateCard.filterSourceSearchPlaceholder")}
                                emptyText={t("accounting.rateCard.filterNoResults")}
                            />
                        </div>
                        <div className="space-y-1.5">
                            <Label>{t("accounting.rateCard.table.destination")}</Label>
                            <Combobox
                                options={manualLocationComboboxOptions}
                                value={manualRateForm.destinationCode}
                                onSelect={(value) =>
                                    setManualRateForm((prev) => ({ ...prev, destinationCode: value || "" }))
                                }
                                placeholder={t("accounting.rateCard.filterDestination")}
                                searchPlaceholder={t("accounting.rateCard.filterDestinationSearchPlaceholder")}
                                emptyText={t("accounting.rateCard.filterNoResults")}
                            />
                        </div>
                        <div className="space-y-1.5">
                            <Label>{t("accounting.rateCard.table.vehicleClass")}</Label>
                            <Select
                                value={manualRateForm.vehicleClass}
                                onValueChange={(value) =>
                                    setManualRateForm((prev) => ({ ...prev, vehicleClass: value }))
                                }
                            >
                                <SelectTrigger>
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                    {manualVehicleClassOptions.map((vehicleClass) => (
                                        <SelectItem key={vehicleClass} value={vehicleClass}>
                                            {displayVehicleTypeCode(vehicleClass)}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>
                        <div className="space-y-1.5">
                            <Label>{t("accounting.rateCard.jobCategory.label", "ประเภทงาน")}</Label>
                            <Select
                                value={manualRateForm.jobCategory}
                                onValueChange={(value) =>
                                    setManualRateForm((prev) => ({ ...prev, jobCategory: value === "SUPPLEMENTARY" ? "SUPPLEMENTARY" : "PRIMARY" }))
                                }
                            >
                                <SelectTrigger>
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                    <SelectItem value="PRIMARY">{t("accounting.rateCard.jobCategory.primary", "หลัก")}</SelectItem>
                                    <SelectItem value="SUPPLEMENTARY">{t("accounting.rateCard.jobCategory.supplementary", "เสริม")}</SelectItem>
                                </SelectContent>
                            </Select>
                        </div>
                        <div className="space-y-1.5">
                            <Label>{t("accounting.rateCard.table.rateThb")}</Label>
                            <Input
                                type="number"
                                value={manualRateForm.rateThb}
                                onChange={(e) => setManualRateForm((prev) => ({ ...prev, rateThb: e.target.value }))}
                            />
                        </div>
                        <div className="space-y-1.5">
                            <Label>{t("accounting.rateCard.table.distanceKm")}</Label>
                            <Input
                                type="number"
                                value={manualRateForm.distanceKm}
                                onChange={(e) =>
                                    setManualRateForm((prev) => ({ ...prev, distanceKm: e.target.value }))
                                }
                            />
                        </div>
                        <div className="space-y-1.5">
                            <Label>{t("accounting.rateCard.table.effectiveFrom")}</Label>
                            <Input
                                type="date"
                                value={manualRateForm.effectiveFrom}
                                onChange={(e) =>
                                    setManualRateForm((prev) => ({ ...prev, effectiveFrom: e.target.value }))
                                }
                            />
                        </div>
                        <div className="flex items-end">
                            <Button onClick={handleCreateManualRate} disabled={savingManualRate} className="w-full">
                                {savingManualRate ? (
                                    <Loader2 className="h-4 w-4 mr-2 animate-spin" />
                                ) : (
                                    <Plus className="h-4 w-4 mr-2" />
                                )}
                                {t("accounting.rateCard.manualAdd.button")}
                            </Button>
                        </div>
                        {manualRateError && (
                            <p className="lg:col-span-4 text-sm text-destructive">{manualRateError}</p>
                        )}
                    </CardContent>
                </Card>
            )}

            <Card>
                <CardHeader className="flex flex-col gap-3 space-y-0 sm:flex-row sm:items-center sm:justify-between">
                    <CardTitle>{t("accounting.rateCard.title")}</CardTitle>
                    <div className="flex flex-wrap items-end gap-3">
                        {showRateCalPreview && (
                            <div className="flex flex-col gap-1.5">
                                <Label className="text-xs">{t("accounting.rateCard.calcPreview.asOfLabel")}</Label>
                                <div className="flex flex-wrap items-center gap-2">
                                    <Input
                                        type="date"
                                        className="h-9 w-[160px]"
                                        value={rateCalAsOfStr}
                                        onChange={(e) => setRateCalAsOfStr(e.target.value)}
                                    />
                                    <Button
                                        type="button"
                                        variant="outline"
                                        size="sm"
                                        className="h-9"
                                        onClick={() => setRateCalAsOfStr(format(new Date(), "yyyy-MM-dd"))}
                                    >
                                        {t("accounting.rateCard.calcPreview.today")}
                                    </Button>
                                </div>
                            </div>
                        )}
                        <Button
                            type="button"
                            variant={showRateCalPreview ? "default" : "outline"}
                            size="sm"
                            className="shrink-0"
                            onClick={() => setShowRateCalPreview((v) => !v)}
                        >
                            <Calculator className="h-4 w-4 mr-2" />
                            {t("accounting.rateCard.calcPreview.button")}
                        </Button>
                    </div>
                </CardHeader>
                <CardContent className="space-y-4">
                    <div className="rounded-lg border p-4">
                        <h3 className="text-sm font-medium mb-3">{t("accounting.filter.title")}</h3>
                        <div className="flex flex-wrap gap-4">
                            <div className="flex flex-col gap-1.5 min-w-[280px]">
                                <Label>{t("accounting.rateCard.filterCustomer")}</Label>
                                <Select value={filterCustomerId} onValueChange={setFilterCustomerId}>
                                    <SelectTrigger>
                                        <SelectValue />
                                    </SelectTrigger>
                                    <SelectContent>
                                        <SelectItem value="all">{t("accounting.rateCard.filterCustomerAll")}</SelectItem>
                                        {customers.map((c) => (
                                            <SelectItem key={c.id} value={c.id}>
                                                {c.code} - {c.name}
                                            </SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>
                            </div>
                            <div className="flex flex-col gap-1.5 min-w-[200px]">
                                <Label>{t("accounting.rateCard.filterSource")}</Label>
                                <Combobox
                                    options={filterLocationComboboxOptions}
                                    value={filterSourceHubId}
                                    onSelect={(value) => setFilterSourceHubId(value || "all")}
                                    placeholder={t("accounting.rateCard.filterSource")}
                                    searchPlaceholder={t("accounting.rateCard.filterSourceSearchPlaceholder")}
                                    emptyText={t("accounting.rateCard.filterNoResults")}
                                    className="min-w-[200px]"
                                />
                            </div>
                            <div className="flex flex-col gap-1.5 min-w-[200px]">
                                <Label>{t("accounting.rateCard.filterDestination")}</Label>
                                <Combobox
                                    options={filterLocationComboboxOptions}
                                    value={filterDestinationCode}
                                    onSelect={(value) => setFilterDestinationCode(value || "all")}
                                    placeholder={t("accounting.rateCard.filterDestination")}
                                    searchPlaceholder={t("accounting.rateCard.filterDestinationSearchPlaceholder")}
                                    emptyText={t("accounting.rateCard.filterNoResults")}
                                    className="min-w-[200px]"
                                />
                            </div>
                            <div className="flex flex-col gap-1.5 min-w-[180px]">
                                <Label>{t("accounting.rateCard.filterVehicleClass")}</Label>
                                <Select value={filterVehicleClass} onValueChange={setFilterVehicleClass}>
                                    <SelectTrigger>
                                        <SelectValue />
                                    </SelectTrigger>
                                    <SelectContent>
                                        <SelectItem value="all">{t("accounting.filter.all")}</SelectItem>
                                        {vehicleOptions.map((vehicleClass) => (
                                            <SelectItem key={vehicleClass} value={vehicleClass}>
                                                {displayVehicleTypeCode(vehicleClass)}
                                            </SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>
                            </div>
                            <div className="flex flex-col gap-1.5 min-w-[260px]">
                                <Label>{t("accounting.rateCard.filterSearch")}</Label>
                                <Input
                                    value={filterSearch}
                                    onChange={(e) => setFilterSearch(e.target.value)}
                                    placeholder={t("accounting.rateCard.filterSearchPlaceholder")}
                                />
                            </div>
                        </div>
                    </div>
                    <Table>
                        <TableHeader>
                            <TableRow>
                                <TableHead>{t("accounting.rateCard.table.customer")}</TableHead>
                                <TableHead>{t("accounting.rateCard.table.hubId")}</TableHead>
                                <TableHead>{t("accounting.rateCard.table.destination")}</TableHead>
                                <TableHead>{t("accounting.rateCard.table.vehicleClass")}</TableHead>
                                <TableHead>{t("accounting.rateCard.jobCategory.label", "ประเภทงาน")}</TableHead>
                                <TableHead className="text-right">{t("accounting.rateCard.table.rateThb")}</TableHead>
                                {showRateCalPreview && (
                                    <TableHead className="text-right">{t("accounting.rateCard.table.newRateThb")}</TableHead>
                                )}
                                <TableHead className="text-right">{t("accounting.rateCard.table.distanceKm")}</TableHead>
                                <TableHead>{t("accounting.rateCard.table.effectiveFrom")}</TableHead>
                                <TableHead>{t("accounting.rateCard.table.importId")}</TableHead>
                                {canEdit && <TableHead className="w-[60px]">{t("accounting.table.actions")}</TableHead>}
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {paginatedEntries.map((row) => {
                                const asOfDate = parseDateInput(rateCalAsOfStr) ?? new Date();
                                const noon = new Date(asOfDate);
                                noon.setHours(12, 0, 0, 0);
                                const fuelAdjList = fuelRowsToAdjustments(
                                    adjustmentsByCustomer.get(row.customerId) ?? []
                                );
                                const ruleDoc = selectFuelAdjustmentForBillingDate(
                                    row.customerId,
                                    noon.getTime(),
                                    fuelAdjList
                                );
                                const newRate =
                                    ruleDoc != null
                                        ? computeFinalRateThb(
                                              row.rateThb,
                                              ruleDoc.rateMultiplier,
                                              ruleDoc.addThbPerTrip
                                          )
                                        : null;
                                return (
                                // Voided rate rows remain visible as history, struck through
                                // because pricing no longer selects them (ADR 0009 §1).
                                <TableRow
                                    key={row.id}
                                    className={row.voided ? "opacity-50 line-through" : undefined}
                                >
                                    <TableCell>
                                        {customerNameById.get(row.customerId) ?? row.customerId}
                                        {row.voided && (
                                            <Badge variant="destructive" className="ml-2 no-underline">
                                                {t("accounting.rateCard.void.badge")}
                                            </Badge>
                                        )}
                                    </TableCell>
                                    <TableCell>{formatSource(row.hubId)}</TableCell>
                                    <TableCell>{formatDestination(row.destinationCode)}</TableCell>
                                    <TableCell>
                                        <Badge
                                            variant="secondary"
                                            className={`font-mono text-xs ${vehicleClassBadgeClasses(displayVehicleTypeCode(row.vehicleClass))}`}
                                        >
                                            {displayVehicleTypeCode(row.vehicleClass)}
                                        </Badge>
                                    </TableCell>
                                    <TableCell>
                                        {row.jobCategory === "SUPPLEMENTARY" ? (
                                            <Badge variant="secondary" className="bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-300">
                                                {t("accounting.rateCard.jobCategory.supplementary", "เสริม")}
                                            </Badge>
                                        ) : (
                                            <Badge variant="secondary" className="bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-300">
                                                {t("accounting.rateCard.jobCategory.primary", "หลัก")}
                                            </Badge>
                                        )}
                                    </TableCell>
                                    <TableCell className="text-right">฿{row.rateThb.toLocaleString()}</TableCell>
                                    {showRateCalPreview && (
                                        <TableCell className="text-right">
                                            {newRate != null && ruleDoc != null ? (
                                                <span
                                                    title={
                                                        ruleDoc.addThbPerTrip !== 0
                                                            ? t("accounting.rateCard.calcPreview.rowTooltipFixed", {
                                                                addPerTrip: `${ruleDoc.addThbPerTrip >= 0 ? "+" : ""}${ruleDoc.addThbPerTrip.toFixed(2)}`,
                                                                delta: formatSignedThbDelta(row.rateThb, newRate),
                                                            })
                                                            : t("accounting.rateCard.calcPreview.rowTooltipPercent", {
                                                                times: formatMultiplierTimes(ruleDoc.rateMultiplier),
                                                                percent: multiplierToPercentText(ruleDoc.rateMultiplier),
                                                                delta: formatSignedThbDelta(row.rateThb, newRate),
                                                            })
                                                    }
                                                >
                                                    ฿{newRate.toLocaleString()}
                                                </span>
                                            ) : (
                                                <span
                                                    className="text-muted-foreground"
                                                    title={t("accounting.rateCard.calcPreview.noRuleHint")}
                                                >
                                                    {t("accounting.rateCard.calcPreview.noRule")}
                                                </span>
                                            )}
                                        </TableCell>
                                    )}
                                    <TableCell className="text-right">
                                        {row.distanceKm != null ? row.distanceKm.toLocaleString() : "-"}
                                    </TableCell>
                                    <TableCell>{format(row.effectiveFrom, "dd/MM/yyyy")}</TableCell>
                                    <TableCell className="font-mono text-xs">{row.importId}</TableCell>
                                    {canEdit && (
                                        <TableCell>
                                            <DropdownMenu>
                                                <DropdownMenuTrigger asChild>
                                                    <Button variant="ghost" size="icon" className="h-8 w-8">
                                                        <MoreHorizontal className="h-4 w-4" />
                                                    </Button>
                                                </DropdownMenuTrigger>
                                                <DropdownMenuContent align="end">
                                                    <DropdownMenuItem onClick={() => handleAnnounceLikeEntry(row)}>
                                                        <Copy className="h-4 w-4 mr-2" />
                                                        {t("accounting.rateCard.entry.announceCorrection")}
                                                    </DropdownMenuItem>
                                                    {!row.voided && (
                                                        <DropdownMenuItem
                                                            onClick={() =>
                                                                setVoidTarget({
                                                                    kind: "entry",
                                                                    id: row.id,
                                                                    label: `${row.hubId} → ${row.destinationCode} (${row.vehicleClass})`,
                                                                })
                                                            }
                                                            className="text-destructive focus:text-destructive"
                                                        >
                                                            <Ban className="h-4 w-4 mr-2" />
                                                            {t("accounting.rateCard.void.action")}
                                                        </DropdownMenuItem>
                                                    )}
                                                </DropdownMenuContent>
                                            </DropdownMenu>
                                        </TableCell>
                                    )}
                                </TableRow>
                                );
                            })}
                            {paginatedEntries.length === 0 && (
                                <TableRow>
                                    <TableCell
                                        colSpan={showRateCalPreview ? (canEdit ? 11 : 10) : (canEdit ? 10 : 9)}
                                        className="h-24 text-center text-muted-foreground"
                                    >
                                        {t("accounting.rateCard.noEntries")}
                                    </TableCell>
                                </TableRow>
                            )}
                        </TableBody>
                    </Table>
                    <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3 px-1 pt-4">
                        <div className="text-sm text-muted-foreground">
                            {t("accounting.rateCard.pagination.showing", {
                                from: entriesRangeStart,
                                to: entriesRangeEnd,
                                total: filteredEntries.length,
                            })}
                        </div>
                        <div className="flex flex-wrap items-center gap-2">
                            <div className="flex items-center gap-2">
                                <span className="text-sm text-muted-foreground">
                                    {t("accounting.rateCard.pagination.rowsPerPage")}
                                </span>
                                <Select
                                    value={String(entriesPerPage)}
                                    onValueChange={(v) => setEntriesPerPage(Number(v))}
                                >
                                    <SelectTrigger className="h-9 w-[84px]">
                                        <SelectValue />
                                    </SelectTrigger>
                                    <SelectContent>
                                        {[10, 15, 25, 50, 100].map((n) => (
                                            <SelectItem key={n} value={String(n)}>
                                                {n}
                                            </SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>
                            </div>
                            <Button
                                variant="outline"
                                size="sm"
                                onClick={() => setEntriesPage((p) => Math.max(1, p - 1))}
                                disabled={entriesPage <= 1}
                            >
                                {t("accounting.rateCard.pagination.previous")}
                            </Button>
                            <Button
                                variant="outline"
                                size="sm"
                                onClick={() => setEntriesPage((p) => Math.min(entriesTotalPages, p + 1))}
                                disabled={entriesPage >= entriesTotalPages}
                            >
                                {t("accounting.rateCard.pagination.next")}
                            </Button>
                        </div>
                    </div>
                </CardContent>
            </Card>

            {/* Rate rounds for the selected customer (ADR 0009 §8). Rendered as ordered half-open
                intervals so a gap or an overlap is visible before a month is invoiced, not after. */}
            {filterCustomerId !== "all" && rateRounds.length > 0 && (
                <Card>
                    <CardHeader>
                        <CardTitle>{t("accounting.rateCard.rounds.title")}</CardTitle>
                        <p className="text-sm text-muted-foreground mt-1">
                            {t("accounting.rateCard.rounds.subtitle")}
                        </p>
                    </CardHeader>
                    <CardContent>
                        <Table>
                            <TableHeader>
                                <TableRow>
                                    <TableHead>{t("accounting.rateCard.rounds.round")}</TableHead>
                                    <TableHead>{t("accounting.rateCard.rounds.period")}</TableHead>
                                    <TableHead>{t("accounting.rateCard.rounds.fuelBand")}</TableHead>
                                    <TableHead className="text-right">
                                        {t("accounting.rateCard.rounds.adjustment")}
                                    </TableHead>
                                    <TableHead>{t("accounting.rateCard.rounds.status")}</TableHead>
                                </TableRow>
                            </TableHeader>
                            <TableBody>
                                {rateRounds.map((r) => (
                                    <TableRow key={r.id} className={r.voided ? "opacity-50" : undefined}>
                                        <TableCell className="font-medium">{r.label}</TableCell>
                                        <TableCell className="font-mono text-xs">
                                            {r.fromStr} → {r.untilStr}
                                        </TableCell>
                                        <TableCell className="font-mono text-xs">{r.bandLabel}</TableCell>
                                        <TableCell className="text-right font-mono text-xs">
                                            {r.adjustmentLabel}
                                        </TableCell>
                                        <TableCell>
                                            {r.voided ? (
                                                <Badge variant="destructive">
                                                    {t("accounting.rateCard.void.badge")}
                                                </Badge>
                                            ) : (
                                                <Badge variant="secondary">
                                                    {t("accounting.rateCard.rounds.active")}
                                                </Badge>
                                            )}
                                        </TableCell>
                                    </TableRow>
                                ))}
                            </TableBody>
                        </Table>
                    </CardContent>
                </Card>
            )}

            <Card>
                <CardHeader className="flex flex-row items-center justify-between space-y-0">
                    <div>
                        <CardTitle>{t("accounting.rateCard.fuelAdjustments.title")}</CardTitle>
                        <p className="text-sm text-muted-foreground mt-1">
                            {t("accounting.rateCard.fuelAdjustments.subtitle")}
                        </p>
                    </div>
                    <div className="flex gap-2">
                        {canEdit && (
                            <Button
                                type="button"
                                size="sm"
                                className="bg-amber-500 hover:bg-amber-600 text-white"
                                onClick={() => { setRecomputeResult(null); setRecomputeError(null); setRecomputeDialogOpen(true); }}
                            >
                                <Calculator className="h-4 w-4 mr-2" />
                                Recompute Billing
                            </Button>
                        )}
                        {canEdit && (
                            <Button
                                type="button"
                                size="sm"
                                onClick={() => {
                                    const latest = latestFleetDieselFromMonthlySnapshots(fuelSnapshots);
                                    setAdjustmentError(null);
                                    setDailyFuelState("idle");
                                    setDailyFuelPrice(null);
                                    setAdjustmentForm({
                                        adjustmentType: "percent",
                                        customerId: "",
                                        effectiveFrom: "",
                                        ratePercent: "0",
                                        addThbPerTrip: "0",
                                        referenceFuelPrice: latest != null ? String(latest.price) : "",
                                        announcementNote: "",
                                        fuelBandEnabled: false,
                                        fuelBandBaselineFuelFloor: "41",
                                        fuelBandThbPerBaht: "10",
                                    });
                                    setFuelAdjustmentDialogOpen(true);
                                }}
                            >
                                <Plus className="h-4 w-4 mr-2" />
                                {t("accounting.rateCard.fuelAdjustments.addRule")}
                            </Button>
                        )}
                    </div>
                </CardHeader>

                {/* Recompute Billing Dialog */}
                <Dialog open={recomputeDialogOpen} onOpenChange={(o) => { setRecomputeDialogOpen(o); if (!o) setRecomputeResult(null); }}>
                    <DialogContent className="max-w-md">
                        <DialogHeader>
                            <DialogTitle className="flex items-center gap-2">
                                <Calculator className="h-5 w-5" />
                                Recompute Billing
                            </DialogTitle>
                            <DialogDescription>
                                คำนวณ billing ใหม่ทุก trip ที่ส่งสำเร็จในช่วงวันที่ที่เลือก — ใช้หลังจากแก้ Fuel Adjustment
                            </DialogDescription>
                        </DialogHeader>
                        <div className="space-y-4 py-2">
                            <div className="space-y-1">
                                <Label>ช่วงวันที่</Label>
                                <DateOnlyRangePicker
                                    from={recomputeFromDate}
                                    to={recomputeToDate}
                                    onChange={(from, to) => {
                                        setRecomputeFromDate(from);
                                        setRecomputeToDate(to);
                                    }}
                                    locale={dateLocale}
                                    className="w-[230px]"
                                />
                            </div>
                            <div className="space-y-1">
                                <Label>ลูกค้า (ไม่ระบุ = ทุกลูกค้า)</Label>
                                <Select value={recomputeCustomerId} onValueChange={setRecomputeCustomerId}>
                                    <SelectTrigger>
                                        <SelectValue placeholder="ทุกลูกค้า" />
                                    </SelectTrigger>
                                    <SelectContent className="z-[1005]" position="popper">
                                        <SelectItem value="all">ทุกลูกค้า</SelectItem>
                                        {customers.map((c) => (
                                            <SelectItem key={c.id} value={c.id}>{c.name}</SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>
                            </div>
                            {recomputeError && (
                                <p className="text-sm text-red-500">{recomputeError}</p>
                            )}
                            {recomputeResult && (
                                <div className="rounded-md bg-green-500/10 border border-green-500/30 p-3 text-sm space-y-1">
                                    <p className="font-semibold text-green-600">✅ เสร็จแล้ว</p>
                                    <p>สแกน: {recomputeResult.scanned} รายการ (เที่ยว + standby)</p>
                                    <p>
                                        อัปเดตสำเร็จ: <span className="font-bold">{recomputeResult.written}</span>
                                        {typeof recomputeResult.standbyWritten === "number" && (
                                            <span className="text-muted-foreground"> (standby {recomputeResult.standbyWritten})</span>
                                        )}
                                    </p>
                                    <p>ข้าม (ไม่มี rate card): {recomputeResult.skipped}</p>
                                    {(recomputeResult.blocked ?? 0) > 0 && (
                                        <p className="text-orange-600">
                                            ⚠ ไม่แก้ {recomputeResult.blocked} รายการ — งวดนั้นออกใบแจ้งหนี้ไปแล้ว
                                            {recomputeResult.blockedInvoices?.length
                                                ? ` (${recomputeResult.blockedInvoices.join(", ")})`
                                                : ""}
                                            {" "}ต้องยกเลิก/ออกใบลดหนี้ก่อน
                                        </p>
                                    )}
                                    {recomputeResult.failed > 0 && (
                                        <>
                                            <p className="text-red-500">ล้มเหลว: {recomputeResult.failed}</p>
                                            {recomputeResult.failures?.slice(0, 5).map((f) => (
                                                <p key={f.tripId} className="text-red-400 text-xs font-mono break-all">• {f.tripId}: {f.error ?? "unknown"}</p>
                                            ))}
                                        </>
                                    )}
                                    {(recomputeResult.eligible ?? 0) > recomputeResult.written && (
                                        <p className="text-amber-500">⚠️ มี trip เหลือ — กด Recompute อีกครั้ง</p>
                                    )}
                                </div>
                            )}
                        </div>
                        <DialogFooter>
                            <Button variant="outline" onClick={() => setRecomputeDialogOpen(false)}>ปิด</Button>
                            <Button onClick={handleRecomputeBilling} disabled={recomputeLoading || !recomputeFromDate || !recomputeToDate}>
                                {recomputeLoading ? <><Loader2 className="h-4 w-4 mr-2 animate-spin" />กำลังคำนวณ...</> : <><Calculator className="h-4 w-4 mr-2" />Recompute</>}
                            </Button>
                        </DialogFooter>
                    </DialogContent>
                </Dialog>
                <CardContent>
                    {canEdit && (
                    <Dialog
                        open={fuelAdjustmentDialogOpen}
                        onOpenChange={(open) => {
                            setFuelAdjustmentDialogOpen(open);
                            if (!open) {
                                handleCancelEditAdjustment();
                                setAdjustmentError(null);
                            }
                        }}
                    >
                        <DialogContent className="sm:max-w-xl">
                            <DialogHeader>
                                <DialogTitle>
                                    {t("accounting.rateCard.fuelAdjustments.dialog.createTitle")}
                                </DialogTitle>
                                <DialogDescription>
                                    {t("accounting.rateCard.fuelAdjustments.dialog.description")}
                                </DialogDescription>
                            </DialogHeader>
                            <div className="space-y-4 py-2">
                                {/* ── STEP 1: Customer ── */}
                                <div className="space-y-1.5">
                                    <p className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                                        {t("accounting.rateCard.fuelAdjustments.form.step1")}
                                    </p>
                                    <Select
                                        value={adjustmentForm.customerId}
                                        onValueChange={(v) =>
                                            setAdjustmentForm((prev) => ({ ...prev, customerId: v }))
                                        }
                                    >
                                        <SelectTrigger>
                                            <SelectValue
                                                placeholder={t("accounting.rateCard.import.selectCustomerPlaceholder")}
                                            />
                                        </SelectTrigger>
                                        <SelectContent className="z-1005" position="popper">
                                            {customers.map((c) => (
                                                <SelectItem key={c.id} value={c.id}>
                                                    {c.code} - {c.name}
                                                </SelectItem>
                                            ))}
                                        </SelectContent>
                                    </Select>
                                </div>

                                <div className="border-t" />

                                {/* ── STEP 2: Adjustment mode ── */}
                                <div className="space-y-2">
                                    <p className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                                        {t("accounting.rateCard.fuelAdjustments.form.step2")}
                                    </p>
                                    <div className="grid grid-cols-2 gap-2">
                                        <button
                                            type="button"
                                            onClick={() =>
                                                setAdjustmentForm((prev) => ({
                                                    ...prev,
                                                    adjustmentType: "percent",
                                                    addThbPerTrip: "0",
                                                    fuelBandEnabled: false,
                                                }))
                                            }
                                            className={`rounded-lg border-2 p-3 text-left transition-colors ${
                                                adjustmentForm.adjustmentType === "percent"
                                                    ? "border-primary bg-primary/5"
                                                    : "border-border bg-background hover:border-muted-foreground/40"
                                            }`}
                                        >
                                            <div className="font-semibold text-sm text-foreground">
                                                {t("accounting.rateCard.fuelAdjustments.form.adjustmentTypePercent")}
                                            </div>
                                            <div className="text-xs text-muted-foreground mt-0.5 font-mono">
                                                เรทใหม่ = เรทฐาน × (1 ± %)
                                            </div>
                                            <div className="text-xs text-muted-foreground mt-1">
                                                {t("accounting.rateCard.fuelAdjustments.form.adjustmentTypePercentHint")}
                                            </div>
                                        </button>
                                        <button
                                            type="button"
                                            onClick={() =>
                                                setAdjustmentForm((prev) => ({
                                                    ...prev,
                                                    adjustmentType: "fixed_thb",
                                                    ratePercent: "0",
                                                }))
                                            }
                                            className={`rounded-lg border-2 p-3 text-left transition-colors ${
                                                adjustmentForm.adjustmentType === "fixed_thb"
                                                    ? "border-primary bg-primary/5"
                                                    : "border-border bg-background hover:border-muted-foreground/40"
                                            }`}
                                        >
                                            <div className="font-semibold text-sm text-foreground">
                                                {t("accounting.rateCard.fuelAdjustments.form.adjustmentTypeFixed")}
                                            </div>
                                            <div className="text-xs text-muted-foreground mt-0.5 font-mono">
                                                เรทใหม่ = เรทฐาน ± ฿/เที่ยว
                                            </div>
                                            <div className="text-xs text-muted-foreground mt-1">
                                                {t("accounting.rateCard.fuelAdjustments.form.adjustmentTypeFixedHint")}
                                            </div>
                                        </button>
                                    </div>
                                </div>

                                <div className="border-t" />

                                {/* ── STEP 3: Date + Value ── */}
                                <div className="grid grid-cols-2 gap-3">
                                    <div className="space-y-1.5">
                                        <Label>{t("accounting.rateCard.fuelAdjustments.form.effectiveFrom")}</Label>
                                        <Input
                                            type="date"
                                            value={adjustmentForm.effectiveFrom}
                                            onChange={(e) =>
                                                setAdjustmentForm((prev) => ({
                                                    ...prev,
                                                    effectiveFrom: e.target.value,
                                                }))
                                            }
                                        />
                                    </div>
                                    <div className="space-y-1.5">
                                        {adjustmentForm.adjustmentType === "percent" ? (
                                            <>
                                                <Label>{t("accounting.rateCard.fuelAdjustments.form.multiplier")}</Label>
                                                <Input
                                                    type="number"
                                                    step="0.01"
                                                    value={adjustmentForm.ratePercent}
                                                    onChange={(e) =>
                                                        setAdjustmentForm((prev) => ({
                                                            ...prev,
                                                            ratePercent: e.target.value,
                                                        }))
                                                    }
                                                    placeholder={t("accounting.rateCard.fuelAdjustments.form.multiplierPlaceholder")}
                                                />
                                            </>
                                        ) : (
                                            <>
                                                <Label>{t("accounting.rateCard.fuelAdjustments.form.addPerTrip")}</Label>
                                                <Input
                                                    type="number"
                                                    step="0.01"
                                                    value={adjustmentForm.addThbPerTrip}
                                                    disabled={adjustmentForm.fuelBandEnabled}
                                                    onChange={(e) =>
                                                        setAdjustmentForm((prev) => ({
                                                            ...prev,
                                                            addThbPerTrip: e.target.value,
                                                        }))
                                                    }
                                                />
                                            </>
                                        )}
                                    </div>
                                </div>

                                {/* ── Fuel Band Calculator (fixed mode only) ── */}
                                {adjustmentForm.adjustmentType === "fixed_thb" && (
                                    <div className="rounded-md border bg-muted/30 p-3 space-y-2">
                                        <div className="flex items-center gap-2">
                                            <input
                                                type="checkbox"
                                                checked={adjustmentForm.fuelBandEnabled}
                                                onChange={(e) =>
                                                    setAdjustmentForm((prev) => ({
                                                        ...prev,
                                                        fuelBandEnabled: e.target.checked,
                                                    }))
                                                }
                                                className="h-4 w-4 rounded border-border"
                                                id="fuelBandToggle"
                                            />
                                            <Label htmlFor="fuelBandToggle" className="mb-0 cursor-pointer text-sm">
                                                {t("accounting.rateCard.fuelAdjustments.form.fuelBandToggle")}
                                            </Label>
                                        </div>
                                        <p className="text-xs text-muted-foreground">
                                            {t("accounting.rateCard.fuelAdjustments.form.fuelBandHelperNote")}
                                        </p>
                                        {adjustmentForm.fuelBandEnabled && (
                                            <div className="space-y-2 rounded-md border bg-background p-2 mt-1">
                                                <div className="grid grid-cols-2 gap-2">
                                                    <div>
                                                        <Label className="text-xs text-foreground" htmlFor="baselineFloor">
                                                            {t("accounting.rateCard.fuelAdjustments.form.fuelBandBaseline")}
                                                        </Label>
                                                        <Input
                                                            id="baselineFloor"
                                                            type="number"
                                                            step="1"
                                                            value={adjustmentForm.fuelBandBaselineFuelFloor}
                                                            onChange={(e) =>
                                                                setAdjustmentForm((prev) => ({
                                                                    ...prev,
                                                                    fuelBandBaselineFuelFloor: e.target.value,
                                                                }))
                                                            }
                                                            className="mt-1 text-sm"
                                                        />
                                                        <p className="text-xs text-muted-foreground mt-1">
                                                            {t("accounting.rateCard.fuelAdjustments.form.fuelBandBaselineHint")}
                                                        </p>
                                                    </div>
                                                    <div>
                                                        <Label className="text-xs text-foreground" htmlFor="thbPerBaht">
                                                            {t("accounting.rateCard.fuelAdjustments.form.fuelBandThbPerBaht")}
                                                        </Label>
                                                        <Input
                                                            id="thbPerBaht"
                                                            type="number"
                                                            step="0.5"
                                                            value={adjustmentForm.fuelBandThbPerBaht}
                                                            onChange={(e) =>
                                                                setAdjustmentForm((prev) => ({
                                                                    ...prev,
                                                                    fuelBandThbPerBaht: e.target.value,
                                                                }))
                                                            }
                                                            className="mt-1 text-sm"
                                                        />
                                                    </div>
                                                </div>
                                                {/* Band Preview Table */}
                                                {adjustmentForm.referenceFuelPrice && (
                                                    <div className="mt-2 text-xs">
                                                        <p className="font-semibold mb-1 text-foreground">
                                                            {t("accounting.rateCard.fuelAdjustments.form.fuelBandPreviewTitle")}
                                                        </p>
                                                        <div className="rounded border overflow-hidden">
                                                            {(() => {
                                                                const refPrice = parseFloat(adjustmentForm.referenceFuelPrice);
                                                                const baseline = parseInt(adjustmentForm.fuelBandBaselineFuelFloor) || 41;
                                                                const thbPerBaht = parseFloat(adjustmentForm.fuelBandThbPerBaht) || 10;
                                                                // (n, n+1] — see ADR 0009 §3. Math.floor put x.00 in the wrong row.
                                                                const currentBand = fuelBandRange(refPrice)?.lowerThb != null
                                                                    ? Math.round((fuelBandRange(refPrice)!.lowerThb - 0.01) * 100) / 100
                                                                    : Number.NaN;
                                                                const rows = [];
                                                                for (let i = -2; i <= 2; i++) {
                                                                    const band = baseline + i;
                                                                    const adj = i * thbPerBaht;
                                                                    const isCurrent = band === currentBand;
                                                                    rows.push(
                                                                        <div
                                                                            key={band}
                                                                            className={`flex justify-between px-2 py-1 border-b last:border-b-0 ${
                                                                                isCurrent
                                                                                    ? "bg-blue-500/10 dark:bg-blue-500/20"
                                                                                    : "bg-background"
                                                                            }`}
                                                                        >
                                                                            <span className={`text-foreground ${isCurrent ? "font-semibold text-blue-600 dark:text-blue-400" : ""}`}>
                                                                                {(band + 0.01).toFixed(2)}–{(band + 1).toFixed(2)} ฿/L
                                                                            </span>
                                                                            <span className={`${
                                                                                adj > 0 ? "text-green-600 dark:text-green-400" :
                                                                                adj < 0 ? "text-red-600 dark:text-red-400" :
                                                                                "text-foreground"
                                                                            } ${isCurrent ? "font-semibold" : ""}`}>
                                                                                {adj >= 0 ? "+" : ""}{adj} ฿
                                                                            </span>
                                                                            <span className="text-muted-foreground">
                                                                                {i === 0 ? `(${t("accounting.rateCard.fuelAdjustments.form.fuelBandPreviewBaseline")})` : ""}
                                                                                {isCurrent ? `(${t("accounting.rateCard.fuelAdjustments.form.fuelBandPreviewCurrent")})` : ""}
                                                                            </span>
                                                                        </div>
                                                                    );
                                                                }
                                                                return rows;
                                                            })()}
                                                        </div>
                                                    </div>
                                                )}
                                            </div>
                                        )}
                                    </div>
                                )}

                                <div className="border-t" />

                                {/* ── Reference price + Note ── */}
                                <div className="grid grid-cols-2 gap-3">
                                    <div className="space-y-1.5">
                                        <div className="flex flex-wrap items-end justify-between gap-1">
                                            <Label className="mb-0 text-sm">
                                                {t("accounting.rateCard.fuelAdjustments.form.referenceFuelPrice")}
                                            </Label>
                                            <Button
                                                type="button"
                                                variant="ghost"
                                                size="sm"
                                                className="h-auto shrink-0 px-2 py-0.5 text-xs text-muted-foreground"
                                                disabled={latestSyncedFleetFuel == null}
                                                onClick={() => {
                                                    if (latestSyncedFleetFuel == null) return;
                                                    setAdjustmentForm((prev) => ({
                                                        ...prev,
                                                        referenceFuelPrice: String(latestSyncedFleetFuel.price),
                                                    }));
                                                }}
                                            >
                                                {t("accounting.rateCard.fuelAdjustments.form.applyLatestSyncedPrice")}
                                            </Button>
                                        </div>
                                        <Input
                                            type="number"
                                            step="0.01"
                                            value={adjustmentForm.referenceFuelPrice}
                                            onChange={(e) =>
                                                setAdjustmentForm((prev) => ({
                                                    ...prev,
                                                    referenceFuelPrice: e.target.value,
                                                }))
                                            }
                                        />
                                        {/* The price of the round's OWN effective date (ADR 0009 §5). A missing
                                            day is stated plainly — never filled from another day. */}
                                        {dailyFuelState === "loading" && (
                                            <p className="text-xs text-muted-foreground leading-snug">
                                                {t("accounting.rateCard.fuelAdjustments.form.dailyPriceLoading")}
                                            </p>
                                        )}
                                        {dailyFuelState === "found" && dailyFuelPrice != null && (
                                            <p className="text-xs text-emerald-600 dark:text-emerald-400 leading-snug">
                                                {t("accounting.rateCard.fuelAdjustments.form.dailyPriceFound", {
                                                    date: adjustmentForm.effectiveFrom,
                                                    price: dailyFuelPrice.toLocaleString(),
                                                })}
                                            </p>
                                        )}
                                        {dailyFuelState === "missing" && (
                                            <p className="text-xs text-amber-600 dark:text-amber-400 leading-snug">
                                                {t("accounting.rateCard.fuelAdjustments.form.dailyPriceMissing", {
                                                    date: adjustmentForm.effectiveFrom,
                                                })}
                                            </p>
                                        )}
                                        {latestSyncedFleetFuel != null && (
                                            <p className="text-xs text-muted-foreground leading-snug">
                                                {t("accounting.rateCard.fuelAdjustments.form.referenceLatestHint", {
                                                    monthKey: latestSyncedFleetFuel.monthKey,
                                                    price: latestSyncedFleetFuel.price.toLocaleString(),
                                                })}
                                            </p>
                                        )}
                                    </div>
                                    <div className="space-y-1.5">
                                        <Label>{t("accounting.rateCard.fuelAdjustments.form.note")}</Label>
                                        <Input
                                            value={adjustmentForm.announcementNote}
                                            onChange={(e) =>
                                                setAdjustmentForm((prev) => ({
                                                    ...prev,
                                                    announcementNote: e.target.value,
                                                }))
                                            }
                                            placeholder={t("accounting.rateCard.fuelAdjustments.form.notePlaceholder")}
                                        />
                                    </div>
                                </div>

                                {adjustmentError && (
                                    <p className="text-sm text-destructive">{adjustmentError}</p>
                                )}
                            </div>
                            <DialogFooter>
                                <Button
                                    type="button"
                                    variant="outline"
                                    onClick={() => setFuelAdjustmentDialogOpen(false)}
                                    disabled={savingAdjustment}
                                >
                                    {t("accounting.rateCard.fuelAdjustments.dialog.cancel")}
                                </Button>
                                <Button
                                    type="button"
                                    onClick={() => void handleCreateAdjustment()}
                                    disabled={savingAdjustment}
                                >
                                    {savingAdjustment ? (
                                        <Loader2 className="h-4 w-4 mr-2 animate-spin" />
                                    ) : (
                                        <Plus className="h-4 w-4 mr-2" />
                                    )}
                                    {savingAdjustment
                                        ? t("accounting.rateCard.fuelAdjustments.form.saving")
                                        : t("accounting.rateCard.fuelAdjustments.form.save")}
                                </Button>
                            </DialogFooter>
                        </DialogContent>
                    </Dialog>
                    )}

                    <Table>
                        <TableHeader>
                            <TableRow>
                                <TableHead>{t("accounting.rateCard.fuelAdjustments.table.customer")}</TableHead>
                                <TableHead>{t("accounting.rateCard.fuelAdjustments.table.effectiveFrom")}</TableHead>
                                <TableHead className="text-right">{t("accounting.rateCard.fuelAdjustments.table.multiplier")}</TableHead>
                                <TableHead className="text-right">{t("accounting.rateCard.fuelAdjustments.table.addPerTrip")}</TableHead>
                                <TableHead className="text-right">{t("accounting.rateCard.fuelAdjustments.table.referencePrice")}</TableHead>
                                <TableHead>{t("accounting.rateCard.fuelAdjustments.table.createdAt")}</TableHead>
                                {canEdit && (
                                    <TableHead className="text-right">{t("accounting.rateCard.fuelAdjustments.table.actions")}</TableHead>
                                )}
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {filteredAdjustments.map((row) => (
                                // Voided rounds stay listed — they are part of the history — but are
                                // struck through, because pricing skips them (ADR 0009 §1).
                                <TableRow
                                    key={row.id}
                                    className={row.voided ? "opacity-50 line-through" : undefined}
                                >
                                    <TableCell>{customerOnlyNameById.get(row.customerId) ?? row.customerId}</TableCell>
                                    <TableCell>
                                        {format(row.effectiveFrom, "dd/MM/yyyy")}
                                        {row.voided && (
                                            <Badge variant="destructive" className="ml-2 no-underline">
                                                {t("accounting.rateCard.void.badge")}
                                            </Badge>
                                        )}
                                    </TableCell>
                                    <TableCell className="text-right">
                                        <div className="flex flex-col items-end gap-0.5">
                                            <span className="tabular-nums font-medium">
                                                {formatMultiplierTimes(row.rateMultiplier)}
                                            </span>
                                            <span className="text-xs text-muted-foreground">
                                                {multiplierToPercentText(row.rateMultiplier)}{" "}
                                                {t("accounting.rateCard.fuelAdjustments.table.multiplierFromBase")}
                                            </span>
                                        </div>
                                    </TableCell>
                                    <TableCell className="text-right">
                                        <div className="flex flex-col items-end gap-0.5">
                                            <span className="tabular-nums font-medium">
                                                {row.addThbPerTrip != null && row.addThbPerTrip !== 0
                                                    ? `${row.addThbPerTrip >= 0 ? "+" : ""}${row.addThbPerTrip.toFixed(2)}`
                                                    : "-"}
                                            </span>
                                            {row.fuelBandEnabled && (
                                                <span className="text-xs px-1.5 py-0.5 bg-blue-50 text-blue-600 rounded">
                                                    {t("accounting.rateCard.fuelAdjustments.table.bandBadge")}
                                                </span>
                                            )}
                                        </div>
                                    </TableCell>
                                    <TableCell className="text-right">
                                        {row.referenceFuelPriceThbPerLitre != null
                                            ? `฿${row.referenceFuelPriceThbPerLitre.toLocaleString()}`
                                            : "-"}
                                    </TableCell>
                                    <TableCell>{row.createdAt ? format(row.createdAt, "dd/MM/yyyy HH:mm") : "-"}</TableCell>
                                    {canEdit && (
                                        <TableCell className="text-right">
                                            <DropdownMenu>
                                                <DropdownMenuTrigger asChild>
                                                    <Button type="button" variant="ghost" size="icon">
                                                        <MoreHorizontal className="h-4 w-4" />
                                                    </Button>
                                                </DropdownMenuTrigger>
                                                <DropdownMenuContent align="end">
                                                    <DropdownMenuItem onClick={() => handleAnnounceLikeAdjustment(row)}>
                                                        <Copy className="h-4 w-4 mr-2" />
                                                        {t("accounting.rateCard.entry.announceCorrection")}
                                                    </DropdownMenuItem>
                                                    {!row.voided && (
                                                        <DropdownMenuItem
                                                            className="text-destructive focus:text-destructive"
                                                            onClick={() =>
                                                                setVoidTarget({
                                                                    kind: "adjustment",
                                                                    id: row.id,
                                                                    label: `${customerNameById.get(row.customerId) ?? row.customerId} · ${format(row.effectiveFrom, "dd/MM/yyyy")}`,
                                                                })
                                                            }
                                                        >
                                                            <Ban className="h-4 w-4 mr-2" />
                                                            {t("accounting.rateCard.void.action")}
                                                        </DropdownMenuItem>
                                                    )}
                                                </DropdownMenuContent>
                                            </DropdownMenu>
                                        </TableCell>
                                    )}
                                </TableRow>
                            ))}
                            {filteredAdjustments.length === 0 && (
                                <TableRow>
                                    <TableCell colSpan={canEdit ? 7 : 6} className="h-20 text-center text-muted-foreground">
                                        {t("accounting.rateCard.fuelAdjustments.noRows")}
                                    </TableCell>
                                </TableRow>
                            )}
                        </TableBody>
                    </Table>
                </CardContent>
            </Card>

            <Card>
                <CardHeader className="flex flex-row items-center justify-between space-y-0">
                    <div>
                        <CardTitle>{t("accounting.serviceFees.title")}</CardTitle>
                        <p className="text-sm text-muted-foreground mt-1">
                            {t("accounting.serviceFees.subtitle")}
                        </p>
                    </div>
                    {canEdit && (
                        <Button
                            type="button"
                            size="sm"
                            onClick={() => {
                                setEditingServiceFeeId(null);
                                setServiceFeeError(null);
                                setServiceFeeForm({
                                    customerId: "",
                                    feeType: "extra_stop",
                                    customTypeName: "",
                                    amountThb: "",
                                    unit: "per_stop",
                                    note: "",
                                });
                                setServiceFeeDialogOpen(true);
                            }}
                        >
                            <Plus className="h-4 w-4 mr-2" />
                            {t("accounting.serviceFees.addButton")}
                        </Button>
                    )}
                </CardHeader>
                <CardContent>
                    {canEdit && (
                        <Dialog
                            open={serviceFeeDialogOpen}
                            onOpenChange={(open) => {
                                setServiceFeeDialogOpen(open);
                                if (!open) {
                                    handleCancelEditServiceFee();
                                }
                            }}
                        >
                            <DialogContent className="sm:max-w-md">
                                <DialogHeader>
                                    <DialogTitle>
                                        {editingServiceFeeId
                                            ? t("accounting.serviceFees.dialog.edit")
                                            : t("accounting.serviceFees.dialog.create")}
                                    </DialogTitle>
                                </DialogHeader>
                                <div className="grid grid-cols-1 gap-3 py-2">
                                    <div className="space-y-1.5">
                                        <Label>{t("accounting.serviceFees.form.customer")}</Label>
                                        <Select
                                            value={serviceFeeForm.customerId}
                                            onValueChange={(v) =>
                                                setServiceFeeForm((prev) => ({ ...prev, customerId: v }))
                                            }
                                        >
                                            <SelectTrigger>
                                                <SelectValue placeholder={t("accounting.rateCard.import.selectCustomerPlaceholder")} />
                                            </SelectTrigger>
                                            <SelectContent className="z-[1005]" position="popper">
                                                {customers.map((c) => (
                                                    <SelectItem key={c.id} value={c.id}>
                                                        {c.code} - {c.name}
                                                    </SelectItem>
                                                ))}
                                            </SelectContent>
                                        </Select>
                                    </div>
                                    <div className="space-y-1.5">
                                        <Label>{t("accounting.serviceFees.form.feeType")}</Label>
                                        <Select
                                            value={serviceFeeForm.feeType}
                                            onValueChange={(v) =>
                                                setServiceFeeForm((prev) => ({ ...prev, feeType: v as ServiceFeeType }))
                                            }
                                        >
                                            <SelectTrigger>
                                                <SelectValue />
                                            </SelectTrigger>
                                            <SelectContent className="z-[1005]" position="popper">
                                                <SelectItem value="extra_stop">{t("accounting.serviceFees.type.extra_stop")}</SelectItem>
                                                <SelectItem value="waiting_time">{t("accounting.serviceFees.type.waiting_time")}</SelectItem>
                                                <SelectItem value="special_handling">{t("accounting.serviceFees.type.special_handling")}</SelectItem>
                                                <SelectItem value="service_charge">{t("accounting.serviceFees.type.service_charge")}</SelectItem>
                                                <SelectItem value="standby">{t("accounting.serviceFees.type.standby")}</SelectItem>
                                                <SelectItem value="custom">{t("accounting.serviceFees.type.custom")}</SelectItem>
                                            </SelectContent>
                                        </Select>
                                    </div>
                                    {serviceFeeForm.feeType === "custom" && (
                                        <div className="space-y-1.5">
                                            <Label>{t("accounting.serviceFees.form.customTypeName")}</Label>
                                            <Input
                                                value={serviceFeeForm.customTypeName}
                                                onChange={(e) =>
                                                    setServiceFeeForm((prev) => ({
                                                        ...prev,
                                                        customTypeName: e.target.value,
                                                    }))
                                                }
                                                placeholder={t("accounting.serviceFees.form.customTypeName")}
                                            />
                                        </div>
                                    )}
                                    <div className="space-y-1.5">
                                        <Label>{t("accounting.serviceFees.form.amount")}</Label>
                                        <Input
                                            type="number"
                                            step="0.01"
                                            min="0"
                                            value={serviceFeeForm.amountThb}
                                            onChange={(e) =>
                                                setServiceFeeForm((prev) => ({
                                                    ...prev,
                                                    amountThb: e.target.value,
                                                }))
                                            }
                                        />
                                    </div>
                                    <div className="space-y-1.5">
                                        <Label>{t("accounting.serviceFees.form.unit")}</Label>
                                        <Select
                                            value={serviceFeeForm.unit}
                                            onValueChange={(v) =>
                                                setServiceFeeForm((prev) => ({ ...prev, unit: v as ServiceFeeUnit }))
                                            }
                                        >
                                            <SelectTrigger>
                                                <SelectValue />
                                            </SelectTrigger>
                                            <SelectContent className="z-[1005]" position="popper">
                                                <SelectItem value="per_trip">{t("accounting.serviceFees.unit.per_trip")}</SelectItem>
                                                <SelectItem value="per_stop">{t("accounting.serviceFees.unit.per_stop")}</SelectItem>
                                            </SelectContent>
                                        </Select>
                                    </div>
                                    <div className="space-y-1.5">
                                        <Label>{t("accounting.serviceFees.form.note")}</Label>
                                        <Input
                                            value={serviceFeeForm.note}
                                            onChange={(e) =>
                                                setServiceFeeForm((prev) => ({
                                                    ...prev,
                                                    note: e.target.value,
                                                }))
                                            }
                                            placeholder={t("accounting.serviceFees.form.note")}
                                        />
                                    </div>
                                    {serviceFeeError && (
                                        <p className="text-sm text-destructive">{serviceFeeError}</p>
                                    )}
                                </div>
                                <DialogFooter>
                                    <Button
                                        type="button"
                                        variant="outline"
                                        onClick={() => setServiceFeeDialogOpen(false)}
                                        disabled={savingServiceFee}
                                    >
                                        {t("accounting.serviceFees.form.cancel")}
                                    </Button>
                                    <Button
                                        type="button"
                                        onClick={() => void handleCreateServiceFee()}
                                        disabled={savingServiceFee}
                                    >
                                        {savingServiceFee ? (
                                            <Loader2 className="h-4 w-4 mr-2 animate-spin" />
                                        ) : editingServiceFeeId ? (
                                            <Pencil className="h-4 w-4 mr-2" />
                                        ) : (
                                            <Plus className="h-4 w-4 mr-2" />
                                        )}
                                        {savingServiceFee
                                            ? t("accounting.serviceFees.form.saving")
                                            : editingServiceFeeId
                                              ? t("accounting.serviceFees.form.update")
                                              : t("accounting.serviceFees.form.save")}
                                    </Button>
                                </DialogFooter>
                            </DialogContent>
                        </Dialog>
                    )}

                    <Table>
                        <TableHeader>
                            <TableRow>
                                <TableHead>{t("accounting.serviceFees.table.customer")}</TableHead>
                                <TableHead>{t("accounting.serviceFees.table.feeType")}</TableHead>
                                <TableHead>{t("accounting.serviceFees.table.unit")}</TableHead>
                                <TableHead className="text-right">{t("accounting.serviceFees.table.amount")}</TableHead>
                                <TableHead>{t("accounting.serviceFees.table.note")}</TableHead>
                                {canEdit && (
                                    <TableHead className="text-right">{t("accounting.serviceFees.table.actions")}</TableHead>
                                )}
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {filteredServiceFees.map((row) => (
                                <TableRow key={row.id}>
                                    <TableCell>{customerOnlyNameById.get(row.customerId) ?? row.customerId}</TableCell>
                                    <TableCell>{getServiceFeeTypeLabel(row.feeType, row.customTypeName)}</TableCell>
                                    <TableCell>{t(`accounting.serviceFees.unit.${row.unit}`)}</TableCell>
                                    <TableCell className="text-right">฿{row.amountThb.toLocaleString()}</TableCell>
                                    <TableCell>{row.note || "—"}</TableCell>
                                    {canEdit && (
                                        <TableCell className="text-right">
                                            <DropdownMenu>
                                                <DropdownMenuTrigger asChild>
                                                    <Button
                                                        type="button"
                                                        variant="ghost"
                                                        size="icon"
                                                        disabled={deletingServiceFeeId === row.id}
                                                    >
                                                        {deletingServiceFeeId === row.id ? (
                                                            <Loader2 className="h-4 w-4 animate-spin" />
                                                        ) : (
                                                            <MoreHorizontal className="h-4 w-4" />
                                                        )}
                                                    </Button>
                                                </DropdownMenuTrigger>
                                                <DropdownMenuContent align="end">
                                                    <DropdownMenuItem onClick={() => handleEditServiceFee(row)}>
                                                        <Pencil className="h-4 w-4 mr-2" />
                                                        {t("accounting.serviceFees.table.edit")}
                                                    </DropdownMenuItem>
                                                    <DropdownMenuItem
                                                        className="text-destructive focus:text-destructive"
                                                        onClick={() => void handleDeleteServiceFee(row.id)}
                                                    >
                                                        <Trash2 className="h-4 w-4 mr-2" />
                                                        {t("accounting.serviceFees.table.delete")}
                                                    </DropdownMenuItem>
                                                </DropdownMenuContent>
                                            </DropdownMenu>
                                        </TableCell>
                                    )}
                                </TableRow>
                            ))}
                            {filteredServiceFees.length === 0 && (
                                <TableRow>
                                    <TableCell colSpan={canEdit ? 6 : 5} className="h-20 text-center text-muted-foreground">
                                        {t("accounting.serviceFees.noRows")}
                                    </TableCell>
                                </TableRow>
                            )}
                        </TableBody>
                    </Table>
                </CardContent>
            </Card>

            {/* ── Standby Rates ──────────────────────────────────────────────────────── */}
            <Card>
                <CardHeader className="flex flex-col gap-3 space-y-0 sm:flex-row sm:items-start sm:justify-between">
                    <div className="space-y-1">
                        <CardTitle>{t("accounting.standbyRates.title", "Standby Rates")}</CardTitle>
                        <p className="text-sm text-muted-foreground">
                            {t("accounting.standbyRates.subtitle", "Fixed rate per standby event, per customer")}
                        </p>
                    </div>
                    {canEdit && (
                        <Button
                            size="sm"
                            className="shrink-0"
                            onClick={() => {
                                setEditingStandbyRateId(null);
                                setStandbyRateForm({ customerId: filterCustomerId !== "all" ? filterCustomerId : "", rateThb: "", effectiveFrom: format(new Date(), "yyyy-MM-dd"), note: "" });
                                setStandbyRateError(null);
                                setStandbyRateDialogOpen(true);
                            }}
                        >
                            <Plus className="h-4 w-4 mr-2" />
                            {t("accounting.standbyRates.addButton", "Add Rate")}
                        </Button>
                    )}
                </CardHeader>
                <CardContent>
                    <div className="rounded-md border overflow-x-auto">
                        <Table>
                            <TableHeader>
                                <TableRow>
                                    <TableHead>{t("accounting.standbyRates.table.customer", "Customer")}</TableHead>
                                    <TableHead className="text-right">{t("accounting.standbyRates.table.rate", "Rate (THB/event)")}</TableHead>
                                    <TableHead>{t("accounting.standbyRates.table.effectiveFrom", "Effective From")}</TableHead>
                                    <TableHead>{t("accounting.standbyRates.table.note", "Note")}</TableHead>
                                    {canEdit && <TableHead className="text-right">{t("accounting.standbyRates.table.actions", "Actions")}</TableHead>}
                                </TableRow>
                            </TableHeader>
                            <TableBody>
                                {standbyRates.map((row) => (
                                    <TableRow key={row.id}>
                                        <TableCell className="font-medium">
                                            {customerNameById.get(row.customerId) ?? row.customerId}
                                        </TableCell>
                                        <TableCell className="text-right font-mono">
                                            ฿{row.rateThb.toLocaleString()}
                                        </TableCell>
                                        <TableCell className="text-sm text-muted-foreground">
                                            {format(row.effectiveFrom, "dd/MM/yyyy")}
                                        </TableCell>
                                        <TableCell className="text-sm text-muted-foreground">
                                            {row.note ?? "—"}
                                        </TableCell>
                                        {canEdit && (
                                            <TableCell className="text-right">
                                                <DropdownMenu>
                                                    <DropdownMenuTrigger asChild>
                                                        <Button variant="ghost" size="icon" className="h-8 w-8" disabled={deletingStandbyRateId === row.id}>
                                                            {deletingStandbyRateId === row.id
                                                                ? <Loader2 className="h-4 w-4 animate-spin" />
                                                                : <MoreHorizontal className="h-4 w-4" />}
                                                        </Button>
                                                    </DropdownMenuTrigger>
                                                    <DropdownMenuContent align="end">
                                                        <DropdownMenuItem onClick={() => handleEditStandbyRate(row)}>
                                                            <Pencil className="h-4 w-4 mr-2" />
                                                            {t("accounting.standbyRates.table.edit", "Edit")}
                                                        </DropdownMenuItem>
                                                        <DropdownMenuItem
                                                            className="text-destructive focus:text-destructive"
                                                            onClick={() => void handleDeleteStandbyRate(row.id)}
                                                        >
                                                            <Trash2 className="h-4 w-4 mr-2" />
                                                            {t("accounting.standbyRates.table.delete", "Delete")}
                                                        </DropdownMenuItem>
                                                    </DropdownMenuContent>
                                                </DropdownMenu>
                                            </TableCell>
                                        )}
                                    </TableRow>
                                ))}
                                {standbyRates.length === 0 && (
                                    <TableRow>
                                        <TableCell colSpan={canEdit ? 5 : 4} className="h-20 text-center text-muted-foreground">
                                            {t("accounting.standbyRates.noRows", "No standby rates configured")}
                                        </TableCell>
                                    </TableRow>
                                )}
                            </TableBody>
                        </Table>
                    </div>
                </CardContent>
            </Card>

            {/* Standby Rate Dialog */}
            <Dialog open={standbyRateDialogOpen} onOpenChange={(open) => { if (!open) { setStandbyRateDialogOpen(false); setEditingStandbyRateId(null); } }}>
                <DialogContent className="max-w-md">
                    <DialogHeader>
                        <DialogTitle>
                            {editingStandbyRateId
                                ? t("accounting.standbyRates.dialog.editTitle", "Edit Standby Rate")
                                : t("accounting.standbyRates.dialog.addTitle", "Add Standby Rate")}
                        </DialogTitle>
                        <DialogDescription>
                            {t("accounting.standbyRates.dialog.description", "Fixed rate charged per standby event for this customer")}
                        </DialogDescription>
                    </DialogHeader>
                    <div className="space-y-4 py-2">
                        <div className="space-y-1.5">
                            <Label>{t("accounting.standbyRates.form.customer", "Customer")}</Label>
                            <Combobox
                                options={customers.map((c): ComboboxOption => ({ value: c.id, label: `${c.code} - ${c.name}` }))}
                                value={standbyRateForm.customerId}
                                onSelect={(v: string) => setStandbyRateForm((p) => ({ ...p, customerId: v }))}
                                placeholder={t("accounting.standbyRates.form.customerPlaceholder", "Select customer...")}
                            />
                        </div>
                        <div className="space-y-1.5">
                            <Label>{t("accounting.standbyRates.form.rate", "Rate per event (THB)")}</Label>
                            <Input
                                type="number"
                                min={0}
                                step={1}
                                placeholder="เช่น 500"
                                value={standbyRateForm.rateThb}
                                onChange={(e) => setStandbyRateForm((p) => ({ ...p, rateThb: e.target.value }))}
                            />
                        </div>
                        <div className="space-y-1.5">
                            <Label>{t("accounting.standbyRates.form.effectiveFrom", "Effective From (dd/MM/yyyy)")}</Label>
                            <Input
                                type="date"
                                value={standbyRateForm.effectiveFrom}
                                onChange={(e) => setStandbyRateForm((p) => ({ ...p, effectiveFrom: e.target.value }))}
                            />
                        </div>
                        <div className="space-y-1.5">
                            <Label>{t("accounting.standbyRates.form.note", "Note (optional)")}</Label>
                            <Input
                                placeholder={t("accounting.standbyRates.form.notePlaceholder", "e.g. Updated Q3 2026")}
                                value={standbyRateForm.note}
                                onChange={(e) => setStandbyRateForm((p) => ({ ...p, note: e.target.value }))}
                            />
                        </div>
                        {standbyRateError && <p className="text-sm text-destructive">{standbyRateError}</p>}
                    </div>
                    <DialogFooter>
                        <Button variant="outline" onClick={() => { setStandbyRateDialogOpen(false); setEditingStandbyRateId(null); }}>
                            {t("accounting.detail.close")}
                        </Button>
                        <Button onClick={() => void handleSaveStandbyRate()} disabled={savingStandbyRate}>
                            {savingStandbyRate && <Loader2 className="h-4 w-4 mr-2 animate-spin" />}
                            {t("accounting.detail.save")}
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>

            <Card>
                <CardHeader className="flex flex-col gap-3 space-y-0 sm:flex-row sm:items-start sm:justify-between">
                    <div className="space-y-1">
                        <CardTitle>{t("accounting.rateCard.fuelMonthly.title")}</CardTitle>
                        <p className="text-sm text-muted-foreground">{t("accounting.rateCard.fuelMonthly.subtitle")}</p>
                        {fuelMonthlySyncMessage && (
                            <p
                                className={
                                    fuelMonthlySyncIsError ? "text-sm text-destructive" : "text-sm text-muted-foreground"
                                }
                            >
                                {fuelMonthlySyncMessage}
                            </p>
                        )}
                    </div>
                    {canEdit && (
                        <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            className="shrink-0"
                            disabled={syncingFuelSnapshot || loading}
                            onClick={() => void handleSyncBangchakFuelSnapshot()}
                        >
                            {syncingFuelSnapshot ? (
                                <Loader2 className="h-4 w-4 mr-2 animate-spin" />
                            ) : (
                                <RefreshCw className="h-4 w-4 mr-2" />
                            )}
                            {t("accounting.rateCard.fuelMonthly.syncButton")}
                        </Button>
                    )}
                </CardHeader>
                <CardContent className="space-y-3">
                    <p className="text-xs text-muted-foreground leading-relaxed">
                        {t("accounting.rateCard.fuelMonthly.tableCaption")}
                    </p>
                    <div className="rounded-md border overflow-x-auto">
                        <Table>
                            <TableHeader>
                                <TableRow>
                                    <TableHead className="min-w-36">{t("accounting.rateCard.fuelMonthly.table.period")}</TableHead>
                                    <TableHead className="w-26">{t("accounting.rateCard.fuelMonthly.table.status")}</TableHead>
                                    <TableHead className="min-w-40">{t("accounting.rateCard.fuelMonthly.table.savedAt")}</TableHead>
                                    <TableHead className="min-w-44">{t("accounting.rateCard.fuelMonthly.table.dieselRetail")}</TableHead>
                                    <TableHead className="text-right min-w-28 whitespace-normal">
                                        {t("accounting.rateCard.fuelMonthly.table.fuelTypesCount")}
                                    </TableHead>
                                    <TableHead className="min-w-32">{t("accounting.rateCard.fuelMonthly.table.source")}</TableHead>
                                </TableRow>
                            </TableHeader>
                            <TableBody>
                                {fuelSnapshots.map((snap) => {
                                    const period = formatFuelMonthPeriod(snap.monthKey, language);
                                    const fetchedParsed = snap.fetchedAtIso ? parseISO(snap.fetchedAtIso) : undefined;
                                    const bangchakTime =
                                        fetchedParsed && isValid(fetchedParsed)
                                            ? format(fetchedParsed, "dd/MM/yyyy HH:mm")
                                            : null;
                                    const dieselLine = formatDieselRetailLine(snap);
                                    const showError = snap.status === "error";

                                    return (
                                        <TableRow key={snap.id}>
                                            <TableCell>
                                                <span className="block font-medium">{period.label}</span>
                                                <span className="font-mono text-xs text-muted-foreground">{period.key}</span>
                                            </TableCell>
                                            <TableCell>
                                                {showError ? (
                                                    <Badge variant="destructive">{t("accounting.rateCard.fuelMonthly.table.statusError")}</Badge>
                                                ) : (
                                                    <Badge variant="secondary" className="font-normal">
                                                        {t("accounting.rateCard.fuelMonthly.table.statusOk")}
                                                    </Badge>
                                                )}
                                            </TableCell>
                                            <TableCell>
                                                <div className="space-y-1 text-sm">
                                                    <div>
                                                        {snap.capturedAt
                                                            ? format(snap.capturedAt, "dd/MM/yyyy HH:mm")
                                                            : "—"}
                                                    </div>
                                                    {bangchakTime && (
                                                        <div className="text-xs text-muted-foreground leading-snug">
                                                            {t("accounting.rateCard.fuelMonthly.bangchakPageTime", {
                                                                time: bangchakTime,
                                                            })}
                                                        </div>
                                                    )}
                                                </div>
                                            </TableCell>
                                            <TableCell
                                                className={
                                                    showError ? "text-destructive text-sm max-w-[min(22rem,55vw)]" : "text-sm"
                                                }
                                                title={showError ? dieselLine : undefined}
                                            >
                                                {showError ? dieselLine || t("accounting.rateCard.fuelMonthly.error") : dieselLine}
                                            </TableCell>
                                            <TableCell className="text-right tabular-nums">{snap.items.length}</TableCell>
                                            <TableCell className="text-sm text-muted-foreground">
                                                {fuelSnapshotSourceLabel(snap.source, t)}
                                            </TableCell>
                                        </TableRow>
                                    );
                                })}
                                {fuelSnapshots.length === 0 && (
                                    <TableRow>
                                        <TableCell colSpan={6} className="h-20 text-center text-muted-foreground">
                                            {t("accounting.rateCard.fuelMonthly.noRows")}
                                        </TableCell>
                                    </TableRow>
                                )}
                            </TableBody>
                        </Table>
                    </div>
                </CardContent>
            </Card>

            <RateCardImportDialog
                open={importOpen}
                onOpenChange={setImportOpen}
                customers={customers}
                initialCustomerId={filterCustomerId !== "all" ? filterCustomerId : undefined}
                knownHubIds={hubs.map((h) => h.id)}
                onImported={() => void loadData()}
            />

            {/* Void announcement dialog — ADR 0009 §1: rows are immutable, so retiring one is
                the only destructive action, and it must say why. */}
            <Dialog
                open={voidTarget !== null}
                onOpenChange={(open) => {
                    if (!open) {
                        setVoidTarget(null);
                        setVoidReason("");
                        setVoidError(null);
                    }
                }}
            >
                <DialogContent>
                    <DialogHeader>
                        <DialogTitle>{t("accounting.rateCard.void.title")}</DialogTitle>
                        <DialogDescription>{t("accounting.rateCard.void.description")}</DialogDescription>
                    </DialogHeader>
                    <div className="grid gap-4 py-2">
                        <div className="rounded-md border bg-muted/40 px-3 py-2 text-sm font-mono">
                            {voidTarget?.label}
                        </div>
                        <div className="space-y-1.5">
                            <Label>{t("accounting.rateCard.void.reasonLabel")}</Label>
                            <Input
                                value={voidReason}
                                onChange={(e) => setVoidReason(e.target.value)}
                                placeholder={t("accounting.rateCard.void.reasonPlaceholder")}
                            />
                        </div>
                        {voidError && (
                            <div className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
                                {voidError}
                            </div>
                        )}
                    </div>
                    <DialogFooter>
                        <Button variant="outline" onClick={() => setVoidTarget(null)}>
                            {t("accounting.detail.close")}
                        </Button>
                        <Button
                            variant="destructive"
                            onClick={() => void handleConfirmVoid()}
                            disabled={voidSaving || !voidReason.trim()}
                        >
                            {voidSaving && <Loader2 className="h-4 w-4 mr-2 animate-spin" />}
                            {t("accounting.rateCard.void.confirm")}
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </div>
    );
}
