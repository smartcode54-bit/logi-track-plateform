"use client";

import { useEffect, useState, useMemo } from "react";
import { useLanguage } from "@/context/language";
import { PagePermissionGuard } from "@/components/page-permission-guard";
import { CAPABILITIES } from "@/lib/capabilities";
import {
    getVehicleExpensesByType,
    getDriversForFilter,
    getTrucksForFilter,
    VehicleExpenseRow,
    DriverOption,
    TruckOption,
} from "../actions.client";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Fuel, DollarSign, Hash, TrendingUp, Loader2, Gauge, Search, RefreshCw, Save } from "lucide-react";
import {
    ImageUrlPreviewView,
    type ImageUrlPreviewLabels,
} from "@/components/image-preview/ImageUrlPreviewView";
import { IMAGE_PREVIEW_VIEWPORT_ACCOUNTING_DIALOG_CLASS } from "@/components/image-preview/image-preview-constants";
import { AccountingPreviewImageActions } from "@/components/accounting/AccountingPreviewImageActions";
import { AccountingBatchImagesZipCard } from "@/components/accounting/AccountingBatchImagesZipCard";
import { looksLikeImageUrl } from "@/features/maintenance/utils/looksLikeImageUrl";
import dynamic from "next/dynamic";

const RefillLocationMap = dynamic(
    () => import("@/components/accounting/RefillLocationMap").then((m) => m.RefillLocationMap),
    { ssr: false }
);
import { format } from "date-fns";
import { httpsCallable } from "firebase/functions";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Button } from "@/components/ui/button";
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from "@/components/ui/dialog";
import { usePermission } from "@/hooks/usePermission";
import { CAPABILITIES } from "@/lib/capabilities";
import { updateVehicleExpense } from "../actions.client";
import { functions } from "@/firebase/client";

export interface FuelRow extends VehicleExpenseRow {
    kmPerLiter?: number;
    /** ระยะทาง (กม.) ตั้งแต่การเติมครั้งก่อน */
    distanceKm?: number;
}

interface BangchakPriceItem {
    nameTh: string;
    nameEn: string;
    price: number;
    unit: string;
}

interface BangchakPriceResponse {
    locale: "th" | "en";
    fetchedAt: string;
    source: string;
    items: BangchakPriceItem[];
}

/** Badge colors approximating Bangchak retail UI (keyword match on API label). */
function bangchakFuelChipClasses(label: string): string {
    const n = label.toLowerCase();
    if (n.includes("e85")) return "bg-red-600";
    if (n.includes("e20")) return "bg-orange-500";
    if (n.includes("b20")) return "bg-emerald-600";
    if (n.includes("98")) return "bg-amber-500";
    if (n.includes("ไฮดีเซล") || n.includes("hi-diesel")) return "bg-blue-950";
    if (n.includes("พรีเมียม") && n.includes("ดีเซล")) return "bg-purple-700";
    if (/\b91\b/.test(n)) return "bg-teal-600";
    if (/\b95\b/.test(n)) return "bg-blue-600";
    return "bg-slate-600";
}

function formatBangchakPriceOnly(price: number): string {
    return price.toLocaleString(undefined, { minimumFractionDigits: 0, maximumFractionDigits: 2 });
}

function computeKmPerLiter(records: VehicleExpenseRow[]): FuelRow[] {
    const byDriver = new Map<string, VehicleExpenseRow[]>();
    records.forEach((r) => {
        const key = r.truckId ?? r.driverId;
        if (!byDriver.has(key)) byDriver.set(key, []);
        byDriver.get(key)!.push(r);
    });
    const result: FuelRow[] = [];
    byDriver.forEach((rows) => {
        const sorted = [...rows].sort((a, b) => a.date.getTime() - b.date.getTime());
        sorted.forEach((row, i) => {
            const prev = i > 0 ? sorted[i - 1] : null;
            const prevOdo = prev?.odometer;
            const currOdo = row.odometer;
            const vol = row.volumeLiters;
            let kmPerLiter: number | undefined;
            let distanceKm: number | undefined;
            if (
                prevOdo != null &&
                currOdo != null &&
                currOdo >= prevOdo
            ) {
                distanceKm = currOdo - prevOdo;
                if (vol != null && vol > 0) {
                    kmPerLiter = Math.round((distanceKm / vol) * 10) / 10;
                }
            }
            result.push({ ...row, kmPerLiter, distanceKm });
        });
    });
    return result.sort((a, b) => b.date.getTime() - a.date.getTime());
}

