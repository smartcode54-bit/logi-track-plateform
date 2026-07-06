"use client";

import { useEffect, useState, useMemo } from "react";
import { useLanguage } from "@/context/language";
import {
    getVehicleExpensesByType,
    getDriversForFilter,
    getTrucksForFilter,
    VehicleExpenseRow,
    DriverOption,
    TruckOption,
    updateVehicleExpense,
} from "../actions.client";
import { TollExpenseImportDialog } from "@/features/accounting";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Receipt, DollarSign, Hash, TrendingUp, Loader2, RefreshCw, Save, Search, Plus } from "lucide-react";
import {
    ImageUrlPreviewView,
    type ImageUrlPreviewLabels,
} from "@/components/image-preview/ImageUrlPreviewView";
import { IMAGE_PREVIEW_VIEWPORT_ACCOUNTING_DIALOG_CLASS } from "@/components/image-preview/image-preview-constants";
import { AccountingPreviewImageActions } from "@/components/accounting/AccountingPreviewImageActions";
import { AccountingBatchImagesZipCard } from "@/components/accounting/AccountingBatchImagesZipCard";
import { looksLikeImageUrl } from "@/features/maintenance/utils/looksLikeImageUrl";
import { format } from "date-fns";
import { Button } from "@/components/ui/button";
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { usePermission } from "@/hooks/usePermission";
import { CAPABILITIES } from "@/lib/capabilities";
import { AddOtherExpenseDialog } from "@/features/accounting/components/AddOtherExpenseDialog";

const categoryKeys: Record<string, string> = {
    tire_repair: "accounting.category.tireRepair",
    maintenance: "accounting.category.maintenance",
    toll: "accounting.category.toll",
    parking: "accounting.category.parking",
    other: "accounting.category.other",
};

