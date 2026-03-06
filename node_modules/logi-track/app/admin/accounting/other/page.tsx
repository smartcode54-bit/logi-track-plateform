"use client";

import { useEffect, useState } from "react";
import { useLanguage } from "@/context/language";
import { getVehicleExpensesByType, VehicleExpenseRow } from "../actions.client";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Receipt, DollarSign, Hash, TrendingUp, Loader2, RefreshCw, Save, ChevronLeft, ChevronRight } from "lucide-react";
import { format } from "date-fns";
import { Button } from "@/components/ui/button";
import { useMemo } from "react";
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
import { updateVehicleExpense } from "../actions.client";

const categoryKeys: Record<string, string> = {
    tire_repair: "accounting.category.tireRepair",
    maintenance: "accounting.category.maintenance",
    toll: "accounting.category.toll",
    parking: "accounting.category.parking",
    other: "accounting.category.other",
};

export default function AccountingOtherPage() {
    const { t } = useLanguage();
    const [records, setRecords] = useState<VehicleExpenseRow[]>([]);
    const [loading, setLoading] = useState(true);
    const [detailRow, setDetailRow] = useState<VehicleExpenseRow | null>(null);
    const [editForm, setEditForm] = useState<VehicleExpenseRow | null>(null);
    const [submitting, setSubmitting] = useState(false);

    const { hasPermission: canEdit } = usePermission(CAPABILITIES.accounting_edit_other);

    const [imageIndex, setImageIndex] = useState(0);

    const imageItems = useMemo(() => {
        if (!detailRow) return [];
        const items = [];
        if (detailRow.receiptPhotoUrl) items.push({ url: detailRow.receiptPhotoUrl, label: t("accounting.detail.receiptPhoto") });
        if (detailRow.odometerPhotoUrl) items.push({ url: detailRow.odometerPhotoUrl, label: t("accounting.detail.odometerPhoto") });
        return items;
    }, [detailRow, t]);
    const currentImage = imageItems[imageIndex] ?? null;

    useEffect(() => {
        setImageIndex(0);
    }, [detailRow]);

    const loadData = () => {
        setLoading(true);
        getVehicleExpensesByType("other").then(setRecords).finally(() => setLoading(false));
    };

    useEffect(() => {
        loadData();
    }, []);

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

    const totalAmount = records.reduce((s, r) => s + r.amount, 0);
    const count = records.length;
    const avgAmount = count > 0 ? totalAmount / count : 0;
    const now = new Date();
    const thisMonth = records.filter(
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
        <div className="container mx-auto p-6 space-y-8 max-w-[1400px]">
            <div className="flex flex-col md:flex-row md:items-center justify-between gap-4">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">{t("accounting.other.title")}</h1>
                    <p className="text-muted-foreground mt-1">{t("accounting.other.subtitle")}</p>
                </div>
                <Button variant="outline" size="sm" onClick={loadData} disabled={loading}>
                    <RefreshCw className={`h-4 w-4 mr-2 ${loading ? "animate-spin" : ""}`} />
                    {t("common.refresh")}
                </Button>
            </div>

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

            {/* Table */}
            <Card>
                <CardHeader>
                    <CardTitle>{t("accounting.other.title")}</CardTitle>
                </CardHeader>
                <CardContent>
                    <Table>
                        <TableHeader>
                            <TableRow>
                                <TableHead>{t("accounting.table.date")}</TableHead>
                                <TableHead>{t("accounting.table.driver")}</TableHead>
                                <TableHead>{t("accounting.table.category")}</TableHead>
                                <TableHead className="text-right">{t("accounting.table.amount")}</TableHead>
                                <TableHead>{t("accounting.table.description")}</TableHead>
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
                            {records.length === 0 && (
                                <TableRow>
                                    <TableCell colSpan={5} className="h-24 text-center text-muted-foreground">
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
                            {detailRow && format(detailRow.date, "dd MMM yyyy")} · {detailRow?.driverName ?? detailRow?.driverId}
                        </DialogDescription>
                    </DialogHeader>
                    {detailRow && editForm && (
                        <div className="grid grid-cols-1 md:grid-cols-2 gap-0 flex-1 min-h-0 overflow-hidden">
                            {/* Left: Huge image + prev/next */}
                            <div className="bg-muted/30 border-t md:border-t-0 md:border-r border-border p-4 flex flex-col min-h-0">
                                {imageItems.length > 0 ? (
                                    <>
                                        <div className="flex-1 min-h-[280px] flex items-center justify-center overflow-hidden rounded-lg border border-border bg-black/5">
                                            {/* eslint-disable-next-line @next/next/no-img-element */}
                                            <img
                                                src={currentImage?.url}
                                                alt={currentImage?.label ?? ""}
                                                className="max-w-full max-h-[60vh] md:max-h-[70vh] w-auto h-auto object-contain select-none"
                                                draggable={false}
                                            />
                                        </div>
                                        <div className="flex items-center justify-between gap-2 pt-2 shrink-0">
                                            <Button
                                                type="button"
                                                variant="outline"
                                                size="icon"
                                                disabled={imageItems.length <= 1}
                                                onClick={() => setImageIndex((i) => (i <= 0 ? imageItems.length - 1 : i - 1))}
                                            >
                                                <ChevronLeft className="h-4 w-4" />
                                            </Button>
                                            <span className="text-sm text-muted-foreground">
                                                {currentImage?.label ?? ""} ({imageIndex + 1} / {imageItems.length})
                                            </span>
                                            <Button
                                                type="button"
                                                variant="outline"
                                                size="icon"
                                                disabled={imageItems.length <= 1}
                                                onClick={() => setImageIndex((i) => (i >= imageItems.length - 1 ? 0 : i + 1))}
                                            >
                                                <ChevronRight className="h-4 w-4" />
                                            </Button>
                                        </div>
                                    </>
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
                                        <span className="font-medium h-9 flex items-center">{format(editForm.date, "dd MMM yyyy")}</span>
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
        </div>
    );
}
