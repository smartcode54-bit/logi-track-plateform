"use client";

import { useEffect, useMemo, useState } from "react";
import { format, isValid, parseISO } from "date-fns";
import { enUS, th as thDateLocale } from "date-fns/locale";
import * as XLSX from "xlsx";
import {
    Calculator,
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
    deleteCustomerFuelRateAdjustment,
    deleteCustomerRateEntry,
    getCustomerFuelRateAdjustments,
    getCustomerRateEntries,
    getFuelMonthlySnapshots,
    updateCustomerFuelRateAdjustment,
    updateCustomerRateEntry,
    type CustomerFuelRateAdjustmentRow,
    type CustomerRateEntryRow,
    type FuelMonthlySnapshotRow,
} from "../actions.client";
import { RateCardImportDialog, type RateCardCustomerOption } from "../rate-card-import-dialog";
import { db, functions } from "@/firebase/client";
import { httpsCallable } from "firebase/functions";
import { collection, getDocs } from "firebase/firestore";
import { COLLECTIONS } from "@/lib/collections";
import {
    latestFleetDieselFromMonthlySnapshots,
    pickFleetBangchakDieselReference,
} from "@/lib/bangchakFleetReferenceFuel";
import { SOC_DESTINATIONS } from "@/validate/taskSchema";
import {
    computeFinalRateThb,
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

export default function AccountingRateCardPage() {
    const { t, language } = useLanguage();
    const { hasPermission: canEdit } = usePermission(CAPABILITIES.accounting_edit_rate_card);
    const [loading, setLoading] = useState(true);
    const [importOpen, setImportOpen] = useState(false);
    const [customers, setCustomers] = useState<RateCardCustomerOption[]>([]);
    const [hubs, setHubs] = useState<HubOption[]>([]);
    const [truckTypes, setTruckTypes] = useState<string[]>([]);
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
    const [deletingAdjustmentId, setDeletingAdjustmentId] = useState<string | null>(null);
    const [editingAdjustmentId, setEditingAdjustmentId] = useState<string | null>(null);
    const [manualRateError, setManualRateError] = useState<string | null>(null);
    const [savingManualRate, setSavingManualRate] = useState(false);
    const [manualRateForm, setManualRateForm] = useState({
        customerId: "",
        hubId: "",
        destinationCode: "",
        vehicleClass: "4WJ",
        rateThb: "",
        distanceKm: "",
        effectiveFrom: "",
    });
    const [adjustmentForm, setAdjustmentForm] = useState({
        customerId: "",
        effectiveFrom: "",
        ratePercent: "0",
        addThbPerTrip: "0",
        referenceFuelPrice: "",
        announcementNote: "",
    });
    // Rate entry edit/delete state
    const [editingEntryId, setEditingEntryId] = useState<string | null>(null);
    const [editEntryForm, setEditEntryForm] = useState({
        rateThb: "",
        distanceKm: "",
        vehicleClass: "",
        effectiveFrom: "",
    });
    const [savingEntry, setSavingEntry] = useState(false);
    const [deletingEntryId, setDeletingEntryId] = useState<string | null>(null);

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
            const [customerRows, entryRows, fuelRows, snapshotRows, hubRows, truckRows] = await Promise.all([
                getCustomers(),
                getCustomerRateEntries(),
                getCustomerFuelRateAdjustments(),
                getFuelMonthlySnapshots(36),
                getDocs(collection(db, COLLECTIONS.HUBS)),
                getDocs(collection(db, COLLECTIONS.TRUCKS)),
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
            setHubs(
                hubRows.docs.map((d) => {
                    const data = d.data();
                    const id = String(data.hubId ?? data.source_id ?? "").trim().toUpperCase();
                    const name = String(data.source_name_en ?? data.source_name_th ?? data.hubName ?? "").trim();
                    return { id, name: name || undefined };
                }).filter((x) => x.id)
            );
            setTruckTypes(
                Array.from(
                    new Set(
                        truckRows.docs
                            .map((d) => String(d.data().type ?? "").trim())
                            .filter(Boolean)
                    )
                ).sort((a, b) => a.localeCompare(b))
            );
        } finally {
            setLoading(false);
        }
    };

    useEffect(() => {
        void loadData();
    }, []);

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
            list = list.filter((e) => e.vehicleClass === filterVehicleClass);
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
    /** When a customer is chosen: only hubId / destinationCode seen for that customer; if none yet, full master list. */
    const manualLocationOptions = useMemo(() => {
        const cid = manualRateForm.customerId.trim();
        if (!cid) return rateCardLocationOptions;
        const scoped = new Set<string>();
        for (const e of entries) {
            if (e.customerId !== cid) continue;
            scoped.add(e.hubId);
            scoped.add(e.destinationCode);
        }
        if (scoped.size === 0) return rateCardLocationOptions;
        return Array.from(scoped).sort((a, b) => a.localeCompare(b));
    }, [manualRateForm.customerId, entries, rateCardLocationOptions]);
    const filterLocationOptions = useMemo(() => {
        if (filterCustomerId === "all") return rateCardLocationOptions;
        const scoped = new Set<string>();
        for (const e of entries) {
            if (e.customerId !== filterCustomerId) continue;
            scoped.add(e.hubId);
            scoped.add(e.destinationCode);
        }
        if (scoped.size === 0) return rateCardLocationOptions;
        return Array.from(scoped).sort((a, b) => a.localeCompare(b));
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
        () => Array.from(new Set(entries.map((e) => e.vehicleClass))).sort((a, b) => a.localeCompare(b)),
        [entries]
    );
    const manualVehicleClassOptions = useMemo(() => {
        const options = truckTypes.length > 0 ? truckTypes : vehicleOptions;
        if (options.length === 0) return ["4WJ"];
        return options;
    }, [truckTypes, vehicleOptions]);
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
        const ratePercent = Number(adjustmentForm.ratePercent);
        const referenceFuelPrice = adjustmentForm.referenceFuelPrice
            ? Number(adjustmentForm.referenceFuelPrice)
            : undefined;
        const rateMultiplier = 1 + ratePercent / 100;
        const addThbPerTrip = Number(adjustmentForm.addThbPerTrip);

        if (
            !customerId ||
            !effectiveFrom ||
            !Number.isFinite(ratePercent) ||
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
            };
            if (editingAdjustmentId) {
                await updateCustomerFuelRateAdjustment(editingAdjustmentId, payload);
            } else {
                await createCustomerFuelRateAdjustment(payload);
            }
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

    const handleDeleteAdjustment = async (id: string) => {
        if (!canEdit) return;
        setAdjustmentError(null);
        setDeletingAdjustmentId(id);
        try {
            await deleteCustomerFuelRateAdjustment(id);
            await loadData();
        } catch (err) {
            console.error(err);
            setAdjustmentError(t("accounting.rateCard.fuelAdjustments.form.deleteError"));
        } finally {
            setDeletingAdjustmentId(null);
        }
    };

    const handleEditAdjustment = (row: CustomerFuelRateAdjustmentRow) => {
        const effectiveFrom = format(row.effectiveFrom, "yyyy-MM-dd");
        const ratePercent = ((row.rateMultiplier - 1) * 100).toFixed(2);
        const latest = latestFleetDieselFromMonthlySnapshots(fuelSnapshots);
        setEditingAdjustmentId(row.id);
        setAdjustmentError(null);
        setAdjustmentForm({
            customerId: row.customerId,
            effectiveFrom,
            ratePercent,
            addThbPerTrip: row.addThbPerTrip != null ? String(row.addThbPerTrip) : "0",
            referenceFuelPrice:
                latest != null
                    ? String(latest.price)
                    : row.referenceFuelPriceThbPerLitre != null
                      ? String(row.referenceFuelPriceThbPerLitre)
                      : "",
            announcementNote: row.announcementNote ?? "",
        });
        setFuelAdjustmentDialogOpen(true);
    };

    const handleCancelEditAdjustment = () => {
        setEditingAdjustmentId(null);
        setAdjustmentError(null);
        setAdjustmentForm({
            customerId: "",
            effectiveFrom: "",
            ratePercent: "0",
            addThbPerTrip: "0",
            referenceFuelPrice: "",
            announcementNote: "",
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
            }));
            await loadData();
        } catch (err) {
            console.error(err);
            setManualRateError(t("accounting.rateCard.manualAdd.error"));
        } finally {
            setSavingManualRate(false);
        }
    };

    const handleDeleteEntry = async (id: string) => {
        if (!confirm(t("accounting.rateCard.entry.confirmDelete"))) return;
        setDeletingEntryId(id);
        try {
            await deleteCustomerRateEntry(id);
            await loadData();
        } catch (err) {
            console.error(err);
        } finally {
            setDeletingEntryId(null);
        }
    };

    const handleStartEditEntry = (entry: CustomerRateEntryRow) => {
        setEditingEntryId(entry.id);
        setEditEntryForm({
            rateThb: String(entry.rateThb),
            distanceKm: entry.distanceKm != null ? String(entry.distanceKm) : "",
            vehicleClass: entry.vehicleClass,
            effectiveFrom: format(entry.effectiveFrom, "yyyy-MM-dd"),
        });
    };

    const handleSaveEntry = async () => {
        if (!editingEntryId) return;
        setSavingEntry(true);
        try {
            await updateCustomerRateEntry(editingEntryId, {
                rateThb: Number(editEntryForm.rateThb),
                distanceKm: editEntryForm.distanceKm ? Number(editEntryForm.distanceKm) : undefined,
                vehicleClass: editEntryForm.vehicleClass,
                effectiveFrom: parseDateInput(editEntryForm.effectiveFrom) ?? undefined,
            });
            setEditingEntryId(null);
            await loadData();
        } catch (err) {
            console.error(err);
        } finally {
            setSavingEntry(false);
        }
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
                    <Button variant="outline" onClick={() => void loadData()}>
                        <RefreshCw className="h-4 w-4 mr-2" />
                        {t("common.refresh")}
                    </Button>
                    {canEdit && (
                        <Button onClick={() => setImportOpen(true)}>
                            <Upload className="h-4 w-4 mr-2" />
                            {t("accounting.rateCard.importButton")}
                        </Button>
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
                                            {vehicleClass}
                                        </SelectItem>
                                    ))}
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
                                                {vehicleClass}
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
                                <TableRow key={row.id}>
                                    <TableCell>{customerNameById.get(row.customerId) ?? row.customerId}</TableCell>
                                    <TableCell>{formatSource(row.hubId)}</TableCell>
                                    <TableCell>{formatDestination(row.destinationCode)}</TableCell>
                                    <TableCell className="font-mono text-xs">{row.vehicleClass}</TableCell>
                                    <TableCell className="text-right">฿{row.rateThb.toLocaleString()}</TableCell>
                                    {showRateCalPreview && (
                                        <TableCell className="text-right">
                                            {newRate != null && ruleDoc != null ? (
                                                <span
                                                    title={t("accounting.rateCard.calcPreview.rowTooltip", {
                                                        times: formatMultiplierTimes(ruleDoc.rateMultiplier),
                                                        percent: multiplierToPercentText(ruleDoc.rateMultiplier),
                                                        delta: formatSignedThbDelta(row.rateThb, newRate),
                                                    })}
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
                                                    <Button
                                                        variant="ghost"
                                                        size="icon"
                                                        className="h-8 w-8"
                                                        disabled={deletingEntryId === row.id}
                                                    >
                                                        {deletingEntryId === row.id ? (
                                                            <Loader2 className="h-4 w-4 animate-spin" />
                                                        ) : (
                                                            <MoreHorizontal className="h-4 w-4" />
                                                        )}
                                                    </Button>
                                                </DropdownMenuTrigger>
                                                <DropdownMenuContent align="end">
                                                    <DropdownMenuItem onClick={() => handleStartEditEntry(row)}>
                                                        <Pencil className="h-4 w-4 mr-2" />
                                                        {t("accounting.rateCard.entry.edit")}
                                                    </DropdownMenuItem>
                                                    <DropdownMenuItem
                                                        onClick={() => void handleDeleteEntry(row.id)}
                                                        className="text-destructive focus:text-destructive"
                                                    >
                                                        <Trash2 className="h-4 w-4 mr-2" />
                                                        {t("accounting.rateCard.entry.delete")}
                                                    </DropdownMenuItem>
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
                                        colSpan={showRateCalPreview ? (canEdit ? 10 : 9) : (canEdit ? 9 : 8)}
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

            <Card>
                <CardHeader className="flex flex-row items-center justify-between space-y-0">
                    <div>
                        <CardTitle>{t("accounting.rateCard.fuelAdjustments.title")}</CardTitle>
                        <p className="text-sm text-muted-foreground mt-1">
                            {t("accounting.rateCard.fuelAdjustments.subtitle")}
                        </p>
                    </div>
                    {canEdit && (
                        <Button
                            type="button"
                            size="sm"
                            onClick={() => {
                                const latest = latestFleetDieselFromMonthlySnapshots(fuelSnapshots);
                                setEditingAdjustmentId(null);
                                setAdjustmentError(null);
                                setAdjustmentForm({
                                    customerId: "",
                                    effectiveFrom: "",
                                    ratePercent: "0",
                                    addThbPerTrip: "0",
                                    referenceFuelPrice: latest != null ? String(latest.price) : "",
                                    announcementNote: "",
                                });
                                setFuelAdjustmentDialogOpen(true);
                            }}
                        >
                            <Plus className="h-4 w-4 mr-2" />
                            {t("accounting.rateCard.fuelAdjustments.addRule")}
                        </Button>
                    )}
                </CardHeader>
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
                                    {editingAdjustmentId
                                        ? t("accounting.rateCard.fuelAdjustments.dialog.editTitle")
                                        : t("accounting.rateCard.fuelAdjustments.dialog.createTitle")}
                                </DialogTitle>
                                <DialogDescription>
                                    {t("accounting.rateCard.fuelAdjustments.dialog.description")}
                                </DialogDescription>
                            </DialogHeader>
                            <div className="grid grid-cols-1 gap-3 py-2 sm:grid-cols-2">
                                <div className="space-y-1.5 sm:col-span-2">
                                    <Label>{t("accounting.rateCard.fuelAdjustments.form.customer")}</Label>
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
                                        placeholder={t(
                                            "accounting.rateCard.fuelAdjustments.form.multiplierPlaceholder"
                                        )}
                                    />
                                </div>
                                <div className="space-y-1.5">
                                    <Label>{t("accounting.rateCard.fuelAdjustments.form.addPerTrip")}</Label>
                                    <Input
                                        type="number"
                                        step="0.01"
                                        value={adjustmentForm.addThbPerTrip}
                                        onChange={(e) =>
                                            setAdjustmentForm((prev) => ({
                                                ...prev,
                                                addThbPerTrip: e.target.value,
                                            }))
                                        }
                                    />
                                </div>
                                <div className="space-y-1.5">
                                    <div className="flex flex-wrap items-end justify-between gap-2">
                                        <Label className="mb-0">
                                            {t("accounting.rateCard.fuelAdjustments.form.referenceFuelPrice")}
                                        </Label>
                                        <Button
                                            type="button"
                                            variant="ghost"
                                            size="sm"
                                            className="h-auto shrink-0 px-2 py-1 text-xs text-muted-foreground"
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
                                    {latestSyncedFleetFuel != null && (
                                        <p className="text-xs text-muted-foreground leading-snug">
                                            {t("accounting.rateCard.fuelAdjustments.form.referenceLatestHint", {
                                                monthKey: latestSyncedFleetFuel.monthKey,
                                                price: latestSyncedFleetFuel.price.toLocaleString(),
                                            })}
                                        </p>
                                    )}
                                </div>
                                <div className="space-y-1.5 sm:col-span-2">
                                    <Label>{t("accounting.rateCard.fuelAdjustments.form.note")}</Label>
                                    <Input
                                        value={adjustmentForm.announcementNote}
                                        onChange={(e) =>
                                            setAdjustmentForm((prev) => ({
                                                ...prev,
                                                announcementNote: e.target.value,
                                            }))
                                        }
                                        placeholder={t(
                                            "accounting.rateCard.fuelAdjustments.form.notePlaceholder"
                                        )}
                                    />
                                </div>
                                {adjustmentError && (
                                    <p className="sm:col-span-2 text-sm text-destructive">{adjustmentError}</p>
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
                                    ) : editingAdjustmentId ? (
                                        <Pencil className="h-4 w-4 mr-2" />
                                    ) : (
                                        <Plus className="h-4 w-4 mr-2" />
                                    )}
                                    {savingAdjustment
                                        ? t("accounting.rateCard.fuelAdjustments.form.saving")
                                        : editingAdjustmentId
                                          ? t("accounting.rateCard.fuelAdjustments.form.update")
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
                                <TableHead className="text-right">{t("accounting.rateCard.fuelAdjustments.table.referencePrice")}</TableHead>
                                <TableHead>{t("accounting.rateCard.fuelAdjustments.table.createdAt")}</TableHead>
                                {canEdit && (
                                    <TableHead className="text-right">{t("accounting.rateCard.fuelAdjustments.table.actions")}</TableHead>
                                )}
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {filteredAdjustments.map((row) => (
                                <TableRow key={row.id}>
                                    <TableCell>{customerOnlyNameById.get(row.customerId) ?? row.customerId}</TableCell>
                                    <TableCell>{format(row.effectiveFrom, "dd/MM/yyyy")}</TableCell>
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
                                        {row.referenceFuelPriceThbPerLitre != null
                                            ? `฿${row.referenceFuelPriceThbPerLitre.toLocaleString()}`
                                            : "-"}
                                    </TableCell>
                                    <TableCell>{row.createdAt ? format(row.createdAt, "dd/MM/yyyy HH:mm") : "-"}</TableCell>
                                    {canEdit && (
                                        <TableCell className="text-right">
                                            <DropdownMenu>
                                                <DropdownMenuTrigger asChild>
                                                    <Button
                                                        type="button"
                                                        variant="ghost"
                                                        size="icon"
                                                        disabled={deletingAdjustmentId === row.id}
                                                    >
                                                        {deletingAdjustmentId === row.id ? (
                                                            <Loader2 className="h-4 w-4 animate-spin" />
                                                        ) : (
                                                            <MoreHorizontal className="h-4 w-4" />
                                                        )}
                                                    </Button>
                                                </DropdownMenuTrigger>
                                                <DropdownMenuContent align="end">
                                                    <DropdownMenuItem onClick={() => handleEditAdjustment(row)}>
                                                        <Pencil className="h-4 w-4 mr-2" />
                                                        {t("accounting.rateCard.fuelAdjustments.table.edit")}
                                                    </DropdownMenuItem>
                                                    <DropdownMenuItem
                                                        className="text-destructive focus:text-destructive"
                                                        onClick={() => void handleDeleteAdjustment(row.id)}
                                                    >
                                                        <Trash2 className="h-4 w-4 mr-2" />
                                                        {t("accounting.rateCard.fuelAdjustments.table.delete")}
                                                    </DropdownMenuItem>
                                                </DropdownMenuContent>
                                            </DropdownMenu>
                                        </TableCell>
                                    )}
                                </TableRow>
                            ))}
                            {filteredAdjustments.length === 0 && (
                                <TableRow>
                                    <TableCell colSpan={canEdit ? 6 : 5} className="h-20 text-center text-muted-foreground">
                                        {t("accounting.rateCard.fuelAdjustments.noRows")}
                                    </TableCell>
                                </TableRow>
                            )}
                        </TableBody>
                    </Table>
                </CardContent>
            </Card>

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

            {/* Edit Entry Dialog */}
            <Dialog open={editingEntryId !== null} onOpenChange={(open) => !open && setEditingEntryId(null)}>
                <DialogContent>
                    <DialogHeader>
                        <DialogTitle>{t("accounting.rateCard.entry.editTitle")}</DialogTitle>
                        <DialogDescription>{t("accounting.rateCard.entry.editDescription")}</DialogDescription>
                    </DialogHeader>
                    <div className="grid gap-4 py-4">
                        <div className="grid grid-cols-2 gap-4">
                            <div className="space-y-1.5">
                                <Label>{t("accounting.rateCard.table.rateThb")}</Label>
                                <Input
                                    type="number"
                                    value={editEntryForm.rateThb}
                                    onChange={(e) => setEditEntryForm((p) => ({ ...p, rateThb: e.target.value }))}
                                />
                            </div>
                            <div className="space-y-1.5">
                                <Label>{t("accounting.rateCard.table.distanceKm")}</Label>
                                <Input
                                    type="number"
                                    value={editEntryForm.distanceKm}
                                    onChange={(e) => setEditEntryForm((p) => ({ ...p, distanceKm: e.target.value }))}
                                />
                            </div>
                        </div>
                        <div className="grid grid-cols-2 gap-4">
                            <div className="space-y-1.5">
                                <Label>{t("accounting.rateCard.table.vehicleClass")}</Label>
                                <Select
                                    value={editEntryForm.vehicleClass}
                                    onValueChange={(v) => setEditEntryForm((p) => ({ ...p, vehicleClass: v }))}
                                >
                                    <SelectTrigger>
                                        <SelectValue />
                                    </SelectTrigger>
                                    <SelectContent>
                                        {manualVehicleClassOptions.map((vc) => (
                                            <SelectItem key={vc} value={vc}>{vc}</SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>
                            </div>
                            <div className="space-y-1.5">
                                <Label>{t("accounting.rateCard.table.effectiveFrom")}</Label>
                                <Input
                                    type="date"
                                    value={editEntryForm.effectiveFrom}
                                    onChange={(e) => setEditEntryForm((p) => ({ ...p, effectiveFrom: e.target.value }))}
                                />
                            </div>
                        </div>
                    </div>
                    <DialogFooter>
                        <Button variant="outline" onClick={() => setEditingEntryId(null)}>
                            {t("accounting.detail.close")}
                        </Button>
                        <Button onClick={() => void handleSaveEntry()} disabled={savingEntry}>
                            {savingEntry && <Loader2 className="h-4 w-4 mr-2 animate-spin" />}
                            {t("accounting.detail.save")}
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </div>
    );
}
