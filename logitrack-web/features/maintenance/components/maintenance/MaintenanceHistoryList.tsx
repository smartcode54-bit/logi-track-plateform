"use client";

import { useState } from "react";
import { useLanguage } from "@/context/language";
import { MaintenanceData } from "@/validate/maintenanceSchema";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { History, Wrench, AlertTriangle, MoreHorizontal, Pencil, Plus, Eye } from "lucide-react";
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuLabel,
    DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { buildMaintenanceGalleryUrls } from "@/features/maintenance/utils/buildMaintenanceGalleryUrls";
import { maintenanceDisplayCost } from "@/features/maintenance/utils/maintenanceDisplayCost";
import {
    MaintenanceImagePreviewDialog,
    type MaintenancePreviewGallery,
} from "@/features/maintenance/components/maintenance/MaintenanceImagePreviewDialog";

interface MaintenanceHistoryListProps {
    history: MaintenanceData[];
    onNewClick: () => void;
    onEditClick: (record: MaintenanceData) => void;
}

export function MaintenanceHistoryList({ history, onNewClick, onEditClick }: MaintenanceHistoryListProps) {
    const { t } = useLanguage();
    const [previewGallery, setPreviewGallery] = useState<MaintenancePreviewGallery>(null);

    const openRecordPreview = (record: MaintenanceData, startOnInvoice?: boolean) => {
        const urls = buildMaintenanceGalleryUrls(record);
        if (urls.length === 0) return;
        let startIndex = 0;
        if (startOnInvoice && record.invoiceUrl) {
            const i = urls.indexOf(record.invoiceUrl);
            if (i >= 0) startIndex = i;
        }
        setPreviewGallery({ urls, startIndex });
    };

    if (history.length === 0) {
        return (
            <div className="text-center py-12 bg-muted/10 rounded-xl border border-dashed">
                <Wrench className="w-12 h-12 text-muted-foreground mx-auto mb-4" />
                <h3 className="text-lg font-medium">{t("maintenance.form.noRecordsTitle")}</h3>
                <p className="text-muted-foreground mb-6">{t("maintenance.form.noRecordsDesc")}</p>
                <Button onClick={onNewClick}><Plus className="w-4 h-4 mr-2" /> {t("maintenance.form.newRecord")}</Button>
            </div>
        );
    }

    return (
        <Card>
            <CardHeader className="flex flex-row items-center justify-between">
                <CardTitle>{t("maintenance.serviceHistory")}</CardTitle>
                <Button onClick={onNewClick} size="sm"><Plus className="w-4 h-4 mr-2" /> {t("maintenance.form.addRecord")}</Button>
            </CardHeader>
            <CardContent>
                <MaintenanceImagePreviewDialog
                    gallery={previewGallery}
                    onClose={() => setPreviewGallery(null)}
                    title={t("maintenance.history.previewTitle")}
                    zoomInLabel={t("maintenance.preview.zoomIn")}
                    zoomOutLabel={t("maintenance.preview.zoomOut")}
                    resetZoomLabel={t("maintenance.preview.resetZoom")}
                    prevLabel={t("maintenance.preview.previous")}
                    nextLabel={t("maintenance.preview.next")}
                    notPreviewableLabel={t("maintenance.preview.notPreviewable")}
                    printLabel={t("maintenance.preview.print")}
                />
                <div className="space-y-4">
                    {history.map((record) => {
                        const galleryUrls = buildMaintenanceGalleryUrls(record);
                        const hasAttachments = galleryUrls.length > 0;
                        const displayTotal = maintenanceDisplayCost(record);
                        return (
                        <div
                            key={record.id}
                            className="flex items-start justify-between p-4 border rounded-lg hover:bg-muted/30 transition-colors group relative"
                        >
                            <div className="flex gap-4 cursor-pointer flex-1" onClick={() => onEditClick(record)}>
                                <div className={`p-3 rounded-full ${record.type === 'PM' ? 'bg-blue-100 text-blue-600' : 'bg-red-100 text-red-600'}`}>
                                    {record.type === 'PM' ? <History className="w-5 h-5" /> : <AlertTriangle className="w-5 h-5" />}
                                </div>
                                <div>
                                    <div className="flex flex-wrap items-center gap-2">
                                        <p className="font-semibold">
                                            {t(`maintenance.service.${record.serviceType}`) !== `maintenance.service.${record.serviceType}` 
                                                ? t(`maintenance.service.${record.serviceType}`) 
                                                : record.serviceType}
                                        </p>
                                        <Badge variant="outline" className="text-xs">{record.status.replace("_", " ")}</Badge>
                                        {record.driverSubmitted ? (
                                            <Badge className="text-xs bg-amber-600 hover:bg-amber-600">
                                                {t("maintenance.history.driverSubmittedBadge")}
                                            </Badge>
                                        ) : null}
                                    </div>
                                    <p className="text-sm text-muted-foreground">
                                        {record.startDate} {record.endDate ? ` - ${record.endDate}` : ''} • {record.provider}
                                    </p>
                                    {record.notes && <p className="text-sm italic text-muted-foreground mt-1">&quot;{record.notes}&quot;</p>}
                                    {hasAttachments ? (
                                        <div
                                            className="mt-3 flex flex-wrap items-center gap-2"
                                            onClick={(e) => e.stopPropagation()}
                                        >
                                            <Button
                                                type="button"
                                                variant="secondary"
                                                size="sm"
                                                className="h-8"
                                                onClick={() => openRecordPreview(record, !!record.invoiceUrl)}
                                            >
                                                <Eye className="mr-1.5 h-3.5 w-3.5" />
                                                {record.invoiceUrl
                                                    ? t("maintenance.history.viewDriverFile")
                                                    : t("maintenance.preview.viewReceipts")}
                                            </Button>
                                            {typeof record.invoiceAmount === "number" && !Number.isNaN(record.invoiceAmount) ? (
                                                <span className="text-sm text-muted-foreground">
                                                    {t("maintenance.history.driverInvoiceAmount")}: ฿
                                                    {record.invoiceAmount.toLocaleString()}
                                                </span>
                                            ) : null}
                                        </div>
                                    ) : null}
                                </div>
                            </div>
                            <div className="flex items-center gap-4">
                                <div className="text-right">
                                    <p className="font-bold text-lg">฿{displayTotal.toLocaleString()}</p>
                                    <p className="text-xs text-muted-foreground">
                                        {t("maintenance.history.labor")}: {record.costLabor || 0} | {t("maintenance.history.parts")}: {record.costParts || 0}
                                    </p>
                                    <p className="text-xs mt-1 text-muted-foreground">{record.currentMileage?.toLocaleString()} km</p>
                                </div>
                                <DropdownMenu>
                                    <DropdownMenuTrigger asChild>
                                        <Button variant="ghost" className="h-8 w-8 p-0" onClick={(e) => e.stopPropagation()}>
                                            <MoreHorizontal className="h-4 w-4" />
                                        </Button>
                                    </DropdownMenuTrigger>
                                    <DropdownMenuContent align="end">
                                        <DropdownMenuLabel>{t("maintenance.table.actions")}</DropdownMenuLabel>
                                        {hasAttachments ? (
                                            <DropdownMenuItem
                                                className="cursor-pointer"
                                                onClick={() => openRecordPreview(record, !!record.invoiceUrl)}
                                            >
                                                <Eye className="mr-2 h-4 w-4" />
                                                {record.invoiceUrl
                                                    ? t("maintenance.history.viewDriverFile")
                                                    : t("maintenance.preview.viewReceipts")}
                                            </DropdownMenuItem>
                                        ) : null}
                                        <DropdownMenuItem onClick={() => onEditClick(record)} className="cursor-pointer">
                                            <Pencil className="mr-2 h-4 w-4" />
                                            {t("maintenance.history.editUpdate")}
                                        </DropdownMenuItem>
                                    </DropdownMenuContent>
                                </DropdownMenu>
                            </div>
                        </div>
                        );
                    })}
                </div>
            </CardContent>
        </Card>
    );
}
