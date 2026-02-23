"use client";

import { useEffect, useState } from "react";
import { useLanguage } from "@/context/language";
import { getVehicleExpensesByType, VehicleExpenseRow } from "../actions.client";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Receipt, DollarSign, Hash, TrendingUp, Loader2, RefreshCw } from "lucide-react";
import { format } from "date-fns";
import { Button } from "@/components/ui/button";
import { ImagePreviewGallery } from "@/components/accounting/ImagePreviewGallery";
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from "@/components/ui/dialog";

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

    const loadData = () => {
        setLoading(true);
        getVehicleExpensesByType("other").then(setRecords).finally(() => setLoading(false));
    };

    useEffect(() => {
        loadData();
    }, []);

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
                <DialogContent className="max-w-2xl max-h-[90vh] overflow-y-auto">
                    <DialogHeader>
                        <DialogTitle>{t("accounting.detail.otherTitle")}</DialogTitle>
                        <DialogDescription>
                            {detailRow && format(detailRow.date, "dd MMM yyyy")}
                        </DialogDescription>
                    </DialogHeader>
                    {detailRow && (
                        <div className="grid gap-6 py-2">
                            <div className="grid grid-cols-2 gap-x-6 gap-y-3 text-sm bg-muted/30 rounded-lg p-4">
                                <span className="text-muted-foreground">{t("accounting.table.date")}</span>
                                <span className="font-medium">{format(detailRow.date, "dd MMM yyyy")}</span>
                                <span className="text-muted-foreground">{t("accounting.detail.driver")}</span>
                                <span className="font-medium">{detailRow.driverName ?? (detailRow.driverId || "—")}</span>
                                <span className="text-muted-foreground">{t("accounting.detail.vehicle")}</span>
                                <span className="font-mono">{detailRow.licensePlate ?? "—"}</span>
                                <span className="text-muted-foreground">{t("accounting.detail.category")}</span>
                                <span className="font-medium">
                                    {detailRow.category ? t(categoryKeys[detailRow.category] ?? detailRow.category) : "—"}
                                </span>
                                <span className="text-muted-foreground">{t("accounting.detail.amount")}</span>
                                <span className="font-semibold">฿{detailRow.amount.toLocaleString()}</span>
                                <span className="text-muted-foreground">{t("accounting.detail.description")}</span>
                                <span className="text-muted-foreground col-span-1">{detailRow.description ?? detailRow.note ?? "—"}</span>
                            </div>
                            {detailRow.receiptPhotoUrl && (
                                <ImagePreviewGallery
                                    items={[{ url: detailRow.receiptPhotoUrl, label: t("accounting.detail.receiptPhoto") }]}
                                />
                            )}
                            <DialogFooter>
                                <Button variant="outline" onClick={() => setDetailRow(null)}>
                                    {t("accounting.detail.close")}
                                </Button>
                            </DialogFooter>
                        </div>
                    )}
                </DialogContent>
            </Dialog>
        </div>
    );
}