export default function AccountingOtherPage() {
    const { t, language } = useLanguage();
    const [records, setRecords] = useState<VehicleExpenseRow[]>([]);
    const [drivers, setDrivers] = useState<DriverOption[]>([]);
    const [trucks, setTrucks] = useState<TruckOption[]>([]);
    const [filterDriverId, setFilterDriverId] = useState<string>("all");
    const [filterTruckId, setFilterTruckId] = useState<string>("all");
    const [plateSearch, setPlateSearch] = useState("");
    const [filterMonth, setFilterMonth] = useState<string>("all");
    const [filterYear, setFilterYear] = useState<string>("all");
    const [quickDateFilter, setQuickDateFilter] = useState<"all" | "thisMonth" | "last30Days">("all");
    const [loading, setLoading] = useState(true);
    const [detailRow, setDetailRow] = useState<VehicleExpenseRow | null>(null);
    const [editForm, setEditForm] = useState<VehicleExpenseRow | null>(null);
    const [submitting, setSubmitting] = useState(false);

    const { hasPermission: canEdit } = usePermission(CAPABILITIES.accounting_edit_other);
    const [isAddOpen, setIsAddOpen] = useState(false);

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
            getVehicleExpensesByType("other"),
            getDriversForFilter(),
            getTrucksForFilter(),
        ]).then(([rows, driverList, truckList]) => {
            setRecords(rows);
            setDrivers(driverList);
            setTrucks(truckList);
        }).finally(() => setLoading(false));
    };

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

    const handleSaveEdit = async () => {
        if (!detailRow || !editForm) return;
        setSubmitting(true);
        try {
            await updateVehicleExpense(detailRow.id, {
                amount: editForm.amount,
                category: editForm.category ?? "",
                description: editForm.description ?? "",
            });

            await loadData();
            setDetailRow(null);
        } catch (err) {
            console.error("Failed to update other expense:", err);
        } finally {
            setSubmitting(false);
        }
    };

    useEffect(() => {
        if (detailRow) {
            setEditForm({ ...detailRow });
        } else {
            setEditForm(null);
        }
    }, [detailRow]);

    const filteredRecords = useMemo(() => {
        let list = records;
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
        if (quickDateFilter === "thisMonth") {
            const now = new Date();
            list = list.filter(
                (r) => r.date.getMonth() === now.getMonth() && r.date.getFullYear() === now.getFullYear()
            );
        } else if (quickDateFilter === "last30Days") {
            const cutoff = new Date();
            cutoff.setHours(0, 0, 0, 0);
            cutoff.setDate(cutoff.getDate() - 30);
            list = list.filter((r) => r.date >= cutoff);
        }
        return list;
    }, [records, filterDriverId, filterTruckId, plateSearch, filterYear, filterMonth, quickDateFilter]);

    const applyQuickDateFilter = (next: "thisMonth" | "last30Days") => {
        setFilterMonth("all");
        setFilterYear("all");
        setQuickDateFilter(next);
    };

    const resetQuickDateFilter = () => {
        setQuickDateFilter("all");
        setFilterMonth("all");
        setFilterYear("all");
    };

    const totalAmount = filteredRecords.reduce((s, r) => s + r.amount, 0);
    const count = filteredRecords.length;
    const avgAmount = count > 0 ? totalAmount / count : 0;
    const now = new Date();
    const thisMonth = filteredRecords.filter(
        (r) => r.date.getMonth() === now.getMonth() && r.date.getFullYear() === now.getFullYear()
    );
    const thisMonthTotal = thisMonth.reduce((s, r) => s + r.amount, 0);

    if (loading) {
        return (
            <div className="flex h-64 items-center justify-center">
                <Loader2 className="h-8 w-8 animate-spin text-primary" />
            </div>
        );
    }

    return (
        <div className="container mx-auto p-6 space-y-8 max-w-[1600px]">
            <div className="flex flex-col md:flex-row md:items-center justify-between gap-4">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">{t("accounting.other.title")}</h1>
                    <p className="text-muted-foreground mt-1">{t("accounting.other.subtitle")}</p>
                </div>
                {canEdit && (
                    <TollExpenseImportDialog onSuccess={loadData} canImport={canEdit} />
                )}
            </div>

            <AccountingBatchImagesZipCard records={records} kind="other" />

            {/* Mini dashboard */}
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
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
                <Card className="border-l-4 border-l-amber-500">
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium text-amber-700">{t("accounting.dashboard.thisMonth")}</CardTitle>
                        <TrendingUp className="h-4 w-4 text-amber-500" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold text-amber-700">฿{thisMonthTotal.toLocaleString()}</div>
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
                <Card>
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium">{t("accounting.dashboard.recordCount")}</CardTitle>
                        <Receipt className="h-4 w-4 text-muted-foreground" />
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
                        <Select
                            value={filterMonth}
                            onValueChange={(v) => {
                                setQuickDateFilter("all");
                                setFilterMonth(v);
                            }}
                        >
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
                        <Select
                            value={filterYear}
                            onValueChange={(v) => {
                                setQuickDateFilter("all");
                                setFilterYear(v);
                            }}
                        >
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
                            {t("accounting.filter.quick")}
                        </label>
                        <div className="flex items-center gap-2">
                            <Button
                                type="button"
                                size="sm"
                                variant={quickDateFilter === "thisMonth" ? "default" : "outline"}
                                onClick={() => applyQuickDateFilter("thisMonth")}
                            >
                                {t("accounting.filter.thisMonth")}
                            </Button>
                            <Button
                                type="button"
                                size="sm"
                                variant={quickDateFilter === "last30Days" ? "default" : "outline"}
                                onClick={() => applyQuickDateFilter("last30Days")}
                            >
                                {t("accounting.filter.last30Days")}
                            </Button>
                            <Button
                                type="button"
                                size="sm"
                                variant="ghost"
                                onClick={resetQuickDateFilter}
                                disabled={quickDateFilter === "all" && filterMonth === "all" && filterYear === "all"}
                            >
                                {t("accounting.filter.reset")}
                            </Button>
                        </div>
                    </div>
                </CardContent>
            </Card>

            {/* Table */}
            <Card>
                <CardHeader className="flex flex-row items-center justify-between space-y-0">
                    <CardTitle>{t("accounting.other.title")}</CardTitle>
                    <div className="flex items-center gap-2">
                        {canEdit && (
                            <Button variant="default" size="sm" onClick={() => setIsAddOpen(true)}>
                                <Plus className="h-4 w-4 mr-2" />
                                {t("accounting.other.addButton")}
                            </Button>
                        )}
                        <Button variant="outline" size="sm" onClick={loadData} disabled={loading}>
                            <RefreshCw className={`h-4 w-4 mr-2 ${loading ? "animate-spin" : ""}`} />
                            {t("common.refresh")}
                        </Button>
                    </div>
                </CardHeader>
                <CardContent>
                    <Table>
                        <TableHeader>
                            <TableRow>
                                <TableHead>{t("accounting.table.date")}</TableHead>
                                <TableHead>{t("accounting.table.licensePlate")}</TableHead>
                                <TableHead>{t("accounting.table.driver")}</TableHead>
                                <TableHead>{t("accounting.table.category")}</TableHead>
                                <TableHead className="text-right">{t("accounting.table.amount")}</TableHead>
                                <TableHead>{t("accounting.table.description")}</TableHead>
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {filteredRecords.map((row) => (
                                <TableRow
                                    key={row.id}
                                    className="cursor-pointer hover:bg-muted/50 transition-colors"
                                    onClick={() => setDetailRow(row)}
                                >
                                    <TableCell className="whitespace-nowrap font-mono text-xs">
                                        {format(row.date, "dd MMM yyyy HH:mm:ss")}
                                    </TableCell>
                                    <TableCell className="font-mono text-sm">{row.licensePlate ?? "—"}</TableCell>
                                    <TableCell>{row.driverName ?? (row.driverId || "—")}</TableCell>
                                    <TableCell>
                                        {row.category ? t(categoryKeys[row.category] ?? row.category) : "—"}
                                    </TableCell>
                                    <TableCell className="text-right font-semibold">฿{row.amount.toLocaleString()}</TableCell>
                                    <TableCell className="max-w-[280px] truncate text-muted-foreground" title={row.description ?? row.note ?? ""}>
                                        {row.description ?? row.note ?? "—"}
                                    </TableCell>
                                </TableRow>
                            ))}
                            {filteredRecords.length === 0 && (
                                <TableRow>
                                    <TableCell colSpan={6} className="h-24 text-center text-muted-foreground">
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
                <DialogContent className="max-w-4xl max-h-[90vh] overflow-hidden flex flex-col p-0">
                    <DialogHeader className="px-6 pt-6 pb-2 shrink-0">
                        <DialogTitle>{t("accounting.detail.otherTitle")}</DialogTitle>
                        <DialogDescription>
                            {detailRow && format(detailRow.date, "dd MMM yyyy HH:mm:ss")} · {detailRow?.driverName ?? detailRow?.driverId}
                        </DialogDescription>
                    </DialogHeader>
                    {detailRow && editForm && (
                        <div className="grid grid-cols-1 md:grid-cols-2 gap-0 flex-1 min-h-0 overflow-hidden">
                            {/* Left: Huge image + prev/next */}
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
                            {/* Right: Editable expense details */}
                            <div className="flex flex-col min-h-0 border-t md:border-t-0 border-border">
                                <div className="flex-1 overflow-y-auto p-6 space-y-4">
                                    <div className="grid grid-cols-2 gap-x-4 gap-y-3 text-sm">
                                        <Label className="text-muted-foreground">{t("accounting.table.date")}</Label>
                                        <span className="font-medium h-9 flex items-center">{format(editForm.date, "dd MMM yyyy HH:mm:ss")}</span>
                                        <Label className="text-muted-foreground">{t("accounting.detail.driver")}</Label>
                                        <Input value={editForm.driverName ?? editForm.driverId ?? "—"} readOnly className="h-9 bg-muted/50 cursor-not-allowed" />
                                        <Label className="text-muted-foreground">{t("accounting.detail.vehicle")}</Label>
                                        <Input value={editForm.licensePlate ?? "—"} readOnly className="h-9 font-mono bg-muted/50 cursor-not-allowed" />

                                        <Label className="text-muted-foreground">{t("accounting.detail.category")}</Label>
                                        {canEdit ? (
                                            <Select
                                                value={editForm.category ?? ""}
                                                onValueChange={(v) => setEditForm(prev => prev ? { ...prev, category: v || undefined } : null)}
                                            >
                                                <SelectTrigger className="h-9">
                                                    <SelectValue placeholder={t("accounting.audit.typeOther")} />
                                                </SelectTrigger>
                                                <SelectContent>
                                                    {Object.entries(categoryKeys).map(([key, labelKey]) => (
                                                        <SelectItem key={key} value={key}>
                                                            {t(labelKey)}
                                                        </SelectItem>
                                                    ))}
                                                </SelectContent>
                                            </Select>
                                        ) : (
                                            <Input value={editForm.category ? t(categoryKeys[editForm.category] ?? editForm.category) : "—"} readOnly className="h-9 bg-muted/50 cursor-not-allowed" />
                                        )}

                                        <Label className="text-muted-foreground">{t("accounting.detail.amount")}</Label>
                                        {canEdit ? (
                                            <Input
                                                type="number"
                                                value={editForm.amount ?? ""}
                                                onChange={(e) => setEditForm({ ...editForm, amount: e.target.value === "" ? editForm.amount : Number(e.target.value) })}
                                                className="h-9 font-semibold"
                                            />
                                        ) : (
                                            <span className="font-semibold h-9 flex items-center">฿{editForm.amount.toLocaleString()}</span>
                                        )}

                                        <div className="col-span-2 pt-2 border-t border-border mt-2 space-y-2">
                                            <Label className="text-muted-foreground">{t("accounting.detail.description")}</Label>
                                            {canEdit ? (
                                                <Input
                                                    className="h-9"
                                                    value={editForm.description ?? editForm.note ?? ""}
                                                    onChange={(e) => setEditForm({ ...editForm, description: e.target.value })}
                                                />
                                            ) : (
                                                <span className="text-muted-foreground text-sm break-words flex min-h-9 items-center p-2 rounded-md bg-muted/20">
                                                    {editForm.description ?? editForm.note ?? "—"}
                                                </span>
                                            )}
                                        </div>

                                        {editForm.adminNote && (
                                            <div className="col-span-2 space-y-2">
                                                <Label className="text-muted-foreground">{t("accounting.audit.adminNote")}</Label>
                                                <span className="text-muted-foreground text-sm break-words flex min-h-9 items-center p-2 rounded-md bg-orange-500/10 border border-orange-500/20 text-orange-700 dark:text-orange-400">
                                                    {editForm.adminNote}
                                                </span>
                                            </div>
                                        )}
                                    </div>

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
                        </div>
                    )}
                </DialogContent>
            </Dialog>

            <AddOtherExpenseDialog
                open={isAddOpen}
                onOpenChange={setIsAddOpen}
                onSaved={loadData}
            />
        </div>
    );
}