export default function AccountingFuelPage() {
    const { t, language } = useLanguage();
    const [records, setRecords] = useState<VehicleExpenseRow[]>([]);
    const [drivers, setDrivers] = useState<DriverOption[]>([]);
    const [trucks, setTrucks] = useState<TruckOption[]>([]);
    const [loading, setLoading] = useState(true);
    const [filterDriverId, setFilterDriverId] = useState<string>("all");
    const [filterTruckId, setFilterTruckId] = useState<string>("all");
    const [plateSearch, setPlateSearch] = useState("");
    const [filterMonth, setFilterMonth] = useState<string>("all");
    const [filterYear, setFilterYear] = useState<string>("all");
    const [filterKmOp, setFilterKmOp] = useState<string>("");
    const [filterKmValue, setFilterKmValue] = useState<string>("");
    const [filterKmMin, setFilterKmMin] = useState<string>("");
    const [filterKmMax, setFilterKmMax] = useState<string>("");
    const [detailRow, setDetailRow] = useState<FuelRow | null>(null);
    const [editForm, setEditForm] = useState<FuelRow | null>(null);
    const [submitting, setSubmitting] = useState(false);
    const [bangchakPrices, setBangchakPrices] = useState<BangchakPriceItem[]>([]);
    const [bangchakFetchedAt, setBangchakFetchedAt] = useState<string | null>(null);
    const [bangchakLoading, setBangchakLoading] = useState(false);
    const [bangchakError, setBangchakError] = useState<string | null>(null);

    const { hasPermission: canEdit } = usePermission(CAPABILITIES.accounting_edit_fuel);

    const [detailMapKey, setDetailMapKey] = useState(0);
    const [slideCaption, setSlideCaption] = useState("");
    const [slideIndex, setSlideIndex] = useState(0);
    const [currentPreviewUrl, setCurrentPreviewUrl] = useState("");

    const imageItems = useMemo(() => {
        if (!detailRow) return [];
        const items = [];
        if (detailRow.receiptPhotoUrl) items.push({ url: detailRow.receiptPhotoUrl, label: t("accounting.detail.receiptPhoto") });
        if (detailRow.odometerPhotoUrl) items.push({ url: detailRow.odometerPhotoUrl, label: t("accounting.detail.odometerPhoto") });
        return items;
    }, [detailRow, t]);

    const imagePreviewLabels: ImageUrlPreviewLabels = useMemo(
        () => ({
            zoomIn: t("accounting.preview.zoomIn"),
            zoomOut: t("accounting.preview.zoomOut"),
            resetZoom: t("accounting.preview.resetZoom"),
            prev: t("accounting.preview.previous"),
            next: t("accounting.preview.next"),
            notPreviewable: t("accounting.preview.notPreviewable"),
        }),
        [t]
    );

    const previewUrls = useMemo(() => imageItems.map((i) => i.url), [imageItems]);

    useEffect(() => {
        setDetailMapKey((k) => k + 1);
        setSlideIndex(0);
        if (!detailRow) {
            setSlideCaption("");
            setCurrentPreviewUrl("");
            return;
        }
        if (detailRow.receiptPhotoUrl) setSlideCaption(t("accounting.detail.receiptPhoto"));
        else if (detailRow.odometerPhotoUrl) setSlideCaption(t("accounting.detail.odometerPhoto"));
        else {
            setSlideCaption("");
            setCurrentPreviewUrl("");
        }
    }, [detailRow, t]);

    const loadData = () => {
        setLoading(true);
        Promise.all([
            getVehicleExpensesByType("fuel"),
            getDriversForFilter(),
            getTrucksForFilter(),
        ]).then(([rows, driverList, truckList]) => {
            setRecords(rows);
            setDrivers(driverList);
            setTrucks(truckList);
        }).finally(() => setLoading(false));
    };

    const handleSaveEdit = async () => {
        if (!detailRow || !editForm) return;
        setSubmitting(true);
        try {
            const pricePerLiterRounded = editForm.pricePerLiter != null && !isNaN(Number(editForm.pricePerLiter))
                ? Math.round(Number(editForm.pricePerLiter) * 100) / 100
                : editForm.pricePerLiter;

            await updateVehicleExpense(detailRow.id, {
                amount: editForm.amount,
                volumeLiters: editForm.volumeLiters,
                pricePerLiter: pricePerLiterRounded,
                odometer: editForm.odometer,
                stationTaxId: editForm.stationTaxId ?? "",
                taxInvId: editForm.taxInvId ?? "",
                distanceKm: editForm.distanceKm,
            });

            await loadData(); // Reload list to get updated data
            setDetailRow(null); // Close dialog
        } catch (err) {
            console.error("Failed to update fuel expense:", err);
        } finally {
            setSubmitting(false);
        }
    };

    useEffect(() => {
        if (detailRow) {
            const row = { ...detailRow };
            if (row.pricePerLiter != null && typeof row.pricePerLiter === "number" && !isNaN(row.pricePerLiter)) {
                row.pricePerLiter = Math.round(row.pricePerLiter * 100) / 100;
            }
            setEditForm(row);
        } else {
            setEditForm(null);
        }
    }, [detailRow]);

    useEffect(() => {
        loadData();
    }, []);

    const filterYearOptions = useMemo(() => {
        const ys = new Set<number>();
        const cy = new Date().getFullYear();
        for (let i = 0; i < 6; i++) ys.add(cy - i);
        records.forEach((r) => ys.add(r.date.getFullYear()));
        return Array.from(ys).sort((a, b) => b - a);
    }, [records]);

    const monthLabels = useMemo(() => {
        const loc = language === "th" ? "th-TH" : "en-US";
        return Array.from({ length: 12 }, (_, i) =>
            new Intl.DateTimeFormat(loc, { month: "long" }).format(new Date(2024, i, 1))
        );
    }, [language]);

    const rowsWithKm = useMemo(() => computeKmPerLiter(records), [records]);
    const bangchakCallable = useMemo(
        () => httpsCallable<{ locale: "th" | "en" }, BangchakPriceResponse>(functions, "getBangchakRetailOilPrices"),
        []
    );
    const canReadExternalFuelPrice = usePermission(CAPABILITIES.accounting_view_fuel).hasPermission;

    const loadBangchakPrices = async () => {
        if (!canReadExternalFuelPrice) return;
        setBangchakLoading(true);
        setBangchakError(null);
        try {
            const locale = language === "th" ? "th" : "en";
            const result = await bangchakCallable({ locale });
            const payload = result.data;
            setBangchakPrices(Array.isArray(payload.items) ? payload.items : []);
            setBangchakFetchedAt(payload.fetchedAt ?? null);
        } catch (error) {
            console.error("Failed to load Bangchak prices:", error);
            setBangchakError(t("accounting.bangchak.errorLoad"));
        } finally {
            setBangchakLoading(false);
        }
    };

    useEffect(() => {
        void loadBangchakPrices();
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [language, canReadExternalFuelPrice]);

    const filteredRecords = useMemo(() => {
        let list = rowsWithKm;
        if (filterDriverId !== "all") list = list.filter((r) => r.driverId === filterDriverId);
        if (filterTruckId !== "all") list = list.filter((r) => r.truckId === filterTruckId);
        if (plateSearch.trim()) {
            const q = plateSearch.trim().toLowerCase();
            list = list.filter(
                (r) =>
                    (r.licensePlate ?? "").toLowerCase().includes(q) ||
                    (r.driverName ?? "").toLowerCase().includes(q)
            );
        }
        if (filterYear !== "all") {
            const y = Number(filterYear);
            if (!Number.isNaN(y)) list = list.filter((r) => r.date.getFullYear() === y);
        }
        if (filterMonth !== "all") {
            const m = Number(filterMonth);
            if (!Number.isNaN(m)) list = list.filter((r) => r.date.getMonth() === m);
        }
        if (filterKmOp && filterKmOp !== "between") {
            const val = parseFloat(filterKmValue);
            if (filterKmValue.trim() !== "" && !Number.isNaN(val)) {
                list = list.filter((r) => {
                    const km = (r as FuelRow).kmPerLiter;
                    if (km == null) return false;
                    if (filterKmOp === "=") return Math.abs(km - val) < 0.01;
                    if (filterKmOp === "<") return km < val;
                    if (filterKmOp === "<=") return km <= val;
                    if (filterKmOp === ">") return km > val;
                    if (filterKmOp === ">=") return km >= val;
                    return true;
                });
            }
        } else if (filterKmOp === "between") {
            const min = filterKmMin.trim() !== "" ? parseFloat(filterKmMin) : null;
            const max = filterKmMax.trim() !== "" ? parseFloat(filterKmMax) : null;
            if (min != null && !Number.isNaN(min)) {
                list = list.filter((r) => (r as FuelRow).kmPerLiter != null && (r as FuelRow).kmPerLiter! >= min);
            }
            if (max != null && !Number.isNaN(max)) {
                list = list.filter((r) => (r as FuelRow).kmPerLiter != null && (r as FuelRow).kmPerLiter! <= max);
            }
        }
        return list;
    }, [rowsWithKm, filterDriverId, filterTruckId, plateSearch, filterYear, filterMonth, filterKmOp, filterKmValue, filterKmMin, filterKmMax]);

    const totalAmount = filteredRecords.reduce((s, r) => s + r.amount, 0);
    const count = filteredRecords.length;
    const avgAmount = count > 0 ? totalAmount / count : 0;
    const now = new Date();
    const thisMonth = filteredRecords.filter(
        (r) => r.date.getMonth() === now.getMonth() && r.date.getFullYear() === now.getFullYear()
    );
    const thisMonthTotal = thisMonth.reduce((s, r) => s + r.amount, 0);
    const kmPerLiterValues = filteredRecords
        .map((r) => (r as FuelRow).kmPerLiter)
        .filter((v): v is number => v != null && v > 0);
    const avgKmPerLiter =
        kmPerLiterValues.length > 0
            ? Math.round((kmPerLiterValues.reduce((a, b) => a + b, 0) / kmPerLiterValues.length) * 10) / 10
            : null;

    if (loading) {
        return (
            <div className="flex h-64 items-center justify-center">
                <Loader2 className="h-8 w-8 animate-spin text-primary" />
            </div>
        );
    }

    return (
        <PagePermissionGuard capability={CAPABILITIES.accounting_view_fuel}>
            <div className="container mx-auto p-6 space-y-8 max-w-[1600px]">
            <div className="flex flex-col md:flex-row md:items-center justify-between gap-4">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">{t("accounting.fuel.title")}</h1>
                    <p className="text-muted-foreground mt-1">{t("accounting.fuel.subtitle")}</p>
                </div>
            </div>

            <AccountingBatchImagesZipCard records={rowsWithKm} kind="fuel" />

            <Card>
                <CardHeader className="flex flex-row items-center justify-between space-y-0 gap-3">
                    <div>
                        <CardTitle>{t("accounting.bangchak.title")}</CardTitle>
                        <p className="text-xs text-muted-foreground mt-1">{t("accounting.bangchak.disclaimer")}</p>
                    </div>
                    <Button variant="outline" size="sm" onClick={loadBangchakPrices} disabled={bangchakLoading || !canReadExternalFuelPrice}>
                        <RefreshCw className={`h-4 w-4 mr-2 ${bangchakLoading ? "animate-spin" : ""}`} />
                        {t("accounting.bangchak.refresh")}
                    </Button>
                </CardHeader>
                <CardContent className="space-y-3">
                    {bangchakError && (
                        <p className="text-sm text-red-600 dark:text-red-400">{bangchakError}</p>
                    )}
                    {!bangchakError && bangchakPrices.length === 0 && !bangchakLoading && (
                        <p className="text-sm text-muted-foreground">{t("accounting.bangchak.noData")}</p>
                    )}
                    {bangchakPrices.length > 0 && (
                        <div className="grid grid-cols-2 sm:grid-cols-4 md:grid-cols-4 lg:grid-cols-8 gap-3">
                            {bangchakPrices.map((item, index) => {
                                const label =
                                    language === "th" ? (item.nameTh || item.nameEn) : (item.nameEn || item.nameTh);
                                return (
                                    <div
                                        key={`${item.nameTh}-${item.nameEn}-${index}`}
                                        className="flex flex-col rounded-xl overflow-hidden border border-border/50 shadow-sm"
                                    >
                                        <div className={`${bangchakFuelChipClasses(label)} px-2 py-2 text-center min-h-[3rem] flex items-center justify-center`}>
                                            <span className="text-white text-[10px] font-semibold leading-tight" title={label}>
                                                {label}
                                            </span>
                                        </div>
                                        <div className="flex flex-col items-center justify-center px-2 py-3 bg-card">
                                            <span className="text-2xl font-bold tabular-nums tracking-tight text-foreground">
                                                {formatBangchakPriceOnly(item.price)}
                                            </span>
                                            <span className="text-[10px] text-muted-foreground mt-0.5">{item.unit || "บาท/ลิตร"}</span>
                                        </div>
                                    </div>
                                );
                            })}
                        </div>
                    )}
                    {bangchakFetchedAt && (
                        <p className="text-xs text-muted-foreground">
                            {t("accounting.bangchak.fetchedAt")}: {format(new Date(bangchakFetchedAt), "dd MMM yyyy HH:mm")}
                        </p>
                    )}
                </CardContent>
            </Card>

            {/* Mini dashboard */}
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-5 gap-4">
                <Card>
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium">{t("accounting.dashboard.totalAmount")}</CardTitle>
                        <DollarSign className="h-4 w-4 text-muted-foreground" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold">฿{totalAmount.toLocaleString()}</div>
                        <p className="text-xs text-muted-foreground">{t("accounting.dashboard.recordCount")}: {count}</p>
                    </CardContent>
                </Card>
                <Card className="border-l-4 border-l-blue-500">
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium text-blue-700">{t("accounting.dashboard.thisMonth")}</CardTitle>
                        <TrendingUp className="h-4 w-4 text-blue-500" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold text-blue-700">฿{thisMonthTotal.toLocaleString()}</div>
                        <p className="text-xs text-muted-foreground">{thisMonth.length} {t("accounting.dashboard.recordCount").toLowerCase()}</p>
                    </CardContent>
                </Card>
                <Card>
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium">{t("accounting.dashboard.avgAmount")}</CardTitle>
                        <Hash className="h-4 w-4 text-muted-foreground" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold">฿{Math.round(avgAmount).toLocaleString()}</div>
                    </CardContent>
                </Card>
                <Card className="border-l-4 border-l-green-500">
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium text-green-700">{t("accounting.dashboard.avgKmPerLiter")}</CardTitle>
                        <Gauge className="h-4 w-4 text-green-500" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold text-green-700">
                            {avgKmPerLiter != null ? `${avgKmPerLiter} km/L` : "—"}
                        </div>
                        <p className="text-xs text-muted-foreground">
                            {kmPerLiterValues.length} {t("accounting.dashboard.recordCount").toLowerCase()}
                        </p>
                    </CardContent>
                </Card>
                <Card>
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium">{t("accounting.dashboard.recordCount")}</CardTitle>
                        <Fuel className="h-4 w-4 text-muted-foreground" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold">{count}</div>
                    </CardContent>
                </Card>
            </div>

            {/* Filters */}
            <Card>
                <CardHeader className="pb-3">
                    <CardTitle className="text-base flex items-center gap-2">
                        <Search className="h-4 w-4" />
                        {t("accounting.filter.title")}
                    </CardTitle>
                </CardHeader>
                <CardContent className="flex flex-wrap gap-4">
                    <div className="flex flex-col gap-1.5">
                        <label className="text-xs font-medium text-muted-foreground">
                            {t("accounting.filter.driver")}
                        </label>
                        <Select value={filterDriverId} onValueChange={setFilterDriverId}>
                            <SelectTrigger className="w-[200px]">
                                <SelectValue placeholder={t("accounting.filter.all")} />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value="all">{t("accounting.filter.all")}</SelectItem>
                                {drivers.map((d) => (
                                    <SelectItem key={d.id} value={d.id}>
                                        {d.name}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </div>
                    <div className="flex flex-col gap-1.5">
                        <label className="text-xs font-medium text-muted-foreground">
                            {t("accounting.filter.vehicle")}
                        </label>
                        <Select value={filterTruckId} onValueChange={setFilterTruckId}>
                            <SelectTrigger className="w-[180px]">
                                <SelectValue placeholder={t("accounting.filter.all")} />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value="all">{t("accounting.filter.all")}</SelectItem>
                                {trucks.map((truck) => (
                                    <SelectItem key={truck.id} value={truck.id}>
                                        {truck.licensePlate}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </div>
                    <div className="flex flex-col gap-1.5">
                        <label className="text-xs font-medium text-muted-foreground">
                            {t("accounting.filter.title")}
                        </label>
                        <Input
                            placeholder={t("accounting.filter.searchPlaceholder")}
                            className="w-[180px]"
                            value={plateSearch}
                            onChange={(e) => setPlateSearch(e.target.value)}
                        />
                    </div>
                    <div className="flex flex-col gap-1.5">
                        <label className="text-xs font-medium text-muted-foreground">
                            {t("accounting.filter.month")}
                        </label>
                        <Select value={filterMonth} onValueChange={setFilterMonth}>
                            <SelectTrigger className="w-[160px]">
                                <SelectValue placeholder={t("accounting.filter.all")} />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value="all">{t("accounting.filter.all")}</SelectItem>
                                {monthLabels.map((label, i) => (
                                    <SelectItem key={i} value={String(i)}>
                                        {label}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </div>
                    <div className="flex flex-col gap-1.5">
                        <label className="text-xs font-medium text-muted-foreground">
                            {t("accounting.filter.year")}
                        </label>
                        <Select value={filterYear} onValueChange={setFilterYear}>
                            <SelectTrigger className="w-[120px]">
                                <SelectValue placeholder={t("accounting.filter.all")} />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value="all">{t("accounting.filter.all")}</SelectItem>
                                {filterYearOptions.map((y) => (
                                    <SelectItem key={y} value={String(y)}>
                                        {y}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </div>
                    <div className="flex flex-col gap-1.5">
                        <label className="text-xs font-medium text-muted-foreground">
                            {t("accounting.filter.kmPerLiter")}
                        </label>
                        <div className="flex flex-wrap items-end gap-2">
                            <Select value={filterKmOp || "none"} onValueChange={(v) => setFilterKmOp(v === "none" ? "" : v)}>
                                <SelectTrigger className="w-[110px]">
                                    <SelectValue placeholder={t("accounting.filter.all")} />
                                </SelectTrigger>
                                <SelectContent>
                                    <SelectItem value="none">{t("accounting.filter.all")}</SelectItem>
                                    <SelectItem value="=">{t("accounting.filter.kmOp.eq")}</SelectItem>
                                    <SelectItem value="<">{t("accounting.filter.kmOp.lt")}</SelectItem>
                                    <SelectItem value="<=">{t("accounting.filter.kmOp.lte")}</SelectItem>
                                    <SelectItem value=">">{t("accounting.filter.kmOp.gt")}</SelectItem>
                                    <SelectItem value=">=">{t("accounting.filter.kmOp.gte")}</SelectItem>
                                    <SelectItem value="between">{t("accounting.filter.kmOp.between")}</SelectItem>
                                </SelectContent>
                            </Select>
                            {filterKmOp && filterKmOp !== "between" && (
                                <Input
                                    type="number"
                                    inputMode="decimal"
                                    min={0}
                                    step={0.1}
                                    placeholder="0"
                                    className="w-[90px]"
                                    value={filterKmValue}
                                    onChange={(e) => setFilterKmValue(e.target.value)}
                                />
                            )}
                            {filterKmOp === "between" && (
                                <>
                                    <Input
                                        type="number"
                                        inputMode="decimal"
                                        min={0}
                                        placeholder="Min"
                                        className="w-[80px]"
                                        value={filterKmMin}
                                        onChange={(e) => setFilterKmMin(e.target.value)}
                                    />
                                    <span className="text-muted-foreground text-sm">–</span>
                                    <Input
                                        type="number"
                                        inputMode="decimal"
                                        min={0}
                                        placeholder="Max"
                                        className="w-[80px]"
                                        value={filterKmMax}
                                        onChange={(e) => setFilterKmMax(e.target.value)}
                                    />
                                </>
                            )}
                        </div>
                    </div>
                </CardContent>
            </Card>

            {/* Table */}
            <Card>
                <CardHeader className="flex flex-row items-center justify-between space-y-0">
                    <CardTitle>{t("accounting.fuel.title")}</CardTitle>
                    <Button variant="outline" size="sm" onClick={loadData} disabled={loading}>
                        <RefreshCw className={`h-4 w-4 mr-2 ${loading ? "animate-spin" : ""}`} />
                        {t("common.refresh")}
                    </Button>
                </CardHeader>
                <CardContent>
                    <Table>
                        <TableHeader>
                            <TableRow>
                                <TableHead>{t("accounting.table.date")}</TableHead>
                                <TableHead>{t("accounting.table.driver")}</TableHead>
                                <TableHead>{t("accounting.filter.vehicle")}</TableHead>
                                <TableHead className="text-right">{t("accounting.table.amount")}</TableHead>
                                <TableHead className="text-right">{t("accounting.table.volume")}</TableHead>
                                <TableHead className="text-right">{t("accounting.table.pricePerLiter")}</TableHead>
                                <TableHead className="text-right">{t("accounting.table.odometer")}</TableHead>
                                <TableHead className="text-right">{t("accounting.table.distanceKm")}</TableHead>
                                <TableHead className="text-right">{t("accounting.table.kmPerLiter")}</TableHead>
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {filteredRecords.map((row) => (
                                <TableRow
                                    key={row.id}
                                    className="cursor-pointer hover:bg-muted/50 transition-colors"
                                    onClick={() => setDetailRow(row as FuelRow)}
                                >
                                    <TableCell className="whitespace-nowrap font-mono text-xs">
                                        {format(row.date, "dd MMM yyyy")}
                                    </TableCell>
                                    <TableCell>{row.driverName ?? (row.driverId || "—")}</TableCell>
                                    <TableCell className="font-mono text-xs">{row.licensePlate ?? "—"}</TableCell>
                                    <TableCell className="text-right font-semibold">฿{row.amount.toLocaleString()}</TableCell>
                                    <TableCell className="text-right text-muted-foreground">
                                        {row.volumeLiters != null ? row.volumeLiters.toLocaleString() : "—"}
                                    </TableCell>
                                    <TableCell className="text-right text-muted-foreground">
                                        {row.pricePerLiter != null ? `฿${row.pricePerLiter.toLocaleString()}` : "—"}
                                    </TableCell>
                                    <TableCell className="text-right text-muted-foreground">
                                        {row.odometer != null ? row.odometer.toLocaleString() : "—"}
                                    </TableCell>
                                    <TableCell className="text-right text-muted-foreground">
                                        {(row as FuelRow).distanceKm != null
                                            ? (row as FuelRow).distanceKm!.toLocaleString()
                                            : "—"}
                                    </TableCell>
                                    <TableCell className="text-right font-medium">
                                        {(row as FuelRow).kmPerLiter != null
                                            ? `${(row as FuelRow).kmPerLiter}`
                                            : "—"}
                                    </TableCell>
                                </TableRow>
                            ))}
                            {filteredRecords.length === 0 && (
                                <TableRow>
                                    <TableCell colSpan={9} className="h-24 text-center text-muted-foreground">
                                        {t("accounting.noRecords")}
                                    </TableCell>
                                </TableRow>
                            )}
                        </TableBody>
                    </Table>
                </CardContent>
            </Card>

            {/* Detail Dialog */}
            <Dialog open={!!detailRow} onOpenChange={(open) => !open && setDetailRow(null)}>
                <DialogContent className="max-w-6xl max-h-[90vh] overflow-hidden flex flex-col p-0">
                    <DialogHeader className="px-6 pt-6 pb-2 shrink-0">
                        <DialogTitle>{t("accounting.detail.title")}</DialogTitle>
                        <DialogDescription>
                            {detailRow && format(detailRow.date, "dd MMM yyyy")} · {detailRow?.driverName ?? detailRow?.driverId}
                        </DialogDescription>
                    </DialogHeader>
                    {detailRow && editForm && (
                        <div className="grid grid-cols-1 md:grid-cols-3 gap-0 flex-1 min-h-0 overflow-hidden">
                            {/* Left: Images */}
                            <div className="bg-muted/30 border-t md:border-t-0 md:border-r border-border p-4 flex flex-col min-h-0">
                                {imageItems.length > 0 ? (
                                    <div className="flex flex-1 min-h-0 flex-col overflow-hidden rounded-lg border border-border bg-black/5">
                                        <ImageUrlPreviewView
                                            urls={previewUrls}
                                            startIndex={0}
                                            active={!!detailRow && previewUrls.length > 0}
                                            labels={imagePreviewLabels}
                                            isPreviewableImage={looksLikeImageUrl}
                                            onCurrentUrlChange={(url) => {
                                                setCurrentPreviewUrl(url);
                                                const i = previewUrls.indexOf(url);
                                                if (i >= 0) setSlideIndex(i);
                                                if (detailRow?.receiptPhotoUrl === url) {
                                                    setSlideCaption(t("accounting.detail.receiptPhoto"));
                                                } else if (detailRow?.odometerPhotoUrl === url) {
                                                    setSlideCaption(t("accounting.detail.odometerPhoto"));
                                                }
                                            }}
                                            viewportClassName={IMAGE_PREVIEW_VIEWPORT_ACCOUNTING_DIALOG_CLASS}
                                            toolbarClassName="shrink-0 border-border bg-muted/30"
                                        />
                                        {slideCaption ? (
                                            <p className="shrink-0 border-t border-border bg-muted/20 px-3 py-2 text-center text-sm text-muted-foreground">
                                                {slideCaption}
                                                {previewUrls.length > 1
                                                    ? ` (${slideIndex + 1} / ${previewUrls.length})`
                                                    : ""}
                                            </p>
                                        ) : null}
                                        <AccountingPreviewImageActions
                                            printUrl={currentPreviewUrl}
                                            zipEntries={[]}
                                            zipFilename=""
                                            includeZipDownload={false}
                                        />
                                    </div>
                                ) : (
                                    <div className="flex items-center justify-center flex-1 min-h-[200px] rounded-lg border border-dashed border-muted-foreground/30 text-muted-foreground text-sm">
                                        {t("accounting.detail.noImages")}
                                    </div>
                                )}
                            </div>

                            {/* Middle: Editable Details */}
                            <div className="flex flex-col min-h-0 border-t md:border-t-0 md:border-r border-border">
                                <div className="flex-1 overflow-y-auto p-6 space-y-4">
                                    <div className="grid grid-cols-2 gap-x-4 gap-y-3 text-sm">
                                        <Label className="text-muted-foreground">{t("accounting.table.date")}</Label>
                                        <span className="font-medium h-9 flex items-center">{format(editForm.date, "dd MMM yyyy")}</span>
                                        <Label className="text-muted-foreground">{t("accounting.detail.driver")}</Label>
                                        <Input value={editForm.driverName ?? editForm.driverId ?? "—"} readOnly className="h-9 bg-muted/50 cursor-not-allowed" />
                                        <Label className="text-muted-foreground">{t("accounting.detail.vehicle")}</Label>
                                        <Input value={editForm.licensePlate ?? "—"} readOnly className="h-9 font-mono bg-muted/50 cursor-not-allowed" />

                                        <Label className="text-muted-foreground">{t("accounting.detail.amount")}</Label>
                                        {canEdit ? (
                                            <Input
                                                type="number"
                                                value={editForm.amount ?? ""}
                                                onChange={(e) => setEditForm({ ...editForm, amount: e.target.value === "" ? editForm.amount : Number(e.target.value) })}
                                                className="h-9 font-semibold"
                                            />
                                        ) : (
                                            <Input value={`฿${editForm.amount.toLocaleString()}`} readOnly className="h-9 font-semibold bg-muted/50 cursor-not-allowed" />
                                        )}

                                        <Label className="text-muted-foreground">{t("accounting.detail.volume")}</Label>
                                        {canEdit ? (
                                            <Input
                                                type="number"
                                                value={editForm.volumeLiters ?? ""}
                                                onChange={(e) => setEditForm({ ...editForm, volumeLiters: e.target.value === "" ? undefined : Number(e.target.value) })}
                                                className="h-9"
                                            />
                                        ) : (
                                            <Input value={editForm.volumeLiters != null ? editForm.volumeLiters.toLocaleString() : "—"} readOnly className="h-9 bg-muted/50 cursor-not-allowed" />
                                        )}

                                        <Label className="text-muted-foreground">{t("accounting.detail.pricePerLiter")}</Label>
                                        {canEdit ? (
                                            <Input
                                                type="number"
                                                value={editForm.pricePerLiter ?? ""}
                                                onChange={(e) => setEditForm({ ...editForm, pricePerLiter: e.target.value === "" ? undefined : Number(e.target.value) })}
                                                className="h-9"
                                            />
                                        ) : (
                                            <Input value={editForm.pricePerLiter != null ? `฿${editForm.pricePerLiter.toLocaleString()}` : "—"} readOnly className="h-9 bg-muted/50 cursor-not-allowed" />
                                        )}

                                        <Label className="text-muted-foreground">{t("accounting.detail.odometer")}</Label>
                                        {canEdit ? (
                                            <Input
                                                type="number"
                                                value={editForm.odometer ?? ""}
                                                onChange={(e) => setEditForm({ ...editForm, odometer: e.target.value === "" ? undefined : Number(e.target.value) })}
                                                className="h-9"
                                            />
                                        ) : (
                                            <Input value={editForm.odometer != null ? editForm.odometer.toLocaleString() : "—"} readOnly className="h-9 bg-muted/50 cursor-not-allowed" />
                                        )}

                                        <Label className="text-muted-foreground">{t("accounting.detail.stationTaxId")}</Label>
                                        {canEdit ? (
                                            <Input
                                                value={editForm.stationTaxId ?? ""}
                                                onChange={(e) => setEditForm({ ...editForm, stationTaxId: e.target.value })}
                                                className="h-9"
                                            />
                                        ) : (
                                            <Input value={editForm.stationTaxId || "—"} readOnly className="h-9 bg-muted/50 cursor-not-allowed" />
                                        )}

                                        <Label className="text-muted-foreground">{t("accounting.detail.taxInvId")}</Label>
                                        {canEdit ? (
                                            <Input
                                                value={editForm.taxInvId ?? ""}
                                                onChange={(e) => setEditForm({ ...editForm, taxInvId: e.target.value })}
                                                className="h-9"
                                            />
                                        ) : (
                                            <Input value={editForm.taxInvId || "—"} readOnly className="h-9 bg-muted/50 cursor-not-allowed" />
                                        )}

                                        <Label className="text-muted-foreground">{t("accounting.detail.distanceKm")}</Label>
                                        <Input value={editForm.distanceKm != null ? editForm.distanceKm.toLocaleString() : "—"} readOnly className="h-9 bg-muted/50 cursor-not-allowed" />
                                        <Label className="text-muted-foreground">{t("accounting.detail.kmPerLiter")}</Label>
                                        <Input value={editForm.kmPerLiter != null ? `${editForm.kmPerLiter} km/L` : "—"} readOnly className="h-9 font-medium bg-muted/50 cursor-not-allowed" />
                                    </div>

                                    {editForm.adminNote && (
                                        <div className="space-y-2 pt-2 border-t border-border mt-2">
                                            <Label className="text-muted-foreground">{t("accounting.audit.adminNote")}</Label>
                                            <span className="text-muted-foreground text-sm break-words flex min-h-9 items-center p-2 rounded-md bg-orange-500/10 border border-orange-500/20 text-orange-700 dark:text-orange-400">
                                                {editForm.adminNote}
                                            </span>
                                        </div>
                                    )}

                                    {canEdit && (
                                        <div className="space-y-2 pt-2 border-t border-border mt-2">
                                            <Label className="text-muted-foreground">{t("accounting.detail.note")}</Label>
                                            <Input
                                                className="h-9"
                                                value={editForm.note ?? ""}
                                                onChange={(e) => setEditForm({ ...editForm, note: e.target.value })}
                                            />
                                        </div>
                                    )}

                                    {(detailRow.createdAt || detailRow.updatedAt) && (
                                        <div className="grid grid-cols-2 gap-x-4 gap-y-1 text-xs text-muted-foreground pt-2 border-t border-border">
                                            {detailRow.createdAt && (
                                                <>
                                                    <span>{t("accounting.audit.created")}</span>
                                                    <span>{format(detailRow.createdAt, "dd MMM yyyy HH:mm")}</span>
                                                </>
                                            )}
                                            {detailRow.updatedAt && (
                                                <>
                                                    <span>{t("accounting.audit.updated")}</span>
                                                    <span>{format(detailRow.updatedAt, "dd MMM yyyy HH:mm")}</span>
                                                </>
                                            )}
                                        </div>
                                    )}
                                </div>

                                <DialogFooter className="px-6 py-4 border-t border-border shrink-0 flex-wrap gap-2 justify-end">
                                    {canEdit && (
                                        <Button variant="secondary" className="gap-1" onClick={handleSaveEdit} disabled={submitting}>
                                            {submitting && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                                            <Save className="mr-2 h-4 w-4" />
                                            {t("accounting.audit.save")}
                                        </Button>
                                    )}
                                    <Button variant="outline" onClick={() => setDetailRow(null)}>
                                        {t("accounting.detail.close")}
                                    </Button>
                                </DialogFooter>
                            </div>

                            {/* Right: Map */}
                            <div className="flex flex-col min-h-0 border-t md:border-t-0 bg-muted/20 p-4 overflow-hidden gap-2">
                                <Label className="text-muted-foreground font-medium shrink-0">
                                    {t("accounting.detail.refillLocation")}
                                </Label>
                                <div className="flex-1 min-h-[280px] flex flex-col min-w-0">
                                    <RefillLocationMap
                                        key={`refill-${detailRow?.id ?? ""}-${detailMapKey}`}
                                        refillLocation={editForm?.refillLocation}
                                        height="100%"
                                        className="flex-1 min-h-[260px] w-full"
                                        noCoordsLabel={t("accounting.detail.refillLocationNoCoords")}
                                    />
                                </div>
                                {canEdit && (
                                    <div className="pt-2">
                                        <Label className="text-muted-foreground mb-1 block">{t("accounting.detail.latLng")}</Label>
                                        <Input
                                            className="h-9 font-mono"
                                            value={editForm.refillLocation ?? ""}
                                            onChange={(e) => setEditForm(prev => prev ? { ...prev, refillLocation: e.target.value } : null)}
                                        />
                                    </div>
                                )}
                            </div>
                        </div>
                    )}
                </DialogContent>
            </Dialog>
            </div>
        </PagePermissionGuard>
    );
}
