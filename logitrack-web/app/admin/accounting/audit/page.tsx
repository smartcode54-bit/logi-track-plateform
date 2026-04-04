"use client";

import dynamic from "next/dynamic";
import { useEffect, useState, useMemo } from "react";
import { useLanguage } from "@/context/language";
import {
    getVehicleExpensesForAudit,
    updateVehicleExpenseStatus,
    updateVehicleExpense,
    VehicleExpenseRow,
    VehicleExpenseStatus,
} from "../actions.client";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { ClipboardCheck, Loader2, RefreshCw, Check, X, Fuel, Receipt, Save } from "lucide-react";
import {
    ImageUrlPreviewView,
    type ImageUrlPreviewLabels,
} from "@/components/image-preview/ImageUrlPreviewView";
import { IMAGE_PREVIEW_VIEWPORT_ACCOUNTING_DIALOG_CLASS } from "@/components/image-preview/image-preview-constants";
import { AccountingPreviewImageActions } from "@/components/accounting/AccountingPreviewImageActions";
import { buildAccountingZipEntries } from "@/lib/download-image-urls-zip";
import { looksLikeImageUrl } from "@/features/maintenance/utils/looksLikeImageUrl";
import { format } from "date-fns";
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from "@/components/ui/dialog";
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";

const RefillLocationMap = dynamic(
    () => import("@/components/accounting/RefillLocationMap").then((m) => m.RefillLocationMap),
    { ssr: false }
);

const categoryKeys: Record<string, string> = {
    tire_repair: "accounting.category.tireRepair",
    maintenance: "accounting.category.maintenance",
    toll: "accounting.category.toll",
    parking: "accounting.category.parking",
    other: "accounting.category.other",
};

const statusLabelKey: Record<VehicleExpenseStatus, string> = {
    PENDING: "accounting.audit.statusPending",
    APPROVED: "accounting.audit.statusApproved",
    REJECTED: "accounting.audit.statusRejected",
};

export default function AccountingAuditPage() {
    const { t } = useLanguage();
    const [records, setRecords] = useState<VehicleExpenseRow[]>([]);
    const [loading, setLoading] = useState(true);
    const [statusFilter, setStatusFilter] = useState<VehicleExpenseStatus | "all">("PENDING");
    const [detailRow, setDetailRow] = useState<VehicleExpenseRow | null>(null);
    const [actionRow, setActionRow] = useState<{ row: VehicleExpenseRow; action: "APPROVED" | "REJECTED" } | null>(null);
    const [adminNote, setAdminNote] = useState("");
    const [submitting, setSubmitting] = useState(false);
    const [editForm, setEditForm] = useState<VehicleExpenseRow | null>(null);
    const [detailMapKey, setDetailMapKey] = useState(0);
    const [slideCaption, setSlideCaption] = useState("");
    const [slideIndex, setSlideIndex] = useState(0);
    const [currentPreviewUrl, setCurrentPreviewUrl] = useState("");

    const loadData = () => {
        setLoading(true);
        getVehicleExpensesForAudit(statusFilter === "all" ? undefined : statusFilter)
            .then(setRecords)
            .finally(() => setLoading(false));
    };

    useEffect(() => {
        loadData();
    }, [statusFilter]);

    const pendingCount = useMemo(() => {
        return records.filter((r) => r.status === "PENDING").length;
    }, [records]);

    const handleConfirmAction = async () => {
        if (!actionRow) return;
        setSubmitting(true);
        try {
            await updateVehicleExpenseStatus(actionRow.row.id, actionRow.action, adminNote.trim() || undefined);
            
            if (actionRow.action === "APPROVED" && actionRow.row.type === "fuel" && actionRow.row.truckId && actionRow.row.odometer) {
                try {
                    const { httpsCallable } = await import("firebase/functions");
                    const { functions } = await import("@/firebase/client");
                    const checkPM = httpsCallable(functions, "checkMaintenanceAlert");
                    await checkPM({ truckId: actionRow.row.truckId, mileage: actionRow.row.odometer });
                } catch (e) {
                    console.error("Smart PM Trigger Error after Approval:", e);
                }
            }

            setActionRow(null);
            setAdminNote("");
            setDetailRow(null);
            loadData();
        } catch (err) {
            console.error("Failed to update expense status:", err);
        } finally {
            setSubmitting(false);
        }
    };

    /** อนุมัติทันที ไม่เปิด popup หมายเหตุ */
    const handleApproveDirect = async (row: VehicleExpenseRow) => {
        setSubmitting(true);
        try {
            await updateVehicleExpenseStatus(row.id, "APPROVED", undefined);
            
            if (row.type === "fuel" && row.truckId && row.odometer) {
                try {
                    const { httpsCallable } = await import("firebase/functions");
                    const { functions } = await import("@/firebase/client");
                    const checkPM = httpsCallable(functions, "checkMaintenanceAlert");
                    await checkPM({ truckId: row.truckId, mileage: row.odometer });
                } catch (e) {
                    console.error("Smart PM Trigger Error after Direct Approval:", e);
                }
            }

            setDetailRow(null);
            loadData();
        } catch (err) {
            console.error("Failed to approve expense:", err);
        } finally {
            setSubmitting(false);
        }
    };

    const openAction = (row: VehicleExpenseRow, action: "APPROVED" | "REJECTED") => {
        setActionRow({ row, action });
        setAdminNote(row.adminNote ?? "");
    };

    const imageItems = useMemo(() => {
        if (!detailRow) return [];
        const items: { url: string; label: string }[] = [];
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

    const zipEntries = useMemo(
        () =>
            detailRow
                ? buildAccountingZipEntries(
                      detailRow.id,
                      detailRow.receiptPhotoUrl,
                      detailRow.odometerPhotoUrl
                  )
                : [],
        [detailRow]
    );

    const zipFilename = useMemo(() => {
        if (!detailRow) return "";
        return `${detailRow.id}_${format(detailRow.date, "yyyyMMdd")}.zip`;
    }, [detailRow]);

    const handleSaveEdit = async () => {
        if (!detailRow || !editForm) return;
        setSubmitting(true);
        try {
            const pricePerLiterRounded = editForm.pricePerLiter != null && !isNaN(Number(editForm.pricePerLiter))
            ? Math.round(Number(editForm.pricePerLiter) * 100) / 100
            : editForm.pricePerLiter;
            await updateVehicleExpense(detailRow.id, {
                date: editForm.date,
                amount: editForm.amount,
                volumeLiters: editForm.volumeLiters,
                pricePerLiter: pricePerLiterRounded,
                odometer: editForm.odometer,
                stationTaxId: editForm.stationTaxId ?? "",
                taxInvId: editForm.taxInvId ?? "",
                refillLocation: editForm.refillLocation ?? "",
                note: editForm.note ?? "",
                category: editForm.category ?? "",
                description: editForm.description ?? "",
                status: editForm.status,
                adminNote: editForm.adminNote ?? "",
            });
            const list = await getVehicleExpensesForAudit(statusFilter === "all" ? undefined : statusFilter);
            setRecords(list);
            const updated = list.find((r) => r.id === detailRow.id);
            if (updated) {
                setDetailRow(updated);
                setEditForm({ ...updated });
            }
        } catch (err) {
            console.error("Failed to update expense:", err);
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
    }, [detailRow?.id]);

    useEffect(() => {
        setDetailMapKey((k) => k + 1);
    }, [detailRow?.id]);

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

    if (loading && records.length === 0) {
        return (
            <div className="flex h-64 items-center justify-center">
                <Loader2 className="h-8 w-8 animate-spin text-primary" />
            </div>
        );
    }

    return (
        <div className="container mx-auto p-6 space-y-8 max-w-[1400px]">
            <div className="flex flex-col md:flex-row md:items-center justify-between gap-4">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight flex items-center gap-2">
                        <ClipboardCheck className="h-8 w-8" />
                        {t("accounting.audit.title")}
                    </h1>
                    <p className="text-muted-foreground mt-1">{t("accounting.audit.subtitle")}</p>
                </div>
                <div className="flex items-center gap-2">
                    <Select
                        value={statusFilter}
                        onValueChange={(v) => setStatusFilter(v as VehicleExpenseStatus | "all")}
                    >
                        <SelectTrigger className="w-[160px]">
                            <SelectValue placeholder={t("accounting.audit.statusFilter")} />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value="all">{t("accounting.audit.statusAll")}</SelectItem>
                            <SelectItem value="PENDING">
                                {t("accounting.audit.statusPending")}
                                {statusFilter === "all" && pendingCount > 0 && ` (${pendingCount})`}
                            </SelectItem>
                            <SelectItem value="APPROVED">{t("accounting.audit.statusApproved")}</SelectItem>
                            <SelectItem value="REJECTED">{t("accounting.audit.statusRejected")}</SelectItem>
                        </SelectContent>
                    </Select>
                    <Button variant="outline" size="sm" onClick={loadData} disabled={loading}>
                        <RefreshCw className={cn("h-4 w-4 mr-2", loading && "animate-spin")} />
                        {t("common.refresh")}
                    </Button>
                </div>
            </div>

            <Card>
                <CardHeader>
                    <CardTitle>{t("accounting.audit.title")}</CardTitle>
                </CardHeader>
                <CardContent>
                    <Table>
                        <TableHeader>
                            <TableRow>
                                <TableHead>{t("accounting.table.date")}</TableHead>
                                <TableHead>{t("accounting.table.driver")}</TableHead>
                                <TableHead>{t("accounting.table.amount")}</TableHead>
                                <TableHead>{t("accounting.audit.tableType")}</TableHead>
                                <TableHead>{t("accounting.audit.tableStatus")}</TableHead>
                                <TableHead className="text-right">{t("accounting.table.actions")}</TableHead>
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {records.map((row) => (
                                <TableRow
                                    key={row.id}
                                    className="cursor-pointer hover:bg-muted/50 transition-colors"
                                    onClick={() => setDetailRow(row)}
                                >
                                    <TableCell className="whitespace-nowrap font-mono text-xs">
                                        {format(row.date, "dd MMM yyyy")}
                                    </TableCell>
                                    <TableCell>
                                        <div className="font-medium">{row.driverName ?? row.driverId ?? "—"}</div>
                                        {row.licensePlate && (
                                            <div className="text-xs text-muted-foreground font-mono">{row.licensePlate}</div>
                                        )}
                                    </TableCell>
                                    <TableCell className="font-semibold">฿{row.amount.toLocaleString()}</TableCell>
                                    <TableCell>
                                        {row.type === "fuel" ? (
                                            <Badge variant="secondary" className="gap-1">
                                                <Fuel className="h-3 w-3" />
                                                {t("accounting.audit.typeFuel")}
                                            </Badge>
                                        ) : (
                                            <Badge variant="outline" className="gap-1">
                                                <Receipt className="h-3 w-3" />
                                                {row.category ? t(categoryKeys[row.category] ?? row.category) : t("accounting.audit.typeOther")}
                                            </Badge>
                                        )}
                                    </TableCell>
                                    <TableCell>
                                        <Badge
                                            variant={
                                                row.status === "APPROVED"
                                                    ? "default"
                                                    : row.status === "REJECTED"
                                                      ? "destructive"
                                                      : "secondary"
                                            }
                                        >
                                            {t(statusLabelKey[row.status as VehicleExpenseStatus])}
                                        </Badge>
                                    </TableCell>
                                    <TableCell className="text-right" onClick={(e) => e.stopPropagation()}>
                                        {row.status === "PENDING" && (
                                            <div className="flex items-center justify-end gap-1">
                                                <Button
                                                    size="sm"
                                                    variant="default"
                                                    className="gap-1 bg-emerald-600 hover:bg-emerald-700 text-white"
                                                    onClick={() => handleApproveDirect(row)}
                                                    disabled={submitting}
                                                >
                                                    {submitting && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                                                    <Check className="h-3.5 w-3.5" />
                                                    {t("accounting.audit.approve")}
                                                </Button>
                                                <Button
                                                    size="sm"
                                                    variant="destructive"
                                                    className="gap-1"
                                                    onClick={() => openAction(row, "REJECTED")}
                                                >
                                                    <X className="h-3.5 w-3.5" />
                                                    {t("accounting.audit.reject")}
                                                </Button>
                                            </div>
                                        )}
                                        {row.status !== "PENDING" && row.adminNote && (
                                            <span className="text-xs text-muted-foreground truncate max-w-[120px] inline-block" title={row.adminNote}>
                                                {row.adminNote}
                                            </span>
                                        )}
                                    </TableCell>
                                </TableRow>
                            ))}
                            {records.length === 0 && (
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

            {/* Detail Dialog — 2 columns: left = images, right = expense details */}
            <Dialog open={!!detailRow} onOpenChange={(open) => !open && setDetailRow(null)}>
                <DialogContent className="max-w-6xl max-h-[90vh] overflow-hidden flex flex-col p-0">
                    <DialogHeader className="px-6 pt-6 pb-2 shrink-0">
                        <DialogTitle>
                            {detailRow?.type === "fuel"
                                ? t("accounting.detail.title")
                                : t("accounting.detail.otherTitle")}
                        </DialogTitle>
                        <DialogDescription>
                            {detailRow && format(detailRow.date, "dd MMM yyyy")} · {detailRow?.driverName ?? detailRow?.driverId}
                        </DialogDescription>
                    </DialogHeader>
                    {detailRow && (
                        <div className="grid grid-cols-1 md:grid-cols-3 gap-0 flex-1 min-h-0 overflow-hidden">
                            {/* Left: Large image + prev/next */}
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
                                            zipEntries={zipEntries}
                                            zipFilename={zipFilename}
                                        />
                                    </div>
                                ) : (
                                    <div className="flex items-center justify-center flex-1 min-h-[200px] rounded-lg border border-dashed border-muted-foreground/30 text-muted-foreground text-sm">
                                        {t("accounting.detail.noImages")}
                                    </div>
                                )}
                            </div>
                            {/* Middle: Editable expense details */}
                            <div className="flex flex-col min-h-0 border-t md:border-t-0 md:border-r border-border">
                                <div className="flex-1 overflow-y-auto p-6 space-y-4">
                                    {editForm && (
                                        <>
                                            <div className="grid grid-cols-2 gap-x-4 gap-y-3 text-sm">
                                                <Label className="text-muted-foreground">{t("accounting.table.date")}</Label>
                                                <Input
                                                    type="date"
                                                    value={editForm.date ? format(editForm.date, "yyyy-MM-dd") : ""}
                                                    onChange={(e) => {
                                                        const v = e.target.value;
                                                        setEditForm((prev) =>
                                                            prev ? { ...prev, date: v ? new Date(v) : prev.date } : null
                                                        );
                                                    }}
                                                    className="h-9"
                                                />
                                                <Label className="text-muted-foreground">{t("accounting.detail.driver")}</Label>
                                                <Input value={editForm.driverName ?? editForm.driverId ?? "—"} readOnly className="h-9 bg-muted/50 cursor-not-allowed" />
                                                <Label className="text-muted-foreground">{t("accounting.detail.vehicle")}</Label>
                                                <Input value={editForm.licensePlate ?? "—"} readOnly className="h-9 font-mono bg-muted/50 cursor-not-allowed" />
                                                <Label className="text-muted-foreground">{t("accounting.audit.tableType")}</Label>
                                                <div className="flex items-center">
                                                    {detailRow.type === "fuel" ? (
                                                        <Badge variant="secondary" className="gap-1">
                                                            <Fuel className="h-3 w-3" />
                                                            {t("accounting.audit.typeFuel")}
                                                        </Badge>
                                                    ) : (
                                                        <Badge variant="outline">
                                                            {editForm.category ? t(categoryKeys[editForm.category] ?? editForm.category) : t("accounting.audit.typeOther")}
                                                        </Badge>
                                                    )}
                                                </div>
                                                <Label className="text-muted-foreground">{t("accounting.audit.tableStatus")}</Label>
                                                <Select
                                                    value={editForm.status}
                                                    onValueChange={(v) =>
                                                        setEditForm((prev) =>
                                                            prev ? { ...prev, status: v as VehicleExpenseStatus } : null
                                                        )
                                                    }
                                                >
                                                    <SelectTrigger className="h-9">
                                                        <SelectValue />
                                                    </SelectTrigger>
                                                    <SelectContent>
                                                        <SelectItem value="PENDING">{t("accounting.audit.statusPending")}</SelectItem>
                                                        <SelectItem value="APPROVED">{t("accounting.audit.statusApproved")}</SelectItem>
                                                        <SelectItem value="REJECTED">{t("accounting.audit.statusRejected")}</SelectItem>
                                                    </SelectContent>
                                                </Select>
                                                <Label className="text-muted-foreground">{t("accounting.detail.amount")}</Label>
                                                <Input
                                                    type="number"
                                                    value={editForm.amount ?? ""}
                                                    onChange={(e) => {
                                                        const v = e.target.value;
                                                        setEditForm((prev) =>
                                                            prev ? { ...prev, amount: v === "" ? prev.amount : Number(v) } : null
                                                        );
                                                    }}
                                                    className="h-9"
                                                />
                                            </div>
                                            {detailRow.type === "fuel" && (
                                                <div className="grid grid-cols-2 gap-x-4 gap-y-3 text-sm pt-2 border-t border-border">
                                                    <Label className="text-muted-foreground">{t("accounting.detail.volume")}</Label>
                                                    <Input
                                                        type="number"
                                                        step="0.01"
                                                        value={editForm.volumeLiters ?? ""}
                                                        onChange={(e) => {
                                                            const v = e.target.value;
                                                            setEditForm((prev) =>
                                                                prev ? { ...prev, volumeLiters: v === "" ? undefined : Number(v) } : null
                                                            );
                                                        }}
                                                        className="h-9"
                                                    />
                                                    <Label className="text-muted-foreground">{t("accounting.detail.pricePerLiter")}</Label>
                                                    <Input
                                                        type="number"
                                                        step="0.01"
                                                        value={
                                                            editForm.pricePerLiter != null && typeof editForm.pricePerLiter === "number" && !isNaN(editForm.pricePerLiter)
                                                                ? editForm.pricePerLiter.toFixed(2)
                                                                : ""
                                                        }
                                                        onChange={(e) => {
                                                            const v = e.target.value;
                                                            const n = v === "" ? undefined : parseFloat(v);
                                                            setEditForm((prev) =>
                                                                prev ? { ...prev, pricePerLiter: n } : null
                                                            );
                                                        }}
                                                        className="h-9"
                                                    />
                                                    <Label className="text-muted-foreground">{t("accounting.detail.odometer")}</Label>
                                                    <Input
                                                        type="number"
                                                        value={editForm.odometer ?? ""}
                                                        onChange={(e) => {
                                                            const v = e.target.value;
                                                            setEditForm((prev) =>
                                                                prev ? { ...prev, odometer: v === "" ? undefined : Number(v) } : null
                                                            );
                                                        }}
                                                        className="h-9"
                                                    />
                                                    <Label className="text-muted-foreground">{t("accounting.detail.stationTaxId")}</Label>
                                                    <Input
                                                        value={editForm.stationTaxId ?? ""}
                                                        onChange={(e) =>
                                                            setEditForm((prev) =>
                                                                prev ? { ...prev, stationTaxId: e.target.value } : null
                                                            )
                                                        }
                                                        className="h-9"
                                                    />
                                                    <Label className="text-muted-foreground">{t("accounting.detail.taxInvId")}</Label>
                                                    <Input
                                                        value={editForm.taxInvId ?? ""}
                                                        onChange={(e) =>
                                                            setEditForm((prev) =>
                                                                prev ? { ...prev, taxInvId: e.target.value } : null
                                                            )
                                                        }
                                                        className="h-9"
                                                    />
                                                </div>
                                            )}
                                            {detailRow.type === "other" && (
                                                <div className="grid grid-cols-2 gap-x-4 gap-y-3 text-sm pt-2 border-t border-border">
                                                    <Label className="text-muted-foreground">{t("accounting.detail.category")}</Label>
                                                    <Select
                                                        value={editForm.category ?? ""}
                                                        onValueChange={(v) =>
                                                            setEditForm((prev) =>
                                                                prev ? { ...prev, category: v || undefined } : null
                                                            )
                                                        }
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
                                                    <Label className="text-muted-foreground col-span-2">{t("accounting.detail.description")}</Label>
                                                    <Input
                                                        className="col-span-2 h-9"
                                                        value={editForm.description ?? ""}
                                                        onChange={(e) =>
                                                            setEditForm((prev) =>
                                                                prev ? { ...prev, description: e.target.value } : null
                                                            )
                                                        }
                                                    />
                                                </div>
                                            )}
                                            <div className="space-y-2 pt-2 border-t border-border">
                                                <Label className="text-muted-foreground">{t("accounting.detail.note")}</Label>
                                                <Input
                                                    className="h-9"
                                                    value={editForm.note ?? ""}
                                                    onChange={(e) =>
                                                        setEditForm((prev) =>
                                                            prev ? { ...prev, note: e.target.value } : null
                                                        )
                                                    }
                                                />
                                                <Label className="text-muted-foreground">{t("accounting.audit.adminNote")}</Label>
                                                <Input
                                                    className="min-h-[60px]"
                                                    value={editForm.adminNote ?? ""}
                                                    onChange={(e) =>
                                                        setEditForm((prev) =>
                                                            prev ? { ...prev, adminNote: e.target.value } : null
                                                        )
                                                    }
                                                />
                                                <Label className="text-muted-foreground">{t("accounting.detail.latLng")}</Label>
                                                <Input
                                                    className="h-9 font-mono"
                                                    value={editForm.refillLocation ?? ""}
                                                    onChange={(e) =>
                                                        setEditForm((prev) =>
                                                            prev ? { ...prev, refillLocation: e.target.value } : null
                                                        )
                                                    }
                                                />
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
                                        </>
                                    )}
                                </div>
                                <DialogFooter className="px-6 py-4 border-t border-border shrink-0 flex-wrap gap-2">
                                    <Button
                                        variant="secondary"
                                        className="gap-1"
                                        onClick={handleSaveEdit}
                                        disabled={submitting || !editForm}
                                    >
                                        {submitting && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                                        <Save className="h-3.5 w-3.5" />
                                        {t("accounting.audit.save")}
                                    </Button>
                                    {detailRow?.status === "PENDING" && (
                                        <>
                                            <Button
                                                variant="default"
                                                className="gap-1 bg-emerald-600 hover:bg-emerald-700 text-white"
                                                onClick={() => handleApproveDirect(detailRow)}
                                                disabled={submitting}
                                            >
                                                {submitting && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                                                <Check className="h-3.5 w-3.5" />
                                                {t("accounting.audit.approve")}
                                            </Button>
                                            <Button
                                                variant="destructive"
                                                className="gap-1"
                                                onClick={() => {
                                                    setDetailRow(null);
                                                    openAction(detailRow, "REJECTED");
                                                }}
                                            >
                                                <X className="h-3.5 w-3.5" />
                                                {t("accounting.audit.reject")}
                                            </Button>
                                        </>
                                    )}
                                    <Button variant="outline" onClick={() => setDetailRow(null)}>
                                        {t("accounting.detail.close")}
                                    </Button>
                                </DialogFooter>
                            </div>
                            {/* Right: สถานที่เติม — แผนที่เท่านั้น */}
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
                            </div>
                        </div>
                    )}
                </DialogContent>
            </Dialog>

            {/* Reject confirmation dialog (อนุมัติไม่เปิด popup) */}
            <Dialog
                open={!!actionRow}
                onOpenChange={(open) => {
                    if (!open) setActionRow(null);
                    setAdminNote("");
                }}
            >
                <DialogContent>
                    <DialogHeader>
                        <DialogTitle>
                            {actionRow?.action === "APPROVED"
                                ? t("accounting.audit.approve")
                                : t("accounting.audit.reject")}
                        </DialogTitle>
                        <DialogDescription>
                            {actionRow && (
                                <>
                                    ฿{actionRow.row.amount.toLocaleString()} · {actionRow.row.driverName ?? actionRow.row.driverId} ·{" "}
                                    {format(actionRow.row.date, "dd MMM yyyy")}
                                </>
                            )}
                        </DialogDescription>
                    </DialogHeader>
                    {actionRow?.action === "REJECTED" && (
                        <div className="space-y-2 py-2">
                            <Label htmlFor="audit-admin-note">{t("accounting.audit.adminNote")}</Label>
                            <Input
                                id="audit-admin-note"
                                placeholder={t("accounting.audit.adminNotePlaceholder")}
                                value={adminNote}
                                onChange={(e) => setAdminNote(e.target.value)}
                                className="min-h-[80px]"
                            />
                        </div>
                    )}
                    <DialogFooter>
                        <Button variant="outline" onClick={() => setActionRow(null)}>
                            {t("firstMile.task.cancel", "Cancel")}
                        </Button>
                        <Button
                            variant={actionRow?.action === "REJECTED" ? "destructive" : "default"}
                            onClick={handleConfirmAction}
                            disabled={submitting}
                        >
                            {submitting && <Loader2 className="h-4 w-4 mr-2 animate-spin" />}
                            {actionRow?.action === "APPROVED" ? t("accounting.audit.approve") : t("accounting.audit.reject")}
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </div>
    );
}
